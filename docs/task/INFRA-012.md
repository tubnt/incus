# INFRA-012 cloud-gateway /v1 适配层（AI 网关对接标准）

- **status**: draft
- **priority**: P1
- **owner**: -
- **createdAt**: 2026-05-26

## 描述

把 incus-admin 包装成 cloud-gateway 标准的 cloud provider，让 AI 通过 MCP 工具
调度 VM。**不动 portal/admin 现有 80+ endpoint**，只增加 `/v1/*` 适配层做 DTO 映射 +
错误格式 + 分页统一 + Idempotency-Key。

参考实现：`cloud-gateway/internal/provider/linode/`（外部 repo）。

## 范围

| 端点                           | 来源                          | 风险等级 |
| ------------------------------ | ----------------------------- | -------- |
| `GET /v1/account`              | `/api/auth/me`                | safe     |
| `GET /v1/instances`            | `/portal/services`            | safe     |
| `GET /v1/instances/{id}`       | `/portal/services/{id}`       | safe     |
| `POST /v1/instances`           | 内部 order + auto pay         | high     |
| `DELETE /v1/instances/{id}`    | `/portal/services/{id}`（trash） | critical |
| `POST /v1/instances/{id}/reboot|shutdown|boot` | actions          | low      |
| `GET /v1/types`                | `/portal/products` active     | safe     |
| `GET /v1/regions`              | `clusters` + 扩展 metadata    | safe     |
| `GET /v1/images`               | `/portal/os-templates`        | safe     |
| `GET /v1/ssh-keys`             | `/portal/ssh-keys`            | safe     |

## 决策（用户已拍）

1. **/v1 适配层方案**（不走 X 重写 / Y 折中）
2. **POST /v1/instances**：走完整 orders 流（auto pay，自己的余额自己扣，`source=api`）
3. **DELETE**：trash + 30s undo（status=`deleting`），不开 `?force=true`
4. **region metadata**：扩 `clusters` 表加 `country` / `city` / `status` / `capabilities`
5. **cloud-gateway provider 代码**：不在本 repo，外部 repo 按 OpenAPI spec 实现

## 验收标准

- [ ] 11 个 `/v1/*` 端点全部可用，单测覆盖
- [ ] 错误响应统一 `{"errors":[{"field","reason"}]}` 格式
- [ ] 分页响应统一 `{"data":[...],"page","pages","total"}` + `?page=N&page_size=M`
- [ ] `Idempotency-Key` 写操作支持（DB 唯一约束 + 24h 缓存响应）
- [ ] `clusters` 表 4 个 region 字段 migration + 默认值回填
- [ ] OpenAPI spec 新增 `/v1/*` 段，`tags: cloud-gateway`
- [ ] curl example 集合 + sandbox token（测试账号余额预充值）
- [ ] 错误码字典（参数错/无权/限流的具体 reason）
- [ ] 限流配额：`/v1/*` 独立桶，默认 100 rpm/token，写操作 burst 限 30

## 依赖

- **blocked by**: 取决于 scope 决策 —— 见 "scope 二选一" 段
- **blocks**: AI 网关上线（cloud-gateway provider 注册）

## scope 二选一（待用户拍板）

**方案 a：一期 monthly 上线，二期切日**
- INFRA-012 只支持 monthly（现状），cloud-gateway 一期能跑
- INFRA-013（按天付费）做完后，`POST /v1/instances` 加 `period` 字段
- 优点：快（3–5 天）；缺点：AI 创建实例先按月扣费，体验偏

**方案 b：与 INFRA-013 并轨上线**
- 一起开发，一起上线（按天为默认 period）
- 优点：用户语义对（"自己开 API key 按天付费"一步到位）
- 缺点：周期长（10–15 天），按天计费 worker 是新风险面

## 相关

- PLAN-053（INFRA-012 详细设计）
- INFRA-013 + PLAN-054（按天付费 billing engine，与本任务联动）
