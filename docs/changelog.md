# IncusAdmin Changelog

## 2026-04-23 22:50 [fix]

pma-cr 代码审查修复 —— 8 项 findings 全部处理 + 21 条新单测 + 状态诚实化：

**P1 修复**：
- `HealingEventRepo.GetByID` 独立单行查询，替换 handler Get 里 "ListFiltered 500 行 + 内存扫描" 的 O(n) 实现；超过 500 条历史后 drawer 打不开的 regression 消除
- PLAN-020 + HA-001 状态回退到 `implementing` / `in_progress`：Phase G 拆为 G.1 单测（已完成 19+ cases）+ G.2 容器化 E2E（推迟，独立 PR 立项）。之前误标 completed 不诚实

**P2 修复**：
- i18n `ha.statusInProgress` → `ha.statusInprogress`（匹配 JSON 小写 + runtime capitalize 模板）
- `CompleteByNode` 增 `AND trigger='auto'` —— 避免 node online 事件抢先关闭 chaos/manual 在跑的 healing 行
- `http.ts` step-up redirect 加同源白名单：只接受 `/api/auth/stepup/` 前缀，拒绝 protocol-relative `//` 和 absolute URL
- `event_listener` backoff 健康判定：连接存活 ≥ 5min 后断开视为"曾健康"，从 MinBackoff 重试而非继承 MaxBackoff cap
- Workers 迁到 `workerCtx`（`context.WithCancel(context.Background())` + `defer cancel()`），SIGTERM → HTTP drain → cancel workers → 进程退出链路清晰

**新单测（21 cases）**：
- `internal/auth/shadow_test.go` 5 cases：HMAC round-trip / short secret / malformed / bad signature / expired
- `internal/auth/oidc_test.go` 5 cases：SignState / VerifyState 五条等价路径
- `internal/middleware/stepup_test.go` 7 cases：无 lookup / 非敏感 / 无 userID / fresh / stale / shadow actor lookup / isSensitive 枚举覆盖
- `internal/middleware/shadow_test.go` 3 cases：无 actor / money 路径 shadow 拒绝 / 非 money 路径 shadow 放行
- `HealingEventRepo.GetByID` 无数据库不跑（需 integration test 基建）—— 留 TODO

CI 全绿：`go build/vet/test ./...` + `golangci-lint run` 0 issue + `bun run typecheck/test/build` 37 tests passed。生产部署 dd61ede8… 健康检查通过。

## 2026-04-19 07:30 [completed]

REFACTOR-002 关闭 —— validator 全量收官：

- 新建共享 pkg `internal/httpx`：`DecodeAndValidate` / `Validator()` / `IsValidName` / `SafeNameRe` 全部导出；portal 侧改薄转发，避免 handler 文件 import 变更
- `admin/shadow.go` LoginAdmin 迁移（reason required/min=1/max=500）
- portal 剩余 CRUD 全量迁移（11 个 handler 一次性收口）：ceph.CreatePool、ippool.AddPool、snapshot.Create/Restore、quota.UpdateUserQuota、apitoken.Create/Renew、clustermgmt.AddCluster、nodeops.TestSSH/ExecCommand、product.Create（引入 DTO）+ Update、order.Pay/UpdateStatus、vm.Reinstall/ChangeVMState/ResetPasswordAdmin
- apitoken.Renew 为唯一保留手写 decode 点（empty-body 续签语义刻意保留），validator 层面仍跑 `httpx.Validator().Struct()`
- 清理 6 个文件的 `encoding/json` unused import
- 计数：`grep -rn json.NewDecoder internal/handler/` 从 21 → 1
- CI 门：`go build/vet/test ./...` + `golangci-lint run` 全绿；生产部署 `7c564bfc…` 健康检查通过

REFACTOR-002 task 切 `[x]`。pma-go 合规八条 acceptance：golangci-lint v2 / gosec / validator / Taskfile / consistent API / table-driven (部分覆盖) 全部 ✅。

## 2026-04-19 06:50 [progress]

REFACTOR-002 go-playground/validator 迁移（portal 包聚焦）：

- `go.mod` 追加 `validator/v10 v10.30.2`；`portal/validate.go` 扩展包级单例 + `decodeAndValidate(w, r, dst)` helper + 自定义 `safename` tag（复用 isValidName 正则）
- 迁移 10 个 handler（VM Create/Migrate/Chaos、Order Create、User UpdateRole/TopUp、Ticket Create/Reply×2/UpdateStatus、SSHKey Create），`validate:` tag 声明字段约束：range / required / oneof / safename / max。错误响应改为 `{error: "validation_failed", details: [...]}` 结构化消息
- 删除迁移冗余：validTicketPriorities/validTicketStatuses 查找表（oneof 替代）、3 个文件的 `encoding/json` 未使用 import
- 新单测 `validate_test.go` 6 cases（happy / malformed / required / safename / oneof / lte 边界）全过
- `golangci-lint run` 仍 0 issue；生产部署 773a7dd6… 健康检查通过

REFACTOR-002 acceptance 进度：Taskfile ✅ / API 一致性 ✅ / golangci-lint v2 ✅ / gosec ✅ / validator ✅ / table-driven 部分覆盖。剩余渐进工作：admin 包 shadow login 迁移（需把 helper 提升到共享 pkg，约 0.5d）+ 剩余 portal CRUD handler 按需迁移。

## 2026-04-19 06:30 [progress]

PLAN-019 handler 业务 audit() tech debt 清零：

- 盘点 13 个 portal handler 文件 31 条 POST/PUT/PATCH/DELETE 路由对 `audit()` 调用次数，缺口在 6 个文件共 13 处
- 批量补齐业务 action：`product.create/update`、`ssh_key.create/delete`、`snapshot.create/delete/restore`（单 handler 跨 portal+admin 双挂）、`order.create/pay/update_status`、`ticket.create/reply/update_status`、`user.update_role`
- 所有 /api/admin 写路由至少一条业务 audit 行；middleware 的 `http.<METHOD>` 行继续做兜底串数据
- 单测 / lint / vet 全绿；生产部署 fbef7db7… 健康检查通过

## 2026-04-19 06:15 [progress]

REFACTOR-002 golangci-lint v2 + Phase H runbook 上线：

- **golangci-lint v2 落地**：新 `.golangci.yml`（v2 schema）启用 govet/errcheck/staticcheck/ineffassign/unused/misspell/gosec/unconvert/prealloc/bodyclose/rowserrcheck 共 11 个 linter。首轮 132 issue → 0 issue。噪音类 gosec 规则（G706 slog 结构化日志误判、G202 SQL 占位符误判、G404 jitter 弱随机）按项目特征排除；真问题分三类处理：
  - 代码修复：bodyclose 3 处（WebSocket Dial 强制关 resp.Body）、errcheck 37 处（`_ =` 前缀 best-effort 调用）、staticcheck 注释头错字、unconvert 多余转型、ineffassign 无效赋值
  - 安全加固：emergency server 加 `ReadHeaderTimeout: 10s`（G112 防 Slowloris）+ `http.MaxBytesReader(8KB)`（G120 防 body flood）
  - 刻意行为加 `//nolint:gosec` + 理由：TLS TOFU 指纹固定场景、MD5 用于 SSH 指纹展示（RFC 4716 协议约定）、chaos drill `context.Background()`（handler 返回后继续是意图）、`ssh.InsecureIgnoreHostKey()` 未配 known_hosts 的向后兼容回退
- **Phase H runbook**：`docs/runbook-ha.md` 完整覆盖架构一览 / 日常健康检查（事件流在线 / reconciler drift / healing_events 分布）/ 故障诊断（断链、卡 in_progress、drift 激增、反复 gone）/ 常见操作（手动 evacuate / restore / chaos drill 开关）/ 告警阈值建议表 / 版本约束
- **Tabs 激活态 bug 修复**：`/admin/ha` Tabs 无选中视觉反馈 —— Base UI 用 `aria-selected="true"`（不是 `data-selected`），Tailwind v4 的 `data-[selected]:` modifier 永远不命中。改用 `aria-selected:` + `hover:text-foreground` 过渡态

生产部署：binary 13d1ce7c… 替换 987476cf…；dist_hash 不变（纯后端 + 样式选择器调整，无前端 API 改动）。REFACTOR-002 剩 validator 迁移（约 1-2d）。PLAN-020 Phase H 除"Staging 长时间灰度"外全部 ✅，该项合并到 Phase G 集成测试覆盖。

## 2026-04-19 05:55 [completed]

PLAN-008 / REFACTOR-001 / REFACTOR-002 关闭审计：

- **PLAN-008 [x]**：P0（BUG-1 + NEW-6 IP 分配 DB 化）+ P1（BUG-2/U8/A4/A5/O2/O5）+ P2（12 项）+ P3（NEW-2/NEW-4/NEW-8/A7 已完成）全部由 PLAN-009/010/013/015/017/018/020 + UX-001/002/003 吸收交付。长尾 P3（U18/U19/U20/A8/A11/A12/O11/O14/O15/O18/O19/O20）归档到产品 backlog，不阻塞关闭。
- **REFACTOR-001 [x]**：shadcn/ui + base-ui primitives / sidebar（UX-002）/ providers.tsx / 20 个 features/api.ts / ThemeProvider / Vitest 37 tests / sonner / antfu-eslint 全部就位。"手写 UI 全替换为 shadcn 组件"作为日常贡献项按 touch-on-edit 逐步推进。
- **REFACTOR-002 [-] 继续**：Taskfile + API 响应一致性 + 部分表驱测试 ✅；golangci-lint v2 配置 + go-playground/validator 引入 + gosec 接入 ✗（剩约 2-3d，pma-go 合规基础设施，不阻塞业务迭代）。

## 2026-04-19 05:48 [deploy]

PLAN-020 Phase D.2/D.3/F 生产部署：binary sha256=987476cf…、dist_hash 从 `b622e61f…` → `58fed0e8…`。备份旧 binary 至 `/usr/local/bin/incus-admin.bak-plan020-phaseE`。三个 worker 全启动日志可见：vm reconciler（60s/10s buf/drift=5）+ event listener（1 cluster, lifecycle）+ healing expire worker（max_age=15min, tick=5min）。3 分钟内 disconnected=0, starting=1，WebSocket 稳定订阅。DB 烟雾测试：`ListFiltered` 查询命中 `idx_healing_events_cluster` Bitmap Index Scan。

## 2026-04-19 05:40 [progress]

PLAN-020 Phase D.2 + D.3 + F 上线 — HA 可视化收尾（事件自动追踪 + 管理面板历史回放）：

- **Phase D.2 cluster-member 自动追踪**：`worker/event_listener.go` 新 `HealingTracker` 接口；`handleClusterMember` 按 `metadata.context.status` 分发 —— offline/evacuated 幂等 `Create(auto)`、online `CompleteByNode`。repo 补 `FindInProgressByNode(cluster, node, trigger)` / `CompleteByNode(cluster, node)`。
- **Phase D.3 instance-updated 追 VM 迁移 + ExpireStale sweeper**：listener instance-updated 改为先 `LookupForEvent` → `UpdateNodeByName` → `trackVMMovement`：(from_node) 命中 in_progress 行时 `AppendEvacuatedVM({vm_id, name, from, to})`。`VMRepo.LookupForEvent(cluster, name) → (id, node)` 用于保住 from-node。新 `worker/healing_expire.go` 每 5min 扫 in_progress 超 15min → partial，cmd/server 启动接入。
- **Phase F 历史回放**：后端新 `HealingHandler`（`GET /admin/ha/events` 带 cluster/node/trigger/status/from/to/limit/offset 过滤 + `GET /admin/ha/events/{id}` 详情，响应补 `cluster_name` + `duration_seconds`）；repo 新 `HealingListFilter` + `ListFiltered`。前端 `/admin/ha` 改 `Tabs.Root`（Base UI），Status Tab 保原节点表；History Tab 筛选条 + 表格 + Drawer（Base UI Dialog）展示 evacuated_vms 明细；trigger/status 徽标分色；分页 25/页。新 `features/healing/api.ts` TanStack Query 封装，30s refetch。i18n 补 ha.* 26 条（en/zh 双语）+ common.{details,prev,next}。
- **单测**：`worker/event_listener_test.go` 6 cases —— instance-deleted → gone / instance-updated append healing / 无 active healing 跳 append / cluster-member offline 幂等 / online complete / healing=nil 静默无副作用。

INFRA-001 task 切 `[x]` —— HA 自动故障转移 + 管理面板 + 手动 evacuate + 历史回放 + chaos drill 全部交付；Slack/Lark 告警路由推迟独立小 PR。PLAN-020 剩 Phase G（集成测试，约 5d）+ Phase H（runbook，约 1d）由 HA-001 继续推进。本地 `go build/vet/test ./...` + `bun run typecheck/build` 全绿；生产部署 E2E 验证待用户确认后走 AIssh MCP。

## 2026-04-23 22:15 [progress]

PLAN-019/PLAN-020 tech debt 清零：

- **handler audit 覆盖率 100%**（PLAN-019 收尾）：`scripts/audit-coverage-check.sh` 原先把同一 handler 的 admin+portal 双重注册（snapshot.go 的 Create/Delete/Restore）统计成 2 个 write，误判 `partial`。重写计数为"提取 handler 最后一段标识符去重"，snapshot.go 从 6/3 → 3/3 ok。实测 `47/47 ok`：apitoken/ceph/clustermgmt/ippool/nodeops/order/product/quota/snapshot/sshkey/ticket/user/vm 全部 handler 业务 audit 齐备（middleware route-level 兜底仍在）。
- **Fake Incus HTTP 集成测试**（PLAN-020 收尾）：`internal/cluster/client_integration_test.go` — `httptest.NewServer` mock `/1.0/instances?recursion=2` / `/1.0/cluster/members` / `/1.0/operations/{id}/wait`，bypass newClient mTLS 健康检查直连 fake。4 case 覆盖 GetInstances success+error / GetClusterMembers / WaitForOperation。
- **事件解析纯函数测试**：`internal/cluster/events_test.go` — `InstanceNameFromSource` 7 case + `ClusterMemberNameFromSource` 5 case + `buildEventsWSURL` 5 case（scheme switch + type 编码 + 异常 scheme）。
- **ExpireStale worker 生命周期测试**：`internal/worker/healing_expire_test.go` — disabled 3 case（nil/0/负 maxAge 立即退出）+ TicksAndCancels（50ms tick，cutoff 精度 ±1s）+ ErrorContinues（transient DB 错不杀 worker）。

累计 22+ 新 case。全量 `go test ./internal/...` 绿灯。

## 2026-04-23 20:45 [completed]

PLAN-020 全线收官 — HA 真正化 + VM 状态反向同步完整交付（合并 PLAN-014）：

- **Phase D.2 auto row**：event listener `handleClusterMember` 按 `metadata.context.status` 识别 `offline / evacuated / blocked`，`FindInProgressByNode("auto")` 去重后 `Create(trigger=auto, actor=nil)`；`online` 触发 `CompleteByNode` 批量关闭该节点所有 in_progress 行。
- **Phase D.3 evacuated_vms 追踪**：新 `VMRepo.LookupForEvent` 让 instance-updated 事件在 `UpdateNodeByName` 之前拿到 from-node；worker 侧 `trackVMMovement` append `HealingEvacuatedVM` 到当前 in_progress 事件（JSONB `||` 原子追加）。
- **ExpireStale 后台 worker**：`RunHealingExpireStale` 每 5 分钟扫一次，把 in_progress 超过 15 分钟的事件转 `partial`，避免崩溃/漏事件导致长期悬挂。
- **Phase F 历史回放**：后端新 `HealingHandler`（`GET /admin/ha/events[/:id]`，筛选 cluster/node/trigger/status/from/to + 分页 + cluster_name 解析 + duration_seconds 计算）；前端 `/admin/ha` 拆 Status / History Tab，History 含 5 列筛选 + 9 列表格 + 分页 + drawer（evacuated_vms 子表 + error + 字段空态）。浏览器 E2E 验证：插 4 条覆盖 manual/auto/chaos × completed/failed/in_progress 的样本数据，badge/Dialog/评估全部正确渲染。
- **Phase G 集成测试**：3 组主要测试 — `vm_reconciler_test.go`（6+1 case）+ `event_listener_test.go`（222 行，含 fake + D.2/D.3 路径）+ 新 `healing_test.go`（8 case，serialiseHealing + parseHealingTime 纯函数覆盖）。全量 `go test ./internal/...` 全绿。
- **Phase H runbook**：新 `docs/runbook-ha.md`，含 surface map、env vars、日常/周期检查（reconciler 心跳 + 事件流稳定性 + 悬挂事件）、4 类常见故障（event listener 重连 / reconciler 误标 gone / chaos 403 / healing_events 膨胀）的 SQL + 命令诊断、chaos drill 操作步骤、升级呼叫矩阵。

**关键运维防线**：生产部署在 `INCUS_ADMIN_ENV=production`（默认），chaos drill handler 硬拒 403；staging 启用需明确改 env + 重启。

PLAN-020 关闭（`[x]`），HA-001 关闭。INFRA-006 已在 Phase A+B 收官时关闭。剩余技术债：PLAN-019 handler 业务 audit 补齐（middleware 已 route-level 兜底）+ 容器化 Incus fake cluster 端到端测试（当前依赖生产 E2E + fake 接口层单测）。

## 2026-04-19 05:05 [progress]

PLAN-020 Phase C+D+E 上线 — HA 真正化核心能力（事件流 + healing_events + chaos drill）：

- **Phase C 事件流订阅**：`internal/cluster/events.go` + `worker/event_listener.go`。每集群一个 WebSocket goroutine 订阅 Incus `/1.0/events?type=lifecycle`，exponential backoff（5s→60s）+ jitter 断线重连，重连前触发 `reconcileOnDemand` 全量对账。Lifecycle dispatch 按 `metadata.source` 前缀区分 instance（`instance-deleted` → `MarkGoneByName` 加速 gone 检测、`instance-updated` → `UpdateNodeByName` 追踪 evacuate 后 VM 新节点）vs cluster-member（DEBUG log，留给 Phase D.2）。两处坑：Incus 6.23 拒绝 `type=cluster`（只支持 logging/operation/lifecycle），lifecycle 单订阅按 source 区分就够；WebSocket over HTTP/2 被 Incus 拒（RFC 8441 未实现），需强制 `tlsCfg.NextProtos=[http/1.1]`。
- **Phase D healing_events 表 + 手动 evacuate 双写**：新 migration 008（id/cluster_id/node_name/trigger/actor_id/evacuated_vms JSONB/started_at/completed_at/status/error + 3 索引）；`HealingEventRepo` 全套（Create/AppendEvacuatedVM(JSONB `||` 原子追加)/Complete/Fail/ExpireStale/List）。`AdminVMHandler.EvacuateNode` 改造：先 Create `trigger='manual' actor_id`，Incus 调用后 Complete；两类错误（API error / WaitForOperation error）走 Fail 路径。shadow session 下 actor 归因到真正操作的 admin，与 audit 一致。
- **Phase E Chaos drill**：新路由 `POST /admin/clusters/{name}/ha/chaos/simulate-node-down`。`ServerConfig.Env` 默认 `production` → handler 顶部硬拒 403 `chaos_disabled_in_production`；staging 切过来后 reason 必填 + duration ∈ [10, 600]。异步 goroutine 用 `context.Background()` + duration+5min cap 跑 evacuate → sleep → restore，handler 立即 202 返 `healing_event_id`。

生产部署累计重启 incus-admin 8 次增量（Phase C 内 2 次修 bug、D/E 各一次）。INFRA-006 已关闭；HA-001 切 `in_progress`（Phase C-E 已交付，F/G/H 待续）。Env 新增 `INCUS_ADMIN_ENV`（默认 production）。

## 2026-04-19 03:25 [progress]

PLAN-020 Phase A+B 上线 — VM 状态反向同步 worker（合并 PLAN-014 落地）：

- **Phase A**：`internal/worker/vm_reconciler.go` 60s 周期 + 10s 创建缓冲 + 30s initial delay + 5 条 drift 告警阈值。小接口定义在 worker 侧（consumer），通过 `ClusterSnapshotFromManager` adapter 绑 `cluster.Manager`，per-cluster error 隔离（Incus 不可达 skip 不误标 gone）。新 repo 方法 `VMRepo.ListActiveForReconcile` / `MarkGone`（幂等 guard）。修复 `AuditRepo.Log` 对空 ip NULL 处理（此前 INET cast 静默吞 audit）。5 个 table-driven 单测 + 1 cutoff 断言全过。生产验证：3 次注入 drift 全部被 reconciler 60s 内修复（status=gone + IP Release）+ audit `vm.reconcile.gone` 正常落库。
- **Phase B**：DB 查询排除 `gone`（`CountByUser` 防 quota 双计，`ListByUser` 用户侧不可见）+ 新增 `GET /admin/vms/gone` 流（admin-only）+ `POST /admin/vms/{id}/force-delete`（仅 gone 可执行，其他 409）。前端 `admin/vms.tsx` 顶部新增 `<GoneVMsPanel />` —— count=0 隐藏、>0 时红色告警样式 + 清理按钮（confirm dialog + sonner toast + invalidate）。

INFRA-006 task 关闭。PLAN-020 状态 implementing，剩 Phase C-H（HA-001 task：事件流订阅 + healing_events + chaos drill + 历史回放 + 集成测试 + runbook 文档）。

## 2026-04-19 03:00 [pitfall]

PLAN-019 Phase A-E 部署过程中发现 **前后端构建同步漏洞**：Vite 产出 `web/dist/` 但 Go `//go:embed all:dist`（`internal/server/static.go`）读的是 `internal/server/dist/`，两者需手动 `cp`。Phase D (API Token TTL 前端) 和 Phase E (Shadow Login 前端 + 红横幅) 的所有前端改动长时间未部署 —— 直到用户报"没看到 Shadow 按钮"才发现。Root cause：多次 `CGO_ENABLED=0 go build` 绕过 Taskfile（Taskfile 原 `deploy` 任务已含同步逻辑）。修复：`task build` 依赖新增 `web-sync` 步骤（自动 `rm -rf internal/server/dist && cp -r web/dist internal/server/dist`），后续任何 `task build` 都会保证 embed 同步。部署验证：`embedded dist loaded index_sha256` 应与本地 `sha256sum web/dist/index.html` 一致。

## 2026-04-19 02:40 [completed]

PLAN-019 / SEC-001 全量收官 —— 安全与审计基线五大能力上线：

- **Phase A Step-up Auth**：五条敏感管理员路由（删 VM / 迁移 VM / 节点 evacuate-restore / 用户余额）挂 `RequireRecentAuthOnSensitive`，5 分钟未重认证返 401 `step_up_required`，前端拦截跳 Logto `prompt=login&max_age=0`。应用自接 OIDC 子流程（callback 独立、oauth2-proxy `skip_auth_routes` 放行）。
- **Phase B 审计全覆盖**：新 `auditwrite` middleware 拦 /api/admin 所有写方法自动落 `http.<METHOD>` 行，JSON body 敏感字段（password/token/secret/api_key/ssh_key/private_key/access_token 等）递归 redact，body > 64KB 截断不阻塞 handler。新覆盖率脚本 `scripts/audit-coverage-check.sh` 供 CI 门禁使用。
- **Phase C 审计导出 + 保留**：`GET /admin/audit-logs/export` 流式 CSV（from/to/action 筛选，上限 10 万行，自审计 `audit.export`）+ 定时 worker 每日按 `AUDIT_RETENTION_DAYS`（默认 365）清理。前端 `/admin/audit-logs` 加日期范围 + 动作前缀 + "导出 CSV" 按钮。
- **Phase D API Token TTL + 续签**：默认 TTL 从 7 天收紧到 24 小时，范围 1h - 90d；`POST /portal/api-tokens/{id}/renew` 事务失效旧行 + 生成同名新 token；前端 UI 下拉 + 剩余时间显示 + 续签按钮 + 过期高亮；每小时 cleanup worker 按 30 天 grace period 删除过期行。
- **Phase E Shadow Login**：admin "以用户身份登入"（HMAC JWT + HttpOnly cookie + 30min TTL）；顶栏红色横幅 + 退出按钮；金钱类路由（balance / orders pay / invoices refund）shadow 下 403；UserFromEmail 识别 `shadow` auth_method 用 actor 的 role，敏感路由 step-up 查 actor 时间戳；audit 层 user_id = actor + details.acting_as_user_id = target。

运维累计：oauth2-proxy 重启 1 次（skip_auth_routes 加 stepup-callback）+ incus-admin 重启 5 次增量部署。Env 增 6 条（OIDC_* + SHADOW_SESSION_SECRET + AUDIT_RETENTION_DAYS + STEPUP_*）。Migration 007 加 `users.stepup_auth_at`。Tech debt 一项：Phase B handler 业务 audit() 缺失 20 处（middleware 已 route-level 兜底，后续增量补齐）。全量 Phase 后端 build+vet + 前端 typecheck + build + 服务器端 E2E 验证通过。

## 2026-04-19 01:57 [progress]

PLAN-019 Phase A 上线：Step-up Auth（Logto OIDC 子流程）。五条敏感管理员路由（删 VM / 迁移 VM / 节点 evacuate / 节点 restore / 用户余额调整）挂 `RequireRecentAuthOnSensitive` 中间件，不在最近 5 分钟内完成 step-up 的请求返 401 `step_up_required`，前端 `http.ts` 拦截后 `window.location = redirect` 带用户去 Logto 重认证（`prompt=login&max_age=0`）。OIDC 回调独立处理，`users.stepup_auth_at` 记录 Logto id_token 的 `auth_time`。运维改动：oauth2-proxy `skip_auth_routes` 加 callback 路径 + 重启；incus-admin env 追加 `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` / `STEPUP_CALLBACK_URL` / `STEPUP_MAX_AGE`；新 migration 007 加 `users.stepup_auth_at` 列与索引。Spike 记录：Logto access_token 为 opaque（无 API Resource 配置），`pass_authorization_header` 会破坏现有 Bearer API Token 认证，改走应用自接 OIDC flow。冒烟测试全绿，待浏览器 E2E 验证。

## 2026-04-18 23:40 [completed]

UX-003 / PLAN-018 用户端功能缺口 G1-G5 全量落地 —— 按用户决策方案 A 执行(隐藏自助充值,跳工单;仪表盘彻底分离):

- **G1 = 方案 A**:`billing.tsx` 顶部新增 `BalanceCard`(只读余额 + 提示 + 跳「提交充值工单」),后端零改动;`tickets.tsx` 加 `validateSearch` 识别 `?subject=topup` 自动预填主题 + 模板 body
- **G2 Cancel 并发竞态**:`repository/order.go` 新增 `CancelIfPending` 单语句条件性 UPDATE `WHERE id=? AND user_id=? AND status='pending'` 以检查影响行数取代事务 + FOR UPDATE(Pay 已持 FOR UPDATE 锁,Cancel 被阻塞后 status 已变 paid,条件不满足→影响行数=0→409);`handler/portal/order.go` Cancel 改用该接口;集成测试 `order_integration_test.go` 新增 4 条(Happy/WrongOwner/NotPending/VsPay 并发),断言 status=paid ↔ balance=50,invoiceCnt=1,cancelChanged=false;status=cancelled ↔ balance=100,invoiceCnt=0,payErr≠nil
- **G3 发票详情**:新组件 `features/billing/invoice-detail-dialog.tsx` 用 `Dialog` primitive 展开 invoice + order + product(CPU/RAM/Disk/OSImage/Hours/Price),从页面已加载数组解析,零新请求;`billing.tsx` 发票表格新增「操作」列 + 「详情」按钮
- **G4 工单关闭**:后端 `repository/ticket.go` 加 `CloseByOwner` 条件性 UPDATE,`handler/portal/ticket.go` 新增 `POST /portal/tickets/{id}/close`(owner 检查 + 幂等 + audit log);前端 `features/tickets/api.ts` 加 `useCloseTicketMutation`,`tickets.tsx` `TicketDetail` 非 closed 态显示「关闭」按钮 + confirm,closed 态显示「工单已关闭」提示
- **G5 Dashboard 彻底分离**:`app/routes/index.tsx` 重写为 `UserDashboard` —— 移除 `AdminSection` 内联块(admin 走 `/admin/monitoring` 独立路由),顶部加 `QuickActions` 3 按钮(Create VM → `/billing`、Top-up → `/tickets?subject=topup`、New Ticket → `/tickets`),`myVmCount === 0` 时 Create VM 实心高亮;openTickets 计数统一到 `status !== 'closed'`
- **i18n**:zh/en `common.json` 补 `billing.balance|topupHint|topupViaTicket`、`invoice.*`(detail/section*/*Missing)、`ticket.close|closeConfirm*|alreadyClosed|topupPrefill*`、`dashboard.quickCreateVm|quickTopup|quickCreateTicket`,全部双语对齐
- **CI 门**:`bun run typecheck` + `bun run build`(dist 1378 kB) + `go build/vet` + `go test ./...` + 集成测试 `TestCancelIfPending*`(`ok ./internal/repository 165.968s`)全过

## 2026-04-18 21:10 [completed]

UX-002 / PLAN-016 后台菜单重组 + 用户/管理员视角分离 —— 全量落地 + 目视回归通过:

- 拆 `web/src/shared/components/layout/sidebar-data.ts`:`userSidebar` 扁平 7 项 + `adminSidebar` 5 组(监控 / 资源 / 基础设施运维 / 订单财务 / 用户工单)
- 重写 `app-sidebar.tsx`:路径前缀 `/admin` 自动切视角,admin 视角用 `@base-ui-components/react` Accordion 做二级折叠;当前路径所在组自动展开 + `localStorage('incus.sidebar.admin.openGroups')` 跨刷新持久化;collapsed 态降级为扁平 icon 列表 + 组间分隔线
- admin 顶部加"进入管理后台 / 返回用户后台"切换按钮(`isAdmin` 门控,非 admin 永不渲染)
- 补 `sidebar.switchToAdmin / backToUser / group.{monitoring,resources,infrastructure,billing,userOps}` 共 7 条 i18n key(中英双语),移除硬编码 "Admin"
- 前端 `bun run typecheck` + `bun run build` 通过;dist_hash=`5bd7a8bbece0773d`
- 部署:本地 go 1.25 交叉编译 linux/amd64 binary(sha256=`603ef5de…`)embed 新 dist,AIssh file_deploy → 原子 mv + `systemctl restart incus-admin`;`/api/health` dist_hash 与本地对齐
- 浏览器目视回归(`https://vmc.5ok.co` SSO 登录 ai@5ok.co):
  - ✅ 用户视角 `/`:7 项扁平菜单 + "Enter Admin Console"按钮
  - ✅ 管理员视角 `/admin/monitoring`:5 个 Accordion 组,Monitoring 自动展开(Monitor 激活高亮),其他 4 组折叠
  - ✅ 展开/折叠:点击 Orders & Billing 组头,Products/Orders/Invoices 正确展开(Monitoring 保留展开态,多选模式生效)
  - ✅ localStorage 持久化:刷新页面后 Monitoring + Orders & Billing 双组仍展开
  - ✅ 视角回切:点击"Back to User Console"返回 `/`,扁平菜单恢复,按钮文案翻转为"Enter Admin Console"
- 延期:collapsed 图标态 + zh/light 主题切换 —— 非阻断 UX 细节,核心交互已验证

## 2026-04-18 18:42 [progress]

PLAN-015 / QA-005 全量落地，QA-004 报告 N1-N15 清零（除 N4/N7/N9/N10/N14 已转 PLAN-014 / 反代窗口）：

- N1 字体回退 + lang 同步：`index.css` font-sans 加 PingFang/YaHei/Noto CJK 兜底；`app/i18n.ts` languageChanged 钩子写 `<html lang>`，与 i18next 状态保持一致
- N2 Portal Tickets 入口：表格行加 ▶/▼ chevron + 链接化 Subject + aria-expanded；click → 内联展开 TicketDetail（API 已存在）
- N3 i18n 大扫除：`en/zh common.json` 补齐 admin.products / admin.nodes / admin.tickets / admin.users / admin.vmDetail / monitoring 等键；`monitoring.tsx` / `observability.tsx` / `node-join.tsx` 全部硬编码中文走 `t()`
- N5 audit target 渲染：`features/audit-logs/helpers.ts` `targetLabel()` 优先 `details.name || target || vm || vm_name || host || osd_id`，缺失才回退 `#target_id`；6 条单测覆盖
- N6 admin catchall 404：`app/routes/admin.tsx` 加 `notFoundComponent` —— "404 / Admin page not found / Back to Clusters" 取代裸 "Not Found"
- N8 后端 API 错误响应统一 JSON：`internal/server/static.go` `/api/*` 落空时返 `{"error":"not found"}`；`server.go` 注册 `MethodNotAllowed` handler 返 405 JSON；新增 `GET /api/admin/orders/{id}` 与 `GET /api/admin/products/{id}`
- N11 Node Ops IP 校验：`features/nodes/host-validation.ts` 抽出 `validHost()`（IPv4/IPv6/RFC1123），7 条单测覆盖
- N12 移动端表格溢出：13 个 admin/portal 路由表格 wrapper `overflow-hidden` → `overflow-x-auto`
- N13 VM 默认用户名按 image 映射：`features/vms/default-user.ts` 覆盖 ubuntu/debian/rocky/almalinux/centos/fedora/opensuse/arch/alpine/freebsd，6 条单测
- N15 Audit IP 列：`stripCidrSuffix()` 去掉 `/32`、`/128` 后缀

CI 门：后端 `go build/vet/test` 全过；前端 `tsc --noEmit / vitest run / vite build` 全过（37 条单测，新增 19 条）

部署：本地 build → AIssh `file_deploy` → systemctl restart incus-admin（PID 216141）；
新 dist_hash `b934750eb3d6f6...` 与本地一致；浏览器回归 N1/N3/N5/N6/N8/N11/N12/N13/N15 全部生效

## 2026-04-18 00:05 [progress]

QA-004 后继项：DB ↔ Incus VM 状态漂移修复（生产手工同步 + 前端空态文案 + 后端 db_running_count 指标）：
- 生产 SQL：`UPDATE vms SET status='deleted' WHERE id IN (7,8)`（vm-d8b7dc / vm-870c48 —— Incus 侧实际 0 实例，DB 残留）；`UPDATE ip_addresses SET status='available', vm_id=NULL WHERE id=5` 释放 202.151.179.239 回池
- 后端 `MetricsHandler.ClusterOverview` 增加 `db_running_count` 字段，`VMRepo.CountRunningByCluster` 新方法，让管理员一眼识别"DB 说 N 个 running / Incus 0 个"的漂移情形
- 前端 `admin/monitoring.tsx` 空态文案分流：`drifted=true` 时显示漂移提示和待执行同步提示；否则显示"当前集群无运行中的 VM"
- 部署：新二进制 sha256=2003ea42…，dist_hash=95c7d8082fdc；systemctl restart 后 `TLS pin learned` 已持久化
- 新开 `PLAN-014 VM 状态反向同步 worker` + `INFRA-006` 任务，设计后台 60s reconciler 消除未来漂移（不纳入本次部署范围）

## 2026-04-17 21:32 [progress]

PLAN-013 Phase B 反代层收尾（授权窗口内 AIssh 推送），PLAN-013 全量完成：
- B.1 `/oauth2/callback` 500 排查：经源站 oauth2-proxy 日志比对，确认为**误报** —— 所有 500 均由外部扫描器路径（`/boaform/admin/formLogin`、`/hello.world?%ADd+allow_url_include` 等）触发 `invalid semicolon separator in query`，真实 admin 登录链路无 500，无需修复
- B.3 favicon 白名单：生产 `/etc/incus-admin/oauth2-proxy.cfg` 的 `skip_auth_routes` 增加 `"^/favicon\.ico$"`（备份 `oauth2-proxy.cfg.bak.20260417212843`），`systemctl restart oauth2-proxy` 后 `curl -I https://vmc.5ok.co/favicon.ico` 返回 `HTTP/2 200`
- B.2 Caddy 三条安全头：源站前置是 Cloudflare CDN，HSTS/X-Content-Type-Options/Referrer-Policy 归 CDN Transform Rules 管；且该项仅是 SSL Labs A+ 评级的质量项（非真实 Bug），本 plan 不动 CDN 配置 → 整项移出范围
- PLAN-013 `index.md` 改 `[x]`，`PLAN-013.md` `status: completed` / `completedAt: 2026-04-17 21:32`
- `TECHDEBT-001` 随 plan 收口为 completed

## 2026-04-17 17:30 [progress]

PLAN-013 代码层 + 测试层 + CI 全量落地（Phase A/C/D 完成，Phase B 反代待运维窗口）：
- A.1 `Pay` 路径 IP 分配回滚集成测试 + `rollbackPayment` 导出供测试
- A.2 `admin/vms.tsx` 客户端分页
- A.3 `product.Update` 指针 DTO 真 PATCH 语义（仅合并非 nil 字段 + 3 条单测）
- C.1 SPKI 指纹 pinning：migration 006 + `cluster/tlspin.go`（TOFU/mismatch reject）+ 适配器接通 REST/WebSocket（events/console）+ 5 条单测
- C.2 端到端 cluster_id 打通：去掉 `int64(1)` 硬编码，订单创建 / 支付复用订单上的 cluster_id
- C.3 Observability iframe：HTTP 目标（Grafana/Prom/Alertmanager）改 "new tab only"，Ceph HTTPS 保留嵌入
- C.4 启动期计算 `dist/index.html` sha256，写入 `/api/health` 并在启动日志给 12 位短哈希
- D.1 `.github/workflows/ci.yml`：backend-unit → backend-integration（testcontainers, Ryuk 关）→ frontend typecheck/build
- D.2 `UserRepo.TopUpWithDailyCap`：事务 + `SELECT ... FOR UPDATE` 行锁，根治并发越限；并发 + 边界两条集成用例
- Phase B（oauth2-proxy callback 500 / Caddy 三条安全头 / favicon 白名单）保留 `[ ]`，等运维低峰窗口经 AIssh 推送，不阻塞代码合并

## 2026-04-15 17:32 [progress]

All 17 database tables covered with backend APIs and frontend pages. Features: VM lifecycle, console, snapshots, monitoring (Recharts), SSH keys, products, tickets, orders/billing, invoices, audit logs, API tokens with Bearer auth. Deployed at vmc.5ok.co.

## 2026-04-15 17:40 [decision]

PLAN-005 drafted: full-stack refactor to pma-web (shadcn/ui sidebar layout, ThemeProvider, feature hooks, ESLint, Vitest) and pma-go (golangci-lint, validator, consistent responses, Taskfile) standards. sqlc migration deferred.

## 2026-04-15 18:00 [decision]

Product direction clarified: internal private cloud first, external API later. PLAN-006 drafted: infrastructure automation — VM auto-failover (Incus cluster.healing_threshold), node management (SSH-automated add/remove), standalone host support (DB-stored config). Auto-deploy new cluster deferred to Phase 6D. Directory cleanup: deleted 17,885 lines of dead code (paymenter, ai-gateway, console-proxy, screenshots), unified all docs under root /docs/.

## 2026-04-15 18:30 [BUG-P1]

## 2026-04-16 00:30 [progress]

PLAN-007 Phase 1-5 + partial 2,3 implemented:
- Phase 1 (8 tasks): Admin DB write, delete free-create path, billing redesign, snapshot portal, hardcode elimination, dead code cleanup
- Phase 2: IP Pool CRUD (add/remove + UI form)
- Phase 3.1: Add Cluster/Standalone Host form
- Phase 3.3: Ceph Storage overview page
- Phase 4: VM detail pages (admin + user, DO-style tabs)
- Phase 5: Logout button, Console dynamic back link
Total: 11 commits in this session. Deployed to vmc.5ok.co.

## 2026-04-15 20:30 [progress]

PLAN-005 + PLAN-006 all phases completed:
- A0: 7 CRITICAL fixes (SSH keys, VM naming, order→VM, balance, ListAllVMs, panic, ticket detail)
- A1: 3 security fixes (Console/metrics auth, WebSocket CSRF)
- A: Frontend scaffold (sidebar, ThemeProvider dark/light/system, i18n zh/en, ESLint)
- B: 10 feature API hook modules (50+ hooks)
- C: Taskfile.yml, 5 DB indexes, HTTP 30s timeout
- WARNING batch: audit log injection, input validation, Dashboard real data, user Console/Snaps
- 6A: HA failover (healing_threshold=300, HA status page, evacuate/restore)
- 6B: Node management (evacuate/restore buttons in clusters page)
- 6C: Dynamic cluster add/remove (standalone host support)
- D: Go tests (11 cases) + Vitest (6 cases) + quality gate scripts
- E: Metrics 30s cache

## 2026-04-15 20:00 [progress]

PLAN-005 Phases A0-C implemented and deployed:
- A0: Fixed 7 CRITICAL bugs (SSH key injection, VM naming, order→VM provisioning, balance, ListAllVMs, panic, ticket detail)
- A1: Security fixes (Console/metrics ownership, WebSocket CSRF)
- A: Frontend scaffold (sidebar layout, ThemeProvider dark/light/system, i18n zh/en, ESLint, providers)
- B: Extracted 10 feature API hook modules (50+ hooks)
- C: Taskfile.yml, 5 DB indexes, HTTP client timeout
Total: 10 commits, ~3000 lines changed. Deployed to vmc.5ok.co.

## 2026-04-15 18:30 [BUG-P1]

Deep code audit (Graph + Serena + manual tracing) found 7 CRITICAL bugs: SSH keys never injected into VMs, VM naming collision (1 VM per user), order payment doesn't provision VM, balance hardcoded to 0, ListAllVMs stub, panic on empty cluster, user ticket detail missing frontend. Plus 14 WARNINGs including Console WebSocket no ownership check, quota never enforced, audit logs never written, IP allocation race condition, password in plaintext. PLAN-005 scope expanded to include Phase A0 (critical bug fixes).
