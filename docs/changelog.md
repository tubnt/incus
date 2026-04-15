# IncusAdmin Changelog

## 2026-04-15 17:32 [progress]

All 17 database tables covered with backend APIs and frontend pages. Features: VM lifecycle, console, snapshots, monitoring (Recharts), SSH keys, products, tickets, orders/billing, invoices, audit logs, API tokens with Bearer auth. Deployed at vmc.5ok.co.

## 2026-04-15 17:40 [decision]

PLAN-005 drafted: full-stack refactor to pma-web (shadcn/ui sidebar layout, ThemeProvider, feature hooks, ESLint, Vitest) and pma-go (golangci-lint, validator, consistent responses, Taskfile) standards. sqlc migration deferred.

## 2026-04-15 18:00 [decision]

Product direction clarified: internal private cloud first, external API later. PLAN-006 drafted: infrastructure automation — VM auto-failover (Incus cluster.healing_threshold), node management (SSH-automated add/remove), standalone host support (DB-stored config). Auto-deploy new cluster deferred to Phase 6D. Directory cleanup: deleted 17,885 lines of dead code (paymenter, ai-gateway, console-proxy, screenshots), unified all docs under root /docs/.

## 2026-04-15 18:30 [BUG-P1]

Deep code audit (Graph + Serena + manual tracing) found 7 CRITICAL bugs: SSH keys never injected into VMs, VM naming collision (1 VM per user), order payment doesn't provision VM, balance hardcoded to 0, ListAllVMs stub, panic on empty cluster, user ticket detail missing frontend. Plus 14 WARNINGs including Console WebSocket no ownership check, quota never enforced, audit logs never written, IP allocation race condition, password in plaintext. PLAN-005 scope expanded to include Phase A0 (critical bug fixes).
