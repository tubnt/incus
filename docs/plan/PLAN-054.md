# PLAN-054 按天付费 billing engine（INFRA-013）

- **status**: draft
- **createdAt**: 2026-05-26
- **task**: INFRA-013

## 0. 摘要

incus-admin 现在是**一次性付费**（products.price_monthly），订单付款后 VM 跑永久不再扣。
AI 网关用户希望"按使用付费"——开 API key 自动扣自己余额按日结算。本 PLAN 落地：

- products 加 `price_daily` 字段
- 新建 `vm_subscriptions` 表（vm 与计费周期解耦）
- 每日 00:00 扣费 worker
- 余额不足 → suspend (VM stop) → 3 天宽限 → trash
- UI 显示"按当前余额可跑 N 天"
- 与现有 monthly 模式**完全并存**（不动现有用户）

## 1. 现状

- `products.price_monthly` 一次性付费（一次扣，永久跑）
- `orders` 表无 billing_period 字段
- 无续费 worker（vm_reconciler / healing_expire / vm_trash_purger 都是 lifecycle，不是计费）
- 用户充值走 `topup_daily_limit`（已有，PLAN-012）

## 2. 设计

### Phase F：schema 扩展（~1 天）

- migration `023_billing_subscriptions.sql`（ORM 生成）：
  ```sql
  ALTER TABLE products
    ADD COLUMN price_daily NUMERIC(10, 4),  -- 4 位小数容纳 ¥0.5/day
    ADD COLUMN period_supported TEXT[] DEFAULT ARRAY['monthly'];

  CREATE TABLE vm_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    vm_id BIGINT NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
    product_id BIGINT NOT NULL REFERENCES products(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    period TEXT NOT NULL CHECK (period IN ('daily', 'monthly')),
    daily_rate NUMERIC(10, 4),       -- daily 周期才填
    monthly_rate NUMERIC(10, 2),     -- monthly 周期才填
    paid_until TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'cancelled')),
    suspended_at TIMESTAMPTZ,
    grace_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  CREATE INDEX idx_subs_user_status ON vm_subscriptions(user_id, status);
  CREATE INDEX idx_subs_paid_until ON vm_subscriptions(paid_until) WHERE status = 'active';

  CREATE TABLE billing_charges (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES vm_subscriptions(id) ON DELETE CASCADE,
    charge_date DATE NOT NULL,    -- 同 sub 每日唯一
    amount NUMERIC(10, 4) NOT NULL,
    status TEXT NOT NULL,  -- 'paid' / 'insufficient' / 'skipped'
    transaction_id BIGINT REFERENCES transactions(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (subscription_id, charge_date)
  );
  ```

### Phase G：订单流 + 创建 hook（~1.5 天）

- `orders` 表加 `period TEXT DEFAULT 'monthly'` 字段
- `POST /portal/orders` 接受 `period`（默认 monthly，兼容现状）
- pay 成功后：
  - monthly：现行逻辑不变（一次扣）+ **额外**创建 `vm_subscriptions` 行记账（period=monthly, paid_until=NOW + 30d）
  - daily：扣 1 天费用 + 创建 sub（paid_until=NOW + 24h）
- VM trash → sub status=`cancelled`（停止扣费）
- VM restore → sub status=`active`，paid_until 重置

### Phase H：扣费 worker（~1.5 天）

- 新建 `worker/billing_daily_charger.go`，cron 表达式 `0 0 * * *`（每天 00:00，UTC）
- 流程：
  1. `SELECT * FROM vm_subscriptions WHERE status='active' AND paid_until <= NOW()`
  2. 对每个 sub：
     - 算应扣金额（daily 一天 / monthly 一个月）
     - INSERT billing_charges（唯一约束防止重扣）
     - 扣余额 → 成功：paid_until += 周期；失败（余额不足）：标 `suspended` + `grace_until = NOW + 3d`
- 第二个 worker `billing_grace_expire.go`：扫 `suspended` 且 `grace_until < NOW`，自动 trash VM
- 余额变化时（topup）→ event hook → 检查 suspended sub 是否能恢复

### Phase I：UI（~1.5 天）

- portal：`/billing` 页加 subscription tab，列每台 VM 的周期 / 单价 / paid_until
- portal/account：余额下方加 "按当前消费速率可跑 N 天"（聚合 daily_rate 之和）
- admin/billing：subscription 列表 + 筛 suspended + 手动恢复按钮
- create-vm/launch 页加"按月 / 按日"切换（默认按用户偏好或全局 env）

### Phase J：审计 + 通知 + cloud-gateway 接口（~1 天）

- audit：扣费 / suspended / grace 触发 / 自动 trash 全记
- alert rule：suspended 触发邮件/webhook
- cloud-gateway：
  - `GET /v1/types` 响应加 `prices.daily`
  - `POST /v1/instances` 接受 `period`
  - `GET /v1/account` 加 `estimated_runway_days`

## 3. 工作量估算

| Phase | 估时 |
| ----- | --- |
| F schema | 1 天 |
| G 订单流 hook | 1.5 天 |
| H worker | 1.5 天 |
| I UI | 1.5 天 |
| J audit + cloud-gateway | 1 天 |
| **合计** | **6.5 天** |

## 4. 风险

1. **现有 monthly 用户回归**：必须 100% 不动现有数据 + 行为。**对策**：sub 是新表，monthly 现有订单 backfill 出 sub 行（脚本），扣费仍按 paid_until 推。
2. **worker 重扣**：cron 跑两次或恢复失败重跑。**对策**：`UNIQUE (sub_id, charge_date)` DB 约束 + worker idempotent
3. **时区**：daily 是按 UTC 00:00 还是用户 timezone？**决策**：先按 UTC（简单 + 一致），未来需要再做 timezone 切分
4. **trash + 计费**：trash 后是否退当天费？**决策**：不退（用户已用），cancel = 立刻停 sub，paid_until 不变
5. **冷启动 / 时钟漂**：worker 错过 00:00 → 下次启动跑 catchup？**对策**：worker 启动时 `SELECT WHERE paid_until <= NOW()`，自动 catchup，不依赖准点

## 5. 与 PLAN-053 关系

PLAN-053 一期可在不依赖本 PLAN 的情况下上线（按 monthly 接 cloud-gateway）。
本 PLAN 落地后 PLAN-053 增量收尾：
- `/v1/types` 响应字段补 `prices.daily`
- `POST /v1/instances` 接受 `period`
- `/v1/account` 加 `estimated_runway_days`

## 6. 验证

- 单测：worker idempotent + 余额不足 + grace 自动恢复
- 集成测试：完整 1 月时间线 mock（freeze time + 跑 30 个 daily tick）
- E2E：开测试账号 → 充 ¥10 → 创 daily VM（¥1/day）→ 第 11 天看 suspended → 充值看恢复

## 7. 实施进度

| Phase | 状态 | 落地点 |
| ----- | --- | ------ |
| F schema | ✅ 完成（L3-C 640vr0dt 2026-05-26） | `db/migrations/028_billing_subscriptions.sql` + products.price_daily/period_supported + orders.period + vm_subscriptions + billing_charges UNIQUE(sub,date) + 3 索引 + model 常量 + repo skeleton（subscription/charge/idempotency） |
| G 订单流 hook | ⏳ 待 L3-E | 订单流 period 透传 + sub 行写入（schema 已就绪） |
| H worker | ⏳ 待 L3-F | `worker/billing_daily_charger.go` + `billing_grace_expire.go` |
| I UI | ⏳ 待 L3-I | portal `/billing` subscription tab + runway 估算 + admin 手动恢复 |
| J audit + cloud-gateway | ⏳ 待 L3-J | /v1/types prices.daily + /v1/account estimated_runway_days |

### Phase F schema（2026-05-26 完成 · L3-C 640vr0dt · 与 PLAN-053 Phase E 同批）

- ✅ migration `028_billing_subscriptions.sql`：products 加 price_daily(NUMERIC(10,4)) +
  period_supported(TEXT[] DEFAULT ['monthly'])；orders 加 period NOT NULL DEFAULT 'monthly'
  + CHECK；新表 vm_subscriptions 三态 status + suspended_at/grace_until +
  3 索引（user_status / paid_until partial / vm_id）；新表 billing_charges 三态 status +
  UNIQUE(sub_id, charge_date) 防重扣
- ✅ migration `029_idempotency_keys.sql`：24h cleanup 索引 + request_hash 列（同 key
  异 payload 检测）
- ✅ `model`：Product +PriceDaily/PeriodSupported；Order +Period；
  新结构 VMSubscription / BillingCharge / IdempotencyKey + 常量集
- ✅ `repository`：抽 `productSelectCols / orderSelectCols` 集中所有 SELECT 列，
  避免后续漂移（参考 d6aee02 firewall.ListBindingsByVM 列数错教训）；
  subscription / charge / idempotency repo skeleton 接口签名定型

#### Phase F 范围内**未做**（按设计）

- 订单流 period hook（L3-E）
- 计费 worker（L3-F）
- Idempotency middleware 本体（L3-H）
- UI（L3-I）+ audit + cloud-gateway 集成（L3-J）
