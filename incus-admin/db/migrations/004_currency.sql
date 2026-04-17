-- 004_currency.sql — 为产品、订单、发票追加 currency 字段
-- 默认 USD，避免破坏已有数据。前端用 formatCurrency(amount, currency) 统一格式化。

ALTER TABLE products ADD COLUMN IF NOT EXISTS currency CHAR(3) NOT NULL DEFAULT 'USD';
ALTER TABLE orders ADD COLUMN IF NOT EXISTS currency CHAR(3) NOT NULL DEFAULT 'USD';
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS currency CHAR(3) NOT NULL DEFAULT 'USD';
