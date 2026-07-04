-- +goose Up
-- PLAN-021 Phase F: add new public IP segment 202.151.179.0/26 VLAN 376
--
-- Physical state (verified 2026-04-24 via ping from node1):
--   - br-pub already trunks VLAN 376 across all 5 cluster nodes
--   - Gateway 202.151.179.62 is reachable (ICMP 0.8 ms)
--   - Existing /27 pool (202.151.179.224-.254) stays untouched
--
-- Seed strategy:
--   - Network:   .0  (reserved)
--   - Gateway:   .62 (reserved)
--   - Broadcast: .63 (reserved)
--   - Low range .1-.9: reserved for future network infrastructure (DNS,
--     management, gateway redundancy) — not seeded to keep them free
--   - VM range:  .10-.61 (52 usable hosts) — seeded as 'available'
--
-- Idempotency: ON CONFLICT on unique cidr / unique ip so reapplying is safe.

INSERT INTO ip_pools (cluster_id, cidr, gateway, vlan_id)
SELECT id, '202.151.179.0/26'::cidr, '202.151.179.62'::inet, 376
FROM clusters WHERE name = 'cn-sz-01'
ON CONFLICT (cidr) DO NOTHING;

INSERT INTO ip_addresses (pool_id, ip, status)
SELECT p.id, ('202.151.179.' || n)::inet, 'available'
FROM ip_pools p
CROSS JOIN generate_series(10, 61) AS n
WHERE p.cidr = '202.151.179.0/26'::cidr
ON CONFLICT (ip) DO NOTHING;

-- +goose Down
-- no-op：本迁移仅 seed 公网 IP 段（202.151.179.0/26 及 .10-.61 地址）。
-- 这些地址可能已被 VM 分配占用，回滚删除会破坏在网数据，故不自动回退。
-- 如确需下线该网段，运维在确认无占用后手动 DELETE。
