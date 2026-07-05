-- +goose Up
-- PLAN-036 用户级防火墙集中管理：默认 firewall group 列表（per-user）
--
-- 用 junction table 而不是 users.default_group_ids INT[]，原因：
--   * FK ON DELETE CASCADE：用户/组删除时自动清空对应行，无需 trigger
--   * 排序字段 sort_order 决定 attach 到新 VM 时的 ACL 列表顺序
--   * 标准 N:N 模型，repo 层与 firewall_groups / vms 一致
--
-- 仅"新建 VM"会读这个表的内容（service/jobs/vm_create finalize 步骤）；
-- 用户改 default 列表不会自动同步到现有 VM，要应用到现有 VM 需用批量绑定。
-- 见 PLAN-036 D7。

CREATE TABLE IF NOT EXISTS user_default_firewall_groups (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES firewall_groups(id) ON DELETE CASCADE,
    sort_order INT    NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_default_firewall_groups_order
    ON user_default_firewall_groups (user_id, sort_order, group_id);

-- +goose Down
DROP TABLE IF EXISTS user_default_firewall_groups;
