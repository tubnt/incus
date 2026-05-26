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

- 新 migration `022_cluster_region_metadata.sql`：
  - `ALTER TABLE clusters ADD COLUMN country TEXT`
  - `ALTER TABLE clusters ADD COLUMN city TEXT`
  - `ALTER TABLE clusters ADD COLUMN region_status TEXT DEFAULT 'available'`
  - `ALTER TABLE clusters ADD COLUMN capabilities JSONB DEFAULT '["instances"]'`
- 默认值回填脚本（生产 5 个 cluster 手动填）
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
  - DB 表 `idempotency_keys (key TEXT PRIMARY KEY, user_id, response_body BYTEA, status_code INT, created_at)`
  - 24h TTL（cleanup worker 复用 audit_cleanup 套路）
  - 命中 key → 直接返缓存响应；未命中 → 走业务，写入 key
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
