-- PLAN-020 Phase D: healing_events
-- Durable record of node evacuation / auto-healing cycles. Distinct from
-- audit_logs: business-level event with structured fields so /admin/ha
-- history UI can render it directly. Admin manual evacuate + Incus auto
-- evacuate both land here (differentiated by trigger).

CREATE TABLE IF NOT EXISTS healing_events (
    id             SERIAL PRIMARY KEY,
    cluster_id     INT NOT NULL REFERENCES clusters(id),
    node_name      TEXT NOT NULL,
    trigger        TEXT NOT NULL,                      -- 'manual' | 'auto' | 'chaos'
    actor_id       INT REFERENCES users(id),           -- filled when trigger='manual' or 'chaos'
    evacuated_vms  JSONB DEFAULT '[]'::jsonb,          -- [{vm_id, name, from_node, to_node}]
    started_at     TIMESTAMPTZ DEFAULT NOW(),
    completed_at   TIMESTAMPTZ,
    status         TEXT NOT NULL DEFAULT 'in_progress', -- 'in_progress'|'completed'|'failed'|'partial'
    error          TEXT
);

CREATE INDEX IF NOT EXISTS idx_healing_events_cluster ON healing_events(cluster_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_healing_events_node    ON healing_events(node_name, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_healing_events_status  ON healing_events(status) WHERE status = 'in_progress';
