-- +goose Up
-- PLAN-021 Phase G: floating_ips
--
-- A floating IP is a public IPv4 address that admin explicitly reserves and
-- can attach to one VM at a time. Separate from ip_pools/ip_addresses (which
-- drive per-VM allocation at create time) because the lifecycle is
-- different: floating IPs are:
--   - admin-allocated (not auto-assigned)
--   - long-lived (outlive any single VM)
--   - explicitly moved between VMs (the key selling point)
--
-- State machine:
--     NULL → allocated  (status='available',  bound_vm_id NULL)
--     available → attached  (status='attached', bound_vm_id=X, attached_at NOW)
--     attached  → available (status='available', bound_vm_id NULL, detached_at NOW)
--     available → released  (DELETE row; audit keeps history)
--
-- Notes on packet plumbing (admin runbook, not enforced by this table):
--   - bridge.nat=false on br-pub so `incus network forward` is not viable
--   - Attach sets NIC security.ipv4_filtering=false so the hypervisor bridge
--     accepts the secondary MAC/IP tuple
--   - VM OS still needs `ip addr add X.Y.Z.W/26 dev eth0` + `arping -U` for
--     upstream routers to learn the new MAC (returned as runbook hint)

CREATE TABLE IF NOT EXISTS floating_ips (
    id           SERIAL PRIMARY KEY,
    cluster_id   INT NOT NULL REFERENCES clusters(id),
    ip           INET NOT NULL UNIQUE,
    bound_vm_id  INT REFERENCES vms(id) ON DELETE SET NULL,
    status       TEXT NOT NULL DEFAULT 'available',  -- 'available' | 'attached'
    description  TEXT NOT NULL DEFAULT '',
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attached_at  TIMESTAMPTZ,
    detached_at  TIMESTAMPTZ,
    CONSTRAINT floating_ips_status_chk CHECK (status IN ('available', 'attached')),
    CONSTRAINT floating_ips_bound_consistency CHECK (
        (status = 'available' AND bound_vm_id IS NULL) OR
        (status = 'attached'  AND bound_vm_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_floating_ips_vm ON floating_ips(bound_vm_id) WHERE bound_vm_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_floating_ips_status ON floating_ips(status);

-- +goose Down
DROP TABLE IF EXISTS floating_ips;
