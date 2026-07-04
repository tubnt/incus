# PLAN-055 深度代码审查整改批量修复（OPS-052）

- 状态：`[~]` 进行中（BKD 三层协调派发）
- 来源：2026-07-04 全项目深度代码审查（8 方向并行审计：认证授权 / VM·网络·控制台 / 计费订阅 / worker·HA·SSE / 集群运维·SSH / 数据层·迁移 / 前端接口闭环 / 杂项 API·terraform·死代码）
- 关联任务：OPS-052
- 协调方式：BKD 三层（L1 本会话 = 集成方；L2 = 派发+30min 健康巡检；L3 = 各工作包 worktree 隔离开发）

## 目标

把审查确认的问题分成**可自动修复**与**需人工决策**两桶。可自动桶按"文件不冲突"拆成 7 个工作包并行开发，每个工作包完成后自审 `/pma-cr`（前端另加 `/pma-des`），通过项目质量门（后端 `golangci-lint + go vet + go test + go build`；前端 `tsc --noEmit + vitest + eslint + vite build`）后合入 L2 分支，最终由 L1 审查合入 main。

## 代码检索约束

所有代码检索优先 code-review-graph（`query_graph` / `semantic_search_nodes` / `get_impact_radius`）与 Serena MCP（`find_symbol` / `find_referencing_symbols`），仅在图谱不覆盖时回退 Grep/Read。

## 可自动修复工作包（L3 并行单元）

### WP-A 认证 / 路由 / 中间件安全
- 文件：`internal/server/server.go`、`internal/middleware/{shadow.go,stepup.go,auth.go}`、`cmd/server/main.go`（部分）、`internal/config/config.go`
- 修复项：
  - **P0-1（高）** `RejectShadowSessionOnMoney` + `RequireRecentAuthOnSensitive` 只挂 `/api/admin`，其白名单含 portal 路径（`/api/portal/orders/{id}/pay`、`/api/portal/services/{id}/initial-credentials`）→ 上提到 portal+admin 公共 `r.Group`；清理 `shadow.go` 中已不存在的 refund 白名单条目；补回归测试。
  - 单条 `PUT /users/{id}/role` 纳入 step-up 敏感清单 + 加 `actorID==id` 自我操作拒绝。
  - `chimw.RealIP` 全局挂载打穿 `TRUSTED_PROXIES` → 去掉全局 RealIP，统一走 `realClientIP`。
  - `server.Run()` 吞启动错误、退出码 0 → errCh 的 err 上抛，让 systemd 正确重启。
  - OIDC discovery 无超时 → `context.WithTimeout(10s)`；已配 OIDC 但 discovery 失败时 step-up 改 fail-closed 或至少 Error 级日志 + 指标。
  - emergency 旧格式 cookie 未配 deadline 时永久接受 → 给代码级默认硬上限。
- 验收：portal 支付/看密码接口在无 step-up / shadow 会话下被拒（测试断言）；`go test ./...` 绿。

### WP-B jobs runtime 可靠性
- 文件：`internal/service/jobs/{cluster_node_add.go,runtime.go,broker.go}`、`internal/repository/provisioning_job.go`、`cmd/server/main.go`（worker panic 包装）
- 修复项：
  - **P0-2（高）** `cluster_node_add.go` ticker channel 死锁（`Stop()` 不关 channel）→ 改显式 `stop chan` + `select` 退出。
  - **P1-1（高）** `FindStaleRunning` 用 `created_at` 应为 `started_at` → 改 `COALESCE(started_at,created_at)`；`Finish` 加 `AND status IN ('queued','running')` 守卫；sweeper 阈值 > jobCtx 上限（35min）。
  - **P1-3（高）** broker cancel `close(ch)` 与 Publish 竞态 → Unsubscribe 不 close，仅从 map 删，订阅者靠 Terminal/ctx 退出。
  - 补偿/finalize 用独立 detached ctx（避免超时 job 退不了款）；`Shutdown` in-flight 计数修正；后台 worker 统一 `safeRun` panic recover。
- 验收：加节点 job 跑完能正常返回、不删已加入节点；`go test ./...` 绿。

### WP-C VM 删除资源回收 + IP 池 + reconciler
- 文件：`internal/worker/{vm_trash_purger.go,vm_reconciler.go}`、`internal/repository/{vm.go,ipaddr.go}`、`internal/handler/portal/vm_batch.go`、`internal/service/vm.go`、`cmd/server/main.go`（reconciler CreateBuffer + RecoverCooldowns 接线）
- 修复项：
  - **P0-3（高）** purge 删 VM 不释放 IP + `RecoverCooldowns` 零调用方 → purge 收口释放 IP；`RecoverCooldowns` 接到周期 worker（或 `AllocateNext` 池空时内联回收）。
  - **P1-5（高）** VM 软删不 detach Floating IP / 不清 firewall 绑定 → purge 统一收口释放全部关联资源。
  - **P1-6（高）** admin 批量删 VM 绕过回收站、漏 IP/订阅联动 → 复用 trash 路径。
  - **P1-2（高）** reconciler 把 `creating` 纳入比对致误判 gone + IP 双分配 → `ListActiveForReconcile` 排除 `creating`。
  - `isAlreadyGoneErr` 宽泛 `"not found"` 收窄；软删清 password 字段。
- 验收：删 VM 后 IP 回到可用池、Floating IP 释放、firewall 绑定清理；reconciler 不误判创建中 VM；`go test ./...` 绿。

### WP-D 计费 / 订阅生命周期（部分，另见 carve-out）
- 文件：`internal/service/billing/service.go`、`internal/handler/portal/{subscription_hooks.go,subscription.go}`、`internal/service/jobs/vm_create.go`、`internal/repository/subscription_repo.go`、`internal/worker/alert_evaluator.go`
- 修复项：
  - **P1-4（高）** `vm_create.go` Rollback 补 `CancelByVM`（异步失败不销订阅→幽灵扣费）。
  - grace `expireOne` 加 `FOR UPDATE` + 状态复查（与 chargeOne/reactivateOne 对齐，消除充值恢复竞态）。
  - `reactivateOne` / admin `Reactivate` 事务内校验 VM 存活（trashed/deleted 则让位 cancel）。
  - 欠费 restore 必须走补扣或拒绝（区分 cancel 原因：用户主动 trash vs 系统欠费回收）。
  - `alert_evaluator` 的 vm_down / balance_low 补 resolve 分支（恢复后翻 phase）。
- 验收：异步失败订阅被取消；grace 与充值不再互相踩；`go test ./...` 绿。
- **carve-out（不在本 WP，需你拍板）**：trash-restore 免费续期是否为期望语义。

### WP-E API 边界安全
- 文件：`internal/handler/portal/{console.go,snapshot.go,firewall.go,order.go}`、`internal/repository/quota.go`、`internal/middleware/idempotency.go`、`internal/handler/v1/instances_write.go`
- 修复项：
  - 控制台/快照跨集群同名越权 → owner 校验后从 VM 行反解 cluster/project，忽略客户端传值；console 的 project/vmName 转义。
  - 配额契约错位 → `QuotaRepo.GetByUserID` 把 `sql.ErrNoRows` 转 `(nil,nil)`；firewall 两处配额检查改 fail-closed。
  - 幂等中间件 TOCTOU → 进程内 keyed 互斥（advisory）先行占位，防并发双执行（**不改 DB schema**）。
  - `/v1/instances` 的 `root_pass`/`user_data`/`tags` 未实现却在 spec 声明 → 先对不支持字段返 422 并从 handler 明确拒绝（真正实现属 carve-out 产品决策）。
- 验收：跨集群越权被拒；配额缺行不再误 503、DB 抖动不放行；`go test ./...` 绿。
- **carve-out**：idempotency_keys PK `(key)`→`(key,user_id)`（改活表 schema）；`/v1` 三字段真正接入 provision。

### WP-F 前端接口闭环（/pma-des）
- 文件：`web/src/shared/lib/query-client.ts`、`web/src/features/*`、`web/public/locales/{zh,en}/common.json`、`web/src/features/vms/components/vm-peek-panel.tsx`
- 修复项：
  - 全局 `MutationCache.onError` 兜底 + 回收站撤销/删VM/开关机/角色/HA疏散/快照 三操作 补成功·失败 toast。
  - 96 个缺失 i18n key 补入 zh/en；`vm.type` 补 key；CI 加 "代码 t() key ⊆ 语言包" 校验脚本。
- 验收：mutation 失败有提示；英文界面无中文残留；`tsc --noEmit && vitest run && eslint . && vite build` 绿。
- **carve-out**：portal Floating IP 前端自助（后端已实现，前端补齐属新功能）。

### WP-G Terraform Provider 契约对齐
- 文件：`terraform-provider-incusadmin/internal/resources/{vm.go,floating_ip.go,ssh_key.go}`、`internal/client/client.go`
- 修复项：
  - **T1/T2/T3** vm 资源：Create 解析 202 真实字段（vm_id/vm_name/ip/job_id）设 ID + name 回填；Read 解析 `"db"` 键；cpu/memory/disk 补 `RequiresReplace`。
  - **T4** floating_ip：schema 补 required cluster+ip，Create body 对齐。
  - **T5** ssh_key：JSON tag 改 `"key"`/`"keys"`。
  - **T7** 四资源 Delete 404 视为成功（幂等）；client Do 返回结构化 StatusCode。
- 验收：`go build` + `go vet` 绿；资源 CRUD 与后端响应键逐字段对齐（人工核对表）。
- **carve-out**：T6 destroy 撞 step-up（需 token 预授权设计）。

## 需人工决策桶（暂不派发，等你拍板）

1. **trash-restore 免费续期语义**（产品）——是否允许免费续期，还是必须补扣。
2. **idempotency_keys PK 改 `(key,user_id)`**（活表 schema 迁移，跨用户 key 碰撞）。
3. **金额精度 `DECIMAL(10,2)`→`NUMERIC(12,4)`**（活的 balance/transactions 表迁移）。
4. **`/v1` root_pass/user_data/tags 真正实现 vs 从 spec 删除**（产品）。
5. **迁移体系接 goose runner + 补版本表**（基础设施；当前手工 psql）。
6. **ProxyAuth 信任模型 / `LISTEN` 默认改 `127.0.0.1`**（可能影响现网部署，需运维确认）。
7. **monthly=30 天语义**（产品，已记录为有意为之）。
8. **集群运维脚本双副本同步 + 3 个漂移文件同步**（需物理机验证，不宜盲改）。
9. **死代码删除**（约 10 个符号 ~150 行，低风险，可并入或单独确认）。

## 并行与合并策略

- WP-A..G 全部 `useWorktree:true` 并行（容量 8 槽足够）。
- 共享热点 `cmd/server/main.go` 被 A/B/C 触及 → L2 按 A→B→C 顺序串行合入并逐次 rebuild，冲突局部可解。
- WP-F（前端）、WP-G（terraform）与 Go 后端完全不相交 → 无冲突。
- 只有 L1（本会话）在你二次确认后合入 main。

## 决策结果（2026-07-04 用户拍板）+ Wave 2 派发

- #1 trash-restore 免费续期 → **不免费,必须补扣**。restore 区分 cancel 原因,恢复时按余额补扣一个周期(不足则拒绝/保持 suspended)。→ WP-H1
- #2 idempotency_keys PK `(key)`→`(key,user_id)` → **执行**(migration 030 + repo Put ON CONFLICT + middleware TOCTOU advisory 占位)。→ WP-H2
- #3 金额精度 → **保持 2 位**。不改 schema;改为扣费前把 amount round 到 2 位,使 balance/transactions/billing_charges 三处一致。→ WP-H1
- #4 `/v1` root_pass/user_data/tags → **从 OpenAPI spec 删除 + handler 对这些字段返 422**(停止"声明支持却静默丢弃"的欺骗);真正实现留作后续产品单。→ WP-H2
- #5 goose 迁移工具 → **暂缓**,单独立项(见对话解释)。
- #6 LISTEN/代理签名 → **暂缓**,仅在用户确认后做 env-gated 可选版(见对话解释)。
- #7 月=30 天 → 调研结论:项目是 anniversary(按 VM 起始日滚动)模型,行业推荐用于分摊负载/免 proration;30 天×日费率对用户公平(付 30 天得 30 天)。**保留 30 天**,仅前端/API 明示"1 个月 = 30 天"。→ WP-H5
- #8 脚本双副本同步 → **纳入 repo 侧**:sync 检查脚本覆盖全部脚本 + 同步 3 个漂移文件(物理机验证另行)。→ WP-H3
- #9 死代码清理 → **执行**。→ WP-H4

### Wave 2 工作包
- WP-H1 计费语义(restore 补扣 + 日费率 round 2 位):`internal/handler/portal/subscription_hooks.go`、`internal/service/billing/service.go`、`internal/repository/subscription_repo.go`
- WP-H2 幂等强化 + /v1 字段诚实化:`db/migrations/030_idempotency_pk.sql`、`internal/repository/idempotency_repo.go`、`internal/middleware/idempotency.go`、`internal/handler/v1/instances_write.go`、`internal/handler/portal/order_v1.go`、`internal/handler/openapi/openapi.yaml`
- WP-H3 运维脚本同步:`scripts/check-join-node-sync.sh`、`cluster/scripts/{apply-network.sh,probe-node.sh}`、`cluster/configs/cluster-env.sh`
- WP-H4 死代码清理:`internal/handler/portal/vm.go`(pickNextIP+ipCache)、`internal/handler/openapi/handler.go`(yamlOnce)、`internal/repository/{floating_ip.go,firewall.go,vm.go,ipaddr.go}`、`internal/model/models.go`(IP 常量)
- WP-H5 前端 月=30天 明示(/pma-des):`incus-admin/web/src/app/routes/billing.tsx` + 相关订阅文案
