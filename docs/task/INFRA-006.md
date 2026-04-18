# INFRA-006 VM state reverse-sync worker

- **status**: pending
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-18 00:05
- **relatedPlan**: PLAN-014

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

See `docs/plan/PLAN-014.md` Phase A/B/C for breakdown:
- Phase A: worker skeleton + 60s interval + cluster-level reconcile + unit tests
- Phase B: add `gone` status, update `CountByUser`, admin VM list badge + force-delete button
- Phase C: audit_logs entry + structured WARN log on drift threshold breach

## Acceptance

- `go test ./internal/worker/...` pass
- Staging: manually `incus delete` a VM → within 60s, DB `status='gone'`, IP released
- Audit log records each reconcile result
