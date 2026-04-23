# IncusAdmin - Task List

> Updated: 2026-04-23 22:50

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

- [x] [**REFACTOR-001 Refactor frontend to pma-web standards**](REFACTOR-001.md) `P1`
- [x] [**REFACTOR-002 Refactor backend to pma-go standards**](REFACTOR-002.md) `P1`
- [x] [**INFRA-001 Enable VM auto-failover with cluster healing**](INFRA-001.md) `P1`
- [ ] [**INFRA-002 Build cluster node management UI and automation**](INFRA-002.md) `P1`
- [ ] [**INFRA-003 Add standalone Incus host management**](INFRA-003.md) `P2`
- [x] [**QA-001 Fix 6 QA bugs from browser testing**](QA-001.md) `P2`
- [ ] [**UX-001 UI/UX completeness per PLAN-007**](UX-001.md) `P1`
- [x] [**QA-002 Code review follow-up — fix PLAN-008 review findings**](QA-002.md) `P0`
- [x] [**QA-003 Fix 15 QA bugs from 2026-04-16 production browser testing**](QA-003.md) `P1`
- [x] [**TECHDEBT-001 Close PLAN-009/010/011/012 deferred items**](TECHDEBT-001.md) `P1`
- [x] [**INFRA-004 Cluster TLS fingerprint pinning**](INFRA-004.md) `P1`
- [~] [**INFRA-005 Observability iframe HTTPS reverse proxy**](INFRA-005.md) `P2`
- [x] [**QA-004 Full web QA on production after PLAN-013 deploy**](QA-004.md) `P1`
- [x] [**QA-005 Fix QA-004 bug findings N1-N15**](QA-005.md) `P1`
- [x] [**INFRA-006 VM state reverse-sync worker**](INFRA-006.md) `P1`
- [x] [**UX-002 后台菜单重组 + 用户/管理员视角分离**](UX-002.md) `P2`
- [x] [**QA-006 全量用户端 E2E + N1-N11 bug 修复**](QA-006.md) `P1`
- [x] [**UX-003 用户端功能缺口填补(G1-G5)**](UX-003.md) `P2`
- [x] [**SEC-001 安全与审计基线加固**](SEC-001.md) `P1`
- [-] [**HA-001 事件驱动 HA 实现 + 故障演练 + 历史回放**](HA-001.md) `P1`
