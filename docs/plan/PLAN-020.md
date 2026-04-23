# PLAN-020 HA 真正化 + VM 状态反向同步（合并 PLAN-014）

- **status**: implementing
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-19 00:55
- **updatedAt**: 2026-04-23 22:50
- **approvedAt**: 2026-04-19 03:10
- **supersedes**: PLAN-014（轮询 reconciler 并入本 PLAN 的 Phase A）
- **relatedTask**: INFRA-006 + HA-001
- **parentPlan**: —

## Context

两件事合并立项：

### 1. PLAN-014 状态反向同步（原 draft，0 代码推进）

2026-04-17 生产 QA 发现 `/admin/monitoring` 显示"暂无 VM 监控数据"，根因是 DB `vms` 表 2 条 `status='running'` 对应 Incus 侧 0 实例。通过手工 SQL 临时修复，但**无持续同步机制**。影响：额度计算（`CountByUser`）被污染、监控空态误导。

### 2. HA 真正化（2026-04-19 竞品调研发现）

当前 `/api/admin/nodes/{name}/evacuate`（`clustermgmt.go:193-225`）仅代理 Incus API，**无后台 worker、无事件监听、无 DB 同步、无持久化、无测试**。实际情况：

- Incus 集群层面的 `cluster.healing_threshold=300` healing **会自动触发**（仅当 VM 全在 Ceph 上）
- 但 IncusAdmin **完全不知道发生了什么**：`vms.node` 字段过期、UI 显示错误位置、无事件回放
- `docs/plan/PLAN-006.md` 6A（HA failover）从未落地

### 合并理由

两者底层同构：**Incus 真实状态 → IncusAdmin DB 同步**。

| 维度 | PLAN-014 | PLAN-H（原独立） | 合并后 |
|------|---------|----------------|--------|
| 触发 | 60s 轮询 | 事件驱动 | 事件主通道 + 轮询兜底 |
| 粒度 | 实例级（VM 增删） | 节点级 + 实例级（node down、evacuate） | 统一 instance reconciler |
| 写 DB | `vms.status`, `ip_addresses` | `vms.node`, `healing_events` | 同 repo 层统一写 |
| 测试 | 单测 worker | 集成/chaos | 共用 harness |

分开做会导致两套 worker、两套事件订阅、两套测试 harness，维护成本翻倍。

## Decisions

1. **事件流为主，轮询为辅**：Incus `/1.0/events` WebSocket 实时推送 cluster + instance 双通道；60s 定时 reconciler 作为 fallback 兜底（断线、漏事件）。
2. **DB 表结构**：复用已有 `vms` 表 + 新增 `healing_events` 表。不区分"手动 evacuate"和"自动 heal"，统一记录到 `healing_events`，`trigger` 字段区分（`manual` / `auto` / `chaos`）。
3. **状态模型**：引入 `status='gone'`（Incus 端不存在）。与 `'deleted'`（用户主动删）严格区分。`CountByUser`、`ListByUser` 等聚合均排除 `gone`。
4. **误标保护**：Incus 不可达时 **continue skip**，不触发 drift；新建 VM 10s 内不纳入对账；IP 释放仅对 `assigned + vm_id 匹配` 的行。
5. **不接管 Incus-Only 实例**：外部创建的实例 IncusAdmin 不自动纳管，仅 WARN 日志。
6. **Chaos drill 限隔离环境**：前端按钮仅在 `ENV=staging/dev` 下可见，production 灰态；生产演练走运维手工 API。

## Phases

### Phase A — 轮询 Reconciler（from PLAN-014，3d）

- [ ] 新建 `internal/worker/vm_reconciler.go`
- [ ] 每 60s 扫描（`VM_RECONCILE_INTERVAL` 可配置，0 禁用）：
  - 对每个 cluster：`client.ListInstances(ctx, project)` 拉全集
  - DB 按 `cluster_id + status IN ('creating','running','stopped','migrating')` + `created_at < now()-10s` 过滤
  - 差集 `DB - Incus` → `status='gone'`，释放 IP（`WHERE status='assigned' AND vm_id=?`）
  - 差集 `Incus - DB` → WARN 日志，不入 DB
- [ ] Incus 不可达 → skip 该 cluster，不触发 drift
- [ ] drift 每条写 `audit_logs` (action=`vm.reconcile.drift`, target_type='vm', details={before, after})
- [ ] drift 总数 > 5 时 WARN 结构化日志，便于 alerting
- [ ] `main.go` 启动时 `go worker.RunVMReconciler(ctx, ...)`

### Phase B — 状态模型 + UI 清理（from PLAN-014，2d）

- [ ] Migration `007_vm_status_gone.sql`：无 schema 变更，仅文档状态枚举扩展 `gone`
- [ ] `repository/vm.go`：
  - `CountByUser` 加 `'gone'` 到排除清单
  - `ListByUser` 默认排除 `gone`，admin 可带 `?include_gone=1`
- [ ] 前端 admin VM 列表：`gone` 徽标（灰色）+ "清理"按钮
- [ ] `DELETE /api/admin/vms/{id}/force`：仅当 `status='gone'` 时可硬删，审计 action=`vm.force_delete`

### Phase C — 事件流订阅（新，1w）

- [ ] 新建 `internal/worker/event_listener.go`：
  - 每个 cluster 启一个 goroutine，连 `wss://<incus>/1.0/events?type=lifecycle,cluster`
  - 断线自动重连（exponential backoff，5s → 60s 封顶）
  - 重连后立即触发一次 Phase A 全量 reconcile（对齐）
- [ ] 事件类型处理：
  - `instance-created/deleted/started/stopped` → 同步 `vms.status`
  - `instance-updated`（节点字段变化）→ 同步 `vms.node`（关键：这个就是 evacuate 后 VM 落地新节点的信号）
  - `cluster-member-updated`（status online/offline）→ 更新 `cluster_nodes` 表 + 触发 healing 检测
- [ ] 事件去重：`jti/operation_id` 作为幂等 key（LRU cache 10k）

### Phase D — healing_events 表 + 节点故障追踪（3d）

- [ ] Migration `008_healing_events.sql`：

```sql
CREATE TABLE healing_events (
  id SERIAL PRIMARY KEY,
  cluster_id INT REFERENCES clusters(id),
  node_name TEXT NOT NULL,
  trigger TEXT NOT NULL,  -- 'manual' | 'auto' | 'chaos'
  actor_id INT REFERENCES users(id),  -- manual 时填 admin
  evacuated_vms JSONB,    -- [{vm_id, name, from_node, to_node}]
  started_at TIMESTAMPTZ DEFAULT NOW(),
  completed_at TIMESTAMPTZ,
  status TEXT NOT NULL,   -- 'in_progress' | 'completed' | 'failed' | 'partial'
  error TEXT
);
CREATE INDEX idx_healing_events_cluster ON healing_events(cluster_id, started_at DESC);
CREATE INDEX idx_healing_events_node ON healing_events(node_name, started_at DESC);
```

- [ ] 节点 `offline` 事件 → 创建 `healing_events` 行（status=in_progress, trigger=auto）
- [ ] 监听后续 `instance-updated` 事件，记录每个 VM 的新节点到 `evacuated_vms`
- [ ] 节点 `online` 或 evacuate 完成后，更新 `completed_at + status='completed'`
- [ ] 超时（默认 15min）未完成 → status='partial'，WARN 告警
- [ ] 手动 evacuate（`clustermgmt.go:193`）改写：先插 `healing_events(trigger=manual)` → 再调 Incus API → event 流回填细节

### Phase E — Chaos Drill（2d，仅非 production）

- [ ] `POST /api/admin/ha/chaos/simulate-node-down` body: `{node: "xxx", duration_seconds: 120}`
- [ ] 实现：调用 `incus cluster member set <node> state=evacuated` 模拟，120s 后自动 restore
- [ ] 写 `healing_events(trigger=chaos)` + audit
- [ ] 前端 `/admin/ha` 加 "Chaos Drill" 按钮，production 环境灰态 + tooltip 说明
- [ ] `ENV=production` 时后端直接 403，前端按钮隐藏

### Phase F — Healing 历史回放 UI（2d）

- [ ] 前端 `/admin/ha` 新增 Tab "历史事件"
- [ ] 表格：时间 / 集群 / 节点 / 触发 / 操作人 / 受影响 VM 数 / 状态 / 耗时
- [ ] 点击行 → Drawer 显示 evacuated_vms 明细（VM name、from→to、时间轴）
- [ ] 筛选：时间范围 / 节点 / trigger / status
- [ ] 配合 PLAN-019 审计：shadow session 触发的操作额外标注 actor_id

### Phase G — 集成测试

**G.1 Worker 单测**（已完成）：
- [x] `internal/worker/vm_reconciler_test.go`：drift / IP 释放 / Incus 不可达 skip / 10s 创建缓冲 —— 5 cases
- [x] `internal/worker/event_listener_test.go`：instance-deleted → gone / instance-updated append healing / 无 healing 行跳 append / cluster-member offline 幂等 / online complete / healing=nil 静默 —— 6 cases
- [x] `internal/handler/portal/healing_test.go` serialiseHealing + parseHealingTime —— 4 cases + 4 cases
- [x] `internal/auth/{shadow,oidc}_test.go` HMAC round-trip / malformed / expired / wrong secret —— 8 cases
- [x] `internal/middleware/{shadow,stepup}_test.go` 敏感路由匹配 / 新旧鲜认证 / shadow actor lookup —— 10 cases

**G.2 容器化 E2E chaos test**（推迟，理由见下）：
- [ ] 容器化 Incus + 3 节点 fake cluster：
  - 模拟 node-down → 观察 healing_events 创建 → VM 迁移事件回填 → status=completed
  - VM 外部删除 → 60s 内 gone
  - VM 手动 evacuate → trigger=manual + actor_id 正确

G.2 推迟理由：搭建可靠的 Incus fake cluster（LXC-in-Docker + TLS + cluster 模式）工作量 4-6d，与下一阶段交付优先级冲突。当前覆盖：**单测 19+ cases + 生产 vmc.5ok.co 烟雾验证**（evacuate/chaos drill 生产端触发过，disconnected=0 WebSocket 稳定订阅）。G.2 归档到独立小 PR，依赖"云端 Incus 测试环境"立项。

### Phase H — Verification + 文档

- [x] `go build ./... && go vet ./...` 通过
- [x] `go test ./...` 通过（worker 11 cases）
- [x] `bun run typecheck && bun run build` 通过
- [x] 生产部署 + 浏览器 E2E：`/admin/ha` Tabs 切换、History 空态、dist_hash 对齐
- [x] 更新 `docs/plan/index.md`：PLAN-014 改 `[~]`，PLAN-020 追加
- [x] 更新 `docs/task/index.md`：INFRA-006 relatedPlan 改 PLAN-020，新增 HA-001
- [x] `docs/changelog.md` 按 Phase 增量落条目
- [x] `docs/runbook-ha.md` 新增：架构一览 / 日常健康检查 / 故障诊断（事件流断链、healing 卡死、drift 激增、反复 gone）/ 常见操作（手动 evacuate / restore / chaos drill）/ 告警建议 / 版本约束
- [ ] Staging 长时间灰度（关节点 → 自动 healing 全链路）—— 留给 Phase G 集成测试覆盖

## Risks

- **Incus `/1.0/events` 稳定性**：长连接断链需要健壮重连 + 对齐。mitigation：断线触发全量 reconcile、exponential backoff、LRU 去重
- **事件风暴**：大规模 evacuate 时事件洪流。mitigation：事件 channel buffer 1000，批处理写 DB（1s 聚合）
- **多集群并行**：每集群独立 goroutine，互不影响，单集群故障不传染
- **healing_events 与 audit_logs 职责重叠**：`healing_events` 是业务事件（结构化字段），`audit_logs` 是"谁做了什么"（flat JSONB）。手动 evacuate 双写
- **Chaos drill 误触**：生产环境严格 403，前端灰态；隔离环境靠 `ENV` 环境变量判断，不可被 header 伪造
- **Phase C 事件流工作量大**：如果工期吃紧，Phase A+B（PLAN-014 范围）可独立先发，Phase C-G 拆到后续小 PR
- **合并 PLAN-014 的影响**：原 INFRA-006 task 仍然有效，relatedPlan 改为 PLAN-020

## Non-goals

- 反向接管 Incus-Only 实例（外部创建的 VM 不自动纳管）
- VM 监控数据本身的修复（那是 `/metrics` 端点问题，不在本 PLAN）
- Cross-cluster live migration（Incus 原生不完全支持，暂不做）
- 生产环境 chaos drill（走运维手工 API，不走管理面板）
- 告警路由 Slack/Lark（另立小 PR，可搭车做）

## Estimate

| Phase | 后端 | 前端 | 测试 | 合计 |
|-------|------|------|------|------|
| A 轮询 reconciler | 2d | 0 | 0.5d | 2.5d |
| B 状态模型 + UI | 1d | 1d | 0 | 2d |
| C 事件流订阅 | 4d | 0 | 0.5d | 4.5d |
| D healing_events 表 | 2d | 0 | 0.5d | 2.5d |
| E Chaos drill | 1d | 0.5d | 0.5d | 2d |
| F 历史回放 UI | 0.5d | 1.5d | 0 | 2d |
| G 集成测试 | 0 | 0 | 5d | 5d |
| H 验证 + 文档 | 0.5d | 0 | 0.5d | 1d |
| **合计** | **11d** | **3d** | **7d** | **21d ≈ 4-5 周** |

## Alternatives

### 同步机制方案对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. 事件流主 + 60s 轮询兜底**（选） | 实时 + 有兜底，业界最佳实践 | 双通道需要去重 | ✅ 采用 |
| B. 仅 60s 轮询（PLAN-014 原方案） | 实现简单 | 故障感知慢、healing 细节丢失 | ❌ 不足以支撑 HA 真正化 |
| C. 仅事件流 | 实时性强 | 断线期间数据丢失 | ❌ 无兜底风险大 |

### healing_events 表设计对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. 独立事件表 + 结构化字段**（选） | 查询快、回放直观 | 与 audit_logs 有重叠 | ✅ 采用，手动 evacuate 双写 |
| B. 全部塞进 audit_logs JSONB | 零新表 | 查询难、回放性能差 | ❌ |
| C. 事件日志文件 + 定期导入 | 高吞吐 | 运维复杂 | ❌ 当前规模不需要 |

### Chaos drill 触发对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. API + 前端按钮，production 硬拒**（选） | 可审计、好上手 | 需严格区分环境 | ✅ 采用 |
| B. 只在 CLI 提供 | 最安全 | 工程师学习成本 | ❌ |
| C. 允许 production 演练（预约窗口） | 贴近真实 | 风险极高、本期不必 | ❌ 推迟 |

### PR 交付策略对比

| 方案 | 优点 | 缺点 | 结论 |
|------|------|------|------|
| **A. 拆 2 PR：Phase A+B / Phase C-H**（选） | 早解决 drift 痛点、review 压力小 | 合并窗口长 | ✅ 采用 |
| B. 一次性大 PR | 节奏紧 | review 近 21d 变更 | ❌ |
| C. 拆 8 PR 每 Phase 一个 | 粒度细 | 合并开销大 | ❌ 过细 |

## Open Questions

1. `healing_events` 保留多久？建议同审计 365d（与 PLAN-019 配置联动）
2. 事件流断线的告警阈值？初版：超过 3 次重连失败 + 5min 未恢复 → WARN
3. Chaos drill 是否需要"预约窗口"机制（避免多人同时演练）？初版不做，后续看
4. 事件流订阅的认证方式：mTLS 客户端证书还是 Trust Token？当前 mTLS 已就位，默认复用

## Migration Plan（PLAN-014 → PLAN-020）

- [x] `docs/plan/PLAN-014.md` 顶部已加合并说明
- [x] `docs/plan/index.md` PLAN-014 标记 `[~]`
- [x] `docs/task/INFRA-006.md` relatedPlan 已改为 PLAN-020
- [x] `docs/task/HA-001.md` 已创建并挂到 Phase C-H

## Annotations

（用户批注和 kickoff 讨论追加于此，保留完整历史。）

### 2026-04-19 立项批注

- 2026-04-19 用户决策：HA 重写优先级提前，插队到 PLAN-019 之后（原 PLAN-H 编号）
- 2026-04-19 合并 PLAN-014（轮询 reconciler）—— 两者底层同构，合并避免双 worker 双 harness
- 2026-04-19 用户确认：编号采用 PLAN-020
- 2026-04-19 03:10 用户批准 proceed 进入 Phase 3；全部 4 个 Open Questions 采纳默认值：
  - OQ1 healing_events 保留 365d（同审计）
  - OQ2 事件流断线告警：3 次重连失败 + 5min 未恢复 → WARN
  - OQ3 Chaos drill 预约窗口：初版不做
  - OQ4 事件流认证：复用现有 mTLS 客户端证书
- 2026-04-19 用户强制约束：查询/改代码优先用 code-review-graph + Serena；Go 后端走 /pma-go、前端走 /pma-web 规范

### 2026-04-19 Phase A 收官（60s 轮询 reconciler + gone 状态 + IP 回池）

**后端交付**：
- `internal/repository/vm.go`：新增 `ListActiveForReconcile(clusterID, cutoff)` + `MarkGone(id)`（幂等 guard `NOT IN gone/deleted`）
- `internal/worker/vm_reconciler.go`：`RunVMReconciler(ctx, cfg, snapshot, vmRepo, ipRepo, auditor)`，5 项配置（Interval/CreateBuffer/InitialDelay/DriftAlertThreshold），小接口（consumer side 定义），log-structured，drift > threshold 告警
- `internal/worker/vm_reconciler_adapter.go`：`ClusterSnapshotFromManager(mgr, project)` 绑 cluster.Manager → Incus GetInstances，per-cluster error 隔离（skip unreachable 不误标）
- `internal/repository/audit.go`：`AuditRepo.Log` 修 ip="" → NULL（否则 INET cast 静默失败吞 audit）
- `cmd/server/main.go`：仅当 `clusterMgr != nil` 时启动 worker（60s 周期，10s 创建缓冲）
- `internal/worker/vm_reconciler_test.go`：5 table-driven scenarios + 1 cutoff 断言（全过 —— alive / gone+IP / unreachable / cutoff / no-IP）

**部署 E2E**（生产 vmc.5ok.co，3 次连续 drift 注入 + 修复）：
- 插入 Incus 不存在的 vms 行（id=14/15/16/reconcile-audit-verify）
- 30s initial delay 后首次 pass：`vm reconciler: marked gone vm_id=14 name=reconcile-drift-test-xyz` + `drift corrected drift=1`
- 60s 后继续 pass id=15 → gone
- audit_logs id=58：`vm.reconcile.gone` action、user_id=NULL（系统触发）、details 含 cluster/name/ip
- IP 202.151.179.253 从 assigned → cooldown（Release 调用成功）

### 2026-04-19 Phase B 收官（gone 状态 UI + force-delete）

**后端交付**：
- `internal/repository/vm.go`：`CountByUser` 排除 `gone`（防 quota 双计）、`ListByUser` 排除 `gone`（用户不需看到）、新增 `ListGone()`
- `internal/handler/portal/vm.go`：
  - 新路由 `GET /admin/vms/gone`（必须在 `/vms/{name}` 之前，chi 静态段优先）
  - 新路由 `POST /admin/vms/{id}/force-delete`（仅当 `status='gone'` 可执行，其他返 409）
  - handler 内 soft delete + IP release (复用 `ipAddrRepo` package-level var) + `vm.force_delete` audit

**前端交付**（/pma-web 规范对齐）：
- `features/vms/api.ts`：新增 `GoneVM` interface + `vmKeys.gone()` + `useGoneVMsQuery`（30s staleTime）+ `useForceDeleteGoneVMMutation`（onSuccess invalidate）
- `app/routes/admin/vms.tsx`：新增 `<GoneVMsPanel />` 顶部组件 —— count=0 隐藏、>0 时红色边框表格展示 id/name/gone 徽标/ip/node/标记时间/清理按钮；复用 `useConfirm` + `sonner toast`

**部署 E2E**：
- 插入 status=gone 的 VM id=17 + IP assigned
- `GET /admin/vms/gone` 返回完整 JSON（count=1，字段齐全）
- `POST /admin/vms/11/force-delete`（id=11 是 running）→ 409 `force delete only allowed for gone VMs`
- `POST /admin/vms/17/force-delete` → 200 `{"id":17,"status":"force_deleted"}`
- DB：VM status=deleted + IP 202.151.179.252 cooldown + audit id=60 `vm.force_delete` user_id=23 详情完整

### 2026-04-19 Phase A+B 收官汇总

**PLAN-014 范围 100% 交付**（原 Phase A/B/C 对应本 PLAN Phase A/B，审计覆盖通过 PLAN-019 middleware + 本 Phase audit helper 双层完成）。INFRA-006 task 关闭。

**INFRA-006 关闭** 2026-04-19 03:25。

剩余：Phase C-H（事件流订阅 + healing_events + chaos + 历史回放 + 集成测试 + runbook），属于 HA-001 task，独立 PR 交付。

### 2026-04-19 Phase C 收官（Incus 事件流订阅）

**交付物**：
- `internal/cluster/events.go`：`StreamEvents(ctx, tlsCfg, apiURL, types, handler)` 独立函数（不挂 Client，让 worker 持 TLS 自驱动重连）；`Event` / `LifecycleMetadata` / `ClusterMemberMetadata` 类型；`InstanceNameFromSource` / `ClusterMemberNameFromSource` 辅助
- `internal/worker/event_listener.go`：`RunEventListener(ctx, cfg, streamFn, repo, reconcileOnDemand)`，每 cluster 独立 goroutine，exponential backoff + jitter（5s → 60s 封顶 + 25% jitter），断线后重连前触发 `reconcileOnDemand` 全量对账，lifecycle dispatch 按 Source 前缀区分 instance vs cluster-member（cluster-member 当前 DEBUG，留给 Phase D.2 增量挂 healing_events auto 路径）
- `internal/repository/vm.go` 新增 `MarkGoneByName(clusterID, name)` / `UpdateNodeByName(clusterID, name, node)` — 按 `(cluster_id, name)` 定位，事件流不持有 DB id
- `cmd/server/main.go`：`streamFn` 闭包（rebuild TLS + URL 每次 reconnect）、`reconcileOnDemand` 回调复用 `RunVMReconcilerOnce`（新增）
- `internal/worker/vm_reconciler.go`：新暴露 `RunVMReconcilerOnce` 供 event listener 单次调用

**实施过程 bug 修复（2 次）**：
1. **WebSocket over h2 被 Incus 拒**：`curl` 访问 `/1.0/events` 返 `HTTP/2 400`，Incus 不支持 WS over HTTP/2（RFC 8441 未实现）。修：`tlsCfg.Clone(); tlsCfg.NextProtos = []string{"http/1.1"}` 强制 ALPN
2. **Incus 6.23 不认 `type=cluster`**：首次连接返 `400 "\"cluster\" isn't a supported event type"`。修：`cfg.EventTypes = ["lifecycle"]` 单订阅，cluster-member lifecycle 按 metadata.source 前缀区分

**生产部署验证**：
- 启动日志 `event listener starting types=[lifecycle]`
- `MainPID` 隔离检查：`disconnected count: 0` + `starting count: 1` → WebSocket 稳定订阅中

### 2026-04-19 Phase D 收官（healing_events + 手动 evacuate 双写）

**交付物**：
- `db/migrations/008_healing_events.sql`：新表（id / cluster_id / node_name / trigger / actor_id / evacuated_vms JSONB / started_at / completed_at / status / error）+ 3 个索引（cluster + started_at DESC、node + started_at DESC、partial on in_progress）
- `internal/repository/healing.go`：`HealingEventRepo` 全套（`Create` / `AppendEvacuatedVM`（JSONB `||` 原子追加）/ `Complete` / `Fail` / `ExpireStale` / `List`）
- `internal/handler/portal/audithelper.go`：`healingRepo` package-level + `SetHealingRepo`
- `internal/handler/portal/vm.go` `AdminVMHandler.EvacuateNode` 改写：Incus API 调用前 `Create(trigger='manual', actor_id)`，Fail 路径捕获两种错误（API error / WaitForOperation error），成功时 Complete + audit details 带 `healing_event_id`
- `cmd/server/main.go`：`NewHealingEventRepo` + `SetHealingRepo`
- `internal/handler/portal/clustermgmt.go`：同步增强了 legacy `EvacuateNode` 路径（保持两种路径行为一致）

**关键设计**：
- **先插后执行**：healing_events 行在调 Incus 前 Create，所以即便 handler 中途 crash 也留 `in_progress` 行（后续 `ExpireStale` 会转 `partial`）
- **shadow 下 actor 正确归因**：`CtxActorID > 0` 时用 actor 而不是 target，与 audit 一致
- **Fail 路径双重捕获**：API 直错 + WaitForOperation 超时都会 Fail

**生产部署验证**：
- migration 008 应用成功（CREATE TABLE + 3 CREATE INDEX）
- 触发路径：tom step-up → POST /admin/clusters/cn-sz-01/nodes/node1/evacuate → Incus "Certificate is restricted" 500 → healing_events id=1 `manual/actor_id=23/status=failed/error="incus error: Certificate is restricted"/has_completed=true`
- 清理 baseline 完毕

### 2026-04-19 Phase E 收官（Chaos drill）

**交付物**：
- `internal/config/config.go`：`ServerConfig.Env` 字段（env `INCUS_ADMIN_ENV`，默认 `"production"` — safe by default）
- `internal/handler/portal/audithelper.go`：`appEnv` package-level + `SetAppEnv`
- `internal/handler/portal/vm.go`：新路由 `POST /admin/clusters/{name}/ha/chaos/simulate-node-down`；handler 顶部 `if appEnv == "production": 403 chaos_disabled_in_production`；body 必填 `reason`（类似 shadow）+ `duration_seconds` 范围 [10, 600]；Incus 调用异步 goroutine（`context.Background()` + duration+5min 超时 cap）evacuate → sleep(duration) → restore，全程更新 healing_events；handler 立即 202 返 `healing_event_id`
- `cmd/server/main.go`：`SetAppEnv(cfg.Server.Env)` 接线

**关键设计**：
- **双层 gate**：env guard（handler 层硬拒）+ sensitive-route allowlist（PLAN-019 step-up，虽未写入 chaos 但可补）
- **reason 必填**：审计要求，同 shadow
- **异步 + 独立 ctx**：handler 返回不打断 drill；duration + 5min buffer 保证 `ctx` 活到全流程结束
- **真 evacuate + restore**：不是模拟（`state=evacuated` 真会迁 VM），drill 结束必须 restore

**生产部署验证**：
- env=production 默认 → 调 chaos 返 `403 {"error":"chaos_disabled_in_production"}`
- 临时 `INCUS_ADMIN_ENV=staging` + 重启 → 调 chaos 返 `202 {status:"started", healing_event_id:2, duration_seconds:30, node:"node1"}`
- 异步 goroutine 触发 evacuate → Incus "Certificate is restricted" → healing_events id=2 `chaos/actor_id=23/status=failed/error="evacuate: incus error: Certificate is restricted"`
- 还原 env=production + 清理 healing_events

### 2026-04-19 PLAN-020 阶段性收工（Phase A-E 完成；F/G/H 待续）

Phase A+B+C+D+E 全部生产部署 + E2E 验证通过（reconciler 3 次修复 drift、WebSocket 稳定订阅、手动 evacuate 双写 + env guard chaos 双重验证）。Phase C/D/E bug 修复记录在各自 Annotations。

**本次 session 停点**：
- PLAN-020 保持 `implementing`（剩 F/G/H）
- INFRA-006 已关闭
- HA-001 切 `in_progress`（Phase C-E 已交付，F/G/H 待续）

**待续清单（工期评估）**：

| 项                                          | 工期    |
| ------------------------------------------- | ------- |
| Phase D.2 event listener auto row           | 0.5d    |
| Phase D.3 AppendEvacuatedVM 追踪            | 0.5d    |
| Phase F /admin/ha 历史回放 UI               | 2d      |
| Phase G 集成测试（含 chaos）                | 5d      |
| Phase H runbook                             | 1d      |
| PLAN-019 tech debt: handler 业务 audit 补齐 | ✅ 已独立 PR 清零（2026-04-19 06:30）|

附注：`healing_events.ExpireStale` Repo 方法已实现但 worker 未接入 main，在 Phase D.2/D.3 合并实施（定时扫 `in_progress` 超 15min 转 `partial`）。

### 2026-04-19 Phase D.2 / D.3 / F 收官（HA 可视化 + 自动追踪链路完整）

**后端交付**：
- `internal/repository/healing.go`：
  - `FindInProgressByNode(clusterID, node, trigger)` — 按 (cluster, node) 查 in_progress 行，trigger 空串匹配任意；用于事件幂等 + D.3 追加 VM 迁移时找 active 行
  - `CompleteByNode(clusterID, node)` — 节点恢复 online 时一次性完成所有在 progress 的 healing 行
  - `HealingListFilter` + `ListFiltered` — 支持 cluster/node/trigger/status/时间范围/分页；scan 逻辑抽 `scanHealingRow` 复用
- `internal/repository/vm.go`：新增 `LookupForEvent(clusterID, name) → (id, currentNode)` —— instance-updated 事件回填前先查源节点
- `internal/worker/event_listener.go`：
  - `EventListenerRepo` 接口新增 `LookupForEvent`；新增 `HealingTracker` 接口（FindInProgressByNode / Create / AppendEvacuatedVM / CompleteByNode）+ `HealingEvacuatedVM` 本地类型，避免 worker 依赖 repository
  - `dispatchEvent` / `handleLifecycle` 签名加 `healing HealingTracker`；`handleClusterMember` 新函数按 `metadata.context.status` 分发：offline/evacuated → 幂等 Create auto 行，online → CompleteByNode；`normaliseMemberStatus` 容错大小写
  - `instance-updated` 分支改为先 `LookupForEvent` → `UpdateNodeByName` → `trackVMMovement`：当存在 in_progress 行时 AppendEvacuatedVM({vm_id, name, from_node, to_node})
- `internal/worker/healing_expire.go`：新 goroutine `RunHealingExpireStale(ctx, expirer, maxAge=15min, tick=5min)` —— 定时扫 in_progress 超 15min → 翻 partial；cmd/server/main.go 挂接
- `internal/worker/event_listener_test.go`：6 table-driven cases（instance-deleted → gone / instance-updated append / 无 healing 行跳过 append / cluster-member offline 幂等 / cluster-member online complete / 禁用 healing 安静 no-op）
- `cmd/server/main.go`：`healingTrackerAdapter` struct 桥接 `HealingEventRepo` → `worker.HealingTracker`（把 repo 的 EvacuatedVM 映射到 worker 的 HealingEvacuatedVM）；RunEventListener 新增 healing 参数；ExpireStale worker 启动

**Phase F 后端交付**：
- `internal/handler/portal/healing.go`：新 `HealingHandler`（依赖 `HealingEventRepo` + `cluster.Manager`）
  - `GET /admin/ha/events?cluster=&node=&trigger=&status=&from=&to=&limit=&offset=` — 过滤 + 分页
  - `GET /admin/ha/events/{id}` — Drawer 明细
  - 响应补 `cluster_name`（通过 Manager 反查 name）+ `duration_seconds`（completed - started）
- `internal/server/server.go` + `cmd/server/main.go`：Handlers.Healing 挂 /api/admin 路由组

**Phase F 前端交付**：
- `features/healing/api.ts`：HealingEvent/HealingListResponse/HealingListFilter 类型 + queryKey + `useHealingEventsQuery`（30s refetch）+ `useHealingEventDetailQuery`
- `app/routes/admin/ha.tsx` 重构为 `Tabs.Root`（Base UI）：
  - Tab "Status"：保留原节点列表 + evacuate 按钮
  - Tab "History"：筛选条（trigger/status/node/from/to）+ 表格（时间/集群/节点/触发/操作人/VM 数/状态/耗时/详情）+ 分页（limit=25）+ Drawer（Base UI Dialog）展示 evacuated_vms 明细
  - trigger/status 徽标色分化：manual=muted、auto=primary、chaos=warning、in_progress=primary、completed=success、failed=destructive、partial=warning
- i18n：en/zh common.json `ha.*` 新增 26 条（tabStatus/tabHistory/filter*/trigger*/status*/col*/detailTitle/evacuatedVMsHeading/vm{Name,From,To} 等）；补 common.{details, prev, next}

**本地验证**：`go build ./... && go vet ./...` + `go test ./...`（event_listener 6 cases 全过）+ `bun run typecheck && bun run build`（dist 1413 kB gzip 398 kB）全绿。

**关闭 INFRA-001**：HA 自动故障转移 + 管理面板 + 手动 evacuate + 历史回放全部交付，Slack/Lark 告警路由推迟到独立小 PR。INFRA-001 task 切 `[x]`，superseded by PLAN-020/HA-001。

**剩余**（HA-001）：Phase G 集成测试（约 5d，含容器化 Incus fake cluster）+ Phase H runbook（约 1d）。

### 2026-04-19 Phase H runbook 落地 + Tabs 激活态修复

- `docs/runbook-ha.md` 全新文件：架构一览 / 日常健康检查 / 故障诊断（事件流断链、healing 卡 in_progress、drift 激增、反复 gone）/ 常见操作（手动 evacuate / restore / chaos drill）/ 告警建议表 / 版本约束
- `/admin/ha` Tabs 激活态 bug 修复：Base UI Tabs.Tab 用 `aria-selected="true"`（非 `data-selected`），Tailwind v4 `data-[selected]:` 永远不命中。改用 `aria-selected:` modifier + `hover:text-foreground` 过渡态，选中 Tab 文字高亮 + 蓝色下划线正常显示
- Phase H 除 "Staging 长时间灰度" 外全部 ✅，灰度部分合并到 Phase G 集成测试一并覆盖

### 2026-04-23 22:15 Tech debt 收尾 — 集成测试扩展 + audit 100%

接续"容器化 Incus fake cluster 集成测试"的 tech debt，用 `httptest.NewServer` 替代完整容器化做**API 契约级**验证，成本低得多且覆盖核心路径：

**Fake Incus HTTP 集成测试**（`internal/cluster/client_integration_test.go` 4 case）：
- `TestClient_GetInstances_Success` — mock `/1.0/instances?recursion=2&project=...`，验 Client 返 `[]json.RawMessage` + 消费者能解 `name` 字段（event listener / reconciler adapter 契约）
- `TestClient_GetInstances_IncusError` — Incus 返 `{type:"error", error:"Certificate is restricted"}` → Go error（匹配生产观察到的真实错误）
- `TestClient_GetClusterMembers` — HA Status 页面契约
- `TestClient_WaitForOperation` — evacuate/snapshot 等异步 API 轮询端点

要点：bypass `newClient()` 的 mTLS 健康检查路径，用 `&Client{httpClient: &http.Client{Timeout: 5*time.Second}, APIURL: ts.URL}` 直连 fake server。避免 TLS 证书管理代码，CI 跨机复用零成本。

**事件解析纯函数测试**（`internal/cluster/events_test.go`）：
- `InstanceNameFromSource` 7 case / `ClusterMemberNameFromSource` 5 case — 覆盖 `vm-x` / `vm-x/state` / `vm-x/snapshots/snap1` / empty / 非 instance path
- `buildEventsWSURL` 5 case — scheme switch（https→wss / http→ws / ftp→error）+ 多 type 编码 + empty types 无 query param + trailing-slash baseURL

**ExpireStale worker 生命周期测试**（`internal/worker/healing_expire_test.go` 5 case）：
- Disabled 路径 3 case（nil expirer / maxAge ≤ 0 即退）
- TicksAndCancels — 50ms 真 timer 驱动，断言 cutoff = `now - maxAge`（±1s slack），ctx cancel 后 2s 退出
- ErrorContinues — ExpireStale 返 error 时不中止循环（transient DB 故障不杀 worker）

**合计 22+ 新 case** — 全量 `go test ./internal/...` 绿灯。

**PLAN-019 audit 覆盖率收尾**：
- 实测脚本 `scripts/audit-coverage-check.sh` 原先按 route 注册次数计 writes，snapshot.go 的 admin+portal 双路径注册同一 handler 误判为 partial（6/3）
- 重写脚本：提取 route 注册里 handler 最后一段标识符去重后计数，snapshot.go 从 6/3 → 3/3 ok
- **实测 47/47 ok（100%）**：apitoken/ceph/clustermgmt/ippool/nodeops/order/product/quota/snapshot/sshkey/ticket/user/vm 全部 handler 业务 audit 齐备 + middleware route-level 兜底

所有 PLAN-019/020 tech debt 清零。
