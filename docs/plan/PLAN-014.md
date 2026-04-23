# PLAN-014 VM 状态反向同步 worker —— 消除 DB ↔ Incus 漂移

- **status**: merged（已合并到 PLAN-020）
- **createdAt**: 2026-04-18 00:05
- **mergedAt**: 2026-04-19
- **mergedInto**: PLAN-020
- **relatedTask**: INFRA-006（relatedPlan 已迁移至 PLAN-020）

> ⚠️ **合并说明**：2026-04-19 HA 真正化立项时，发现本 PLAN 的"状态同步"与 HA 的"事件订阅"底层同构，合并到 PLAN-020 以避免两套 worker / 两套测试 harness。
>
> - 原 Phase A（60s 轮询 reconciler）→ **PLAN-020 Phase A**
> - 原 Phase B（`gone` 状态 + UI 清理）→ **PLAN-020 Phase B**
> - 原 Phase C（审计 + 告警）→ 并入 **PLAN-020 Phase A 审计部分**
>
> 本文件仅作历史留档，不再更新。

## Context

2026-04-17 生产 QA 发现，`/admin/monitoring` 页面显示"暂无 VM 监控数据。请确认 Incus metrics 已启用。"
实际根因并非 metrics 未启用，而是 **DB 记录与 Incus 实时状态不一致**：

| 源 | 数据 |
|---|---|
| DB `vms` 表 | 2 条 `status='running'`（id=7 `vm-d8b7dc`，id=8 `vm-870c48`） |
| Incus `incus list --all-projects` | **0 个实例** |
| Incus `/1.0/metrics` | 有 cluster 开销指标，但无任何 `incus_cpu_seconds_total{name=...}` 行（因为 0 VM） |

这 2 条漂移的 VM 已通过手工 SQL 标为 `deleted`、`ip_addresses.5` 回池 `available`。
但系统**缺少持续的反向同步机制** —— 只要 Incus 侧发生"绕过 app 的手工删除 / 节点故障后实例丢失 / evacuate 异常" 等情况，
DB 就会再次漂移；额度计算（`CountByUser`）与配额校验都会被错误的"running" 记录污染。

## Proposal

### Phase A —— 反向同步 worker 骨架

- 新增 `internal/worker/vm_reconciler.go`：后台 goroutine，每 60s 扫一次
  - 对每个 cluster：`client.ListInstances(ctx, project)` 拉 Incus 当前存在的 VM 全集
  - DB `vms` 按 cluster_id 过滤 `status IN ('creating','running','stopped','migrating')`
  - 差集 `DB - Incus` → 视为 Incus 侧已删除，标 `status='gone'`（不复用 `'deleted'`，避免覆盖"用户主动删除"语义）；释放 `ip_addresses`
  - 差集 `Incus - DB` → 写 warn 日志（"外部创建的实例，app 不接管"），**不写 DB**（避免被 rogue 实例污染）
- 可配置：`VM_RECONCILE_INTERVAL`（默认 60s，0 代表禁用）
- main.go 启动时 `go worker.Run(ctx, ...)`，随 server 关停

### Phase B —— 状态模型扩展

- 新增 `status='gone'`（Incus 端消失）：与 `'deleted'`（用户主动删除）区分
- `CountByUser` 新增：`AND status NOT IN ('deleted','error','gone')`
- admin VM 列表页加 "gone" 徽标，提供"清理"按钮调 `DELETE /admin/vms/:id/force`（硬删）

### Phase C —— 审计 & 告警

- 每次 reconcile 结果（drift 数量）记 `audit_logs`，type=`vm_reconcile`
- 发现 drift 数 > 阈值（默认 5）时结构化日志 level=WARN，便于运维 alerting

## Risks

1. **Incus 临时不可达会误标 gone**：
   修复：Incus 端错误必须 `continue`（跳过此 cluster，不触发 drift），只有成功拉到全量实例才对账。
2. **Reconciler 与创建 / 删除竞态**：
   修复：对 DB 查询加 `created_at < now() - 10s` 过滤，给"刚建、Incus 还没 visible"的 VM 一个缓冲期。
3. **`ip_addresses` 回池的幂等**：
   修复：`WHERE status='assigned' AND vm_id=?` 才 UPDATE，避免把"其他 VM 接手该 IP 后"又误释放。
4. **多集群扩展后的 IDByName 依赖**：
   reconciler 按 cluster 循环，依赖 `ClusterManager.IDByName` 返回正确 ID —— PLAN-013 C.2 已打通，风险小。

## Scope

- ✅ Phase A worker 骨架 + 60s 定时 + 单测
- ✅ Phase B `gone` 状态 + 前端徽标 + 清理按钮
- ✅ Phase C 审计 + 告警阈值
- ❌ 不做"反向接管 Incus 端独立实例"（Incus-Only 实例暂不纳管）
- ❌ 不做实时 event 驱动同步（Incus 的 `/1.0/events` 虽然支持，但接入复杂度远高于轮询；本 plan 只做轮询）

## Alternatives

- **a) 改用 Incus `/1.0/events` 长连接推送**：实时性更好，但断链重连 / 压力反压 / 多集群并行都要处理，首版成本高。保留为 Phase D 再评估。
- **b) 靠前端每次拉 VM 列表时实时 `incus list`**：打穿每个页面的 latency，拒绝。
