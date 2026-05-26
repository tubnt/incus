# cloud-gateway 集成（incus-admin `/v1` 适配层）

> PLAN-053 / INFRA-012：把 incus-admin 包装成 cloud-gateway 标准 cloud
> provider。AI 用户在 portal 拿 Bearer token 后，通过 12 个 `/v1` 端点
> 调度自己账下 VM，不动 `/portal` 与 `/admin` 现有 UI。

## 端点总览

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/v1/account` | 当前 token 持有人账户（余额 + 运行天数预估） |
| GET | `/v1/instances` | 列出账下 VM（分页；trashed/deleted/gone 过滤） |
| GET | `/v1/instances/{id}` | 单台 VM 详情（owner-scoped） |
| POST | `/v1/instances` | 一键创建 VM（订单 + 自动付款 + 异步 provisioning） |
| DELETE | `/v1/instances/{id}` | trash VM（30s 软删，期间 GET 返 status=`deleting`） |
| POST | `/v1/instances/{id}/boot` | 启动 VM |
| POST | `/v1/instances/{id}/reboot` | 重启 VM |
| POST | `/v1/instances/{id}/shutdown` | 关机 VM |
| GET | `/v1/types` | 列出可购买套餐（含 monthly + daily 价格） |
| GET | `/v1/regions` | 列出可用 region（cluster + capabilities） |
| GET | `/v1/images` | 列出 OS 镜像（admin 启用态） |
| GET | `/v1/ssh-keys` | 列出当前用户 SSH key |

完整 OpenAPI 3.0 spec 见 [`internal/handler/openapi/openapi.yaml`](../internal/handler/openapi/openapi.yaml)
（线上 `GET /api/openapi.yaml`，Swagger UI 在 `/api/docs`，tag=`cloud-gateway`）。

## 1. 生成 API token

1. 登录 portal（`https://vmc.5ok.co/`），进入 **/api-tokens** 页（顶栏「API Tokens」）。
2. 点击「新建 Token」→ 选 TTL（推荐 ≥ 7 天，AI 任务跑得久不易因过期断流）。
3. 复制弹窗里的 `ica_xxxxxxxx...` 字符串。**只显示一次**，丢了只能撤销重发。
4. 配在客户端：

   ```bash
   export INCUSADMIN_TOKEN="ica_xxxxxxxx..."
   export INCUSADMIN_BASE="https://vmc.5ok.co/api"
   ```

   随后所有请求都加 `Authorization: Bearer $INCUSADMIN_TOKEN`。

## 2. 鉴权 / 限流 / 错误格式

### 鉴权

`Authorization: Bearer ica_xxx`。不接受 oauth2-proxy header / shadow cookie /
emergency cookie（与 portal 强隔离）。失败：

```
401 { "errors": [ { "field": "Authorization", "reason": "missing_bearer" } ] }
401 { "errors": [ { "field": "Authorization", "reason": "invalid_token" } ] }
```

### 限流

- **100 rpm/token**（`INCUS_ADMIN_RATELIMIT_V1_RPM`），**burst 30**
  （`INCUS_ADMIN_RATELIMIT_V1_BURST`）。
- 写端点（POST/PUT/PATCH/DELETE）额外过一个 **burst 30** 的写桶；写桶顶住
  时退回 main 桶 token，避免读写互相饿死。
- 命中 429 时响应头：`RateLimit-Limit` / `RateLimit-Remaining` /
  `RateLimit-Reset`（秒） + `Retry-After`（秒）。body 走 StructuredError
  `{errors:[{reason:"rate_limited"}]}`。

### 错误格式

所有非 2xx 响应统一 `StructuredError`：

```json
{
  "errors": [
    { "field": "balance", "reason": "insufficient_balance", "message": "..." }
  ]
}
```

- `field`：出问题的字段名（空串表示请求级错误）。
- `reason`：机器可读 snake_case 错误代码（见下表）。
- `message`：人话描述，可选；不要在生产 UI 直显，用 reason 做 i18n。

| reason | 触发场景 | HTTP |
| --- | --- | --- |
| `missing_bearer` / `invalid_token` | Authorization 缺失 / 无效 / 过期 | 401 |
| `not_found` | 资源不存在 / 不属于当前 token / trashed-out | 404 |
| `insufficient_balance` | POST /instances 余额不足 | 402 |
| `region_unavailable` | region 不存在或非 available | 422 |
| `unsupported` | period 不在 product.period_supported（场景：daily 价 nil） | 422 |
| `rate_missing` | 选 daily/monthly 但 product 对应 rate 为 null | 422 |
| `not_found` (image/type/ssh-key) | 引用资源越权 / 已禁用 | 422/404 |
| `invalid` (`Idempotency-Key`) | header 形状违规（长度 / 字符集） | 422 |
| `mismatch` (`Idempotency-Key`) | 同 key 异 payload | 422 |
| `rate_limited` | 触发限流 | 429 |
| `internal` | 服务端 5xx（已记 slog.Error） | 500 |

### 分页

读端点统一 `?page=&page_size=`（默认 25，上限 100，1-indexed）。响应：

```json
{
  "data": [ /* item DTO */ ],
  "page": 1,
  "page_size": 25,
  "pages": 4,
  "total": 87
}
```

`pages = ceil(total / page_size)`；`total=0` 时 `pages=0`。

### Idempotency-Key

POST + DELETE 端点接受可选 `Idempotency-Key` header。

- 长度 16..255，字符集 `[A-Za-z0-9._-]`，违规返 422 reason=`invalid`。
- 24h 内同 key 同 payload → 命中缓存，回放原响应 + 响应头
  `Idempotent-Replay: true`。
- 24h 内同 key 异 payload → 422 reason=`mismatch`（`field: Idempotency-Key`）。
- 5xx 不缓存（让 client 重试拿到恢复后的后端）；2xx / 4xx 缓存 24h。
- 推荐用 UUIDv4 / hash(business-id + retry-count)；同一逻辑请求每次重发用
  同一 key，client 自行管理生命周期。

## 3. 端到端 curl example（完整跑通 12 端点）

> 设 `$INCUSADMIN_TOKEN` / `$INCUSADMIN_BASE`（见 §1）。下面所有请求都假设已
> export 这两个变量。

```bash
# 0. 通用 alias（避免每行重复 -H）
alias gw='curl -sS -H "Authorization: Bearer $INCUSADMIN_TOKEN" -H "Accept: application/json"'

# 1. 账户 + 余额 + 运行天数预估（无 active sub 时省略 estimated_runway_days）
gw "$INCUSADMIN_BASE/v1/account"

# 2. 套餐（含 prices.monthly 与 prices.daily 非空时输出）
gw "$INCUSADMIN_BASE/v1/types?page=1&page_size=10"

# 3. region 列表
gw "$INCUSADMIN_BASE/v1/regions"

# 4. OS 镜像
gw "$INCUSADMIN_BASE/v1/images"

# 5. SSH key
gw "$INCUSADMIN_BASE/v1/ssh-keys"

# 6. 创建按日付费 VM（Idempotency-Key 推荐传，断网重试不会重复扣费）
gw -X POST "$INCUSADMIN_BASE/v1/instances" \
   -H "Content-Type: application/json" \
   -H "Idempotency-Key: ce3f7d8e-cb38-4f74-bc1a-7c2c9e3b9b6f" \
   -d '{
     "region": "incus-cluster-01",
     "type": "small",
     "image": "ubuntu-2404",
     "label": "ai-worker-01",
     "period": "daily",
     "ssh_keys": [42]
   }'
# → 201 + Location: /v1/instances/123 + body {id, label, status:"pending", order_id, job_id, ...}

# 7. 轮询直到 status=running（最长几分钟，看 image + cluster 负载）
INSTANCE_ID=123
gw "$INCUSADMIN_BASE/v1/instances/$INSTANCE_ID"
gw "$INCUSADMIN_BASE/v1/instances?page=1&page_size=10"

# 8. 重启 / 关机 / 启动
gw -X POST "$INCUSADMIN_BASE/v1/instances/$INSTANCE_ID/reboot"
gw -X POST "$INCUSADMIN_BASE/v1/instances/$INSTANCE_ID/shutdown"
gw -X POST "$INCUSADMIN_BASE/v1/instances/$INSTANCE_ID/boot"

# 9. 回收（30s 内 GET 仍可见，status=deleting；过窗后 GET 返 404）
gw -X DELETE "$INCUSADMIN_BASE/v1/instances/$INSTANCE_ID"
```

### 错误路径示例

```bash
# 余额不足
gw -X POST "$INCUSADMIN_BASE/v1/instances" -H "Content-Type: application/json" \
   -d '{"region":"...","type":"jumbo","image":"...","period":"monthly"}'
# → 402 {"errors":[{"field":"balance","reason":"insufficient_balance"}]}

# region 不可用（不存在 / status != available）
gw -X POST "$INCUSADMIN_BASE/v1/instances" -H "Content-Type: application/json" \
   -d '{"region":"nope","type":"small","image":"ubuntu-2404"}'
# → 422 {"errors":[{"field":"region","reason":"region_unavailable"}]}

# period 不在 product.period_supported（如 monthly-only 产品要 daily）
gw -X POST "$INCUSADMIN_BASE/v1/instances" -H "Content-Type: application/json" \
   -d '{"region":"...","type":"small","image":"ubuntu-2404","period":"daily"}'
# → 422 {"errors":[{"field":"period","reason":"unsupported"}]}

# Idempotency-Key 形状违规
gw -X POST "$INCUSADMIN_BASE/v1/instances" \
   -H "Idempotency-Key: short" -H "Content-Type: application/json" -d '{...}'
# → 422 {"errors":[{"field":"Idempotency-Key","reason":"invalid"}]}
```

## 4. POST /v1/instances 请求 schema

```json
{
  "region": "incus-cluster-01",   // 必填，来自 /v1/regions item.id（cluster.name）
  "type": "small",                // 必填，套餐 id (int) 或 slug
  "image": "ubuntu-2404",         // 必填，OS 镜像 slug
  "label": "ai-worker-01",        // 可选，[a-z0-9-]{1,63}，缺省服务端生成
  "root_pass": "...",             // 可选，>=8 字符，缺省服务端生成 + DonePanel 返
  "ssh_keys": [42, "13"],         // 可选，int 或字符串 id，自动校验属本人
  "tags": ["ai", "exp-2026q2"],   // 可选
  "user_data": "#cloud-config...",// 可选，cloud-init user-data
  "period": "daily"               // 可选，daily | monthly，缺省 monthly
}
```

成功响应（201）：

```json
{
  "id": 123,
  "label": "ai-worker-01",
  "type": 1,
  "region": "incus-cluster-01",
  "image": "ubuntu/24.04/cloud",
  "status": "pending",
  "ip4": "",
  "ip6": "",
  "tags": [],
  "created_at": "2026-05-26T...",
  "order_id": 4567,
  "job_id": 8901
}
```

VM 完成 provisioning 需 1–10 分钟（image + cluster 负载决定）。客户端轮询
`GET /v1/instances/{id}` 直到 `status == "running"`；超时建议 600s。

## 5. 计费 / 周期

- 一期支持 monthly（一次扣 + 自动续费记账）和 daily（24h 扣一次）。
- 选 daily 时 product 必须有 price_daily 且 `period_supported` 含 `daily`。
- 余额不足 worker 会标 `suspended` + 72h grace；grace 过后 trash。
- `GET /v1/account.estimated_runway_days` 用 active sub 折算 daily burn 给出
  剩余可跑天数；balance ≤ 0 / 无 active sub 时省略字段。

详情见 PLAN-054 / INFRA-013。

## 6. 与 cloud-gateway provider 的关系

cloud-gateway 上游 provider 代码不在本仓库（按 OpenAPI spec 实现）。
本 `/v1` 适配层是 incus-admin 端的服务面，对外契约即 `openapi.yaml` 的
`cloud-gateway` tag 段。Provider 实现细节（缓存、超时、重试）由其自行决定，
但需遵守：

- 用 Bearer token（不要塞 cookie / 自定义 header）。
- 写端点务必传 `Idempotency-Key`，否则重试可能重复创建 VM / 扣费。
- 限流 429 → 用 `Retry-After` 退避；5xx → 短退避 + 重试。
- 创建实例返 201 后必须**轮询** GET 直到 running，不能假定立即可用。

## 7. 本地 / 测试环境跑通

```bash
# 启动 incus-admin（worktree 根）
cd incus-admin
task web-build && task build
./incus-admin

# 在另一终端：
export INCUSADMIN_BASE="http://localhost:8080/api"
# 走 portal /api-tokens 拿 token，或直接看 db users + api_tokens 表注入

# 跑 E2E 脚本（不会真创建 VM；只覆盖 read-only + 错误路径）
bash scripts/e2e-cloud-gateway.sh
```

完整 E2E 含创建 VM、轮询 running、reboot、shutdown、trash、Idempotency replay、
错误路径、限流等步骤，见 [`scripts/e2e-cloud-gateway.sh`](../scripts/e2e-cloud-gateway.sh)。
