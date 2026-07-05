-- +goose Up
-- PLAN-054 / INFRA-013：按天付费 billing engine schema
--
-- 与现有 monthly 一次性付费**完全并存**：products 加按天单价 + 支持周期数组；
-- orders 加 period 字段（默认 monthly，兼容现网订单）；vm_subscriptions 把
-- 每台 VM 与计费周期解耦；billing_charges 记录每次日扣记账。
--
-- 设计要点：
--   1. price_daily NUMERIC(10,4)：4 位小数容纳 ¥0.5/day 等小额按天定价；
--      与 price_monthly NUMERIC(10,2) 精度差异是 PLAN-054 拍板（决策 #2）。
--   2. period_supported TEXT[] NOT NULL DEFAULT ARRAY['monthly']：现有产品
--      行 ALTER 后自动得到 ['monthly']，UI/校验侧无需 backfill。
--   3. orders.period NOT NULL DEFAULT 'monthly' + CHECK：未指定走 monthly，
--      与现行下单流量 100% 兼容；新订单流走 'daily' 时显式传字段。
--   4. vm_subscriptions.UNIQUE(vm_id) 不加 —— PLAN-054 risk #4「trash + 计费」
--      允许 cancelled 行历史保留，将来 restore 时新插 active 行；只在 active
--      态语义上一对一，由 worker 与服务层保证。
--   5. billing_charges UNIQUE (subscription_id, charge_date) 是 worker 重扣
--      防御（PLAN-054 risk #2），cron 跑两次也只能 INSERT 一行。
--   6. NUMERIC vs DOUBLE PRECISION：金额一律 NUMERIC，避免浮点累加误差。
--      日费率用 NUMERIC(10,4) 容纳 ¥0.0001/day 级别精度。

-- ============================================================================
-- products 扩展：按天单价 + 支持周期数组
-- ============================================================================
ALTER TABLE products
  ADD COLUMN IF NOT EXISTS price_daily NUMERIC(10, 4);

ALTER TABLE products
  ADD COLUMN IF NOT EXISTS period_supported TEXT[] NOT NULL DEFAULT ARRAY['monthly'];

-- ============================================================================
-- orders 扩展：计费周期
-- ============================================================================
ALTER TABLE orders
  ADD COLUMN IF NOT EXISTS period TEXT NOT NULL DEFAULT 'monthly';

-- CHECK 约束分开建：IF NOT EXISTS 不支持 ADD CONSTRAINT，用 DO 块兜底
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'orders_period_check'
  ) THEN
    ALTER TABLE orders
      ADD CONSTRAINT orders_period_check CHECK (period IN ('daily', 'monthly'));
  END IF;
END $$;
-- +goose StatementEnd

-- ============================================================================
-- vm_subscriptions：vm 与计费周期解耦
-- ============================================================================
CREATE TABLE IF NOT EXISTS vm_subscriptions (
    id           BIGSERIAL PRIMARY KEY,
    vm_id        BIGINT NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    product_id   BIGINT NOT NULL REFERENCES products(id),
    user_id      BIGINT NOT NULL REFERENCES users(id),
    period       TEXT   NOT NULL CHECK (period IN ('daily', 'monthly')),
    -- daily_rate / monthly_rate 同一行只填一个，由 period 决定；记录创建时
    -- 锁定的费率，避免产品后续调价时回溯影响订阅。
    daily_rate   NUMERIC(10, 4),
    monthly_rate NUMERIC(10, 2),
    -- worker 用 paid_until <= NOW() 找到期 sub；扣费成功后 += 一个周期。
    paid_until   TIMESTAMPTZ NOT NULL,
    status       TEXT   NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'suspended', 'cancelled')),
    -- 余额不足进入 suspended，3 天宽限；grace_until 过后 worker trash VM。
    suspended_at TIMESTAMPTZ,
    grace_until  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- portal /billing 列表 + admin suspended 筛选：
CREATE INDEX IF NOT EXISTS idx_subs_user_status ON vm_subscriptions(user_id, status);
-- worker 扫到期：partial index 跳过 cancelled/suspended，热路径更快
CREATE INDEX IF NOT EXISTS idx_subs_paid_until  ON vm_subscriptions(paid_until)
    WHERE status = 'active';
-- VM 详情页反向查 sub：
CREATE INDEX IF NOT EXISTS idx_subs_vm_id       ON vm_subscriptions(vm_id);

-- ============================================================================
-- billing_charges：每次扣费的不可变记账
-- ============================================================================
CREATE TABLE IF NOT EXISTS billing_charges (
    id              BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES vm_subscriptions(id) ON DELETE CASCADE,
    -- charge_date 是按天的整点（UTC）维度；按 PLAN-054 决策 #3 暂走 UTC。
    charge_date     DATE   NOT NULL,
    amount          NUMERIC(10, 4) NOT NULL,
    status          TEXT   NOT NULL CHECK (status IN ('paid','insufficient','skipped')),
    -- paid 状态对应一行 transactions（user balance 扣减）；insufficient/skipped
    -- 不写 transactions，transaction_id 留 NULL。
    transaction_id  BIGINT REFERENCES transactions(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- worker 重扣防御：同 sub + 同日只能落 1 行
    UNIQUE (subscription_id, charge_date)
);

-- 用户账单页：按 sub 查最近扣费记录，DESC 取最新
CREATE INDEX IF NOT EXISTS idx_charges_subscription
    ON billing_charges(subscription_id, charge_date DESC);

-- +goose Down
DROP TABLE IF EXISTS billing_charges;
DROP TABLE IF EXISTS vm_subscriptions;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_period_check;
ALTER TABLE orders DROP COLUMN IF EXISTS period;
ALTER TABLE products DROP COLUMN IF EXISTS period_supported;
ALTER TABLE products DROP COLUMN IF EXISTS price_daily;
