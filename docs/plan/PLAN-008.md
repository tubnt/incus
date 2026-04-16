# PLAN-008 QA fixes + three-persona user journey gaps

- **status**: draft
- **createdAt**: 2026-04-16 01:30
- **approvedAt**: (pending)
- **relatedTask**: (none yet)

## Context

Full lifecycle testing (API + browser) on 2026-04-16 revealed 3 confirmed bugs and, through systematic persona-based journey review, identified 27 missing items across user, admin, and ops personas.

## Part A: Confirmed Bugs from Testing

### BUG-1 (HIGH): IP allocation race condition
- 3 VMs share 202.151.179.235
- Root cause: `pickNextIP` uses 60-second in-memory cache; concurrent creates within the cache window get the same IP
- Fix: Replace memory cache with DB-level `SELECT FOR UPDATE` on `ip_addresses` table, or add distributed lock

### BUG-2 (MEDIUM): VM metrics only available for ~30% of VMs
- Incus `/1.0/metrics` only returns Prometheus lines for instances with active counters
- VMs with very low I/O may not appear in metrics text at all
- Fix: Supplement Prometheus metrics with Incus instance state API (`/1.0/instances/{name}/state`) for CPU/RAM/disk as fallback

### BUG-3 (LOW): VM start returns "ok" but VM stays Stopped
- `ChangeVMState` returns success when the async operation is accepted, not when it completes
- Frontend doesn't poll for status update — user sees stale state until next refetch (10s)
- Fix: Frontend should show "Starting..." state optimistically and poll until status changes

## Part B: User Journey Gaps (3 Personas)

### Persona 1: Regular User (customer)

Full expected journey: Login → Dashboard → Create VM → View VM → Use Console → Monitor → Manage Snapshots → View Billing → Submit Ticket → Manage SSH Keys → API Tokens → Logout

| # | Step | Current State | Gap |
|---|------|---------------|-----|
| U1 | Dashboard shows real data | My VMs=0 for admin user | Dashboard "My VMs" queries DB but admin VMs created before Phase 1.4 fix are not in DB |
| U2 | Create VM from billing | ✅ Works (Buy→OS→Pay→Credentials) | — |
| U3 | See created VM in "My VMs" | ⚠️ Only DB VMs visible | Admin-created VMs via `+ Create VM` admin page don't appear in user's list |
| U4 | Click VM → detail page | Link goes to `/vm-detail?id=X` | Works only if VM in DB; Incus-only VMs have no ID |
| U5 | VM detail shows monitoring | ⚠️ "暂无监控数据" for most VMs | BUG-2 — metrics not available for all VMs |
| U6 | Console from VM detail | Console tab uses iframe | Iframe embedding may cause double sidebar; direct link works |
| U7 | Create/restore snapshot | Portal route exists | ✅ Works via `/portal/vms/{name}/snapshots` |
| U8 | Reinstall VM | No portal reinstall endpoint | ❌ **Missing**: user cannot reinstall their own VM |
| U9 | View billing history | ✅ Orders + invoices visible | — |
| U10 | Balance display | ✅ Header shows $17.00 | — |
| U11 | Submit/view ticket | ✅ Create + expand detail + reply | — |
| U12 | SSH key management | ✅ Add/delete keys | — |
| U13 | API token management | ✅ Create/delete tokens | — |
| U14 | Logout | ✅ Logout icon in header | — |
| U15 | i18n content | ⚠️ Sidebar translates, page body mostly hardcoded | Partial — not blocking but poor UX |
| U16 | Password visibility | ✅ Click-to-reveal | — |
| U17 | OS reinstall choice | No user reinstall | ❌ Same as U8 |
| U18 | Resize/upgrade VM | Not implemented | ❌ **Missing**: no upgrade path |
| U19 | rDNS configuration | Not implemented | ❌ **Missing** |
| U20 | VM bandwidth/traffic stats | Not shown | ❌ **Missing**: no traffic counter |

### Persona 2: Admin (platform administrator)

Full expected journey: Everything User can do + Manage all VMs + Manage users + Manage products/orders + View audit + Create VM for any user + Top-up balance

| # | Step | Current State | Gap |
|---|------|---------------|-----|
| A1 | Admin creates VM → shows password | ✅ Fixed in Phase 1.4 | — |
| A2 | Admin VM in "All VMs" | ✅ Incus query works | — |
| A3 | Admin VM in DB (My VMs) | ✅ Fixed in Phase 1.4 | — |
| A4 | Assign VM to specific user | ❌ CreateVM always uses admin's own userID | **Missing**: no "create for user X" option |
| A5 | Edit product | Backend `PUT /admin/products/{id}` exists | ❌ **Missing** frontend — no edit/delete buttons on Products page |
| A6 | View all invoices | Backend `GET /admin/invoices` exists | ❌ **Missing** frontend — no admin invoices page |
| A7 | Cancel/refund order | Not implemented | ❌ **Missing**: order status can only go forward |
| A8 | Delete user | Not implemented | ❌ **Missing**: no user deletion |
| A9 | User quota management | `quotas` table exists but never enforced | ❌ **Missing**: no quota edit UI, no enforcement |
| A10 | Admin role management beyond dropdown | Only role dropdown on users page | ⚠️ Functional but minimal |
| A11 | Bulk VM operations | Not implemented | ❌ **Missing**: can't stop/start multiple VMs at once |
| A12 | Email notification on events | Not implemented | ❌ **Missing** |
| A13 | Product edit/deactivate UI | Backend exists | ❌ Frontend missing |

### Persona 3: Ops Engineer (infrastructure operator)

Full expected journey: Cluster health → Node management → Storage monitoring → Network management → Incident response → Maintenance → Capacity planning

| # | Step | Current State | Gap |
|---|------|---------------|-----|
| O1 | Cluster overview dashboard | ✅ Clusters page with 5 nodes | — |
| O2 | Node detail page (CPU/RAM/disk graphs) | ❌ Not implemented | **Missing**: no per-node detail page like Proxmox |
| O3 | Add node to cluster (automated) | Node Ops has SSH test + commands | ⚠️ Manual commands only — no automated "join cluster" wizard |
| O4 | Remove node from cluster | Evacuate button exists | ⚠️ No full removal wizard (evacuate → remove OSD → leave cluster) |
| O5 | Node maintenance mode toggle | ❌ Not implemented | **Missing**: no one-click maintenance mode |
| O6 | Ceph health monitoring | ✅ Storage page with real-time SSH data | — |
| O7 | Ceph OSD management (mark out/in) | ❌ Read-only OSD list | **Missing**: no OSD actions |
| O8 | Ceph pool CRUD | ❌ Not implemented | **Missing**: can't create/edit Ceph pools from UI |
| O9 | Near-full storage alerts | ❌ Not implemented | **Missing**: no capacity warning |
| O10 | IP pool CRUD | ✅ Add/remove implemented | — |
| O11 | IP address lifecycle (reserve/release/cooldown) | ❌ Not implemented | **Missing**: no manual IP management |
| O12 | VLAN topology view | ❌ Not implemented | **Missing** |
| O13 | Incus event stream (real-time) | ❌ Only polling audit log | **Missing**: no SSE/WebSocket event feed |
| O14 | Alert rules configuration | ❌ Not implemented | **Missing**: can't define thresholds |
| O15 | Scheduled tasks (auto-backup, cleanup) | ❌ Not implemented | **Missing**: no cron UI |
| O16 | Live migration (move VM between nodes) | Only evacuate (moves all VMs off a node) | ⚠️ No single-VM migration |
| O17 | Grafana dashboard integration | ✅ Observability page with embed/link | — |
| O18 | Long-term metrics history | ❌ Only 30s cache snapshots | **Missing**: no historical graphs |
| O19 | Backup/restore cluster config | ❌ Not implemented | **Missing** |
| O20 | SSH key management for node access | Manual key deployment | ⚠️ No UI to manage node SSH keys |

## Part C: Additional gaps found during Graph audit (2026-04-16 02:00)

Graph + Grep verification confirmed all 30 original items. 8 new items discovered:

| # | Gap | Priority | Detail |
|---|-----|----------|--------|
| NEW-1 | No global Toast/notification system | P2 | Mutation errors only shown inline; no success feedback |
| NEW-2 | No Skeleton/Loading components | P3 | All loading states use plain text "加载中..." |
| NEW-3 | No ErrorBoundary | P2 | Route render errors cause white screen |
| NEW-4 | No user settings/profile page | P3 | No `/settings` or `/profile` route |
| NEW-5 | No VM password reset capability | P2 | User gets password once at creation, cannot reset later |
| NEW-6 | `ip_addresses` DB table completely unused | P1 | Full schema exists (pool_id, status, cooldown) but repository has zero operations; `pickNextIP` bypasses DB entirely — this is BUG-1's root cause |
| NEW-7 | A5 and A13 are duplicates | — | Merge into single item: "Product edit/deactivate UI" |
| NEW-8 | No systematic mobile/responsive design | P3 | Partial responsive classes in 9 files, no mobile-first approach |

### Root cause note for BUG-1
`ip_addresses` table was designed in PLAN-004 DB schema to track individual IP allocations with status (available/assigned/reserved/cooldown). But the implementation completely bypassed this table — `pickNextIP` scans Incus instances directly and uses an in-memory cache. Fix should migrate IP allocation to use `ip_addresses` table with `SELECT FOR UPDATE`.

## Summary (Updated)

| Category | Total Items | Working | Gaps |
|----------|------------|---------|------|
| Confirmed bugs | 3 | — | 3 |
| User journey | 20 | 13 | 7 |
| Admin journey | 12 | 6 | 6 |
| Ops journey | 20 | 7 | 13 |
| New findings | 7 | — | 7 |
| **Total** | **62** | **26** | **36** |

(A5/A13 merged = -1; +7 new = net +6; 30 → 36)

## Priority Ranking (Updated)

### P0 — Fix immediately
- ✅ BUG-1 + NEW-6: IP allocation → migrate to `ip_addresses` DB table with `SELECT FOR UPDATE`

### P1 — Core functionality gaps
- ✅ BUG-2: VM metrics fallback (use `GetInstanceState` API)
- ✅ U8: User VM reinstall (add portal endpoint + ownership check)
- ✅ A4: Create VM for specific user (add `target_user_id` param)
- ✅ A5: Product edit/delete UI (backend exists, frontend missing)
- ✅ O2: Node detail page (Proxmox-style per-node dashboard)
- ✅ O5: Node maintenance mode (evacuate + prevent placement)

### P2 — Important operations
- ✅ BUG-3: VM state polling (optimistic UI + refetch)
- ✅ NEW-1: Global Toast system (sonner)
- ✅ NEW-3: ErrorBoundary
- NEW-5: VM password reset (需要 Incus exec WebSocket API)
- ✅ A6: Admin invoices page
- ✅ A9: Quota management (enforcement + edit UI)
- ✅ O3: Automated node join wizard
- ✅ O7: Ceph OSD actions (mark in/out via SSH)
- ✅ O8: Ceph pool CRUD
- ✅ O9: Storage alerts (threshold warnings)
- O13: Event stream (Incus SSE → WebSocket)
- ✅ O16: Single-VM live migration

### P3 — Nice to have
- ✅ NEW-2: Skeleton loading components
- ✅ NEW-4: User settings page (语言/主题)
- ✅ NEW-8: Mobile responsive design
- U18: VM resize/upgrade
- U19: rDNS
- U20: Traffic stats
- ✅ A7: Order cancel/refund
- A8: User deletion
- A11: Bulk VM operations
- A12: Email notifications
- O11: IP lifecycle management
- O14: Alert rules
- O15: Scheduled tasks
- O18: Long-term metrics
- O19: Cluster backup
- O20: Node SSH key UI

## Annotations

(User annotations and responses. Keep all history.)
