-- +goose Up
-- PLAN-041 / INFRA-009：监控告警闭环
--
-- 三张新表 + system_alerts 扩展：
--   notify_channels    - 通知通道（钉钉 / 飞书 / 企微 / Webhook / SMTP），secret 加密存
--   alert_rules        - 阈值规则（CPU/内存/磁盘/VM down/节点 offline/job/balance）
--   alert_deliveries   - 分发记录，dedup 三元组 (channel_id, group_key, status)
--
-- system_alerts 扩展：放宽 kind CHECK + 加 group_key / scope_kind / scope_id，
-- 让评估器 / dispatcher 共用同一表。group_key 是稳定标识（"imbalance:cluster1"
-- / "vm_cpu:vm-abc"），dispatcher 按 (channel_id, group_key, status) 去重，避免
-- firing→resolved→firing 时 status 翻转的恢复通知被吞掉（社区 Alertmanager
-- 标准模式）。

-- ============================================================================
-- system_alerts 扩展
-- ============================================================================

-- 旧 CHECK 只有 'imbalance'；新评估器引入更多 kind，放宽到白名单。
ALTER TABLE system_alerts DROP CONSTRAINT IF EXISTS system_alerts_kind_check;
ALTER TABLE system_alerts ADD CONSTRAINT system_alerts_kind_check
    CHECK (kind IN (
        'imbalance',
        'vm_cpu', 'vm_mem', 'vm_disk', 'vm_down',
        'cluster_node_offline',
        'order_failed', 'job_failed',
        'balance_low',
        'channel_delivery_failed',
        'backup_failed' -- PLAN-040 二期接入预留
    ));

-- group_key：稳定的 dedup 标识，"<kind>:<scope>"。NULL 兼容旧行（imbalance）。
ALTER TABLE system_alerts ADD COLUMN IF NOT EXISTS group_key TEXT;

-- scope 三元组：scope_kind in (global/cluster/vm/user/node)，scope_id 是
-- vm_id / user_id / cluster_id / -1（global / cluster 时存 cluster_id）。
ALTER TABLE system_alerts ADD COLUMN IF NOT EXISTS scope_kind TEXT;
ALTER TABLE system_alerts ADD COLUMN IF NOT EXISTS scope_id BIGINT;

-- rule_id：触发本次 alert 的 rule（NULL = 内置 watchdog 触发，如 imbalance）。
ALTER TABLE system_alerts ADD COLUMN IF NOT EXISTS rule_id BIGINT;

-- 历史 imbalance 行回填 group_key（cluster 名 + ":" + kind）。
UPDATE system_alerts
SET group_key = kind || ':' || cluster
WHERE group_key IS NULL;

CREATE INDEX IF NOT EXISTS idx_system_alerts_group_key
    ON system_alerts (group_key, resolved_at);

-- P0 修复（CR #1）：仅 (cluster, kind) 唯一不够，多个 vm_down 等 scope-VM
-- 维度的告警会互相 UPDATE 覆盖。新加 (group_key) UNIQUE 让评估器路径以
-- group_key 为聚合键。watchdog 走 (cluster, kind) 索引保留兼容（imbalance
-- 每集群仅 1 行，group_key 天然等于 'imbalance:<cluster>'，两个索引一致）。
CREATE UNIQUE INDEX IF NOT EXISTS system_alerts_group_active_uniq
    ON system_alerts (group_key)
    WHERE resolved_at IS NULL AND group_key IS NOT NULL;

-- ============================================================================
-- notify_channels
-- ============================================================================
CREATE TABLE IF NOT EXISTS notify_channels (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL
                 CHECK (kind IN ('dingtalk','feishu','wecom','webhook','smtp')),
    -- AES-256-GCM 加密的 JSON config，复用 OPS-022 的 PASSWORD_ENCRYPTION_KEY。
    -- dingtalk: { "webhook_url": "...", "sign_secret": "..." }
    -- feishu:   { "webhook_url": "...", "sign_secret": "..." }
    -- wecom:    { "webhook_url": "..." }（无加签）
    -- webhook:  { "url": "https://...", "method": "POST", "headers": {...}, "bearer": "..." }
    -- smtp:     { "host": "...", "port": 587, "username": "...", "password": "...",
    --             "from": "alerts@x", "to": ["a@x","b@x"], "tls": "starttls"|"tls" }
    config_enc   TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notify_channels_enabled
    ON notify_channels (enabled, kind);

-- ============================================================================
-- alert_rules
-- ============================================================================
CREATE TABLE IF NOT EXISTS alert_rules (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    -- 与 system_alerts.kind 一致；评估器按 kind 派发到具体子例程。
    kind            TEXT NOT NULL
                    CHECK (kind IN (
                        'imbalance',
                        'vm_cpu','vm_mem','vm_disk','vm_down',
                        'cluster_node_offline',
                        'order_failed','job_failed',
                        'balance_low',
                        'backup_failed'
                    )),
    -- scope_kind：'global' / 'cluster' / 'vm' / 'user'。
    -- scope_id：vm_id / user_id / cluster_id；'global' 时为 NULL。
    scope_kind      TEXT NOT NULL DEFAULT 'global'
                    CHECK (scope_kind IN ('global','cluster','vm','user')),
    scope_id        BIGINT,
    -- 阈值（含义按 kind 解释）：
    --   vm_cpu/mem/disk: 0..1 百分比（0.9 = 90%）
    --   balance_low: 余额下限（USD）
    --   order_failed/job_failed/backup_failed: 时间窗口内最少失败次数
    --   vm_down/cluster_node_offline: 不用（持续 window_seconds 即触发）
    threshold       DOUBLE PRECISION,
    -- 持续 N 秒越线才触发，防抖。默认 5 分钟。
    window_seconds  INT NOT NULL DEFAULT 300,
    severity        TEXT NOT NULL DEFAULT 'warning'
                    CHECK (severity IN ('info','warning','error','critical')),
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- 通道 id 数组，UNNEST 即可枚举送达。
    channel_ids     BIGINT[] NOT NULL DEFAULT '{}',
    -- 系统内置规则不允许 admin 删除（imbalance 由 watchdog 写入）。
    builtin         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_enabled
    ON alert_rules (enabled, kind);

-- ============================================================================
-- alert_deliveries
-- ============================================================================
CREATE TABLE IF NOT EXISTS alert_deliveries (
    id           BIGSERIAL PRIMARY KEY,
    alert_id     BIGINT REFERENCES system_alerts(id) ON DELETE SET NULL,
    rule_id      BIGINT REFERENCES alert_rules(id) ON DELETE SET NULL,
    channel_id   BIGINT NOT NULL REFERENCES notify_channels(id) ON DELETE CASCADE,
    -- group_key 与 system_alerts.group_key 对齐；dedup 三元组的中段。
    group_key    TEXT NOT NULL,
    -- status: pending → success / failed，转成 resolved 时 status='resolved'。
    -- 重试中保持 pending，attempts++ 直到 max_attempts。
    status       TEXT NOT NULL
                 CHECK (status IN ('pending','success','failed','resolved')),
    -- alert 当前 phase: firing / resolved。dedup 三元组的尾段。
    -- 同一 (channel_id, group_key, phase) 在 retention 窗口内只发 1 条 →
    -- firing→resolved→firing 走两次 firing + 一次 resolved，不会被吞。
    phase        TEXT NOT NULL DEFAULT 'firing'
                 CHECK (phase IN ('firing','resolved')),
    severity     TEXT NOT NULL DEFAULT 'warning',
    payload      JSONB NOT NULL DEFAULT '{}'::jsonb,
    attempts     INT NOT NULL DEFAULT 0,
    last_error   TEXT,
    next_retry_at TIMESTAMPTZ,    -- 重试退避：1m / 5m / 15m
    sent_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 调度：dispatcher 扫 status='pending' 且 (next_retry_at IS NULL OR next_retry_at <= now())
CREATE INDEX IF NOT EXISTS idx_alert_deliveries_pending
    ON alert_deliveries (status, next_retry_at)
    WHERE status = 'pending';

-- 历史查询：按 channel / group_key 倒序
CREATE INDEX IF NOT EXISTS idx_alert_deliveries_recent
    ON alert_deliveries (channel_id, created_at DESC);

-- dedup 三元组的辅助索引（24h 窗口内查同一 channel + group_key + phase
-- 是否已有 success/pending → 跳过插入）
CREATE INDEX IF NOT EXISTS idx_alert_deliveries_dedup
    ON alert_deliveries (channel_id, group_key, phase, created_at DESC);

-- ============================================================================
-- 内置 imbalance 规则（builtin=TRUE，admin 不可删）
--
-- watchdog 一直在写 system_alerts；本规则让 dispatcher 知道用哪些 channel
-- 推送 imbalance 告警。channel_ids 默认空，admin 在 UI 把通道勾上即生效。
-- ============================================================================
INSERT INTO alert_rules (name, kind, scope_kind, threshold, window_seconds, severity, enabled, channel_ids, builtin)
VALUES ('Cluster Imbalance', 'imbalance', 'global', NULL, 900, 'warning', TRUE, '{}', TRUE)
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS alert_deliveries;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS notify_channels;
DROP INDEX IF EXISTS system_alerts_group_active_uniq;
DROP INDEX IF EXISTS idx_system_alerts_group_key;
ALTER TABLE system_alerts DROP COLUMN IF EXISTS rule_id;
ALTER TABLE system_alerts DROP COLUMN IF EXISTS scope_id;
ALTER TABLE system_alerts DROP COLUMN IF EXISTS scope_kind;
ALTER TABLE system_alerts DROP COLUMN IF EXISTS group_key;
ALTER TABLE system_alerts DROP CONSTRAINT IF EXISTS system_alerts_kind_check;
ALTER TABLE system_alerts ADD CONSTRAINT system_alerts_kind_check CHECK (kind IN ('imbalance'));
