# INFRA-001 Enable VM auto-failover with cluster healing

- **status**: pending
- **priority**: P1
- **owner**: (unassigned)
- **createdAt**: 2026-04-15 18:00

## Description

Configure Incus cluster auto-healing so VMs automatically evacuate from failed nodes to healthy ones. Add admin UI for HA status monitoring.

Acceptance criteria:
- `cluster.healing_threshold` configured and tested
- All VMs verified on shared Ceph storage (no local devices)
- Admin dashboard shows node health, HA status per VM, evacuation history
- Manual evacuation button per node
- Alert on node going offline

## ActiveForm

Enabling VM auto-failover with cluster healing

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Related plan: PLAN-006 Phase 6A.
Incus docs: cluster.healing_threshold, `incus cluster evacuate`.
Risk: partial connectivity can cause false evacuations. Need conservative threshold (300s+).
