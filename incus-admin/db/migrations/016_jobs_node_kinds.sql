-- +goose Up
-- PLAN-026 / INFRA-002：扩 provisioning_jobs.kind CHECK 约束
--
-- migration 015 限制 kind 仅为 vm.create / vm.reinstall。INFRA-002 复用
-- 同一 jobs runtime + SSE 基础设施做集群节点 add/remove 编排，需要加两
-- 个新 kind。其余字段 schema 完全沿用，无需新表。

ALTER TABLE provisioning_jobs DROP CONSTRAINT IF EXISTS provisioning_jobs_kind_check;

ALTER TABLE provisioning_jobs
  ADD CONSTRAINT provisioning_jobs_kind_check
  CHECK (kind IN ('vm.create','vm.reinstall','cluster.node.add','cluster.node.remove'));

-- +goose Down
ALTER TABLE provisioning_jobs DROP CONSTRAINT IF EXISTS provisioning_jobs_kind_check;
ALTER TABLE provisioning_jobs
  ADD CONSTRAINT provisioning_jobs_kind_check
  CHECK (kind IN ('vm.create','vm.reinstall'));
