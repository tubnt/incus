-- +goose Up
-- PLAN-037 / OPS-040：扩 provisioning_jobs.kind CHECK 约束
--
-- 复用同一 jobs runtime + SSE 基础设施做"批量冷迁移"编排，加一个新
-- kind。其余字段 schema 完全沿用，无需新表。

ALTER TABLE provisioning_jobs DROP CONSTRAINT IF EXISTS provisioning_jobs_kind_check;

ALTER TABLE provisioning_jobs
  ADD CONSTRAINT provisioning_jobs_kind_check
  CHECK (kind IN (
    'vm.create',
    'vm.reinstall',
    'cluster.node.add',
    'cluster.node.remove',
    'cluster.vm.migrate-batch'
  ));

-- +goose Down
ALTER TABLE provisioning_jobs DROP CONSTRAINT IF EXISTS provisioning_jobs_kind_check;
ALTER TABLE provisioning_jobs
  ADD CONSTRAINT provisioning_jobs_kind_check
  CHECK (kind IN (
    'vm.create',
    'vm.reinstall',
    'cluster.node.add',
    'cluster.node.remove'
  ));
