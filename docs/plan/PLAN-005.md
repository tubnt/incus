# PLAN-005 Full-stack refactor to pma-web and pma-go standards

- **status**: draft
- **createdAt**: 2026-04-15 17:40
- **approvedAt**: (pending)
- **relatedTask**: REFACTOR-001, REFACTOR-002

## Context

IncusAdmin is a Go + React cloud management platform managing a 5-node Incus + Ceph cluster. All 17 database tables have corresponding backend APIs and frontend pages. The platform is deployed and running at vmc.5ok.co.

### Current frontend state

| Area | Current | pma-web standard |
|------|---------|------------------|
| Layout | Top horizontal nav | Sidebar navigation (industry standard) |
| UI library | Hand-written Tailwind classes | shadcn/ui + @base-ui/react primitives |
| Theme | Dark-only hardcoded | ThemeProvider (light/dark/system) |
| Providers | Inline in `__root.tsx` | Composed in `app/providers.tsx` |
| Data hooks | useQuery inline in route components | Feature-level `useXxxQuery` / `useXxxMutation` |
| Shared UI | None, badges/buttons duplicated everywhere | `shared/components/ui/` via shadcn CLI |
| ESLint | None | @antfu/eslint-config v8+ |
| Tests | None | Vitest 4 |
| Entry point | Missing StrictMode, no provider factory | Standard main.tsx with router type registration |

### Current backend state

| Area | Current | pma-go standard |
|------|---------|-----------------|
| Lint | None | golangci-lint v2 |
| Config | Hand-written env parsing | koanf (defaults → file → env → flags) |
| DB access | Hand-written SQL + `rows.Scan` | sqlc + pgx (compile-time SQL safety) |
| Input validation | Manual `if` checks | go-playground/validator |
| API responses | Inconsistent (null vs [], no envelope) | Consistent envelope, `[]` for empty arrays |
| Task runner | None | Taskfile.yml |
| Tests | None | Table-driven tests for handler/service/repository |
| Error handling | Mixed patterns | `fmt.Errorf` with `%w`, sentinel errors at boundary |

### Competitor research (DigitalOcean / Vultr / Hetzner / Linode)

All major cloud providers use sidebar navigation (240-280px) with collapsible groups:
- Top: logo + global search (Cmd+K)
- User section: VMs, SSH Keys, Billing, Tickets, API
- Admin section: Clusters, All VMs, Monitor, Users, Products, Orders, Audit
- Bottom: user avatar + theme toggle
- Content area: KPI cards at top, card-based grid below
- Mobile: sidebar becomes Sheet drawer

Reference template: [satnaing/shadcn-admin](https://github.com/satnaing/shadcn-admin) — React + Vite + shadcn/ui with data-driven sidebar, theme toggle, responsive layout.

## Proposal

### Phase A: Frontend scaffold (pma-web baseline)

1. **Initialize shadcn/ui** with base-ui primitives into `src/shared/components/ui/`
   - `bun add @base-ui/react`
   - `npx shadcn@latest init` with base-nova style, neutral base color, CSS variables
   - Generate: Button, Badge, Card, Table, Dialog, Select, Input, Textarea, Separator, Sheet, Tooltip, DropdownMenu, Command (Cmd+K)

2. **Create sidebar layout** replacing top nav
   - `src/shared/components/layout/app-sidebar.tsx` — data-driven sidebar with collapsible groups
   - `src/shared/components/layout/app-header.tsx` — breadcrumb + user menu + theme toggle
   - `src/shared/components/layout/sidebar-data.ts` — navigation config
   - User/Admin sections with role-based visibility

3. **ThemeProvider** (light/dark/system)
   - `src/shared/components/theme-provider.tsx` — localStorage persistence
   - `src/shared/components/mode-toggle.tsx` — cycle button
   - oklch CSS variables in `index.css` via `@theme`

4. **Provider composition**
   - Extract `app/providers.tsx` with QueryClientProvider + ThemeProvider
   - Update `main.tsx` with StrictMode + router type registration
   - Refactor `__root.tsx` to use new layout components

5. **ESLint setup**
   - `bun add -D eslint @antfu/eslint-config @eslint-react/eslint-plugin`
   - Create `eslint.config.ts` flat config
   - Fix all lint errors across codebase

### Phase B: Migrate pages to new architecture

1. **Extract feature hooks** from all 18 route files:
   - `features/vms/api.ts` — useVMsQuery, useVMStateMutation, useDeleteVMMutation, useReinstallMutation
   - `features/clusters/api.ts` — useClustersQuery, useClusterNodesQuery, useClusterVMsQuery
   - `features/monitoring/api.ts` — useMetricsOverviewQuery, useVMMetricsQuery
   - `features/ssh-keys/api.ts` — useSSHKeysQuery, useCreateSSHKeyMutation, useDeleteSSHKeyMutation
   - `features/tickets/api.ts` — useTicketsQuery, useCreateTicketMutation, etc.
   - `features/billing/api.ts` — useOrdersQuery, useInvoicesQuery, useProductsQuery, etc.
   - `features/users/api.ts` — useUsersQuery, useUpdateRoleMutation, etc.
   - `features/api-tokens/api.ts` — useAPITokensQuery, etc.
   - `features/audit/api.ts` — useAuditLogsQuery

2. **Replace hand-written UI** with shadcn components:
   - All `<button>` → `<Button>` with variant props
   - All inline badges → `<Badge>`
   - All `<table>` → shadcn `<Table>` / DataTable
   - All `<select>` → `<Select>`
   - All `<input>` / `<textarea>` → `<Input>` / `<Textarea>`
   - confirm() dialogs → `<AlertDialog>`
   - Snapshot/Reinstall panels → `<Sheet>` or `<Collapsible>`

3. **KPI dashboard** overhaul
   - Dashboard index page: 4-6 metric cards using shadcn Card
   - Cluster overview: Recharts inside Card containers
   - Consistent spacing via Tailwind spacing scale

### Phase C: Backend refactor (pma-go baseline)

1. **golangci-lint v2** setup
   - `.golangci.yml` with revive, govet, errcheck, staticcheck, gosec
   - Fix all lint warnings

2. **Consistent API response format**
   - Empty slices return `[]` not `null`
   - Error responses: `{"error": "message", "code": "ERROR_CODE"}`
   - Success responses: direct data or `{"data": ..., "meta": {...}}`

3. **Input validation**
   - Add `go-playground/validator` for all request structs
   - Validate at handler boundary before calling service

4. **Taskfile.yml**
   - `task lint`, `task test`, `task build`, `task dev`, `task deploy`

5. **sqlc migration** (optional, high effort)
   - Move queries to `db/queries/*.sql`
   - Generate type-safe Go code
   - Replace manual `rows.Scan` patterns
   - NOTE: This is a large change. Can be deferred to a separate plan.

### Phase D: Quality gates

1. **Vitest** for frontend
   - Test feature hooks with MSW
   - Test utility functions (fmtBytes, sshFingerprint equivalent)

2. **Go tests**
   - Table-driven handler tests with httptest
   - Repository tests with test database

3. **CI scripts** in package.json / Taskfile.yml
   - `bun run lint && bun run typecheck && bun run build && bun run test`
   - `task lint && task test && task build`

## Risks

1. **Sidebar migration breaks mobile** — need responsive Sheet fallback from day 1
2. **shadcn init may conflict with existing Tailwind v4 config** — test in isolation first
3. **sqlc migration touches every repository** — can be deferred; manual SQL still works
4. **ESLint @antfu/eslint-config may generate 100+ warnings** — bulk fix needed, some rules may need explicit overrides
5. **Phase B (page migration) is the largest phase** — 18 route files to update; do in batches by feature area

## Scope

| Phase | Files affected | Effort |
|-------|---------------|--------|
| A: Frontend scaffold | ~15 new files, 3 modified | Large (new infra) |
| B: Page migration | ~18 route files, ~10 new feature API files | Large (volume) |
| C: Backend refactor | ~13 handler files, ~9 repo files, config | Large |
| D: Quality gates | ~15 new test files, 2 config files | Medium |

Total: ~80 files touched or created. Recommend executing A→B→C→D sequentially, deploying after each phase.

## Alternatives

### Frontend UI library

| Option | Pros | Cons |
|--------|------|------|
| **shadcn/ui + base-ui (chosen)** | pma-web standard, owned code, full control | Initial setup effort |
| Radix UI direct | Lower level | More boilerplate, no pre-built styles |
| Ant Design | Feature-rich | Heavy bundle, opinionated, not aligned with pma-web |
| Keep hand-written | Zero migration | Stays non-standard, duplicated code |

### Backend DB access

| Option | Pros | Cons |
|--------|------|------|
| **Keep manual SQL, add lint+validation** | Low risk, quick win | Not compile-time safe |
| sqlc + pgx (full migration) | pma-go standard, type-safe | Touches every repo file |
| GORM | Quick CRUD | Not aligned with pma-go preference |

**Recommendation**: Phase C does lint + validation + response consistency (quick wins). sqlc migration deferred to a separate PLAN-006 after the platform stabilizes.

## Deep Audit Findings (2026-04-15 18:30)

Full code audit using Graph + Serena + manual code tracing revealed 7 CRITICAL, 14 WARNING, and 8 INFO issues. These must be addressed in the refactor.

### CRITICAL (must fix in PLAN-005)

1. **C1: SSH keys not injected into VMs** — `SSHKeyRepo.GetByUser()` exists but never called in `CreateService` or `CreateVM`. SSH key feature is decorative.
2. **C2: VM naming collision** — `vm-{userID}` means each user can only have 1 VM. Fix: `vm-{userID}-{timestamp}` or `vm-{userID}-{seq}`.
3. **C3: Order payment doesn't trigger VM provisioning** — `PayWithBalance` ends at invoice creation. No code transitions to provisioning. Billing flow is broken end-to-end.
4. **C4: `/api/auth/me` balance hardcoded to 0** — User balance always shows $0. Fix: query user from DB.
5. **C5: `ListAllVMs` returns hardcoded empty array** — Endpoint registered but not implemented.
6. **C6: Panic on empty cluster list** — `clusters.List()[0]` without bounds check in `ChangeVMState` and `DeleteVM`.
7. **C7: User ticket detail/reply has no frontend** — Table rows look clickable but have no onClick. Backend endpoints exist but unused.

### WARNING (must fix for production)

W1: Portal metrics no ownership check. W2: Console WebSocket no ownership check (SECURITY). W3: WebSocket CheckOrigin always true (CSRF). W4: Quota system never enforced. W5: Portal CreateService bypasses order/payment. W7: Admin DeleteVM doesn't update DB. W8: findClusterName ignores clusterID. W10: Audit logs never written. W11: IP allocation race condition. W12: API Token auth doesn't set email. W13: No input validation (negative CPU/RAM). W14: Password stored/displayed in plaintext. W15: Emergency login incomplete. W16: Mixed Chinese/English, inconsistent currency symbols.

### INFO (performance)

I1-I4: N+1 queries and missing caching in IP pool, pickNextIP, metrics, scheduler. I5: nil→null JSON serialization. I6: Missing DB indexes. I7: No rate limiting. I8: HTTP client no timeout.

### Impact on PLAN-005 scope

Original PLAN-005 was UI refactor + code quality. With these findings, PLAN-005 must also include:
- **Phase A0 (new)**: Fix all CRITICAL bugs before any refactor
- **Phase A**: Frontend scaffold (unchanged)
- **Phase B**: Page migration + fix WARNING UI issues
- **Phase C**: Backend refactor + fix WARNING backend issues
- **Phase D**: Quality gates (unchanged)

Estimated additional effort: +40% over original estimate.

## Annotations

(User annotations and responses. Keep all history.)
