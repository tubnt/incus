# IncusAdmin - Task List

> Updated: 2026-04-15

## Usage

Each task is a single line linking to its detail file. All detailed information lives in `docs/task/PREFIX-NNN.md`.

### Format

- [ ] [**PREFIX-001 Short imperative title**](PREFIX-001.md) `P1`

### Status Markers

| Marker | Meaning |
|--------|---------|
| `[ ]`  | Pending |
| `[-]`  | In progress |
| `[x]`  | Completed |
| `[~]`  | Closed / Won't do |

### Priority: P0 (blocking) > P1 (high) > P2 (medium) > P3 (low)

### Rules

- Only update the checkbox marker; never delete the line.
- New tasks append to the end.
- See each `PREFIX-NNN.md` for full details.

---

## Tasks

- [ ] [**REFACTOR-001 Refactor frontend to pma-web standards**](REFACTOR-001.md) `P1`
- [ ] [**REFACTOR-002 Refactor backend to pma-go standards**](REFACTOR-002.md) `P1`
- [ ] [**INFRA-001 Enable VM auto-failover with cluster healing**](INFRA-001.md) `P1`
- [ ] [**INFRA-002 Build cluster node management UI and automation**](INFRA-002.md) `P1`
- [ ] [**INFRA-003 Add standalone Incus host management**](INFRA-003.md) `P2`
- [x] [**QA-001 Fix 6 QA bugs from browser testing**](QA-001.md) `P2`
- [ ] [**UX-001 UI/UX completeness per PLAN-007**](UX-001.md) `P1`
