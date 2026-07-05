-- +goose Up
-- PLAN-035: user-owned firewall groups
--
-- 模型从"admin 模板 + 用户应用"扩展为"admin 共享组 + 用户私有组"双轨：
--   * owner_id IS NULL  → admin 共享组（任何用户可绑），保留生产现有 3 个 seed 组
--   * owner_id = users.id → 用户私有组，仅 owner 可见 / 编辑 / 绑到自己 VM
--
-- Slug 唯一性需要按 (owner, slug) 隔离——不同用户可以都用 slug "myapp"。
-- 用 COALESCE(owner_id, 0) 把 NULL 映射成哨兵 0，避免 PostgreSQL UNIQUE NULL 不唯一的陷阱。
-- Service 层 ACLName 同样按 OwnerID 决定 Incus ACL 命名前缀：
--   * admin 共享组保留旧名 fwg-<slug>（不破坏生产已绑 VM 的 NIC security.acls 配置）
--   * 用户私有组用 fwg-u<owner_id>-<slug>，namespace 隔离

ALTER TABLE firewall_groups
    ADD COLUMN IF NOT EXISTS owner_id BIGINT NULL REFERENCES users(id) ON DELETE CASCADE;

-- 删旧 slug 唯一约束（migration 011 建的）；用组合 (owner, slug) 替代
ALTER TABLE firewall_groups DROP CONSTRAINT IF EXISTS firewall_groups_slug_key;
CREATE UNIQUE INDEX IF NOT EXISTS firewall_groups_owner_slug_key
    ON firewall_groups (COALESCE(owner_id, 0), slug);

-- 仅给用户私有组建索引（admin 共享组数极少，全扫即可）
CREATE INDEX IF NOT EXISTS idx_firewall_groups_owner
    ON firewall_groups (owner_id) WHERE owner_id IS NOT NULL;

-- 用户级配额：
--   max_firewall_groups        每人最多 firewall group 数
--   max_firewall_rules_per_group  每组最多规则数
-- 默认 5 / 20，admin 后续按需调整。
ALTER TABLE quotas
    ADD COLUMN IF NOT EXISTS max_firewall_groups INT NOT NULL DEFAULT 5;
ALTER TABLE quotas
    ADD COLUMN IF NOT EXISTS max_firewall_rules_per_group INT NOT NULL DEFAULT 20;

-- +goose Down
ALTER TABLE quotas DROP COLUMN IF EXISTS max_firewall_rules_per_group;
ALTER TABLE quotas DROP COLUMN IF EXISTS max_firewall_groups;
DROP INDEX IF EXISTS idx_firewall_groups_owner;
DROP INDEX IF EXISTS firewall_groups_owner_slug_key;
ALTER TABLE firewall_groups DROP COLUMN IF EXISTS owner_id;
-- 注意：不自动恢复 011 的 firewall_groups_slug_key 全局唯一约束——
-- 用户私有组允许跨用户重名 slug，直接重建唯一约束可能因重复值失败。
-- 如需回到 019 语义，运维应先去重再手动 ADD CONSTRAINT。
