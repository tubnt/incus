-- PLAN-019 Phase A: Step-up auth
-- 记录用户最近一次 step-up（敏感操作重认证）完成时间
-- 应用层 middleware 判断：NOW() - stepup_auth_at > 5min 则要求重新 step-up

ALTER TABLE users ADD COLUMN IF NOT EXISTS stepup_auth_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_users_stepup_auth_at ON users(stepup_auth_at);
