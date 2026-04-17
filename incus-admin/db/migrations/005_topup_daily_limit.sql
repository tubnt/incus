-- 005_topup_daily_limit.sql —— TopUp 日额度支撑
-- 给 transactions 追加 (user_id, type, created_at) 索引，支持 handler 侧按日累加扫描。
-- 不新增 daily_limit 列：日额度是系统级策略而非每用户独立配置。

CREATE INDEX IF NOT EXISTS idx_transactions_user_type_created
  ON transactions (user_id, type, created_at);
