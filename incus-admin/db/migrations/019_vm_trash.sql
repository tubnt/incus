-- +goose Up
-- PLAN-034: VM trash-with-undo（30s 回收站窗口）
--
-- 现状：VM 删除走硬路径——先停机再 Incus DELETE 再 DB UPDATE status='deleted'，
-- 完全无回退余地。NN/g 危险动作研究指出："仅最危险 + 最罕见动作才用 type-to-confirm"，
-- 普通 VM 删除应给短窗口的撤销机会。
--
-- 设计：
--   trashed_at TIMESTAMPTZ NULL  —— 进回收站时刻；NULL 表示未在回收站
--   trashed_prev_status TEXT     —— 记原 status，restore 后据此决定是否再启动
--
-- 列表 / 详情 / IP-by-name / reconcile / count 等 SELECT 全部加 trashed_at IS NULL 过滤
-- 由 repository/vm.go 一并处理。后端 worker 30s 后扫到 trashed_at <= NOW()-30s 的行
-- 走原 hard-delete 路径（先停 Incus 实例再 Incus DELETE）。

ALTER TABLE vms ADD COLUMN IF NOT EXISTS trashed_at TIMESTAMPTZ;
ALTER TABLE vms ADD COLUMN IF NOT EXISTS trashed_prev_status TEXT;

-- 仅给 trashed 行建索引，正常列表读不会膨胀
CREATE INDEX IF NOT EXISTS idx_vms_trashed_at
    ON vms (trashed_at)
    WHERE trashed_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_vms_trashed_at;
ALTER TABLE vms DROP COLUMN IF EXISTS trashed_prev_status;
ALTER TABLE vms DROP COLUMN IF EXISTS trashed_at;
