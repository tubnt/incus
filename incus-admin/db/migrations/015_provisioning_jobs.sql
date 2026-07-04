-- +goose Up
-- PLAN-025 / INFRA-007: VM provisioning 异步化基础设施
--
-- 三件事：
--   1) provisioning_jobs：每次 VM 创建/重装的 job 记录，承载状态机 + 退款幂等位
--   2) provisioning_job_steps：步骤明细，seq 既是顺序也是 SSE 的 Last-Event-ID
--   3) vms 加 partial UNIQUE：保证 (cluster_id, name) 在活跃态唯一；deleted/gone
--      的历史墓碑行不参与，避免历史数据迁移成本
--
-- 历史 bug：vms.name 没有 UNIQUE 约束。原 sync 流程靠 Incus 端拒重名兜底；
-- 异步化后 handler 要"先 INSERT vms row=creating 再入队 job"，并发情况下两次
-- 同名请求会撞 race，必须 DB 层强约束。

CREATE TABLE IF NOT EXISTS provisioning_jobs (
    id              SERIAL PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('vm.create','vm.reinstall')),
    user_id         INT NOT NULL REFERENCES users(id),
    cluster_id      INT NOT NULL REFERENCES clusters(id),
    -- vm.create 阶段 order_id 必填；vm.reinstall NULL（重装不走订单）
    order_id        INT REFERENCES orders(id),
    -- 早期阶段（allocate_ip 之后）就把 vms 行 INSERT 拿到 id 写回这里
    vm_id           INT REFERENCES vms(id),
    target_name     TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','running','succeeded','failed','partial')),
    error           TEXT,
    -- 幂等 guard：rollback 走 worker 重跑时只有 IS NULL 才执行退款，防双倍退款
    refund_done_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ
);

-- 进程崩溃恢复扫描的"加速车道"：只索引未终态的 row
CREATE INDEX IF NOT EXISTS idx_jobs_active
    ON provisioning_jobs(status, created_at) WHERE status IN ('queued','running');
-- 用户主页倒序看 job 列表
CREATE INDEX IF NOT EXISTS idx_jobs_user
    ON provisioning_jobs(user_id, created_at DESC);
-- 通过 vm_id 反查 job（前端 VM 详情页 reinstall 进度）
CREATE INDEX IF NOT EXISTS idx_jobs_vm
    ON provisioning_jobs(vm_id) WHERE vm_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS provisioning_job_steps (
    id           SERIAL PRIMARY KEY,
    job_id       INT NOT NULL REFERENCES provisioning_jobs(id) ON DELETE CASCADE,
    -- seq 0,1,2,... 是步骤顺序，也用作 SSE Last-Event-ID。
    -- UNIQUE(job_id, seq) 保证 worker 不会同步写同一 seq 两次。
    seq          INT NOT NULL,
    name         TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped')),
    detail       TEXT,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    UNIQUE(job_id, seq)
);

-- 实时按 job 顺序流式拉 step（SSE handler / GET /jobs/{id}）
CREATE INDEX IF NOT EXISTS idx_job_steps_job
    ON provisioning_job_steps(job_id, seq);

-- vms 活跃态唯一约束。历史 deleted/gone 的墓碑行不参与（partial）。
-- 命名 vms_cluster_name_active_uniq 让违约错误日志一眼就能定位是这个约束。
CREATE UNIQUE INDEX IF NOT EXISTS vms_cluster_name_active_uniq
    ON vms (cluster_id, name)
    WHERE status NOT IN ('deleted','gone');

-- +goose Down
DROP INDEX IF EXISTS vms_cluster_name_active_uniq;
DROP TABLE IF EXISTS provisioning_job_steps;
DROP TABLE IF EXISTS provisioning_jobs;
