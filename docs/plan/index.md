# Incus Cloud Platform - Plan Index

> Updated: 2026-04-17

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
- [x] [**PLAN-007 UI/UX completeness — compete with DigitalOcean/Vultr/Hetzner**](PLAN-007.md) `2026-04-15`
- [ ] [**PLAN-008 QA fixes + three-persona user journey gaps**](PLAN-008.md) `2026-04-16`
- [x] [**PLAN-009 Code review follow-up — PLAN-008 findings**](PLAN-009.md) `2026-04-16`
- [x] [**PLAN-010 Production QA bug fixes (post-PLAN-009)**](PLAN-010.md) `2026-04-16`
- [x] [**PLAN-011 Frontend pma-web compliance cleanup**](PLAN-011.md) `2026-04-17`
- [x] [**PLAN-012 后端核算收尾 —— PayWithBalance 回滚集成测试 + TopUp 日额度**](PLAN-012.md) `2026-04-17`
- [x] [**PLAN-013 历史延期项全量清零 —— PLAN-009 架构级 + PLAN-010 反代 + PLAN-011 收尾 + PLAN-012 限制**](PLAN-013.md) `2026-04-17`
