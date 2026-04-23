# PLAN-019 安全与审计基线（Step-up / 审计全覆盖 / Token TTL / Shadow Login）

- **status**: completed
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-19 00:40
- **updatedAt**: 2026-04-19 02:40
- **approvedAt**: 2026-04-19 01:40
- **completedAt**: 2026-04-19 02:40
- **relatedTask**: SEC-001
- **parentPlan**: —

## Context

竞品调研（主流私有云 / VPS 面板 / 公有云）与本项目对标，识别后台运维侧五项安全/审计缺口：

1. 敏感操作（删除 VM、迁移、踢节点、充值、重置密码）**无二次验证**
2. 管理员写操作审计**覆盖不全**（audit_logs 表已存在，但 handler 调用 audit helper 的覆盖率未知）
3. 审计日志**无保留策略、无导出**（合规要求 CSV 导出 + 定期归档冷存储）
4. API Token 表已有 `expires_at` 字段，但**无默认 TTL、无续签 API、无前端到期提示**
5. 工单排障常需"以用户身份看问题"，目前靠翻 DB 或造假数据，**缺 Shadow Login 能力**

### 明确不在本 PLAN 做的事

- **MFA / 2FA**：由 Logto 原生提供（TOTP / WebAuthn），应用层不复刻。运营侧在 Logto 后台开启即可。
- 密码策略、账号锁定：同由 Logto 管。

## Decisions

1. **Step-up auth 走 OIDC 重认证**：不自己发验证码。敏感操作触发 302 → Logto `/oidc/auth?prompt=login&max_age=0`，回调后应用读取 `auth_time` claim，≤ 5 分钟视为"最近已 step-up"。
2. **审计打点用 middleware + handler 双层**：middleware 自动记录 HTTP 写方法（POST/PUT/PATCH/DELETE），handler 层 `audit(ctx, ...)` 补充业务细节（target_type/target_id/details）。两层合并去重。
3. **审计保留策略配置化**：新 migration 加 `audit_retention_days` 参数（默认 365），后台定时任务清理超期记录到归档表或删除。
4. **API Token 默认 TTL = 24h**，支持用户创建时自定义（1h ~ 90d）。**续签 API**：持有有效 Token 即可换新 Token，原 Token 立即失效。
5. **Shadow Login 走独立 session cookie**：不复用 oauth2-proxy header。生成 `shadow_session` JWT，包含 `actor_id`（admin）+ `target_user_id` + `expires_at`（30min）+ `reason`。所有以 Shadow 身份发起的请求审计里额外标注 `actor_id`。金钱类操作（充值、扣费、退款）在此 session 下一律 **403**。

## Phases

### Phase A — Step-up Auth（2-3d）

- [ ] `internal/middleware/stepup.go` 新增 `RequireRecentAuth(maxAge time.Duration)` middleware
- [ ] 从 oauth2-proxy header 读取 `X-Auth-Request-Auth-Time`（Logto claim 透传），与当前时间比较
- [ ] 超过 maxAge 时返回 `401 {"error":"step_up_required","redirect":"<OIDC URL>"}`
- [ ] OIDC URL 构造：Logto endpoint + `prompt=login&max_age=0&redirect_uri=<原请求>`
- [ ] 前端 axios interceptor 识别 `step_up_required` 响应，弹窗提示"敏感操作需要重新认证"，确认后 `window.location = redirect`
- [ ] 挂载点（初版 5 处）：
  - `DELETE /api/admin/vms/{id}`（VM 强删）
  - `POST /api/admin/vms/{id}/migrate`（VM 迁移）
  - `POST /api/admin/nodes/{name}/evacuate` + `/restore`（节点踢出/恢复）
  - `POST /api/admin/users/{id}/balance`（充值/扣费）
  - `POST /api/portal/vms/{id}/reset-password`（密码重置，PLAN-M 预留，接口先占位）
- [ ] oauth2-proxy 配置确认：需透传 `auth_time` claim（verify/update `pass_user_headers` + `pass_access_token`）

### Phase B — 审计全覆盖（3-5d）

- [ ] 新增 `internal/middleware/auditwrite.go`：拦截所有 POST/PUT/PATCH/DELETE，自动写 `audit_logs`（action = `method + path`，details = request body 摘要，敏感字段 redact）
- [ ] 全量 handler 审查：列出未手写 `audit()` 调用的写路径，补齐 target_type + target_id + 业务上下文
- [ ] **redact 清单**：`password`、`token`、`secret`、`api_key`、`ssh_key` 字段内容替换为 `***redacted***`
- [ ] 定义 action 命名规范（doc）：`<resource>.<verb>`，如 `vm.delete`、`node.evacuate`、`user.balance.topup`
- [ ] 覆盖率基线：脚本 `scripts/audit-coverage-check.sh` 扫描所有 admin handler，统计已打点比例 → CI 门禁（< 90% 告警）

### Phase C — 审计导出 + 保留策略（2-3d）

- [ ] Migration `007_audit_retention.sql`：
  - 新表 `system_config`（key, value, updated_at），插入默认 `audit_retention_days = 365`
  - 或：直接加到 `config.yaml`，用 koanf 读（项目已用 koanf）。**决策**：走 koanf，避免新表
- [ ] 后台任务 `internal/worker/audit_cleanup.go`：每日 03:00 删除 `created_at < now() - retention_days` 的记录
- [ ] CSV 导出：`GET /api/admin/audit-logs/export?from=...&to=...&action=...` 返回 stream CSV
- [ ] 前端 `/admin/audit-logs` 加"导出 CSV"按钮 + 日期范围 + 操作类型筛选
- [ ] 导出操作本身也要写 audit（谁在什么时候导出了哪段数据）

### Phase D — API Token TTL + 续签（2-3d）

- [ ] 创建 Token 时默认 TTL = 24h（可用户自定义 1h-90d），前端下拉：1h / 6h / 24h / 7d / 30d / 90d / 自定义
- [ ] 新接口 `POST /api/portal/api-tokens/{id}/renew`：生成新 token，原 token `expires_at = now()` 立即失效（不级联删，留历史）
- [ ] 前端 `/api-tokens` 列表显示"剩余时间"（到期红色高亮，< 1h 黄色）+ "续签"按钮
- [ ] token 校验路径（`middleware/auth.go:53` 的 `tokenValidator`）加 `expires_at > now()` 条件 —— 需确认当前实现是否已校验过期
- [ ] 过期 token 清理任务（每小时），删除 `expires_at < now() - 30d` 的老记录

### Phase E — Shadow Login（3d，独立）

- [ ] 后端 `handler/admin/shadow.go`：
  - `POST /api/admin/users/{id}/shadow-login` body: `{reason: string}` → 返回 `{redirect_url: "/shadow/enter?token=..."}`
  - 生成 JWT：`{actor_id, target_user_id, reason, exp: now+30min, jti}`，签名用独立 key（区别于 API Token）
  - 写审计 `shadow.enter`，含 actor + target + reason
- [ ] `GET /shadow/enter?token=...`：校验 JWT，Set-Cookie `shadow_session`（HttpOnly, SameSite=Strict, 30min），302 到 `/`
- [ ] `middleware/auth.go` 加 `shadow_session` 分支：优先级高于 proxy header，读取 target_user_id 作为 CtxUserID
- [ ] 所有请求 ctx 额外挂 `CtxActorID`（shadow 身份下 = admin id，否则 = 空）
- [ ] 金钱类路由 middleware `RejectShadowSession`：挂在 `balance/topup`、`orders/*/pay`、`invoices/refund` 前，shadow 下 403
- [ ] 全局审计 writer：有 `CtxActorID` 时，`details` 加 `{acting_as_shadow: true, actor_id: ...}`
- [ ] 前端顶栏 `ShadowBanner`：检测 cookie 存在时显示红色横幅 "You are acting as <user.email> · 剩余 XX:XX · 退出" + 退出按钮（删 cookie）
- [ ] 退出接口 `POST /shadow/exit`：清 cookie + 写审计

### Phase F — Verification（1d）

- [ ] `go build ./... && go vet ./...` 通过
- [ ] `go test ./...` 通过，新增测试覆盖：
  - step-up middleware：auth_time 过期返 401，未过期放行
  - audit middleware：redact 密码字段，action 格式正确
  - token renew：旧 token 立即失效，新 token 可用
  - shadow login：金钱路由 403，非金钱路由带 actor_id 审计
- [ ] `bun run typecheck && bun run build` 通过
- [ ] 手工 E2E：
  - admin 删 VM → 触发 step-up → Logto 登录 → 回调继续删除成功
  - 导出一天的审计 CSV，字段完整，敏感字段已 redact
  - Shadow login 到某用户 → 顶栏红横幅 → 尝试充值被 403 → 退出 Shadow

### Phase G — Docs

- [ ] 更新 `docs/plan/index.md` 追加 PLAN-019 条目
- [ ] 更新 `docs/task/index.md` 添加 SEC-001
- [ ] 更新 `docs/changelog.md` 单条目
- [ ] `docs/security.md` 新增（如不存在）：step-up 触发列表、审计规范、Shadow Login 使用规范

## Risks

- **oauth2-proxy auth_time 透传**：需确认当前 oauth2-proxy 配置是否传了 `X-Auth-Request-Auth-Time` 头。如果没有，需升级 oauth2-proxy 配置并重启容器 —— 影响所有在线用户 session，需运维窗口
- **audit middleware 性能**：每个写请求落一次 DB 写。现有 QPS 不高（内部 <10 qps），但需加批量异步 flush（channel + 1s 聚合），避免阻塞 handler
- **redact 误伤**：request body 可能不是 JSON（multipart / form），先只处理 JSON，其他类型 details 记 `<non-json>`
- **Shadow Login session 劫持**：JWT 签名泄露会被滥用。key 独立存 `.env`，生产用 `openssl rand -base64 32` 生成，严格不进 git
- **金钱类路由识别**：`RejectShadowSession` 需要准确枚举，新增金钱相关接口时容易忘记挂。用"允许列表取反"策略不现实，靠 code review + 路由测试兜底
- **审计清理误删**：`retention_days` 默认 365 天，但合规有时要求 3-7 年。配置可改，初版默认 365，上线前与合规沟通

## Non-goals

- MFA / 2FA 实现（走 Logto）
- 密码策略（走 Logto）
- 审计日志不可篡改（hash chain / blockchain）—— 如需合规升级，另立 PLAN
- IP 白名单 / 地理围栏 —— 由 Cloudflare/WAF 层处理
- 审计日志流式导出到 SIEM（Splunk/ELK）—— V2 能力，本期只做 CSV
- Shadow Login 的"时间旅行"（查看用户过去状态）—— 太复杂，不做
- API Token scope/权限细粒度（现在 Token 等同于用户全权）—— 另立 PLAN

## Estimate

| Phase | 后端 | 前端 | 合计 |
|-------|------|------|------|
| A Step-up auth | 2d | 1d | 3d |
| B 审计全覆盖 | 3d | 0 | 3d |
| C 导出 + 保留 | 1.5d | 0.5d | 2d |
| D Token TTL + 续签 | 1.5d | 1d | 2.5d |
| E Shadow Login | 2d | 1d | 3d |
| F 验证 + G 文档 | 0.5d | 0.5d | 1d |
| **合计** | **10.5d** | **4d** | **14.5d ≈ 3 周** |

## Alternatives

### Step-up auth 方案对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. OIDC 重认证**（选） | 复用 Logto、含 MFA、零密码学实现 | 依赖 oauth2-proxy 透传 auth_time | ✅ 采用 |
| B. 应用层发 TOTP 二次码 | 与 Logto 解耦 | 重复造轮子、密钥管理风险 | ❌ 已在 memory 约定不做 |
| C. 密码二次确认 | 极简 | 用户体验差、明文密码传输风险 | ❌ |

### 审计清理方案对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. koanf 配置 + 定时清理**（选） | 零新表、配置热更 | 清理时机受 worker 可用性影响 | ✅ 采用 |
| B. 新表 `system_config` | DB 级事务 | 增加表 + migration 复杂度 | ❌ 过度设计 |
| C. 归档到冷存储（S3） | 合规强 | 需要新基建 | ❌ 超出本期 |

### API Token 默认 TTL 方案对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. 24h 默认 + 可自定义 1h-90d**（选） | 平衡安全与便利 | 需要前端续签 UI | ✅ 采用 |
| B. 永不过期 | 脚本友好 | 合规风险 | ❌ 现状已是这样，要改 |
| C. 强制 1h | 安全最强 | 脚本不可用 | ❌ 过严 |

### Shadow Login 安全边界方案对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. 独立 JWT session + 金钱类 403**（选） | 职责清晰、审计清楚 | 路由白名单需维护 | ✅ 采用 |
| B. 复用 proxy header + actor 字段 | 无新机制 | 身份混乱、审计难以区分 | ❌ |
| C. 只读模式（只能看不能写） | 最安全 | 排障经常需要写操作 | ❌ 工单效率不及预期 |

## Open Questions（kickoff 前需确认）

1. ~~Logto 当前有没有开启 `auth_time` claim 透传？~~ ✅ **已确认已开启**（2026-04-19，OIDC discovery `claims_supported` 含 `auth_time`）
2. ~~oauth2-proxy 是否透传 `X-Auth-Request-Auth-Time`？~~ ⚠️ **已确认但路径调整**：oauth2-proxy v7.9.0 `set_xauthrequest=true` 已开，但默认**不单独透传 auth_time header**；改为应用层解 `X-Auth-Request-Access-Token`（JWT）取 `auth_time` claim，零运维改动
3. ~~金钱类路由清单除已列 5 条外是否有遗漏？~~ ✅ **用户确认保持已列清单**（`DELETE /vms/{id}` / `POST /vms/{id}/migrate` / `/nodes/{name}/evacuate` / `/nodes/{name}/restore` / `/users/{id}/balance` / 未来 `/vms/{id}/reset-password`）；实施期代码审查时如发现遗漏追加
4. ~~审计保留 365d 是否满足合规要求？~~ ✅ **用户确认 365d 满足**，无额外合规要求
5. ~~Shadow Login 是否允许 read-only 模式？~~ ✅ **用户选择保持默认**：全权 + 金钱类路由 403，**不**采用 read-only 兜底

## Annotations

（用户批注和 kickoff 讨论追加于此，保留完整历史。）

### 2026-04-19 立项批注

- 2026-04-19 用户决策：MFA 走 Logto，应用层不实现 → 已删除原草案中 MFA 条目
- 2026-04-19 用户决策：Shadow Login 并入 PLAN-S → 作为 Phase E 落地
- 2026-04-19 用户确认：编号采用 PLAN-019
- 2026-04-19 基础设施检测（vmc.5ok.co / 139.162.24.177）：
  - Logto OIDC discovery `claims_supported` 含 `auth_time` ✅
  - oauth2-proxy v7.9.0 配置 `set_xauthrequest=true` + `pass_access_token=true` ✅
  - 但 oauth2-proxy 默认**不单独**透传 `X-Auth-Request-Auth-Time` header
  - **实施路径调整**：Phase A 改为从 `X-Auth-Request-Access-Token`（已透传）解码 JWT 取 `auth_time` claim，复用 Logto JWKS（`https://auth.l.5ok.co/oidc/jwks`）验签
  - **收益**：零 oauth2-proxy 改动 → 零重启 → 无 session 中断，立项即可实施
- 2026-04-19 用户关闭 OQ #3/#4/#5：
  - #3 金钱类路由清单保持已列 5 条 + 1 条预留，实施期 code walk-through 时补遗
  - #4 审计保留 365d 满足合规
  - #5 Shadow Login 采用全权 + 金钱类 403 默认方案，不引入 read-only 兜底
- 2026-04-19 PLAN-019 所有 Open Questions 已清零，具备进入 Phase 3 条件

### 2026-04-23 Tech debt 收尾 — audit 覆盖率 100%

**实测覆盖率**（`scripts/audit-coverage-check.sh` 修改后统计）：

| file | writes | audits | status |
|------|-------:|-------:|--------|
| apitoken/ceph/clustermgmt/ippool/nodeops/order/product/quota/snapshot/sshkey/ticket/user/vm | 47 | 47 | **ok** |
| 合计 | **47** | **47** | **100%** |

脚本修复：原先按 `r.Post/Put/Patch/Delete(` 次数统计 writes，把 snapshot.go 的 admin+portal 双重注册（同一 handler）误判为 2 个 write。改成提取 handler 最后一段标识符（`Create/Delete/Restore`）去重后计算，snapshot.go 从 6/3 partial 修正为 3/3 ok。

全部 handler 的业务 audit 调用齐备（协作工具在 Phase A-E 各期滚动补齐 — snapshot/sshkey/order/ticket/user/apitoken 所有分支均有 `audit()` 或走 middleware `AuditAdminWrites` 兜底）。

### 2026-04-19 Phase A 实施批注

**实施路径最终版（V3 方案：应用自接 OIDC 子流程）**

Spike 发现 oauth2-proxy v7.9.0 的 `pass_authorization_header` 会覆盖 Authorization header、破坏现有 `Bearer ica_*` API Token 认证，原 V4 方案废弃，回到 V3：应用自己接一个小 OIDC flow，Logto 回调到应用独立的 `/api/auth/stepup-callback`。

**交付物（均已构建并部署到 vmc.5ok.co）**：

- 后端
  - `db/migrations/007_stepup_auth.sql` — users 表 `stepup_auth_at TIMESTAMPTZ` 列（已执行）
  - `internal/auth/oidc.go` — Logto OIDC client + state HMAC 签名（sign/verify）
  - `internal/handler/auth/stepup.go` — Start（登录态）+ Callback（公开）
  - `internal/middleware/stepup.go` — `sensitiveRoutes` allowlist + `RequireRecentAuthOnSensitive`（lookup=nil 则 no-op）
  - `internal/repository/user.go` — `GetStepUpAuthAt` / `SetStepUpAuthAt`（不改既有 Scan）
  - `internal/config/config.go` — AuthConfig 新增 5 项 OIDC 字段 + `parseDurationOr` helper
  - `cmd/server/main.go` — 初始化 OIDC client（可缺省降级），`StepUpLookup` 挂 middleware
  - `internal/server/server.go` — Handlers 加 `Auth StepUpHandler`、`/api/admin` 挂敏感 middleware、callback 和 start 分别注册
  - `go.mod` — `coreos/go-oidc/v3 v3.18.0` + `golang.org/x/oauth2 v0.36.0`
- 前端
  - `web/src/shared/lib/http.ts` — `request()` 拦截 401 `step_up_required`，`window.location = redirect`
- 运维
  - `oauth2-proxy.cfg`：`skip_auth_routes` 追加 `"^/api/auth/stepup-callback"` + 重启（一次性）
  - `/etc/incus-admin/incus-admin.env`：追加 `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` / `STEPUP_CALLBACK_URL` / `STEPUP_MAX_AGE=5m`（`STEPUP_STATE_SECRET` 未设，运行时回落 `SESSION_SECRET`）
  - 新二进制 `/usr/local/bin/incus-admin`（14.6MB，linux/amd64，`-s -w` stripped）

**敏感路由 allowlist（代码内集中维护 `sensitiveRoutes`）**：
- `DELETE /api/admin/vms/\d+$`
- `POST /api/admin/vms/\d+/migrate$`
- `POST /api/admin/nodes/[^/]+/evacuate$`
- `POST /api/admin/nodes/[^/]+/restore$`
- `POST /api/admin/users/\d+/balance$`

**部署验证（已通过）**：
- systemd `step-up OIDC ready` 日志打印，`max_age=5m`
- `/api/auth/stepup-callback` 无参数返 400（路径已放行，未被 oauth2-proxy 拦）
- `/api/auth/stepup-callback?code=x&state=y` 返 400 invalid state（HMAC 验签正确拒绝）
- `/api/auth/stepup/start` 未登录返 403（oauth2-proxy 保护生效）
- `DELETE /api/admin/vms/999` 未登录返 403（同上）

**待浏览器 E2E 验证**（用户侧）：
- 点敏感按钮 → 跳 Logto 带 `prompt=login&max_age=0` → 完成重认证 → 回原页 → 再点秒过 → 5min 后再次 step-up

### 2026-04-19 02:05 Phase A Bug 修复：sensitiveRoutes regex 前缀

**发现**：用户点"疏散节点"按钮后"没有反应"。日志显示实际请求路径是 `/api/admin/clusters/cn-sz-01/nodes/node1/evacuate`（带 `/clusters/{name}/` 前缀），但 `sensitiveRoutes` regex 只匹配 `/api/admin/nodes/...`（legacy 路径），导致 middleware 放行，Incus 返 `Certificate is restricted` 500 错误（TLS cert 配置的另一个 bug，与本 PLAN 无关）。

**修复**：`sensitiveRoutes` 扩展到 7 条，覆盖新旧两种路径形态，VM id 改为 `[^/]+`（实际用 VM name 而非 numeric id）。重新 build + redeploy。

### 2026-04-19 02:14 Phase A 端到端自测结果

绕过 oauth2-proxy 直打 `incus-admin:8080`，用 `X-Auth-Request-Email` 模拟登录态，通过 `UPDATE users.stepup_auth_at` 切 3 种状态验证 middleware 行为。测试前后自动清零不影响生产用户。

| 场景 | 预期 | 实际 |
|------|------|------|
| 敏感路由 stepup=NULL | 401 `step_up_required` + redirect | ✅ 401 + `{"error":"step_up_required","redirect":"/api/auth/stepup/start?rd=%2Fapi%2Fadmin%2F..."}` |
| `/api/auth/stepup/start?rd=...` | 302 到 Logto with `prompt=login&max_age=0` | ✅ Location 完整正确，含签名 state |
| 敏感路由 stepup=NOW | middleware 放行，handler 执行 | ✅ 500 Incus cert restricted（另一 bug，验证 middleware 已放行） |
| 敏感路由 stepup=NOW-10min | 再次 401 `step_up_required` | ✅ 401 |
| 非敏感路由（GET /admin/clusters） | 200 不受影响 | ✅ 200 |

Phase A 收官。

### 2026-04-19 Phase A 完结摘要

- ✅ 后端：OIDC client / handler / middleware / migration / config / main / server 全量落地
- ✅ 前端：`http.ts` 401 interceptor
- ✅ 运维：oauth2-proxy skip_auth_routes + incus-admin env + 二进制部署（重启 2 次：一次 oauth2-proxy，两次 incus-admin 含一次 bug 修复）
- ✅ 冒烟测试 + 端到端自测全绿
- ✅ changelog `2026-04-19 01:57 [progress]` 条目
- Phase A 涉及的 `Certificate is restricted` Incus 侧错误记录为 tech debt，不在本 PLAN 修

### 2026-04-19 Phase B 收官（auditwrite middleware + redact + 覆盖率脚本）

**交付物**：
- `internal/middleware/auditwrite.go`：拦截 /api/admin POST/PUT/PATCH/DELETE 自动写 `audit_logs`，action=`http.<METHOD>`；JSON body 递归 redact 敏感字段（password/token/secret/api_key/ssh_key/private_key/access_token/refresh_token 等 substring 匹配）；body > 64KB 截断；handler 仍可读完整 body；异步 goroutine 写不阻塞请求
- `internal/server/server.go`：`/api/admin` 组新增 `AuditAdminWrites` middleware，`auditWriter` 通过 `server.New` 注入
- `cmd/server/main.go`：`auditRepo.Log` 绑定为 `AuditWriter`
- `scripts/audit-coverage-check.sh`：扫描 handler 写路由 vs `audit()` 调用数量，输出覆盖率报告，支持 `--strict` CI 门禁

**E2E 验证（服务器端）**：
- POST 带 `password` 字段 → audit_logs.details.body.password=`***redacted***`
- DELETE 不存在资源 → audit_logs 记 status=404
- GET /admin/clusters → audit_logs 不新增（非 write）
- handler `vm.create` audit 与 middleware `http.POST` audit 同时存在，业务语义 + route coverage 互补

**Tech debt**：覆盖率脚本显示 4 个 handler 文件（apitoken/product/snapshot/sshkey）+ 3 个 partial（order/ticket/user）共 20 处缺业务 audit()，middleware 已兜底 route-level 100%，业务语义补齐后续增量完成。**[2026-04-19 06:30 已清零]** 手动补齐：product.create/update、sshkey.create/delete、snapshot.create/delete/restore、order.create/pay/update_status、ticket.create/reply(portal+admin)/update_status、user.update_role 共 13 个业务 action。所有 /api/admin 写路由至少一条业务 audit；middleware 的 `http.<METHOD>` 行作为兜底，便于串数据与统计。

### 2026-04-19 Phase C 收官（CSV 导出 + 保留策略 + 清理 worker）

**交付物**：
- `internal/repository/audit.go` 新增 `ExportRange`（流式、最多 100k 行，支持 from/to/action prefix 过滤）+ `DeleteOlderThan`
- `internal/handler/portal/audit.go` `ExportCSV` handler + `/audit-logs/export` 路由；自审计 `audit.export` 记录导出范围 + 行数 + 操作人
- `internal/worker/audit_cleanup.go`：每日清理 `created_at < NOW() - retention_days`；30s warm-up + 24h tick；retention <= 0 禁用
- `internal/config/config.go`：`AuthConfig.AuditRetentionDays` (env `AUDIT_RETENTION_DAYS`，默认 365)
- `cmd/server/main.go`：`worker.RunAuditCleanup` goroutine 启动
- 前端 `/admin/audit-logs`：导出表单（起始/截止日期 + 动作前缀 + "导出 CSV" 按钮）

**E2E 验证**：
- 全量导出 38 行 +  header → status=200, content-type=text/csv
- action=vm.* 前缀筛选生效
- 自审计 `audit.export` 写入含 from/to/rows
- 启动日志 `audit cleanup worker started retention_days=365`

### 2026-04-19 Phase D 收官（API Token TTL + 续签 + 前端）

**交付物**：
- `internal/handler/portal/apitoken.go`：默认 TTL 改为 24h（原 7d），范围 [1h, 90d]；新增 `ExpiresInHours` 字段兼容原 `ExpiresInDays`；`Renew` handler (`POST /api-tokens/{id}/renew`) 原子失效旧 token + 生成新 token 继承 name；create/renew/revoke 均写 audit
- `internal/repository/apitoken.go`：`Renew` 事务（FOR UPDATE 锁旧行、失效、生成新行）+ `DeleteExpiredBefore`
- `internal/worker/apitoken_cleanup.go`：每小时删除过期 + 30 天 grace period 外的行（保留审计交叉引用）
- 前端 `/api-tokens`：创建表单加 TTL 下拉（1h/6h/24h/7d/30d/90d）+ 每行续签按钮 + 剩余时间显示（< 1h 黄色 / 已过期红色）

**E2E 验证**：
- 创建 TTL=1h token，返回 expires_at = 创建+1h
- 新 token 调 `/auth/me` 返 200
- 续签 TTL=24h → 新 token id+1，新 expires_at
- 旧 token 立即失效（401）、新 token 可用（200）
- DB 两行 token（旧 expires_at=NOW 标失效、新 expires_at 正确）
- audit_logs 记录 `api_token.create` + `api_token.renew`（含 old_id + ttl_hours）
- 启动日志 `api token cleanup worker started grace_period_hours=720`

### 2026-04-19 Phase E 收官（Shadow Login + 金钱类 403 + 红横幅）

**设计决策**：
- HMAC-SHA256 签名 JWT（payload base64url + "." + sig），30min TTL
- cookie `shadow_session` HttpOnly + SameSite=Strict + Secure（TLS 检测）
- Role 继承：shadow 下 CtxUserID=target 但 CtxUserRole=actor 的 role（必为 admin），admin 路由仍可达
- Step-up 检查 actor（不是 target）的 `stepup_auth_at`
- 金钱类 allowlist（`internal/middleware/shadow.go::moneyRoutes`）：balance / orders pay / invoices refund —— shadow 下一律 403

**交付物**：
- `internal/auth/shadow.go`：`ShadowClaims` 结构 + `SignShadow` / `VerifyShadow` + `ShadowSessionCookie` 常量 + `ShadowTTL` 常量
- `internal/middleware/auth.go`：ProxyAuth 顶部插 shadow cookie 分支（最高优先级）；`CtxActorID` / `CtxActorEmail` 新 ctx key；`SetShadowVerifier` 注入；UserFromEmail 识别 `shadow` auth_method 并用 actor role
- `internal/middleware/shadow.go`：`moneyRoutes` allowlist + `RejectShadowSessionOnMoney` middleware
- `internal/middleware/auditwrite.go`：shadow session 下 user_id=actor + details.acting_as_user_id=target
- `internal/middleware/stepup.go`：shadow 下查 actor 的 stepup_auth_at
- `internal/handler/portal/audithelper.go`：handler 层 `audit()` 同款 acting_as 语义
- `internal/handler/admin/shadow.go`：`LoginAdmin` / `Enter` / `Exit` 三端点；self-shadow 守卫 + reason 必填 + 独立 auditor 回调
- `internal/config/config.go`：`ShadowSessionSecret` env（fallback `SESSION_SECRET`）
- `internal/server/server.go`：`/api/admin/users/{id}/shadow-login`（admin 内） + `/shadow/enter`（ProxyAuth 内） + `/shadow/exit`（同前）；`/api/auth/me` 返 `acting_as` 字段；`RejectShadowSessionOnMoney` 挂 /api/admin
- `cmd/server/main.go`：`adminhandler.NewShadowHandler` + `middleware.SetShadowVerifier`
- 前端 `shared/lib/auth.ts`：`ShadowActingAs` + `User.acting_as` + `isShadowing` helper
- 前端 `shared/components/layout/app-header.tsx`：shadow 下红色 header + target/actor email + "退出 Shadow" 表单（POST /shadow/exit）
- 前端 `app/routes/admin/users.tsx`：每行新增 "Shadow" 按钮（弹 reason 输入 → POST shadow-login → window.location = redirect_url）

**E2E 验证（完整通过）**：
1. Reason 必填 → 400 `{"error":"reason is required"}`
2. 带 reason → 返 `redirect_url=/shadow/enter?token=<signed_jwt>`
3. `/shadow/enter?token=` → 302 to `/` + Set-Cookie `shadow_session=...; HttpOnly; SameSite=Strict`
4. 带 shadow cookie 调 `/api/auth/me` → `{id:1, email:ai@5ok.co, role:admin, acting_as:{actor_id:23, actor_email:tom@5ok.co, target_id:1, target_email:ai@5ok.co}}`
5. 金钱路由 `/api/admin/users/1/balance` → 403 `{"error":"shadow_session_forbidden", "message":"Money-moving operations are not allowed..."}`
6. 非金钱 `GET /api/admin/clusters` → 200
7. audit_logs 新行：user_id=23 (actor=tom) + action=http.POST + details.acting_as_user_id=1 + auth_method=shadow
8. `/shadow/exit` → Set-Cookie `shadow_session=; Max-Age=0`（清除）
9. 清除后 `/api/auth/me` 回 tom 自己身份

**开发过程 bug 修复**:
1. 初版 middleware 层 audit 缺 `actor_id` 处理 → 修复后 user_id 记 actor、details 加 acting_as_user_id
2. 初版 UserFromEmail 未识别 shadow auth_method，导致 CtxUserRole 被覆盖为 target role → 修复后 shadow 下用 actor 的 role

### 2026-04-19 PLAN-019 全量收官

全部 5 个 Phase（A-E）后端 + 前端 + 部署 + E2E 验证通过。oauth2-proxy 重启 1 次 + incus-admin 重启 5 次（含 4 次增量部署）。Tech debt 项：Phase B 业务层 audit() 补齐 20 处（middleware 已兜底 route level，不阻塞关闭）。Close as completed.

### 2026-04-19 06:30 Tech debt 清零

Phase B 业务 audit 补齐独立 PR —— 盘点 13 个 handler 共 31 条写路由，逐文件对 `audit()` 调用次数，缺口定位在 product/sshkey/snapshot/order/ticket/user 六个文件。批量补齐 13 个新业务 action：`product.create/update` · `ssh_key.create/delete` · `snapshot.create/delete/restore` · `order.create/pay/update_status` · `ticket.create/reply/update_status` · `user.update_role`。所有 handler 的写路由 audit call 数 ≥ 路由数（snapshot 以单 handler 跨 portal/admin 双挂，计数按 handler 方法）。`golangci-lint run ./...` 仍 0 issue，`go test ./...` 全绿；生产部署 fbef7db7… 健康检查通过。
