-- PLAN-053 / INFRA-012：cloud-gateway 标准 Idempotency-Key 缓存表
--
-- 写操作（POST / DELETE）的客户端可在 header 带 Idempotency-Key；middleware
-- 命中已写入 key 直接回放缓存响应，未命中则走业务并把响应（含 status_code +
-- response_body）写入此表。24h TTL，cleanup worker 复用 audit_cleanup 套路。
--
-- 设计要点：
--   1. PRIMARY KEY (key)：天然唯一约束，并发命中靠 SELECT 再 INSERT；冲突
--      时 fallback 到缓存（PLAN-053 risk #2 对策）。
--   2. user_id ON DELETE CASCADE：用户注销时缓存随之清理，避免悬挂引用。
--   3. request_hash：SHA-256(method+path+sorted(body))，用于检测「同 key
--      但不同 payload」攻击场景；middleware 应当比对 hash，不一致返 422。
--   4. response_body BYTEA：原样保存 JSON 响应字节，重放时直接 w.Write，
--      不再二次序列化，保证 byte-for-byte 一致。
--   5. status_code INT：HTTP 状态码（200/201/400/402/...），重放时同时回放。

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key            TEXT       PRIMARY KEY,
    user_id        BIGINT     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method         TEXT       NOT NULL,            -- 'POST' / 'DELETE'
    path           TEXT       NOT NULL,            -- '/v1/instances'
    status_code    INT        NOT NULL,
    response_body  BYTEA      NOT NULL,
    -- SHA-256(method + path + sorted(body)) 十六进制；同 key 异 payload → 422
    request_hash   TEXT       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 用户最近 idempotency 调用列表（admin 审计 / 调试）
CREATE INDEX IF NOT EXISTS idx_idemp_user_created
    ON idempotency_keys(user_id, created_at DESC);

-- cleanup worker 用：扫 created_at < NOW() - 24h
CREATE INDEX IF NOT EXISTS idx_idemp_cleanup
    ON idempotency_keys(created_at);
