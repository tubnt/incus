# INFRA-003 Add standalone Incus host management

- **status**: pending
- **priority**: P2
- **owner**: (unassigned)
- **createdAt**: 2026-04-15 18:00

## Description

Allow IncusAdmin to manage standalone (non-clustered) Incus servers alongside clusters. Requires moving cluster config from env vars to database for dynamic add/remove.

Acceptance criteria:
- Admin can add a standalone Incus host (API URL + TLS certs)
- Standalone hosts appear alongside clusters in the management UI
- All VM operations work on standalone hosts (create/start/stop/delete/console/monitor)
- Cluster/host config stored in `clusters` DB table (not just env)
- Dynamic add/remove without service restart

## ActiveForm

Adding standalone Incus host management

## Dependencies

- **blocked by**: INFRA-002 (node management patterns reused)
- **blocks**: (none)

## Notes

Related plan: PLAN-006 Phase 6C.
DB migration needed: move cluster config from CLUSTER_* env vars to `clusters` table.
