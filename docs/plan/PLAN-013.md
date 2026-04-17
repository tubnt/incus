# PLAN-013 历史延期项全量清零 —— PLAN-009/010/011/012 未完成项归集

- **status**: awaiting_ops
- **createdAt**: 2026-04-17 16:40
- **approvedAt**: 2026-04-17 16:45
- **completedAt**: 2026-04-17 17:30（代码层 A/C/D + CI D.1；Phase B 反代层待线上窗口确认后由 ops 执行）
- **relatedTask**: 汇总 PLAN-009（QA-002）/ PLAN-010（QA-003）/ PLAN-011（REFACTOR-001+002）/ PLAN-012 的延期项，并同步修正 PLAN-009 `status` 字段与 `index.md` 不一致问题。

## 执行进度 (2026-04-17 17:30)

- ✅ Phase A（A.1 / A.2 / A.3）—— 全量完成，绿测
- ✅ Phase C（C.1 SPKI pin + TOFU；C.2 cluster_id 打通；C.3 observability iframe 降级为 new-tab-only；C.4 dist hash 启动提示）
- ✅ Phase D（D.1 `.github/workflows/ci.yml` 加 unit + integration + frontend 三 job；D.2 `TopUpWithDailyCap` 行锁原子化 + 并发集成测试）
- ✅ Phase E（元数据修正已在早批次完成）
- ⏸ Phase B（B.1/B.2/B.3）—— 需 prod Caddy/oauth2-proxy 变更，会踢掉线上 admin 登录态，等运维选低峰窗口后用 AIssh 推

## Context

对最近 4 个 plan 做了全量 diff 梳理，遗留项按"代码层"/"架构层"/"基础设施层"/"测试执行层"分组如下。
所有条目均有精确的文件 + 行号，避免再次"计划里说改了但 grep 不到"。

### C1. PLAN-011 承诺但未覆盖（代码层，P1）

| 项 | 位置 | 现状 | 缺口 |
|---|---|---|---|
| C1.1 handler 级 IP 分配失败回滚测试 | `internal/handler/portal/order.go`（`Pay` 路径）；预期测试文件 `internal/handler/portal/order_test.go::TestPay_RollbackOnIPAllocFail` | grep 不到此测试函数 | 无法证明 "VM 创建成功但 IP 分配失败" 场景下扣款/订单是否回滚 |
| C1.2 `admin/vms.tsx` 分页 | `web/src/app/routes/admin/vms.tsx` 全文无 `Pagination/limit/offset` | 前端仍一次性加载全量 VM；后端 handler 也不走 DB 的 `ListPaged` | 集群规模上来后前端会卡 |
| C1.3 `product.Update` 字段级 PATCH | `internal/handler/portal/product.go:76-101` | 用 `json.Decode(existing)` 覆盖合并 —— 无法区分"缺省字段"和"显式 0 / 空串" | 管理员改价时若只传 `price`，可能把 `stock=0` 也意外清零 |

### C2. PLAN-010 延期（反代层，P2）

| 项 | 位置 | 现状 | 缺口 |
|---|---|---|---|
| C2.1 `/oauth2/callback` 500 | 生产 Caddyfile + oauth2-proxy 配置 | 回调 500 | 应用不受影响（只 admin 登录链有偶发报错） |
| C2.2 安全响应头 | Caddyfile global | 缺 `Strict-Transport-Security / X-Content-Type-Options / Referrer-Policy` | A+ 评级缺失 |
| C2.3 `/favicon.ico` 403 | oauth2-proxy `skip_auth_routes` | favicon 被拦截鉴权 | 浏览器控制台噪音，不影响功能 |

### C3. PLAN-009 架构级（P1/P2，需拍板的点多）

| 项 | 位置 | 现状 | 缺口 / 改造方向 |
|---|---|---|---|
| C3.1 M4 TLS 指纹按集群固定 | `internal/cluster/manager.go:190`、`internal/handler/portal/events.go:68` 两处 `InsecureSkipVerify=true` | 信任任何集群端 TLS 证书 | `clusters` 表加 `tls_fingerprint text`，客户端做 SPKI pinning；首次接入时自动学习；不匹配拒连 |
| C3.2 L1 `ClusterID: 1` 后端硬编码 | `internal/handler/portal/order.go:103` 直接写 `clusterID := int64(1)` | 订单永远落到 cluster 1 | 接入订单 DTO `cluster_id`（前端 create-vm 已经支持集群选择，对齐即可） |
| C3.3 L2 前端集群硬编码 | `web/src/app/routes/admin/create-vm.tsx` 已有 `ClusterPicker` —— 需检查剩余 admin 页面（vms / ha / ip-pools / ceph）是否也拿了选中集群 | 部分 admin 页面仍可能绑死 | 所有 admin API 调用链路统一 `clusterID` 参数 |
| C3.4 L3 iframe http | `web/src/app/routes/admin/observability.tsx:9-11` 三个 `http://10.0.20.1:*` iframe | HTTPS 站内嵌 HTTP iframe，浏览器 Mixed-Content 阻断 | 2 选 1：a) 把 grafana/prom/alertmanager 挂 Caddy 反代 + HTTPS；b) 前端不用 iframe，改"新窗口打开 + VPN 提示" |
| C3.5 L4 `internal/server/dist/` 陈旧检测 | `cmd/server/main.go` 没有检测 dist 是否匹配当前二进制的 hash | build 流程容易忘记 `task web-build` | 构建时把 `dist/assets/` 的 hash 写入 `version.go`，启动校验并 log.Warn |

### C4. PLAN-012 残留限制（测试执行 & 原子性，P2）

| 项 | 位置 | 现状 | 缺口 |
|---|---|---|---|
| C4.1 集成测试未真正跑过 | `internal/repository/order_integration_test.go`、`user_integration_test.go` | 在嵌套容器环境下 `t.Skip`（Docker 网络返回 "wg0"）；本地 / CI 直连 Docker 没跑过 | 需单独 CI job（或本地带 `DOCKER_HOST=unix:///var/run/docker.sock` 的直连环境）执行一次并留证 |
| C4.2 日额度非原子 | `internal/handler/portal/user.go` TopUp 先 `SumDepositsSince` 再 `AdjustBalance` | 并发时末单可能略超 `MaxTopUpPerDay` | 合并成一条"条件 INSERT"：`INSERT INTO transactions SELECT ... WHERE (SELECT SUM(amount) ... ) + $amt <= $limit` 带 RETURNING 判失败 |

### C5. 元数据修正（小但必须，P3）

- `docs/plan/PLAN-009.md` 第 3 行 `status: implementing`，但 `index.md` 已标 `[x]` —— 改为 `completed`，补 `completedAt`。

## Proposal

按"影响大小 + 前置依赖"分 5 个 Phase，可并行 / 分批部署。

### Phase A —— PLAN-011 收尾（代码层，C1）

- **A.1** 新增 `internal/handler/portal/order_test.go::TestPay_RollbackOnIPAllocFail`
  - 构造：mock `IPPoolRepo.Allocate` 返回 error
  - 断言：`orders.status='pending'`（未置 paid）、`users.balance` 未扣、`transactions` 无新行、创建出的 VM 被清理（调用 `ClusterManager.DeleteVM` 补偿）
  - 若当前 `Pay` 路径没做补偿，本 plan 同步补 handler 补偿逻辑（try/defer 或显式错误分支）
- **A.2** `admin/vms.tsx` 加分页
  - 后端 handler `ListAdminVMs` 从"逐 cluster 全量拉"改成"DB 聚合 + `ListPaged(limit, offset)`"
  - 前端接 `Pagination` 组件（与 `users.tsx` / `orders.tsx` 一致），查询键改 `adminKeys.list({limit,offset})`
- **A.3** `product.Update` 改真 PATCH 语义
  - 请求 DTO：`type UpdateProductReq struct { Name, Description *string; Price *float64; Stock *int64; ... }`（全指针，区分 unset / explicit）
  - handler：把 non-nil 字段合并进 `existing`，保持其余字段
  - `repo.Update` 改 `UPDATE ... SET COALESCE($1, name)=... WHERE id=$id`（或构建动态 SET 列表）
  - 补 2 个单测：仅改 price / 同时改多个字段

### Phase B —— PLAN-010 反代层（C2）

不改 app 代码，改生产 `/etc/caddy/Caddyfile` + oauth2-proxy config。通过 AIssh MCP：

- **B.1** 修 `/oauth2/callback` 500 —— 排查 oauth2-proxy 日志，常见根因是 `cookie_secret` 长度或 `upstreams` 后端不通
- **B.2** Caddy global 加三条 header：
  ```caddy
  header Strict-Transport-Security "max-age=31536000; includeSubDomains"
  header X-Content-Type-Options "nosniff"
  header Referrer-Policy "strict-origin-when-cross-origin"
  ```
- **B.3** oauth2-proxy `--skip-auth-routes='^/favicon\.ico$'`

部署后 `curl -I` 验证三条 header + favicon 200。

### Phase C —— PLAN-009 架构级（C3）

Phase C 是本 plan 的主体。4 个架构改造按独立性拆子 Phase，可分批发布。

#### C.1 TLS 指纹 pinning（M4）

- schema: `db/migrations/006_cluster_tls_pin.sql`
  ```sql
  ALTER TABLE clusters ADD COLUMN IF NOT EXISTS tls_fingerprint text;
  ```
- `ClusterManager.newHTTPClient`：把 `InsecureSkipVerify=true` 换成 `VerifyPeerCertificate` 回调，对比 SPKI SHA256；`tls_fingerprint` 为空时 **trust-on-first-use**（学习并写回 DB）并记审计
- `events.go:68` 同步
- admin UI：集群详情页展示指纹 + "重置指纹"（需 confirm）按钮
- 回滚：DROP 列 + 还原 `InsecureSkipVerify`

#### C.2 订单 / 集群选择端到端打通（L1 + L2）

- `order.go:103` 删掉 `clusterID := int64(1)`，改从 `req.ClusterID` 取；若空则落 `DEFAULT_CLUSTER_ID`（env，默认 1，兼容老客户端）
- 审查 `admin/{vms,ha,ip-pools,ceph,create-vm}.tsx` 的 cluster 入参，统一拿 `useClustersQuery` + `ClusterPicker`
- 后端所有 `/admin/*` 在解析 `cluster_id` 时做 404 校验
- 单测：下单时传 cluster_id=2 → VM 落 cluster 2

#### C.3 observability iframe HTTPS（L3）

拍板方案（建议走 a）：Caddy 反代 grafana/prom/alertmanager 到 `vmc.5ok.co/observability/{grafana,prometheus,alertmanager}/`，iframe src 改相对路径；若三者中有任何一个不能 reverse-proxy（WebSocket / 特殊 Cookie），回退"新窗口打开"文案并删除 iframe。

#### C.4 dist 陈旧检测（L4）

- 构建脚本生成 `internal/server/dist_version.txt`（内容 = `index.html` sha256）
- `main.go` 启动把真 `index.html` hash 与 `dist_version.txt` 对比，不一致 `log.Warn("dist may be stale, run `task web-build`")`
- 不阻断启动，只提示；CI 同时加一条 `task verify-dist` step 阻断 PR

### Phase D —— PLAN-012 残留（C4）

- **D.1** CI 加 `test-integration` job（GitHub Actions 用 `services: postgres` 或本地 runner 带 Docker）——跑 `task test-integration`，要求 4 个用例真实通过
- **D.2** TopUp 日额度原子化
  - 新增 `UserRepo.AdjustBalanceWithDailyCap(ctx, userID, amount, cap, since) (oldBal, newBal float64, err error)`
  - SQL：单事务里 `SELECT balance + SUM(deposits) FOR UPDATE` → 判超 → `UPDATE users / INSERT transactions`
  - handler 用这个新方法替换现在的两步
  - 集成测试：并发 N 个 TopUp（各 1×MaxTopUpPerDay/N+epsilon），断言恰好 floor(N/…) 次成功、余额 ≤ cap

### Phase E —— 元数据 + 文档（C5）

- 改 `PLAN-009.md` 头：`status: completed`、`completedAt: 2026-04-17`
- `docs/changelog.md` 追加本 plan 执行记录
- `docs/task/index.md` 新增 3 个 task：
  - `TECHDEBT-001 Close PLAN-009/010/011/012 deferred items` `P1`（本 plan 主 task）
  - `INFRA-004 Cluster TLS fingerprint pinning` `P1`（C.1）
  - `INFRA-005 Observability iframe HTTPS reverse proxy` `P2`（C.3）

## Risks

1. **Phase C.1 TLS 学习模式**：首次接入时 TOFU 有 MITM 窗口。规避：首次学习必须在 WireGuard 隧道内完成，审计日志强制记录"首次学习"事件便于事后审查。
2. **Phase C.2 ClusterID 变动会影响订单历史统计**：`cluster_id` 从"永远 1"变成"实际值"后，现有 admin 报表若按 `cluster_id=1` 过滤会数据缺口。缓解：历史订单不动；新订单用实际值；报表侧同步改。
3. **Phase C.3 反代 Grafana 的 WebSocket / subpath**：Grafana 需 `GF_SERVER_SERVE_FROM_SUB_PATH=true` + `GF_SERVER_ROOT_URL`；Prometheus `--web.external-url` 同理。不配置的话 URL rewrite 会坏。
4. **Phase D.2 原子化重构**：`AdjustBalance` 是热点路径，SQL 改动需用 `EXPLAIN` 核对走索引（`idx_transactions_user_type_created` 已经建了）。
5. **Phase B.1 改 oauth2-proxy 会踢所有登录态**：部署窗口选低峰，变更前发告知。
6. **Phase A.3 product.Update 指针 DTO**：前端 `products/api.ts` 当前传的是普通字段，不是 partial；如果直接上后端 PATCH 语义，旧前端提交仍会"把字段压到 0/空"——需前后端同批发布。

## Scope

- ✅ Phase A.1/A.2/A.3（PLAN-011 代码收尾）
- ✅ Phase B.1/B.2/B.3（反代配置，AIssh 推）
- ✅ Phase C.1/C.2/C.3/C.4（架构四项，最大头）
- ✅ Phase D.1/D.2（CI + 原子化）
- ✅ Phase E（文档 + 任务同步）
- ❌ 不做多集群联邦 / 跨集群订单迁移（C.2 仅是本集群路由修复）
- ❌ 不做 Grafana / Prometheus 账号体系（C.3 只做 HTTPS 代理）
- ❌ 不做 `settings` 表化多用户差异限额（PLAN-012 已声明外置）

## Alternatives

- **C.1 替代：每集群独立 CA。** 更严但要自签 CA + 分发，运维成本高。指纹 pinning 已足够防 MITM，拒绝。
- **C.3 替代：用 `<a target="_blank">` 全部替换 iframe。** 实现最简但 UX 降级（无法在 admin 内查面板）。保留作为 fallback。
- **D.2 替代：Redis 分布式锁。** 引入新依赖。当前单实例 + 行锁足够，拒绝。
- **C.4 替代：启动时强制拒启动。** 开发体验太差（每次前端改一行就启动不了），选 warn。

## 执行顺序建议

1. E（元数据） / B（反代）—— 零代码或零 app 改动，先跑
2. A（代码收尾）—— 中等，不影响架构
3. D.2（原子化）—— 碰核心账目，独立部署
4. C.1 / C.2 / C.3 / C.4（架构四项）—— 每项独立 commit + 独立部署 + 独立 smoke
5. D.1（CI）—— 最后配好就长期跑
