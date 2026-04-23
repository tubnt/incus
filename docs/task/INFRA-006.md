# INFRA-006 VM state reverse-sync worker

- **status**: completed
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-18 00:05
- **updatedAt**: 2026-04-19 03:25
- **completedAt**: 2026-04-19 03:25
- **relatedPlan**: PLAN-020（原 PLAN-014 已合并）

## Summary

Add a background reconciler that periodically compares `vms` table against
Incus live instance list (per cluster) and reconciles drift:

- Instances present in DB but missing in Incus → mark `status='gone'` + release `ip_addresses`
- Instances present in Incus but missing in DB → log warn only (do not auto-adopt)

## Why

Production incident 2026-04-17: `/admin/monitoring` showed "暂无 VM 监控数据"
because 2 VMs were `status='running'` in DB while Incus had 0 instances.
Root cause: no reverse-sync mechanism — any out-of-band delete leaves DB stale,
polluting quota counts (`CountByUser`) and misleading admin dashboards.

## Scope

See `docs/plan/PLAN-020.md` Phase A/B for breakdown（原 PLAN-014 Phase A/B/C 合并后分布）:
- PLAN-020 Phase A: polling reconciler skeleton + 60s interval + audit drift
- PLAN-020 Phase B: `gone` status + CountByUser filter + admin UI badge + force-delete
- (PLAN-020 Phase C-G covers event-driven HA, not in original PLAN-014 scope)

## Acceptance

- `go test ./internal/worker/...` pass
- Staging: manually `incus delete` a VM → within 60s, DB `status='gone'`, IP released
- Audit log records each reconcile result
