-- +goose Up
-- PLAN-021 Phase E: firewall groups (security groups)
--
-- Thin DB model on top of Incus network ACLs. One firewall_group row ==
-- one Incus ACL named "fwg-<slug>" in the default project (the customers
-- project shares networks with default, so ACLs are effectively
-- cluster-global and can be attached to any NIC regardless of project).
--
-- firewall_rules is a flat, ingress-only table. Incus supports egress rules
-- too but admin users only ever ask for "let 22/80/443 in", so we stop at
-- that scope for Phase E. Promote to bidirectional when a real use case
-- shows up.
--
-- vm_firewall_bindings is a plain N:N join. NIC security.acls on the
-- instance side is the source of truth; this table is a mirror so the
-- admin UI can list "which VMs belong to group X?" without hitting Incus.

CREATE TABLE IF NOT EXISTS firewall_groups (
    id          SERIAL PRIMARY KEY,
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS firewall_rules (
    id               SERIAL PRIMARY KEY,
    group_id         INT NOT NULL REFERENCES firewall_groups(id) ON DELETE CASCADE,
    action           TEXT NOT NULL,                            -- 'allow' | 'reject' | 'drop'
    protocol         TEXT NOT NULL DEFAULT 'tcp',              -- 'tcp' | 'udp' | 'icmp4' | 'icmp6' | ''
    destination_port TEXT NOT NULL DEFAULT '',                 -- '22' | '22,80,443' | '1000-2000'
    source_cidr      TEXT NOT NULL DEFAULT '',                 -- '' = any; '10.0.0.0/8'
    description      TEXT NOT NULL DEFAULT '',
    sort_order       INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_firewall_rules_group ON firewall_rules(group_id, sort_order);

CREATE TABLE IF NOT EXISTS vm_firewall_bindings (
    vm_id      INT NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    group_id   INT NOT NULL REFERENCES firewall_groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (vm_id, group_id)
);

-- Seed three batteries-included groups so a fresh deploy doesn't start empty.
-- Slugs are stable and may be referenced by downstream automation; do not
-- rename. Rule ordering matches Incus's top-to-bottom match-and-stop
-- semantics — explicit allow rules first, implicit default deny comes from
-- Incus's fall-through behavior when no rule matches.

INSERT INTO firewall_groups (slug, name, description) VALUES
    ('default-web',  'Web (HTTP + HTTPS + SSH)', 'Allow 22/80/443 from anywhere'),
    ('ssh-only',     'SSH Only',                  'Allow 22 from anywhere'),
    ('database-lan', 'Database (LAN only)',       'Allow 22/3306/5432 from RFC1918 ranges')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '22,80,443', '', 'web + ssh', 10
FROM firewall_groups g WHERE g.slug = 'default-web'
ON CONFLICT DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '22', '', 'ssh', 10
FROM firewall_groups g WHERE g.slug = 'ssh-only'
ON CONFLICT DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '22', '10.0.0.0/8', 'ssh from LAN', 10
FROM firewall_groups g WHERE g.slug = 'database-lan'
ON CONFLICT DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '3306,5432', '10.0.0.0/8', 'db from LAN', 20
FROM firewall_groups g WHERE g.slug = 'database-lan'
ON CONFLICT DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '3306,5432', '192.168.0.0/16', 'db from LAN /16', 30
FROM firewall_groups g WHERE g.slug = 'database-lan'
ON CONFLICT DO NOTHING;

INSERT INTO firewall_rules (group_id, action, protocol, destination_port, source_cidr, description, sort_order)
SELECT g.id, 'allow', 'tcp', '3306,5432', '172.16.0.0/12', 'db from LAN /12', 40
FROM firewall_groups g WHERE g.slug = 'database-lan'
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS vm_firewall_bindings;
DROP TABLE IF EXISTS firewall_rules;
DROP TABLE IF EXISTS firewall_groups;
