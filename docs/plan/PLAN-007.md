# PLAN-007 UI/UX completeness — compete with DigitalOcean/Vultr/Hetzner

- **status**: draft
- **createdAt**: 2026-04-15 22:10
- **approvedAt**: (pending)
- **relatedTask**: UX-001

## Context

### User feedback (2026-04-15)

1. Admin creates VM → password not shown after creation
2. User creates VM via portal → VM not visible in "My VMs" (only in admin All VMs)
3. Product plans not shown in user VM creation page (only hardcoded S/M/L)
4. Cannot add IP ranges/VLANs
5. Cannot add node servers to cluster (UI missing, API exists)
6. Cannot create new clusters (UI missing, API exists)
7. Ceph health status not visible

### Competitor research

**DigitalOcean** (reference: [VPC](https://docs.digitalocean.com/products/networking/vpc/), [Firewalls](https://www.digitalocean.com/products/cloud-firewalls)):
- VM creation → clear result page with IP, password, SSH instructions
- Products are the VM creation form (choose CPU/RAM/Disk/Price, not separate catalog)
- Networking: VPC, Cloud Firewalls (free, stateful), Reserved IPs, DNS
- Monitoring graphs on each VM detail page
- Activity feed (audit log per resource)

**Vultr** ([Control Panel](https://www.vultr.com/features/control-panel/), [Networking](https://www.vultr.com/features/advanced-networking/)):
- VM creation → success page with IP, root password, SSH key info
- Plan selection integrated into creation flow
- Networking: VPC, Firewall, Reserved IPs, BGP, CDN, DNS
- Real-time billing dashboard
- Sub-user management with granular permissions

**Hetzner** ([Cloud Console](https://docs.hetzner.com/cloud/)):
- VM creation → 2-3 clicks, immediate detail page with credentials
- Networks: Private Networks (Layer 3), Firewalls (stateful, free)
- Snapshots & backups with predictable pricing
- Load Balancers
- Browser-based console on server detail page

### Current gaps matrix

| Feature | DO | Vultr | Hetzner | IncusAdmin | Gap |
|---------|----|----|---------|------------|-----|
| VM create → show credentials | ✅ | ✅ | ✅ | ❌ | Admin create doesn't show password |
| VM in user portal after create | ✅ | ✅ | ✅ | ❌ | Portal create goes to DB but user list doesn't refresh |
| Product plans in create flow | ✅ | ✅ | ✅ | ❌ | User sees hardcoded S/M/L not DB products |
| IP/subnet management | ✅ | ✅ | ✅ | ❌ | Read-only pool display, no CRUD |
| VLAN management | ✅ | ✅ | ✅ | ❌ | No UI |
| Add nodes to cluster | — | — | — | ❌ | API exists, no UI |
| Create new cluster | — | — | — | ❌ | API exists (AddCluster), no UI form |
| Ceph health dashboard | — | — | — | ❌ | No Ceph integration |
| VM detail page | ✅ | ✅ | ✅ | ❌ | No per-VM detail page (inline only) |
| Firewall rules per VM | ✅ | ✅ | ✅ | ❌ | Not implemented |
| DNS management | ✅ | ✅ | ❌ | ❌ | Not implemented |
| Reserved IPs | ✅ | ✅ | ❌ | ❌ | Not implemented |
| Activity feed / resource logs | ✅ | ✅ | ✅ | ⚠️ | Audit logs exist but no per-resource view |
| Logout button | ✅ | ✅ | ✅ | ❌ | No logout |

## Proposal

### Phase 1: Critical UX fixes (immediate bugs)

**1.1 Admin VM create → show password result**
- After `CreateVM` success, show a modal/alert with VM name, IP, username, password
- Frontend: `admin/create-vm.tsx` — display `createMutation.data` on success

**1.2 User portal VM → fix "My VMs" visibility**
- Root cause: `CreateService` (portal) writes to `vms` DB and creates via Incus, but `ListServices` queries DB. The order-driven flow (`Pay`) also creates DB record. Need to verify the DB record is correct and refreshing works.
- Fix: After payment success, invalidate `["myServices"]` query + show success banner

**1.3 User VM creation → use DB products (not hardcoded S/M/L)**
- Replace `vms.tsx` hardcoded `SIZES` with dynamic product list from `/portal/products`
- User selects product → creates order → pays → VM auto-provisioned
- If no products defined, fall back to current S/M/L

### Phase 2: Networking & IP management

**2.1 IP Pool CRUD**
- Admin can add/edit/delete IP pools (CIDR, gateway, VLAN ID, cluster)
- Backend: CRUD for `ip_pools` and `ip_addresses` tables
- Frontend: Enhance `/admin/ip-pools` with add/edit forms

**2.2 VLAN management**
- Display VLANs configured per IP pool
- Associate VLANs with network bridges

### Phase 3: Cluster infrastructure UI

**3.1 Add cluster form**
- Admin form: name, display_name, API URL, cert/key file paths
- Uses existing `POST /admin/clusters/add` API
- Validate connectivity before saving

**3.2 Add node to cluster**
- Admin enters node hostname + SSH credentials
- Backend: SSH to node → install Incus → join cluster → add Ceph OSD
- This is the most complex feature — requires SSH provisioner

**3.3 Ceph health dashboard**
- New admin page `/admin/ceph`
- Backend: `ceph -s` equivalent via Ceph REST API or SSH
- Show: cluster health, OSD count, pool usage, PG status

### Phase 4: VM detail page (DigitalOcean-style)

**4.1 Dedicated VM detail route `/admin/vms/:name`**
- Overview: status, IP, config, node, created date
- Graphs: CPU/Memory/Disk/Network (reuse monitoring)
- Actions: Start/Stop/Restart/Reinstall/Delete
- Console: embedded xterm.js
- Snapshots: list/create/restore/delete
- Firewall: per-VM rules (future)
- Activity: filtered audit log for this VM

**4.2 User VM detail route `/vms/:id`**
- Same as admin but scoped to user's VMs
- No node/cluster info exposed

### Phase 5: Missing UX polish

**5.1 Logout button** — clear oauth2-proxy session + redirect
**5.2 Global toast/notification system** — success/error feedback
**5.3 Admin create VM → select cluster** (not just first)
**5.4 Admin VM actions → update DB status** (currently only Incus)
**5.5 Order-driven flow complete** — billing page "Buy" → OS selection → create order → pay → result with credentials

### Phase 6: Operations — Storage (Ceph Dashboard)

Reference: [Ceph Dashboard](https://docs.ceph.com/en/latest/mgr/dashboard/), Proxmox Ceph integration

**6.1 Ceph cluster overview `/admin/storage`**
- Health status (HEALTH_OK / WARN / ERR) with color
- Cluster IOPS, throughput, latency
- Capacity: total / used / available with pie chart
- PG status distribution

**6.2 OSD management**
- List all OSDs: ID, host, status (up/down/in/out), weight, usage %
- Actions: mark in/out, reweight
- Performance: commit latency, apply latency per OSD

**6.3 Pool management**
- List pools: name, size, min_size, PG count, usage, applications
- Create pool (name, pg_num, size, application type)
- Edit pool (size, min_size, quotas)
- Autoscale status

**6.4 Storage alerts**
- Near-full warnings (>75%, >85%, >95%)
- OSD down alerts
- Slow OSD detection

### Phase 7: Operations — Network (IPAM, NetBox-inspired)

Reference: [NetBox IPAM](https://netboxlabs.com/docs/netbox/features/ipam/)

**7.1 IP prefix/subnet management `/admin/networks`**
- Hierarchical prefix tree (like NetBox)
- CRUD: add prefix (CIDR, VLAN, gateway, description)
- Visual utilization bar per prefix
- Auto-calculate available IPs

**7.2 IP address registry**
- List all assigned IPs: IP, VM name, status (assigned/available/reserved/cooldown)
- Manual assign/release
- Cooldown timer for recently released IPs
- Search by IP or VM name

**7.3 VLAN management**
- CRUD VLANs: ID, name, description, associated prefix
- Map VLANs to network bridges
- Show which VMs are on which VLAN

**7.4 Network topology view** (future)
- Visual diagram of nodes, bridges, VLANs, VM connections

### Phase 8: Operations — Node lifecycle (Proxmox-inspired)

Reference: [Proxmox Cluster Manager](https://pve.proxmox.com/wiki/Cluster_Manager)

**8.1 Node detail page `/admin/nodes/:name`**
- Real-time graphs: CPU, RAM, disk I/O, network I/O (from scheduler cache)
- VM list running on this node
- Storage: local disks, OSD status
- Network interfaces
- System info: uptime, kernel, Incus version

**8.2 Node provisioning wizard**
- Step 1: Enter IP, SSH credentials (or key)
- Step 2: Connectivity check (ping, SSH test)
- Step 3: Auto-install (Incus + Ceph OSD + monitoring agent)
- Step 4: Join cluster + verify
- Progress bar with real-time log output

**8.3 Node maintenance mode**
- Toggle button: enter/exit maintenance
- Auto-evacuate VMs before maintenance
- Prevent new VM placement on maintenance nodes
- Scheduler respects maintenance flag

**8.4 Node removal wizard**
- Step 1: Evacuate all VMs
- Step 2: Remove Ceph OSDs (wait for rebalance)
- Step 3: Leave Incus cluster
- Step 4: Remove from monitoring

### Phase 9: Operations — Alerting & Events

**9.1 Event stream `/admin/events`**
- Real-time SSE feed from Incus `/1.0/events`
- Filterable: VM lifecycle, cluster, storage, network
- Timestamp, severity, source node, message

**9.2 Alert rules**
- Configurable thresholds: CPU > X%, RAM > X%, disk > X%, OSD down, node offline
- Notification channels: in-app bell icon + future webhook/email
- Based on Prometheus Alertmanager (already running)

**9.3 Scheduled tasks**
- Cron-like UI for: auto-backup, expired VM cleanup, bandwidth reset
- Status: last run, next run, success/fail

### Phase 10: Operations — Observability

**10.1 Integrated Grafana embed** (quick win)
- Iframe embed of existing Grafana dashboards in admin panel
- SSO passthrough or anonymous viewer mode

**10.2 Built-in resource graphs**
- Per-node CPU/RAM/disk/network history (beyond current scheduler 60s snapshot)
- Store metrics in TimescaleDB or Prometheus long-term storage
- 1h / 24h / 7d / 30d time ranges

## Scope (Updated)

| Phase | Content | Effort | Priority |
|-------|---------|--------|----------|
| **1** | Critical UX fixes (password, VM list, products) | Small | **P0** |
| **2** | IP/VLAN management | Medium | P1 |
| **3** | Cluster + node UI (add cluster, add node, Ceph) | Large | P1 |
| **4** | VM detail page (DO-style) | Large | P2 |
| **5** | UX polish (logout, toast, cluster selector) | Medium | P2 |
| **6** | Ceph storage dashboard (OSD, pools, alerts) | Large | **P1** |
| **7** | Network IPAM (prefix tree, IP registry, VLAN) | Large | P1 |
| **8** | Node lifecycle (detail, provision, maintenance, remove) | X-Large | P1 |
| **9** | Alerting & events (event stream, alert rules, cron UI) | Large | P2 |
| **10** | Observability (Grafana embed, long-term metrics) | Medium | P3 |

## Risks

1. **Ceph Dashboard integration**: Ceph Manager REST module (port 8443) provides all needed data, but requires authentication setup. Alternative: parse `ceph -s` output via SSH.
2. **Node provisioning via SSH**: Complex multi-step operation. Mitigate with progress UI + rollback on failure.
3. **Per-VM firewall**: Incus supports security.acls and nftables device rules. Need to design a good UI abstraction.
4. **Network topology view**: Requires graph rendering library (e.g., vis-network). Defer to Phase 10+.
5. **Metrics history**: Current system only has 60s scheduler snapshots. Long-term graphs need Prometheus integration or own time-series storage.

## Competitor feature matrix (Operations)

| Feature | Proxmox | OpenStack | Ceph Dashboard | IncusAdmin |
|---------|---------|-----------|----------------|------------|
| Node detail (CPU/RAM graphs) | ✅ | ✅ | — | ❌ |
| Node add/remove wizard | ✅ | ✅ | — | ❌ |
| Node maintenance mode | ✅ | ✅ | — | ⚠️ evacuate only |
| OSD management | ✅ | — | ✅ | ❌ |
| Pool CRUD | ✅ | ✅ | ✅ | ❌ |
| Storage health/alerts | ✅ | ✅ | ✅ | ❌ |
| IP prefix tree | — | ✅ | — | ❌ |
| VLAN CRUD | ✅ | ✅ | — | ❌ |
| IP address registry | — | ✅ | — | ❌ |
| Event stream | ✅ | ✅ | ✅ | ❌ |
| Alert rules | ✅ | ✅ | ✅ | ❌ |
| Scheduled tasks | ✅ | ✅ | — | ❌ |
| Grafana integration | ✅ | ✅ | ✅ | ❌ |
| VM console (browser) | ✅ | ✅ | — | ✅ |
| VM snapshots | ✅ | ✅ | — | ✅ |
| VM live migration | ✅ | ✅ | — | ✅ evacuate |
| Cluster HA | ✅ | ✅ | — | ✅ |

## Alternatives

### VM creation flow
**Recommendation**: Hybrid — products as cards in create form, admin can also customize.

### Ceph integration
| Option | Pros | Cons |
|--------|------|------|
| **Ceph Manager REST API (chosen)** | Official, comprehensive | Requires auth setup |
| Parse `ceph` CLI via SSH | Simple | Fragile, no real-time |
| Prometheus + Grafana embed | Already running | No write operations |

### IPAM
| Option | Pros | Cons |
|--------|------|------|
| **Built-in (chosen)** | Integrated, no dependency | Build from scratch |
| NetBox integration | Full-featured IPAM | External dependency, complex |
| DB-only (current) | Simple | No prefix hierarchy |

## Annotations

(User annotations and responses. Keep all history.)

### 2026-04-15 22:30 — Deep ops research added

Expanded from 5 phases to 10 phases based on Proxmox VE, OpenStack Horizon, Ceph Dashboard, and NetBox research. New phases 6-10 cover storage ops, network IPAM, node lifecycle, alerting/events, and observability. Competitor matrix now covers 17 operations features.

### 2026-04-15 23:00 — Graph-verified root cause analysis

**User Journey 1: Admin creates VM → no password shown**
- Graph verified: `AdminVMHandler.CreateVM` → `VMService.Create` → returns `CreateVMResult{VMName, IP, Username, Password, Node}` → `writeJSON(w, 201, result)` — **backend returns password correctly**
- Root cause: `admin/create-vm.tsx` line 40-42: `onSuccess: () => { navigate({ to: "/admin/vms" }) }` — **discards response data, immediately navigates**
- Fix: show modal with credentials before navigating

**User Journey 2: Admin VM not in "My VMs"**
- Graph verified: `AdminVMHandler.CreateVM` sets `UserID: 0` (line 445) and **never calls vmRepo.Create** — admin-created VMs are only in Incus, not in DB
- Root cause: Two disconnected data sources — "My VMs" reads DB (`vmRepo.ListByUser`), "All VMs" reads Incus API (`ListInstances`)
- Fix: Admin CreateVM should write to DB with admin's userID (or target user)

**User Journey 3: Products not in user VM creation**
- Graph verified: `vms.tsx` hardcodes `SIZES = [{Small, 1C/1G/25G}, {Medium, 2C/2G/50G}, {Large, 4C/4G/100G}]`
- Products API `GET /portal/products` exists and works, but `vms.tsx` doesn't call it
- Fix: Replace hardcoded SIZES with dynamic products from API

**User Journey 4: IP pool management**
- Graph verified: `IPPoolHandler` has only `ListPools` (read-only), no Create/Update/Delete
- IP pools come from config `cc.IPPools` (env-based), not DB `ip_pools` table
- Fix: CRUD for ip_pools table + API + frontend form

**User Journey 5: Add cluster**
- Backend `POST /admin/clusters/add` exists (`ClusterMgmtHandler.AddCluster`)
- Frontend: zero UI — Grep found no references to this API in web/src
- Fix: Add form in clusters page

**User Journey 6: Ceph status**
- Zero Ceph integration in codebase — only "ceph-pool" string literal
- Fix: New backend endpoint querying Ceph via SSH or Manager REST API

**Additional gaps found**:
- Admin `CreateVM` doesn't write to DB → admin VMs invisible to billing/quota/My VMs
- Admin `DeleteVM` doesn't update DB status → orphaned DB records
- No logout button in frontend
- No global success/error toast notification system
- Console "Back to VMs" hardcoded to `/admin/vms`
- Dashboard VM count only queries first cluster
