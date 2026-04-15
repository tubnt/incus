# INFRA-002 Build cluster node management UI and automation

- **status**: pending
- **priority**: P1
- **owner**: (unassigned)
- **createdAt**: 2026-04-15 18:00

## Description

Build a UI-driven workflow for adding/removing nodes from the Incus + Ceph cluster. Backend automates: OS prep → Incus install → cluster join → Ceph OSD → network → monitoring.

Acceptance criteria:
- Admin can add a node by entering hostname + IP + SSH key
- Backend SSH-automates the full join sequence
- Admin can remove a node (evacuate → leave cluster → remove OSDs)
- Node dashboard shows real-time status, roles, maintenance mode toggle
- All operations logged in audit_logs

## ActiveForm

Building cluster node management UI and automation

## Dependencies

- **blocked by**: (none)
- **blocks**: INFRA-003

## Notes

Related plan: PLAN-006 Phase 6B.
Requires SSH access from IncusAdmin server to cluster nodes (deploy key).
