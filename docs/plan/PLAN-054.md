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
| G 订单流 hook | ✅ 完成（L3-E z071x2iz 2026-05-26） | `POST /portal/orders` 接 period + 校验 product.period_supported / rate 不为 null；pay 成功 + vm row 写入后 INSERT vm_subscriptions（sub 失败回滚整单）；VM trash → sub cancelled；VM restore → sub active + paid_until 重置 |
| H worker | ✅ 完成（L3-F q8xd9rpn 2026-05-26） | `service/billing/service.go`（ChargeDue/ExpireGrace/ReactivateOnTopUp）+ `worker/billing_daily_charger.go` + `worker/billing_grace_expire.go` + topup hook + BillingConfig + main.go 注入 |
| I UI | ✅ 完成（L3-I snuahzue 2026-05-26） | portal `/billing` Tabs + subscription tab + runway 余额预估 + /launch 按月/按日切换 + admin `/admin/subscriptions` 手动恢复 + `/api-tokens` cloud-gateway banner |
| J audit + cloud-gateway | ✅ 完成（L3-J wm5iv91f 2026-05-26） | /v1/account 加 estimated_runway_days（与 PLAN-053 Phase F 同批落地）；/v1/types prices.daily 已在 PLAN-053 Phase B mapper 接通（product.PriceDaily *float64 → PricesDTO.Daily omitempty）；/v1/instances 接 period（已在 PLAN-053 Phase D 落地） |

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

### Phase G 订单流 hook（2026-05-26 完成 · L3-E z071x2iz）

- ✅ `POST /portal/orders` 接 `period` 字段（omitempty + `oneof=daily monthly`），未传默认
  monthly（兼容现状）。校验 `product.PeriodSupported` 包含目标 period，否则 422
  `{errors:[{field:"period", reason:"unsupported"}]}`；对应单价（daily=price_daily /
  monthly=price_monthly）缺失返 422 `{reason:"rate_missing"}`。订单写入走
  `OrderRepo.CreateWithPeriod`，amount 用对应 rate。
- ✅ pay 成功后写 `vm_subscriptions`：vm row 写入完成后调
  `OrderHandler.createSubscriptionForOrder`；daily/monthly 单价从 product 取并仅写一侧
  rate；`paid_until = NOW + BillingPeriodDuration(period)`（daily 24h / monthly 30d）。
  sub 失败 → 删 VM 行 + rollbackPayment（refund + IP release + order cancelled），
  保持订单 + 余额 + VM + sub 四方一致；job create / enqueue 失败也清 sub。
- ✅ VM trash → sub cancelled：`portal/services/{id}` DELETE + admin `vms/{name}` DELETE
  都在 `MarkTrashed` 成功后调 `cancelSubscriptionOnTrash`（仅动 status='active' 行，
  paid_until 不变）。audit `subscription_cancelled`。
- ✅ VM restore → sub active + paid_until 重置：portal + admin restore 路径在
  `UnmarkTrashed` 成功后调 `reactivateSubscriptionOnRestore` —— GetLatestByVM 取
  period 后 paid_until 重置为 `NOW + duration`（免费"恢复"语义，不重新扣费）。
  audit `subscription_restored`。
- ✅ `SubscriptionRepo` 业务补全：`GetLatestByVM`（任意状态）/ `CancelByVM`（只动
  active）/ `ReactivateByVM`（只动最新 cancelled）；与原 skeleton（Insert / GetByVM /
  ListByUser / ListDueActive / UpdateStatus / UpdatePaidUntil / UpdateSuspension /
  ClearSuspension）正交。
- ✅ 共享 helper：`model.BillingPeriodDuration(period)` 单点定义周期长度，避免
  worker / restore 两处硬编码漂移。
- ✅ 测试：repo 集成（CancelByVM idempotent / Reactivate 改 paid_until / 二次 no-op）+
  handler 集成（pay → sub 写入 daily / monthly / trash → cancelled paid_until 不动 /
  restore → active 重置 paid_until / subs nil noop）+ 单测（BillingPeriodDuration
  锁定 24h/30d）。
- ✅ wiring：`cmd/server/main.go` 注入 `subRepo` 到 `OrderHandler.WithSubscriptions` +
  `VMHandler.WithSubscriptions` + `AdminVMHandler.WithSubscriptions`。

#### Phase G 范围内**未做**（按设计）

- billing worker（L3-F）：每日扣费 / suspension / grace expire / topup 触发恢复
- `/v1/instances` 一键创建（L3-G）：走 cloud-gateway 内部调本 phase 改造好的 OrderService
- UI（L3-I）+ cloud-gateway 集成（L3-J）

### Phase I UI（2026-05-26 完成 · L3-I snuahzue）

- ✅ portal `/billing` 升级 Tabs：「我的订单 / 订阅 / 发票」三 tab，订阅 tab 用
  新组件 `features/billing/subscription-list/subscription-list.tsx`：VM 名（来自
  `useMyVMsQuery` 映射） / 周期 chip / 单价 / suspended 红行 + ⚠ Tooltip（含 grace_until 时间）
  / paid_until / 剩余天数（`runwayDaysFromNow` 整数天）；suspended 行排在最上。
- ✅ 余额 `BalanceCard` 下方加运行时预估：`computeRunwayDays(balance, subs)` —— daily +
  monthly（折算 daily=monthly/30）汇总 daily burn，余额 / burn = 剩余天数；< 7 天
  warning 黄；balance ≤ 0 / 无 active sub / 全部 rate 缺失时不显示。
- ✅ `/launch` 加 §2「计费周期」FormSection：`PeriodPicker` 二选一 RadioGroup（按月 /
  按日），默认 monthly；product.period_supported 不含目标 period 时 disable + Tooltip
  解释；提交 `POST /portal/orders` 带 period 字段；SummaryCard hero 单价 + 单位
  跟 period 切换。
- ✅ admin `/admin/subscriptions` 新页：filter（status × user）+ 列出全用户订阅，
  suspended/cancelled 行有「恢复」按钮 → `POST /admin/subscriptions/{id}/reactivate`。
  后端走 `SubscriptionHandler.Reactivate`：调 `SubscriptionRepo.AdminReactivate`
  把 status 切回 active + paid_until 重置 + 清空 suspended_at/grace_until。
  audit `subscription_admin_reactivated`。
- ✅ `/api-tokens` 顶部加 cloud-gateway banner（accent 强调）：说明 token 用法 +
  建议 TTL ≥ 7 天 + docs 占位锚点；不动现有 CRUD。
- ✅ 单测：`subscriptions-api.test.ts` 覆盖 `computeRunwayDays`（balance=0/负 / 无
  active / daily/monthly 单独 + 混合 / rate null 边界 / NaN+Infinity）和
  `runwayDaysFromNow`（过期 / 同时刻 / 未来 / 非法 date）共 14 个 case；
  `period-picker.test.tsx` jsdom 渲染 PeriodPicker 验证 disabled 状态 + onChange
  路径 + productSupports 边界共 7 个 case；全 62 测试通过。
- ✅ i18n：`subscription` / `period` 顶层 block 中英双语 + `billing.runwayHint` +
  `apiToken.cloudGateway*` + `admin.subscriptions.*` + `nav.subscriptions`，
  同步加 `common.all`（admin 筛选器用）。
- ✅ DESIGN.md 严格合规：`grep -E 'p-\[|m-\[|h-\[|w-\[|gap-\[|text-\['` 在
  features/billing / features/launch / 4 个改动路由 0 命中；新增 `--size-input-medium`
  token 取代 arbitrary。

#### Phase I 范围内**未做**（按设计）

- `/v1/account` 加 `estimated_runway_days` 字段（L3-J 后端补 + 前端读）
- 审计页加 `subscription_*` 事件展示（cosmetic，下期）
- Playwright E2E（L3-J 收尾时跑）

### Phase H worker（2026-05-26 完成 · L3-F q8xd9rpn）

- ✅ `internal/service/billing/service.go`：单一 `Service` 聚合三条工作流，
  全部走单事务 + UNIQUE(sub,date) 防重扣
  - `ChargeDue(ctx)`：扫 paid_until<=NOW 的 active 订阅，逐个原子扣费
    （INSERT charge 占位 → SELECT balance FOR UPDATE → 够扣 → 扣 + 交易流水
    + UpdatePaid + paid_until += period；不够 → 维持 insufficient + suspended +
    grace_until=NOW+72h）。catchup safe（UNIQUE 让重跑变 skipped）。
  - `ExpireGrace(ctx)`：扫 grace_until<NOW 的 suspended → VMRepo.MarkTrashed +
    sub status=cancelled + audit（trash 失败仍 cancel 以避免无限重试）。
  - `ReactivateOnTopUp(ctx, userID)`：列用户全部 suspended → 按 grace_until ASC
    逐个尝试补扣 + ClearSuspension + paid_until=NOW+period；余额不够留 suspended。
  - 可注入时钟 `WithClock` + 宽限期 `WithGraceDuration` + 批量上限 `WithListLimit`。
- ✅ `internal/worker/billing_daily_charger.go`：启动 catchup + tick（默认 1h）
- ✅ `internal/worker/billing_grace_expire.go`：firstDelay 15min 错开 charger，
  避免同秒锁 users.balance；tick 默认 1h
- ✅ `internal/handler/portal/user.go`：TopUpBalance 成功后异步触发 `ReactivateOnTopUp`
  （独立 30s ctx，不阻塞响应；reactivator==nil 时跳过）
- ✅ `internal/config/config.go`：`BillingConfig.Enabled / ChargerInterval /
  GraceInterval / GraceDuration` + `parseBoolOr` helper；env 变量
  `INCUS_ADMIN_BILLING_{ENABLED,CHARGER_INTERVAL,GRACE_INTERVAL,GRACE_DURATION}`
- ✅ `cmd/server/main.go`：billingSvc 注入 + 两个 worker spawn + UserHandler
  reactivator wire + 5 个 thin adapter（VMTrasher/AuditWriter/Charger/Gracer/
  Reactivator）；Enabled=false 时全跳过
- ✅ Repo 补：`SubscriptionRepo.ListSuspendedExpired/ListSuspendedByUser` +
  `ChargeRepo.UpdatePaid`
- ✅ 测试：worker 单测（启动 catchup / firstDelay / 错误不停 / nil-charger）
  + service 集成测试 9 个（daily paid / monthly insufficient → suspended /
  catchup-safe / 缺 rate / grace expire 三态 / reactivate 三态 / 30 天时间线）

#### Phase H 时区与时钟

- 全 UTC（service.clock() 默认 `time.Now().UTC()`）。charge_date 是 PG DATE，
  worker 写入前 `Truncate(24h)` 落到当天 00:00。
- 没引入 cron 库：tick 间隔由 catchup 兜底，与精确 cron `0 0 * * *` 行为等价。
- worker 启动立即跑一次 catchup（错过整点 / 进程重启场景），后续按 tick。
- charger 与 grace_expire 错开 15min 起跑，避免同时锁 users.balance。

#### Phase H 自审 CR 关键修复

- **P0**：charger 与 reactivate hook 并发可能在同一 (sub, today) 双扣 ——
  两路均在事务开头 `SELECT vm_subscriptions FOR UPDATE` 串行；reactivate 找不
  到 insufficient charge 时直接让位（不再 INSERT-ON-CONFLICT 覆写 charger 已
  写的 paid 行）。回归测试 `TestService_ConcurrentChargeAndReactivateNoDoubleCharge`
  连跑 8 轮校验「至多 1 笔 transactions 流水 + 1 笔 billing_charges 行」。
- **P1**：多日 backlog 不再让 paid_until 永远卡过去 —— charge 后 `paid_until =
  max(old + period, now + period)`。一次 catchup tick 把 paid_until 抬到当下
  cadence；用户白得 N 天服务（业务取舍：catchup 不补扣老账，避免一次性大额
  扣款抢用户余额）。
- **P2**：grace expire 时 `MarkTrashed` 失败 → 不 cancel sub，下个 tick 重试；
  否则 Incus 长时间不可用会导致 sub cancelled + VM 永久在跑无人付费。回归
  测试模拟「先 trash 失败 → 修好 → 下个 tick 成功 cancel」。
- **P3 / 工程**：`isUniqueViolation` 用标准 `strings.Contains`；`listLimit`
  默认 1000，避免一次 ChargeDue 锁 users 全表。

### Phase J cloud-gateway 集成（2026-05-26 完成 · L3-J wm5iv91f · 与 PLAN-053 Phase F 同批）

PLAN-054 §2 Phase J 列了 3 件事 —— alert / audit / cloud-gateway。
其中 cloud-gateway 三项（/v1/types prices.daily, /v1/instances period,
/v1/account estimated_runway_days）由本 phase 收尾；audit `subscription_*` 事件
已在 Phase G 落地（trash → cancelled、restore → active），alert rule for
billing/suspended 归 INFRA-009 monitoring 范畴（PLAN-041 / PLAN-053 §"不在
范围"），不在本 phase。

- ✅ `/v1/types` 响应 `prices.daily`：在 PLAN-053 Phase B `toTypeDTO` 已经
  把 `product.PriceDaily *float64` 映射到 `PricesDTO.Daily *float64`
  （`omitempty` 让 nil 不输出）；本 phase **验证**该路径仍生效，并写入
  `openapi.yaml` `TypeDTO.prices.daily` 字段 spec。`TestTypes_Happy` 单测
  覆盖 nil 与非 nil 两条路径。
- ✅ `POST /v1/instances` `period` 字段：PLAN-053 Phase D 已经接受
  `period: "daily" | "monthly"`，并在 `instances_write.go` 校验
  `product.PeriodSupported`、对应 rate 非空。本 phase 把 enum/默认值
  写入 `openapi.yaml` `InstanceCreateRequest.period`。
- ✅ `/v1/account` 加 `estimated_runway_days`（本 phase 新增）：
  - 后端：`internal/handler/v1/dto.go` `AccountDTO` 加
    `EstimatedRunwayDays *float64` (omitempty)；同文件加
    `computeRunwayDays(balance, []model.VMSubscription)` —— 算法与前端
    `web/src/features/billing/subscriptions-api.ts` `computeRunwayDays`
    1:1 对齐：daily sub 累加 daily_rate，monthly sub 累加 monthly_rate/30，
    `balance / dailyBurn`；balance<=0 / 无 active sub / burn==0 / rate
    全空 → nil（JSON 不输出字段）
  - Deps：`internal/handler/v1/handler.go` 加 `Subscriptions
    subscriptionReader` 接口；`internal/handler/v1/readonly.go` Account 路径
    nil 容忍（缺依赖不阻断响应，与既有"sub list 出错只 slog.Warn"对齐）
  - wiring：`cmd/server/main.go` `Subscriptions: subRepo`（subRepo 在
    Phase G 已注入）
  - 单测：`readonly_test.go` +7 个 case 覆盖 daily-only / monthly-mixed /
    no-active / balance-zero / rate-nil / sub-repo-err 不致命 / nil-dep
    不致命；含 raw body grep 防字段意外输出
  - OpenAPI：`AccountDTO.estimated_runway_days` 加 `nullable: true` + 算法
    描述
- ✅ 文档：`incus-admin/docs/cloud-gateway.md` 新建 —— 12 端点 + 鉴权 +
  限流 + 错误 reason 表 + 分页 + Idempotency + curl example + 错误路径 +
  POST schema + 计费 + provider 契约 + 本地启动指引
- ✅ E2E：`incus-admin/scripts/e2e-cloud-gateway.sh` 新建 —— 12 端点 + 5
  错误路径 + Idempotency replay + 限流（手动开关），可重跑

#### Phase J 范围内**未做**（按设计）

- alert rule for billing/suspended：归 INFRA-009 monitoring 范畴
  （PLAN-041 已有 alert_rules 表 + dispatcher，后续 OPS 单独配规则）
- audit 页前端展示 `subscription_*` 事件：cosmetic，PLAN-054 Phase I 已
  备注下期
- cloud-gateway provider 实现：外部 repo，按本 phase 的 openapi.yaml 接入
