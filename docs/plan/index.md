# Incus Cloud Platform - Plan Index

> Updated: 2026-04-15

## Usage

Each plan is a single line linking to its detail file. All detailed information lives in `docs/plan/PLAN-NNN.md`.

### Format

- [ ] [**PLAN-001 Short plan title**](PLAN-001.md) `YYYY-MM-DD`

### Status Markers

| Marker | Meaning |
|--------|---------|
| `[ ]`  | Draft / Pending review |
| `[-]`  | Approved / Implementing |
| `[x]`  | Completed |
| `[~]`  | Rejected / Abandoned |

### Rules

- Only update the checkbox marker; never delete the line.
- New plans append to the end.
- See each `PLAN-NNN.md` for full details.

---

## Plans

- [x] [**PLAN-001 Single-node Incus setup with public IP bridge**](PLAN-001.md) `2026-04-02`
- [x] [**PLAN-002 Cluster Infrastructure — Incus + Ceph + Network + Monitoring**](PLAN-002.md) `2026-04-09`
- [~] [**PLAN-003 Business Platform — Paymenter + Incus Extension + Billing**](PLAN-003.md) `2026-04-09`
- [x] [**PLAN-004 IncusAdmin — Self-hosted Cloud Platform Management System**](PLAN-004-incus-admin.md) `2026-04-15`
- [x] [**PLAN-005 Full-stack refactor to pma-web and pma-go standards**](PLAN-005.md) `2026-04-15`
- [x] [**PLAN-006 Infrastructure automation — HA failover, node management, standalone host**](PLAN-006.md) `2026-04-15`
