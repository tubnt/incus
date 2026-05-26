# INFRA-013 按天付费 billing engine（订阅周期 + 余额定时扣费）

- **status**: draft
- **priority**: P1
- **owner**: -
- **createdAt**: 2026-05-26

## 描述

incus-admin 目前是**一次性付费**（`price_monthly`，付一次跑永久）。AI 网关接入
要求"用户开 API key 按使用付费"，必须新增按天/按月**订阅式扣费**：
products 加 `price_daily`、订单加 `billing_period`、新建 `vm_subscriptions` 表 +
每日 00:00 扣费 worker；余额不足 → suspend → 3 天宽限期 → trash。

## 范围（Phase 划分见 PLAN-054）

- products 表加 `price_daily`、`period_supported` 字段
- 新建 `vm_subscriptions` 表（vm_id, plan_id, daily_rate, period, paid_until, status）
- 订单流加 `billing_period`（daily / monthly），创建订单时按 period 计费首次
- 新增 `billing_daily_charger` worker：每日 00:00 扫 paid_until ≤ now 的订阅，扣费
- 余额不足：标 suspended → VM stop → 通知用户 → 3 天宽限 → trash
- 用户/admin UI：余额预估剩余天数 + 周期切换 + 订阅列表
- audit：扣费/挂起/恢复全记
- cloud-gateway: `POST /v1/instances` 默认 `period=daily`，`GET /v1/types` 返 `prices.daily/monthly`

## 验收标准

- [ ] daily worker idempotent（同一天跑多次不重扣，DB 唯一约束 `(subscription_id, charge_date)`）
- [ ] 余额不足 + 宽限期 + 自动恢复全跑通（集成测试）
- [ ] 现有 monthly 订单**零回归**（不动现有用户的计费方式）
- [ ] UI 显示"按当前余额可跑 N 天"
- [ ] 取消 / trash VM 立刻停止扣费（不重复扣到月底）
- [ ] cloud-gateway side: `POST /v1/instances {period:"daily"}` 端到端 OK

## 依赖

- **blocked by**: 无技术依赖（业务上 cloud-gateway 接入更顺）
- **blocks**: INFRA-012 中"AI 自助按天付费"诉求

## 相关

- PLAN-054（详细设计）
- INFRA-012 + PLAN-053（cloud-gateway 适配层）
