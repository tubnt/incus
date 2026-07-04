-- +goose Up
-- WP-H2 / OPS-052：idempotency_keys 主键由 (key) 收窄为 (key, user_id) 复合主键
--
-- 背景：029 建表时用 `key TEXT PRIMARY KEY`，是**全局**唯一约束。若两个不同
-- 用户恰好使用了相同的 Idempotency-Key，第二个用户的 INSERT 会撞上
-- ON CONFLICT (key) DO NOTHING 被静默吞掉；随后中间件按 (key, user_id) 读回
-- 又被过滤成 miss，业务被迫重复执行——既泄露「key 已被别人用过」的存在性，
-- 又破坏幂等语义。改为复合主键 (key, user_id) 后，key 仅在单用户维度唯一，
-- 跨用户互不干扰。
--
-- 数据安全：旧 PK (key) 是复合 PK (key, user_id) 的**更严格**约束——任何满足
-- 「key 全局唯一」的既有行，天然满足「(key, user_id) 唯一」。因此收紧到复合键
-- 不会产生冲突行，无需去重 / 清洗，既有缓存数据原样保留。
--
-- 可重放：DROP CONSTRAINT IF EXISTS 幂等；先无条件 DROP 再 ADD，确保重复
-- apply 时不会因主键已存在而报错。user_id 早在 029 即为 NOT NULL，满足 PK 要求。
--
-- 依赖旧 PK 的对象：029 仅声明了 idx_idemp_user_created / idx_idemp_cleanup
-- 两个普通索引（不依赖主键），以及 user_id 的外键（引用 users(id)，不受本表
-- 主键变更影响）。故无需调整其它索引 / 约束。

ALTER TABLE idempotency_keys DROP CONSTRAINT IF EXISTS idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (key, user_id);

-- +goose Down
ALTER TABLE idempotency_keys DROP CONSTRAINT IF EXISTS idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD CONSTRAINT idempotency_keys_pkey PRIMARY KEY (key);
