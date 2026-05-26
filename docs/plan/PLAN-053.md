# PLAN-053 cloud-gateway /v1 适配层（INFRA-012）

- **status**: draft
- **createdAt**: 2026-05-26
- **task**: INFRA-012

## 0. 摘要

把 incus-admin 包装为 cloud-gateway 标准的 cloud provider —— 用户创建 API token，
AI 通过 MCP 工具调度自己账下 VM；不动 portal/admin 现有 API，只增加 `/v1/*` 适配层。

## 1. 现状 vs cloud-gateway 标准

| 项                   | 现状                                          | 标准要求                                                      | 修复           |
| -------------------- | --------------------------------------------- | ------------------------------------------------------------- | -------------- |
| Bearer token         | ✅ ica_ + Authorization header                | Bearer Token                                                  | -              |
| Token CRUD + TTL     | ✅ /api-tokens + 1h–90d                       | create/list/revoke/expire                                     | -              |
| 限流 + IETF headers  | ✅ token bucket 5 rps + burst 30              | ≥100 rpm + Retry-After                                        | 给 /v1/* 单独桶 |
| 审计                 | ✅ PLAN-019 全覆盖                            | (token_id, action, ip, ts, success)                           | 确认 token_id 写入 |
| OpenAPI spec         | ✅ /api/openapi.yaml + /api/docs              | OpenAPI 3.0                                                   | 补 /v1/* 段     |
| 实例 CRUD            | ✅ /portal/services                           | /instances                                                    | DTO 映射        |
| 创建实例             | 订单流 (POST /orders + POST /orders/{id}/pay) | POST /instances 一步                                          | 内部串单 (Phase B) |
| catalog              | /products + /os-templates                     | /types + /images                                              | alias + 字段映射 |
| region               | clusters 表（无 country/city）                | /regions + capabilities                                       | 扩 clusters 表  |
| 错误响应             | `{"error":"..."}`                             | `{"errors":[{"field","reason"}]}`                             | 包装层          |
| 分页响应             | `?limit&offset` + `{products,total,...}`      | `?page&page_size` + `{data,page,pages,total}`                 | 包装层          |
| Idempotency-Key      | ❌                                            | 写操作可选                                                    | 新增（推荐做）  |

## 2. 决策（已拍）

1. **/v1 适配层方案**（不走 X 重写 / Y 折中）
2. **POST /v1/instances**：完整 orders 流 + auto pay，`source=api` 标记
3. **DELETE**：trash + 30s undo（status=`deleting`），无 `?force=true`
4. **region metadata**：扩 `clusters` 表加 `country` / `city` / `region_status` / `capabilities`
5. **cloud-gateway provider 代码**：外部 repo 按 OpenAPI spec 实现，不在本 repo

## 3. Phase 划分

### Phase A：v1 路由骨架 + 基础设施（~0.5 天）

- 新增 `internal/handler/v1/` 包，挂 `/v1/*` chi router
- 复用 `middleware.Auth` + 新建 `middleware.RateLimitV1`（单独桶 100 rpm/token）
- 错误包装 helper：`writeErr(w, 400, "field", "required")` → `{errors:[...]}`
- 分页包装 helper：`writePage(w, data, page, pages, total)`
- 注册到 `internal/server/server.go`：`r.Route("/v1", h.V1.Routes)`

### Phase B：read-only endpoints（~1 天）

| 端点 | DTO 映射 |
| --- | --- |
| `GET /v1/account` | `/auth/me` → `{id, email, balance, currency}` |
| `GET /v1/instances` | services 列表 → instances；`name→label`，`product_id→type`，`cluster_id→region`，`os_template→image` |
| `GET /v1/instances/{id}` | 同上单条 |
| `GET /v1/types` | products active → `{id, label, vcpus, memory_mb, disk_gb, bandwidth_tb, price.monthly}` |
| `GET /v1/regions` | clusters + 新字段 → `{id, country, city, status, capabilities}` |
| `GET /v1/images` | os-templates → `{id, label, os, version, arch}` |
| `GET /v1/ssh-keys` | ssh-keys → `{id, label, fingerprint, public_key}` |

### Phase C：clusters 表 region metadata migration（~0.5 天）

- 新 migration `027_cluster_region_metadata.sql`（PLAN-053 原文写 022，但仓库实际占用至 026；落地用 027）：
  - `ALTER TABLE clusters ADD COLUMN country TEXT` （nullable，待运维填）
  - `ALTER TABLE clusters ADD COLUMN city TEXT` （nullable，待运维填）
  - `ALTER TABLE clusters ADD COLUMN region_status TEXT NOT NULL DEFAULT 'available'` + CHECK (available|unavailable|maintenance)
  - `ALTER TABLE clusters ADD COLUMN capabilities JSONB NOT NULL DEFAULT '["instances"]'`
- 默认值 cover 所有现状（country/city NULL = 待填，region_status='available'，capabilities=["instances"]），不写回填 SQL
- 仍走 sqlx，**不要手写 migration**（按 PMA 规则 #10 用 ORM 工具生成）

### Phase D：POST /v1/instances 一键创建（~1 天）

- 路由：`POST /v1/instances`
- 入参：`{region, type, image, label, root_pass, ssh_keys, tags, user_data, period?}`
- 流程：
  1. 校验 `region` 在 clusters 表，`type` 是 active product，`image` 是 os-template
  2. 校验余额 ≥ product.price_monthly（一期只支持 monthly，`period` 字段忽略）
  3. 内部调 `OrderService.Create` → `OrderService.PayWithBalance`（自动）
  4. 走 `jobs.Runtime` 异步 provisioning
  5. **同步返 201**，body 含 instance 对象（status=`pending`）+ `Location` header 指向 `/v1/instances/{id}`
- `source=api` 写入 audit + order metadata
- 余额不足 → 402 + `{errors:[{field:"balance", reason:"insufficient"}]}`

### Phase E：DELETE + actions + Idempotency-Key（~1 天）

- `DELETE /v1/instances/{id}` → trash，status=`deleting`
- `POST /v1/instances/{id}/reboot|shutdown|boot` → 复用 `vm.VMAction`
- `Idempotency-Key` middleware：
  - DB 表 `idempotency_keys` —— migration `029_idempotency_keys.sql`（实际编号；
    025/026 alert + 027 cluster region 占用，本表落在 028 billing 之后）
    schema: `key TEXT PRIMARY KEY, user_id, method, path, status_code, response_body BYTEA, request_hash, created_at`
  - 24h TTL（cleanup worker 复用 audit_cleanup 套路）
  - 命中 key → 比对 request_hash → 一致回放缓存响应，异 payload 返 422
  - 未命中 → 走业务，写入 key + status_code + response_body
  - 只对 POST/DELETE 生效

### Phase F：OpenAPI spec + 单测 + 文档（~0.5–1 天）

- `internal/handler/openapi/openapi.yaml` 新增 `/v1/*` paths，tag=`cloud-gateway`
- 错误响应 schema：`StructuredError`
- 分页响应 schema：`PaginatedResponse`
- 单测：每个 endpoint 至少 happy path + 1 错误 path
- CI 跑 `swag fmt --check` + `bun run typecheck` + `go test ./...`
- README 新增 "cloud-gateway integration" 段：endpoint 列表 + curl example + token 生成步骤

## 4. 工作量估算

| Phase | 估时 |
| ----- | --- |
| A 骨架 | 0.5 天 |
| B read-only | 1 天 |
| C migration | 0.5 天 |
| D POST 创建 | 1 天 |
| E DELETE + Idempotency | 1 天 |
| F OpenAPI + 单测 + 文档 | 0.5–1 天 |
| **合计** | **4.5–5 天** |

## 5. 风险

1. **DTO 漂移**：services / products schema 改动时 v1 mapper 不会自动跟。**对策**：写单测覆盖映射；同一 PR 同步改两边
2. **Idempotency-Key 性能**：高并发下 DB 唯一约束冲突。**对策**：先 SELECT 再 INSERT，冲突回缓存；24h 自动 cleanup
3. **`POST /v1/instances` 异步语义**：返 201 时 status=`pending`，AI 需轮询。需在 OpenAPI 文档明确说明
4. **限流穿透**：`/v1/*` 单独桶若配置太宽会把 cluster 打挂。**对策**：100 rpm/token 是上限，可改 env 配置 + 监控
5. **关闭 stateful trash 行为**：cloud-gateway 标准的 DELETE 是 critical 不可恢复；我们用 trash，AI 在 30s 内重新 GET 会看到 status=`deleting`（不是 404）。需在 OpenAPI 明确说明

## 6. 与 INFRA-013 关系

PLAN-053 一期**只支持 monthly**（沿用现有计费）。
INFRA-013 / PLAN-054 落地"按天付费 billing engine"后：

- `/v1/types` 响应加 `prices.daily`
- `POST /v1/instances` 接受 `period: "daily" | "monthly"`，默认按用户决策
- `/v1/account` 加 `estimated_runway_days` 字段

scope 是 **二选一**（见 INFRA-012 task），等用户拍板。

## 7. 验证

- 本地：`bun run typecheck && go test ./... && go build ./cmd/server`
- E2E：起 incus-admin → 创 token → curl 跑完 11 端点（顺路给 cloud-gateway 团队的 curl example）
- 灰度：先在测试 cluster 开 `/v1/*`，验证 1–2 周再开生产

## 8. 实施进度

| Phase | 状态 | 落地点 |
| ----- | --- | ------ |
| A 骨架 | ✅ 完成（L3-A 2hvsynkt） | `internal/handler/v1/` 12 占位端点 + StructuredError + 分页 helper + 双桶限流 + RequireBearer + 22 单测 |
| B read-only | ⏳ 待 L3-D | /v1/account /v1/instances /v1/types /v1/regions /v1/images /v1/ssh-keys |
| C cluster region migration | ✅ 完成（L3-B q21mhhyk） | `db/migrations/027_cluster_region_metadata.sql` + model.Cluster Country/City/RegionStatus/Capabilities + cluster_repo 4 SELECT 路径回填 |
| D POST /v1/instances | ⏳ 待 L3-G | 复用 OrderService + Idempotency-Key 接入 |
| E DELETE + Idempotency schema | ✅ schema 完成（L3-C 640vr0dt） | `db/migrations/029_idempotency_keys.sql` + `model.IdempotencyKey` + `repository.IdempotencyRepo` skeleton（middleware 留给 L3-H） |
| F OpenAPI + 单测 + 文档 | ⏳ 待 L3-J | openapi.yaml 增 /v1/* + curl example |

### Phase A 骨架（2026-05-26 完成 · campaign cloud-gateway-20260526202415）

- ✅ `internal/handler/v1/` 包：
  - `handler.go` — `Handler` 结构体 + `New()` 构造函数（Phase A 无依赖；后续阶段按需注入）
  - `router.go` — `Routes(r chi.Router)` 挂载 11 个端点占位（7 read-only + 4 写，写端点拆出 reboot/shutdown/boot 共 5 个；EndpointCount=12）；占位统一返 `501` + `{"errors":[{"field":"","reason":"not_implemented"}]}`
  - `errors.go` — `FieldError{Field,Reason}` + `writeErr/writeErrs`；空 errs 兜底为 `unknown`，避免空数组歧义
  - `pagination.go` — `parsePagination` 默认 page=1 / page_size=25、上限 100；非法/越界返 `*paginationErr`；`writePage` 自动算 `pages = ceil(total/page_size)`，total<0 钳到 0
- ✅ `internal/middleware/ratelimit_v1.go`：
  - 自实现 token bucket（capacity=burst，refill=rpm/60）；
    每 token 一桶 → 用户互不影响
  - 写操作（POST/PUT/PATCH/DELETE）额外过独立 write 桶；
    write 桶顶住时退回 main 桶 token，避免读写互相饿死
  - 命中 429 时：IETF `RateLimit-Limit/Remaining/Reset` + `Retry-After` + StructuredError
  - env：`INCUS_ADMIN_RATELIMIT_V1_RPM`（默认 100）+ `INCUS_ADMIN_RATELIMIT_V1_BURST`（默认 30）
  - 10 分钟清理 idle 桶，防止 map 无限增长
- ✅ `internal/middleware/auth.go` 新增 `RequireBearer`：Bearer-only 鉴权
  （不接受 oauth2-proxy header / shadow cookie / emergency cookie），
  失败返 401 StructuredError（reason: `missing_bearer` / `invalid_token`），
  `tokenValidator` 未设置返 503（reason: `token_validator_unset`），
  通过后写入 `CtxUserID + CtxAuthMethod=api_token`
- ✅ `internal/server/server.go`：
  - 新增 `Handlers.V1 RouteRegistrar`
  - 在 ProxyAuth Group **之外** 挂载 `/v1`：
    `r.Use(middleware.RequireBearer)` → `r.Use(middleware.RateLimitV1FromEnv())` → `h.V1.Routes(r)`
  - 启动日志：`slog.Info("v1 routes registered", "endpoints", v1handler.EndpointCount)`
- ✅ `cmd/server/main.go`：`Handlers.V1 = v1handler.New()`
- ✅ 单测：
  - `handler/v1/errors_test.go`：writeErr / writeErrs / 空数组兜底 / notImplemented
  - `handler/v1/pagination_test.go`：缺省 / 边界 100 / >100 / 非整数 / 负值 / writePage 计算（0、101→5）
  - `handler/v1/router_test.go`：11 端点全部 501 + Content-Type + StructuredError；与 EndpointCount 同步校验
  - `middleware/ratelimit_v1_test.go`：burst 30 全放行 / 31 个 429 + headers / 多用户隔离 / 写桶独立 / env defaults
  - `middleware/ratelimit_v1_test.go` 同文件覆盖 RequireBearer：tokenValidator nil / 缺 header / 非 ica_ / 合法 token
- ✅ `go build ./...` 全绿；`go test ./...` 全绿；`golangci-lint run ./internal/handler/v1/... ./internal/middleware/... ./internal/server/...` 零警告（main.go 的 `rowserrcheck` 是 pre-existing 不在本 Phase 范围）

#### Phase A 范围内**未做**（按设计）

- 任何具体业务实现（DTO mapping → Phase B；POST /instances → Phase D；DELETE+actions → Phase E）
- Idempotency-Key middleware（Phase E）
- OpenAPI yaml 更新（Phase F）
- region metadata migration（Phase C）

### Phase C clusters region metadata（2026-05-26 完成 · L3-B q21mhhyk）

- ✅ `db/migrations/027_cluster_region_metadata.sql`：4 列 ADD COLUMN IF NOT EXISTS；
  region_status NOT NULL DEFAULT 'available' + CHECK；
  capabilities NOT NULL DEFAULT '["instances"]'::jsonb；
  不写回填 SQL（默认值 cover 现状）
- ✅ `model.Cluster`：Country/City（json omitempty, nullable）+
  RegionStatus + CapabilitiesJSON([]byte) + Capabilities([]string) 拆字段，
  与现有 IPPoolsJSON 模式对齐
- ✅ `model` 加 3 个 RegionStatus 常量 + `DefaultClusterCapabilities = ["instances"]`
- ✅ `repository/cluster.go`：抽 `clusterBaseColumns` / `scanBase` / `finalizeRegion`；
  GetByName/GetByID/List/ListFull 4 条 SELECT 全部 COALESCE 兜底；CreateFull 不写新列
- ✅ 集成测试：docker postgres:16 真连验证 4 条 SELECT 路径 + CHECK 拒绝 bogus；
  本地 testcontainers 因 sandbox 网络限制 skip（与 ci_pitfalls 一致，CI 不受影响）
- ✅ `go build ./...` + 全量 `go test ./...` 全绿

#### Phase C 范围内**未做**（按设计）

- /v1/regions endpoint 本体（Phase B / L3-D 负责）
- admin UI 编辑 country/city/region_status（后续单独 task）
- pre-existing：cluster_repo 对 display_name 不做 COALESCE（schema 允许 NULL）；
  非本 phase 引入，建议后续单 issue 处理

### Phase E schema —— Idempotency-Key + billing （2026-05-26 完成 · L3-C 640vr0dt 与 PLAN-054 Phase F 同批落地）

- ✅ `db/migrations/028_billing_subscriptions.sql`：products 加 price_daily + period_supported；
  orders 加 period（CHECK daily|monthly）；新表 vm_subscriptions + billing_charges +
  UNIQUE(sub_id, charge_date) 防重扣 + 3 索引
- ✅ `db/migrations/029_idempotency_keys.sql`：key PRIMARY KEY + 24h cleanup index +
  request_hash 列（同 key 异 payload 检测）
- ✅ `model`：Product +PriceDaily/PeriodSupported；Order +Period；
  新结构 VMSubscription / BillingCharge / IdempotencyKey + 常量集
- ✅ `repository`：subscription_repo / charge_repo / idempotency_repo skeleton；
  product / order repo SELECT 列 helper 抽出避免后续漂移

#### Phase E schema 范围内**未做**（按设计）

- Idempotency-Key middleware 本体（L3-H 负责）
- 订单流 period hook + sub 行写入（L3-E 负责）
- billing worker（L3-F 负责）
