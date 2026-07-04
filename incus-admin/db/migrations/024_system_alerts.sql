-- +goose Up
-- PLAN-039 / OPS-044：不均衡持续监控告警表
--
-- 复用 jobs runtime / healing_events 不合适（语义不同）；新表专注 system-level
-- 告警，未来可扩 kind 为 storage / network / cluster_quorum 等。

CREATE TABLE IF NOT EXISTS system_alerts (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL CHECK (kind IN ('imbalance')),
    cluster      TEXT NOT NULL,
    severity     TEXT NOT NULL DEFAULT 'warning'
                 CHECK (severity IN ('info','warning','error')),
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ,
    dismissed_by INT REFERENCES users(id) ON DELETE SET NULL
);

-- 单 cluster + kind 同时只能有一条 active alert（resolved_at IS NULL）。
-- watchdog 周期 upsert 走该 partial unique index。
CREATE UNIQUE INDEX IF NOT EXISTS system_alerts_active_uniq
    ON system_alerts (cluster, kind)
    WHERE resolved_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_system_alerts_active
    ON system_alerts (created_at DESC) WHERE resolved_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS system_alerts;
