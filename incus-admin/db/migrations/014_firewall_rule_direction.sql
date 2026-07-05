-- +goose Up
-- OPS-017 Phase: firewall_rules.direction (ingress / egress)
--
-- Phase E shipped ingress-only because every customer scenario at the time
-- read "let 22/80/443 in". OPS-017 adds outbound (egress) so admins can
-- model "this VM may not call out to *", "blocklist 25 to stop spam",
-- "egress only to RFC1918". Default is 'ingress' to match historical rows
-- + existing tests.
--
-- Mirrors Incus ACL rule.direction, so the service-layer EnsureACL only
-- has to forward the value verbatim into the rules array it builds.

ALTER TABLE firewall_rules
    ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'ingress';

-- Cheap check: invalid values would silently disable a rule when forwarded
-- to Incus, so we'd rather hard-fail at insert time than chase ghosts later.
ALTER TABLE firewall_rules
    ADD CONSTRAINT firewall_rules_direction_check
        CHECK (direction IN ('ingress', 'egress'));

-- +goose Down
ALTER TABLE firewall_rules DROP CONSTRAINT IF EXISTS firewall_rules_direction_check;
ALTER TABLE firewall_rules DROP COLUMN IF EXISTS direction;
