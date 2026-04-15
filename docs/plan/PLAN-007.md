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

## Scope

| Phase | Effort | Priority |
|-------|--------|----------|
| 1: Critical UX fixes | Small | P0 — immediate |
| 2: IP/VLAN management | Medium | P1 |
| 3: Cluster infra UI | Large | P1 |
| 4: VM detail page | Large | P2 |
| 5: UX polish | Medium | P2 |

## Risks

1. Ceph health API requires either SSH access to a mon node or REST API — need to research Ceph Manager REST module
2. Node provisioning via SSH is complex and error-prone
3. Per-VM firewall requires nftables or Incus device rules — significant new capability

## Alternatives

### VM creation flow

| Option | Pros | Cons |
|--------|------|------|
| **Product-based (DigitalOcean style)** | Integrated pricing, one flow | Requires products to be configured first |
| Keep separate catalog + order | More flexible billing | Two-step UX |
| Hybrid (recommended) | Products as presets in create form | Best of both |

**Recommendation**: Hybrid — show products as cards in create form, let admin also customize.

## Annotations

(User annotations and responses. Keep all history.)
