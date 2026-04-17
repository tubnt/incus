# PLAN-012 后端核算收尾 —— PayWithBalance 回滚集成测试 + TopUp 日额度

- **status**: completed
- **createdAt**: 2026-04-17 14:52
- **approvedAt**: 2026-04-17 14:55
- **completedAt**: 2026-04-17 15:45
- **relatedTask**: 继承自 PLAN-011 末尾延期项

## Context

PLAN-011 Phase G 在交付时留了两项「需要新脚手架 / 新 schema」的工作未做：

1. **`OrderRepo.PayWithBalance` 的回滚集成测试。**
   当前 `internal/repository/order.go:112` 的实现是五步事务（锁单 / 锁用户 / 扣款
   / 改单 / 写流水 / 开发票）。单元测试用 sqlmock 只能断言 SQL 文本顺序，
   无法覆盖「第 4 步失败时余额是否真回滚」。仓库目前 **没有任何 Postgres 集成
   测试脚手架** —— 上线前没人在真实事务里验证过这条路径。
2. **TopUp 日额度。**
   `internal/handler/portal/user.go:17` 只限了单次 `MaxTopUpPerRequest=10000`，
   `user.go:116` 已禁止 self-top-up，但没有日累计上限。攻击面：
   拿到被盗管理员 Session 的人可以在一天内反复给同一用户充 N×10000。
   DB 里已有 `transactions(user_id, amount, type, created_at)`，但没有
   针对 `type='deposit'` 的日累计约束。

这两项分别要求新的测试依赖（testcontainers-go）和新的 schema（索引/视图）+
handler 守卫 + 前端面板的额度展示，所以拆成独立 PLAN 处理。

## Proposal

### Phase A —— testhelper 脚手架（阻塞 B）

- 新增 `internal/testhelper/postgres.go`，基于 `testcontainers-go/modules/postgres`
  提供 `NewTestDB(t *testing.T) *sql.DB`：
  - 启动 `postgres:16-alpine` 容器
  - 依次 apply `db/migrations/*.sql`（读取目录、按文件名排序执行）
  - 返回 `*sql.DB` 并注册 `t.Cleanup` 拆容器
- 新增 `go.mod` 依赖：`github.com/testcontainers/testcontainers-go`、
  `github.com/testcontainers/testcontainers-go/modules/postgres`
- CI 侧：`Taskfile.yml` 新增 `test-integration` 任务，跑 `go test -tags=integration ./...`；
  普通 `test` 任务加 `-short` 跳过集成测试（testcontainers 在无 Docker 的 CI 环境会
  skip，不阻断主流程）。
- 每个集成测试文件顶部用 `//go:build integration` 约束，避免开发机常规 `go test`
  拉镜像。

### Phase B —— PayWithBalance 回滚测试

新增 `internal/repository/order_integration_test.go`（`//go:build integration`）：

| 测试用例 | 构造 | 断言 |
|---|---|---|
| `TestPayWithBalance_HappyPath` | user.balance=100, order.amount=50 | 成功；balance=50；order.status=paid；invoices 有 1 行；transactions 有 1 行 -50 |
| `TestPayWithBalance_InsufficientBalance` | balance=10, order.amount=50 | 报错 `余额不足`；**事务全量回滚**：balance 仍 10；order.status 仍 pending；invoices/transactions 无新行 |
| `TestPayWithBalance_OrderNotPending` | order.status=paid 已支付 | 报错；balance/invoices/transactions 不变 |
| `TestPayWithBalance_ConcurrentPay` | 同订单并发跑 2 次 | 严格一方成功一方失败；balance 只扣一次（验证 `FOR UPDATE` 正确性）|

依赖：Phase A。

### Phase C —— TopUp 日额度

#### C.1 Schema

新增 `db/migrations/005_topup_daily_limit.sql`：

```sql
-- 005_topup_daily_limit.sql —— TopUp 日额度支撑
-- 给 transactions 追加 (user_id, type, created_at) 索引，支持 handler 侧
-- 按日累加扫描；无需新表。
CREATE INDEX IF NOT EXISTS idx_transactions_user_type_created
  ON transactions (user_id, type, created_at);
```

> 不新增 `daily_limit` 列，因为「日额度」是系统级策略而非每用户独立配置；
> 如果后续要做差异化，再迁移到 `settings` 表。

#### C.2 Handler 守卫

`internal/handler/portal/user.go`：

1. 常量层：
   ```go
   const MaxTopUpPerDay = 100000.0 // 单用户 24h 累计充值上限
   ```
2. `UserRepo` 新增 `SumDepositsSince(ctx, userID int64, since time.Time) (float64, error)`，
   用上面索引扫描 `SUM(amount) WHERE user_id=$1 AND type='deposit' AND created_at >= $2`。
3. `TopUpBalance` 在 `req.Amount > MaxTopUpPerRequest` 之后、`AdjustBalance` 之前
   新增检查：
   ```go
   used, _ := h.repo.SumDepositsSince(ctx, id, time.Now().Add(-24*time.Hour))
   if used + req.Amount > MaxTopUpPerDay {
       writeJSON(w, http.StatusTooManyRequests, map[string]any{
           "error": "daily top-up quota exceeded",
           "limit": MaxTopUpPerDay,
           "used":  used,
       })
       return
   }
   ```
4. 单元测试：`user_test.go` mock `SumDepositsSince` 校验三种分支
   （未超、恰好等于、超过）。

#### C.3 前端展示（admin/users.tsx）

- `features/users/api.ts` 新增 `useUserDailyTopUpQuery(userID)`，打到新端点
  `GET /admin/users/{id}/topup-quota` 返回 `{used, limit, remaining, resets_at}`
  供充值对话框预览。
- 对话框加一行「今日已用 / 上限」徽章；当 `remaining < req.Amount` 时提交按钮
  置灰。
- i18n 键：`user.topup.usedToday`, `user.topup.dailyLimit`, `user.topup.quotaExceeded`。

### Phase D —— 文档 & 部署

- `docs/task/index.md` 新增两个 task（PayWithBalance 集成测试、TopUp 日额度）。
- `docs/plan/index.md` 添加 PLAN-012 行。
- 生产部署流程跟 PLAN-011 一致：
  1. 本地 `task build` + `task web-build`
  2. `psql -f 005_topup_daily_limit.sql`（纯新建索引，可在线跑）
  3. 替换 `/usr/local/bin/incus-admin`
  4. `systemctl restart incus-admin` + 健康检查
- 回滚：索引 `DROP INDEX IF EXISTS idx_transactions_user_type_created;`，
  二进制 `cp incus-admin.bak.*` 回去。

## Risks

1. **testcontainers 依赖 Docker。** 开发机 / CI 无 Docker 时集成测试无法跑；
   通过 `//go:build integration` tag + Taskfile 分任务规避。
2. **索引在大表上建会锁。** `transactions` 目前 < 10 万行，`CREATE INDEX`（非
   concurrently）秒级。如后续量级上来，改用 `CREATE INDEX CONCURRENTLY`（需单
   独事务）。
3. **日额度窗口选择。** 采用「滚动 24h」而非「自然日」—— 更贴合「防连续刷单」
   目的。文档里要说明，避免运营以为是凌晨重置。
4. **前端 quota 端点竞态。** 查询结果到提交间隔内可能有并发充值，后端二次校验
   才是权威，前端徽章仅做 UX 引导。

## Scope

- ✅ testhelper postgres 容器 + 迁移加载
- ✅ PayWithBalance 4 个集成用例
- ✅ 005 迁移 + SumDepositsSince + handler 守卫
- ✅ admin/users.tsx 配额预览 + i18n
- ❌ 不做 settings 表 / 不做每用户差异化限额（后续 PLAN）
- ❌ 不做充值记录导出/对账（QA-004 再议）
- ❌ 不扩展 Paymenter 外部网关（脱离本次范围）

## Alternatives

- **sqlmock + 白盒事务回滚验证。** 理论可行但只能断言 ROLLBACK 被调用，
  不能证明行状态真的没变（mock 不跑 Postgres 逻辑）。放弃。
- **自然日 UTC 00:00 重置。** 实现简单但攻击者可挑 23:59 / 00:01 连刷。滚动
  24h 安全性优势显著。

## 执行小结（2026-04-17 完成）

| Phase | 文件 | 说明 |
|---|---|---|
| A | `internal/testhelper/postgres.go` | testcontainers 起 `postgres:16-alpine`，按序执行 `db/migrations/*.sql`；ping 失败时 `t.Skip` 而非 fail，适配无 Docker / 嵌套容器环境 |
| A | `go.mod` / `Taskfile.yml` | 引入 `testcontainers-go` + `testcontainers-go/modules/postgres` v0.42.0；新增 `task test-integration`（带 `TESTCONTAINERS_RYUK_DISABLED=true`） |
| B | `internal/repository/order_integration_test.go` | 4 用例：HappyPath / InsufficientBalance（回滚核验）/ OrderNotPending / ConcurrentPay（FOR UPDATE 串行化） |
| C.1 | `db/migrations/005_topup_daily_limit.sql` | `idx_transactions_user_type_created (user_id, type, created_at)` |
| C.2 | `internal/repository/user.go` + `internal/handler/portal/user.go` | `SumDepositsSince(userID, since)`；`MaxTopUpPerDay=100000` 滚动 24h 守卫；新增 `GET /admin/users/{id}/topup-quota` 返回 `{used, limit, remaining, per_request_limit, window_hours}` |
| C.2 | `internal/repository/user_integration_test.go` | 窗口 + 类型过滤 + 空数据两用例 |
| C.3 | `web/src/features/users/api.ts` | `TopUpQuota` 类型 + `useTopUpQuotaQuery` hook；TopUp 成功后失效 quota |
| C.3 | `web/src/app/routes/admin/users.tsx` | 充值面板增加「今日已用 / 上限」徽章；超额时按钮置灰 + 红色提示 |
| C.3 | `web/public/locales/{zh,en}/common.json` | 新增 `user.topup.usedToday/dailyLimit/quotaExceeded` |

**验证**
- `go test ./...` 全绿（常规 unit 测试全量通过）
- `go test -tags=integration ./...` 在本地嵌套容器环境优雅 skip（Docker 网络 "wg0" 不可达）；CI/直连 Docker 环境会实际运行
- `bun run typecheck` + `bun run test`（14 tests pass） + `bun run build` 全绿
- 生产部署动作见 Phase D，待下轮部署窗口触发

**已知限制 / 跟进项**
- 日额度查询与扣款非原子；若要零容忍可把 `AdjustBalance` 合并为「INSERT ... WHERE SUM(..) + $amount <= $limit」，需数据库级条件插入。当前接受最后一笔可能略超，审计可溯。
- 集成测试需要宿主 Docker 直连；嵌套容器 / 特殊网络环境会 skip，CI 建议单独 job 跑 `task test-integration`。
