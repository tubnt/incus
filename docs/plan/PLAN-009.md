# PLAN-009 Code review follow-up — PLAN-008 findings

- **status**: completed
- **createdAt**: 2026-04-16 22:00
- **approvedAt**: 2026-04-16 22:00
- **completedAt**: 2026-04-16 23:30
- **relatedTask**: QA-002

## Context

`/pma-cr` deep review over the 19 PLAN-008 commits (`6ea7e88 → 7114f2d`, ~4600 LOC)
found 1 CRITICAL, 4 HIGH, 4 MEDIUM, and 7 LOW issues in:

- `internal/service/vm.go`
- `internal/cluster/client.go`
- `internal/repository/vm.go`, `internal/repository/ipaddr.go`
- `internal/handler/portal/events.go`, `ipallocator.go`, `vm.go`, `order.go`
- `web/src/app/routes/admin/observability.tsx`
- `web/src/app/routes/vm-detail.tsx`

## Proposal

Fix all actionable findings in a single plan; defer only architectural items.

### P0 — CRITICAL

**C1 Shell-escape / command injection in admin VM password reset**
- `service/vm.go:320` — `fmt.Sprintf("echo '%s:%s' | chpasswd", username, newPassword)` with
  attacker-controlled `username` from `ResetPasswordAdmin` JSON body.
- Fix: validate `username` against `^[a-z_][a-z0-9_-]{0,31}$`; use `chpasswd` with a fixed
  argv (no `sh -c`) and pass `user:pwd` via stdin by enabling exec's stdin channel, OR
  keep `sh -c` but single-quote-escape via `strings.ReplaceAll(s, "'", "'\\''")`.
- Choose: validate username + keep shell form; quote-escape password too for belt-and-braces.

### P1 — HIGH

**H1 IP allocation leaves `ip_addresses.vm_id` NULL forever**
- All call sites use `allocateIP(ctx, cc, 0)`. After VM row insert, no code back-fills
  `vm_id`. Breaks reverse lookup and release-on-delete traceability.
- Fix: add `IPAddrRepo.AttachVM(ctx, ip, vmID)` and call it in `order.Pay`, admin
  `CreateVM`, and user `vm.Create` right after `h.vmRepo.Create`.

**H2 `VMRepo.GetByName` SELECT missing `ip`, `order_id`**
- `repository/vm.go:66-79` diverges from `GetByID`/`ListByUser`.
- Fix: match the 15-column shape used elsewhere, scan `host(ip)::text` and `order_id`.

**H3 Event WebSocket has no idle/ping handling**
- `events.go` — both read loops block forever, TCP half-close leaks goroutines.
- Fix: on both legs add 60s `SetReadDeadline`, a 30s ping ticker from the proxy side,
  and `SetPongHandler` that refreshes the deadline. Close on timeout.

**H4 `paused` button is a no-op**
- `observability.tsx:118` — `paused ? events : events`. onmessage always setEvents.
- Fix: use a `pausedRef` read inside `ws.onmessage`; when paused, skip `setEvents`.

### P2 — MEDIUM

**M1 `allocateIP` dead fallback returns silent empty IP**
- `ipallocator.go:45-51` — unreachable `clients` closure, returns `""` with no error.
- Fix: delete dead code; when DB path fails return explicit `error`; callers already
  propagate.

**M2 `AllocateNext` returns CIDR form (`x.x.x.x/32`)**
- `ipaddr.go:37` — `SELECT ip::text` on `inet`.
- Fix: `SELECT host(ip)::text`, align with read queries.

**M3 EventStream reconnect timer leaks on unmount**
- `observability.tsx:97-100` — `setTimeout(connect, 5000)` handle discarded.
- Fix: store in `reconnectTimerRef`; clear on cleanup and on successful open.

### P3 — LOW (quick wins)

**L5 `ExecNonInteractive` swallows stdout/stderr on failure**
- `cluster/client.go:220-226` — only reads `return`.
- Fix: on non-zero return, also parse `metadata.output.{stdout,stderr}` and log.

**L6 `ExecNonInteractive` wait timeout fixed at 30s**
- Fix: use `ctx` by dropping `?timeout=30` and relying on HTTP client timeout + ctx.
  Also respect `ctx.Err()` before returning.

**L7 `buildEventsWSURL` ignores `url.Parse` error**
- `events.go:122-129` — returns malformed URL on bad input.
- Fix: return `(string, error)`, propagate.

### Deferred (documented in annotations)

| ID | Reason |
|----|--------|
| M4 `InsecureSkipVerify` with no pinning | Needs per-cluster cert pinning design; affects all cluster client calls. Track separately. |
| L1 `ClusterID: 1` hardcoded | Needs clusters Manager `IDByName()` + DB lookup; multi-cluster work. |
| L2 Frontend `cluster=cn-sz-01` hardcoded | Needs API to return cluster in VM list; tracked with L1. |
| L3 Observability `iframe` over `http://` | Mixed content — UX/ops call, leave external link as primary. |
| L4 `internal/server/dist/` staleness | Build workflow, not a code fix. Add Makefile target separately. |

## Risks

- H1 back-fill runs after VM insert; if the UPDATE fails the IP is still marked
  `assigned` without `vm_id`. Acceptable: the audit log captures the VM name and the
  periodic cooldown sweeper can reconcile. Log error loudly.
- H3 ping frame might interfere with Incus's own keep-alive. Incus events WS is
  unidirectional metadata; writing control frames from client side is standard.
- H4 using a ref skips React re-renders on pause; ensure the button still updates via
  `paused` state (it does — the ref is read-only inside onmessage).
- M1 turning silent empty-IP into an error makes the Pay flow fail hard when pool is
  exhausted. That is the correct behavior; capture explicit 409 already handled by
  admin CreateVM.

## Scope

- Go: 5 files (`service/vm.go`, `cluster/client.go`, `repository/vm.go`,
  `repository/ipaddr.go`, `handler/portal/{events.go, ipallocator.go, vm.go, order.go}`)
- TS: 1 file (`observability.tsx`)
- No migrations. No new packages.
- Tests: add unit test for `sanitizeUsername` and `AttachVM`.

## Alternatives

- For C1: rewriting `ResetPassword` to run `chpasswd` with argv-only and pipe
  `user:pass` through exec's `stdin` channel is the ideal fix, but Incus
  `record-output` exec currently requires WebSocket for stdin. Picking
  validation + quote-escape keeps the non-interactive path and avoids WS complexity.

## Annotations

- 2026-04-16 22:00 — Plan drafted from `/pma-cr` findings; user requested fix-all in auto mode.
