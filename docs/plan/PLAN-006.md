# PLAN-006 Infrastructure automation — HA failover, node management, standalone host

- **status**: draft
- **createdAt**: 2026-04-15 18:00
- **approvedAt**: (pending)
- **relatedTask**: INFRA-001, INFRA-002, INFRA-003

## Context

### Product direction
IncusAdmin is an internal private cloud management system for 5ok.co. Current state: 5-node Incus + Ceph cluster managed via Go+React platform. Future: API for external resellers. Current focus: internal operations efficiency.

### Current infrastructure capabilities
- 5-node cluster (202.151.179.224/27) with Ceph 29 OSD / 25 TiB
- Manual node setup via shell scripts (`cluster/scripts/`)
- `cluster/scripts/setup-healing.sh` exists but needs verification
- No UI for cluster topology management
- No standalone host management
- No automated node provisioning

### Incus native capabilities (researched)

**Auto-healing / Failover:**
- `cluster.healing_threshold` config — automatically evacuate instances from offline nodes
- Requires shared storage (Ceph) — ✅ we have this
- Instances must not use local devices — need to verify our VMs
- Risk: partial connectivity can cause data corruption → need proper fencing
- Known issues in 2025-2026: VMs sometimes stay in "Error" state, websocket errors during evacuation with Ceph

**Node management:**
- `incus cluster add <name>` generates a join token
- New node runs `incus admin init` with the token to join
- `incus-deploy` (official Ansible playbooks) can automate full cluster + Ceph + OVN deployment

**Standalone host:**
- Any Incus server can be managed via its REST API with mTLS
- Current IncusAdmin `cluster.Manager` already supports multi-cluster via config
- Standalone = single-node "cluster" with local storage

## Proposal

### Phase 6A: VM auto-failover (HA)

1. **Enable and configure cluster healing**
   - Set `cluster.healing_threshold` via Incus API
   - Verify all VMs use shared Ceph storage (no local devices)
   - Add healing status to admin dashboard (node health, evacuation events)

2. **Fencing and safety**
   - IPMI/BMC integration for hard fencing (prevent split-brain)
   - Pre-flight check: verify VM storage is on Ceph before marking as HA-capable
   - Alert on partial connectivity (node flapping)

3. **Admin UI for HA status**
   - Node health timeline
   - Evacuation history log
   - Manual evacuation trigger button
   - HA-capable badge per VM

### Phase 6B: Cluster node management

1. **Add node workflow (UI-driven)**
   - Admin enters: hostname, IP, SSH credentials (or key)
   - Backend: SSH to new server → install Incus → `incus cluster add` on leader → get join token → `incus admin init` on new node with token → join Ceph as OSD
   - Steps: OS prep → Incus install → cluster join → Ceph OSD add → network config → monitoring agent

2. **Remove node workflow**
   - Evacuate all VMs first
   - Remove from Incus cluster
   - Remove Ceph OSDs (wait for rebalance)
   - Remove monitoring targets

3. **Node status dashboard**
   - Real-time node list with CPU/RAM/disk/network
   - Node roles (database-leader, database, database-standby)
   - Add/Remove buttons
   - Maintenance mode toggle (evacuate + prevent new placement)

### Phase 6C: Standalone Incus host management

1. **Add standalone host**
   - Admin provides: hostname, API URL, TLS client cert/key
   - Backend registers as a new "cluster" with 1 node in `cluster.Manager`
   - Persist to database (currently config-driven; needs DB migration)

2. **Unified management**
   - All VMs across clusters + standalone hosts in one view
   - Same operations: create/start/stop/delete/console/snapshot/monitor
   - Storage type indicator (Ceph shared vs local)

3. **DB migration**
   - Move cluster config from env vars to `clusters` table
   - Support dynamic add/remove without restart
   - Keep env-based config as bootstrap fallback

### Phase 6D: Auto-deploy new cluster (future)

1. **Cluster template**
   - Define: node count, network CIDR, Ceph config, monitoring setup
   - Based on `incus-deploy` Ansible playbooks

2. **Orchestration**
   - SSH to all target servers
   - Run `incus-deploy` with generated inventory
   - Register new cluster in IncusAdmin

3. **This phase is deferred** — requires significant Ansible/orchestration work and is not needed for initial internal use.

## Risks

1. **Healing threshold + Ceph**: Partial network partition can cause VMs to evacuate unnecessarily, then Ceph rebalance causes I/O storm. Mitigation: conservative threshold (300s+), IPMI fencing.
2. **SSH-based node deployment**: Requires SSH access from IncusAdmin to cluster nodes. Security: use dedicated deploy key, audit all commands.
3. **Standalone host certs**: Each host needs its own TLS cert pair uploaded. UX complexity for admin.
4. **Dynamic cluster config (6C)**: Moving from env to DB changes startup behavior. Need migration path and fallback.
5. **incus-deploy dependency (6D)**: Ansible on the IncusAdmin server adds operational complexity.

## Scope

| Phase | Effort | Priority |
|-------|--------|----------|
| 6A: VM auto-failover | Medium (mostly config + UI) | P1 — critical for production |
| 6B: Node management UI | Large (SSH orchestration + UI) | P1 — high operational value |
| 6C: Standalone host | Medium (DB migration + manager refactor) | P2 — extends reach |
| 6D: Auto-deploy cluster | Large (Ansible integration) | P3 — future |

Recommend: 6A → 6B → 6C sequentially. 6D deferred.

## Alternatives

### Failover approach

| Option | Pros | Cons |
|--------|------|------|
| **Incus native healing (chosen)** | Built-in, Ceph-aware | Limited control, known bugs |
| Custom watchdog + `incus cluster evacuate` | Full control over timing | More code to maintain |
| Pacemaker/Corosync | Industry standard HA | Heavy, complex setup |

### Node deployment

| Option | Pros | Cons |
|--------|------|------|
| **SSH + scripted steps (chosen)** | Simple, auditable | Fragile for complex setups |
| incus-deploy (Ansible) | Official, comprehensive | Ansible dependency |
| Cloud-init + PXE boot | Zero-touch | Requires DHCP/PXE infra |

## Annotations

(User annotations and responses. Keep all history.)
