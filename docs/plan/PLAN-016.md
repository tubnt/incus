# PLAN-016 后台菜单重组 + 视角分离

- **status**: in_progress
- **priority**: P2
- **owner**: claude
- **createdAt**: 2026-04-18 18:50
- **startedAt**: 2026-04-18 18:50
- **relatedTask**: UX-002

## Context

Current sidebar (`web/src/shared/components/layout/app-sidebar.tsx` + `sidebar-data.ts`)
is a single flat list. Admin users see 23 items — 7 user + 16 admin — packed into
one column with no grouping. Admin group title is also hardcoded English.

User ask: two perspectives (user / admin), admin with a two-level menu grouped
into finance vs ops buckets.

## Decisions

- **Perspective switch via URL path**, not a dropdown state. `/admin/*` → admin
  sidebar; anything else → user sidebar. No extra client state to persist.
- **Admin top button** (`isAdmin` gated): from user side go to `/admin/monitoring`;
  from admin side return to `/`. One button slot, label flips by perspective.
- **Collapsible groups** use `@base-ui-components/react` Accordion (already installed).
  Rules: current-path's group auto-opens; other groups open/close at user will.
  Persist open-state across refresh in `localStorage` key `incus.sidebar.admin.openGroups`.
- **5 admin groups**: monitoring, resources, infrastructure/ops, billing, userOps.
- **User sidebar stays flat** (only 7 items — grouping adds no value).
- **i18n**: move hardcoded "Admin" title → proper keys; add group titles + switch
  button labels. zh + en.

## Phases

### Phase A — Data layer

- [ ] Refactor `sidebar-data.ts`:
  - Export `userSidebar: NavItem[]` (7 items, flat, no group wrapper)
  - Export `adminSidebar: NavGroup[]` (5 groups, each with `key`, `titleKey`, `icon`, `items`)
  - Remove old `sidebarGroups` export after sidebar migrates

### Phase B — Rendering

- [ ] `app-sidebar.tsx`:
  - Derive `perspective = currentPath.startsWith('/admin') ? 'admin' : 'user'`
  - Render perspective-switch button (admin-gated) under the logo
  - User perspective: flat list, same look as today
  - Admin perspective: Accordion with 5 groups; current-path group initially open
    (merged with persisted localStorage state, current-path always in open set)
  - Collapsed sidebar: icon-only, group triggers show icon + chevron only;
    items still tooltip-on-hover via `title` attr
  - Mobile drawer: same Accordion, no special casing

### Phase C — i18n

- [ ] `locales/{zh,en}/common.json`:
  - `sidebar.switchToAdmin` / `sidebar.backToUser`
  - `sidebar.group.monitoring` / `sidebar.group.resources` /
    `sidebar.group.infrastructure` / `sidebar.group.billing` / `sidebar.group.userOps`

### Phase D — Verification

- [ ] `bun run typecheck` + `bun run build` pass
- [ ] Browser: `/` shows user sidebar (7 items, no groups)
- [ ] Browser: `/admin/monitoring` shows admin sidebar, "概览监控" group open, others closed
- [ ] Browser: click another group header — expands; click again — collapses
- [ ] Browser: refresh → open-state preserved
- [ ] Browser: toggle sidebar collapsed → icons only, groups still navigable
- [ ] Browser: admin user sees switch button; non-admin user does NOT
- [ ] Browser: both zh + en; light + dark themes

### Phase E — Docs

- [ ] changelog entry
- [ ] Close UX-002 + PLAN-016

## Non-goals

- No route changes
- No permission logic changes
- No new shadcn component installs (Accordion from already-installed `@base-ui-components/react`)
- No backend changes
