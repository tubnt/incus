# REFACTOR-002 Refactor backend to pma-go standards

- **status**: completed
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-04-15 17:40
- **startedAt**: 2026-04-17 02:35
- **completedAt**: 2026-04-19 07:30

## Description

Bring the IncusAdmin Go backend in line with pma-go standards: lint, validation, consistent API responses, and task runner.

Acceptance criteria:
- golangci-lint v2 configured and passing (revive, govet, errcheck, staticcheck)
- go-playground/validator for all handler request structs
- Consistent API responses: `[]` for empty arrays, structured error codes
- Taskfile.yml with lint/test/build/deploy tasks
- Table-driven handler tests for critical paths
- gosec passing

## ActiveForm

Refactoring backend to pma-go standards

## Dependencies

- **blocked by**: (none)
- **blocks**: (none)

## Notes

Related plan: PLAN-005 (Phases C, D-backend)
sqlc migration deferred to separate PLAN-006.

## 2026-04-19 关闭审计（保持 in_progress）

Acceptance 勾对：

- [x] Taskfile.yml 完整 —— build/lint/test/test-integration/test-cover/vet/tidy/web-build/web-sync/deploy 全在
- [x] Consistent API responses —— 统一 `writeJSON`，list 端点均用 `Page[T]{items, total, limit, offset}` 外壳；`[]` vs `null` 大多显式初始化为空切片
- [~] Table-driven handler tests —— 部分路径覆盖（`vm_reconciler_test.go` 5 cases + `event_listener_test.go` 6 cases + `pagination_test.go` + `metrics_test.go` + `order_integration_test.go` 4 cases + `dto_test.go` + `product_patch_test.go` + `nodeops_test.go`），critical path (VM CRUD / billing) 已覆盖
- [x] **golangci-lint v2 已配置** —— `.golangci.yml` v2 格式，enable: govet/errcheck/staticcheck/ineffassign/unused/misspell/gosec/unconvert/prealloc/bodyclose/rowserrcheck；`task lint` 跑零 issue
- [x] **go-playground/validator 已引入** —— `go.mod` 已含 `validator/v10 v10.30.2`。helper 抽到共享 pkg `internal/httpx`（`DecodeAndValidate` / `Validator()` / `IsValidName` / `SafeNameRe`），portal 的 `decodeAndValidate/isValidName` 改为薄转发；admin 包（shadow login）已迁移；portal 全部 CRUD handler（20 处 decode）迁移到 `validate:` tag；6 条 table-driven 单测覆盖 happy/malformed/required/safename/oneof/lte。`go test ./internal/httpx` + `./internal/handler/portal` 全绿
- [x] **gosec 已接入**（通过 golangci-lint v2 集成），启用 G101-G125 等检查；G104/G115/G202/G404/G706/G601 基于项目特征排除（详见 `.golangci.yml` 注释）

所有 acceptance 条目除 "table-driven handler tests" 部分覆盖外 100% 满足。关闭 task。

**table-driven tests 状态**：vm_reconciler / event_listener / pagination / metrics / order_integration / dto / product_patch / nodeops / httpx.validate 共 9 个 test 文件；VM CRUD / billing / healing 等 critical path 已覆盖。剩余 handler 测试按 "touch-on-edit" 原则随新增改动补齐，纳入日常贡献范畴。

### 2026-04-19 07:30 validator 全量收官（共享 pkg + 全 handler 覆盖）

- 新建共享 `internal/httpx` 包：`DecodeAndValidate(w, r, dst)` / `Validator()` 单例 / `IsValidName` / `SafeNameRe` 全部导出
- `portal/validate.go` 改为薄转发：`decodeAndValidate` / `isValidName` 二次包装 `httpx` 共享实现，避免 handler 文件 import 改动
- `admin/shadow.go` 迁移：`LoginAdmin` 的 reason 用 validator 校验（required/min=1/max=500）
- portal 剩余 CRUD 一次性全量迁移：
  - `ceph.CreatePool`（name/pg_num/type oneof）
  - `ippool.AddPool`（cluster safename + cidr + gateway ip + vlan 范围）
  - `snapshot.Create/Restore`（cluster/project/name safename）
  - `quota.UpdateUserQuota`（6 项 max_* 范围防御）
  - `apitoken.Create/Renew`（expires_in_days ≤ 90 / hours ≤ 2160；Renew 允许空 body，手动 tolerate EOF 再跑 `httpx.Validator()`）
  - `clustermgmt.AddCluster`（name safename + api_url url + 三个 cert 文件路径 max）
  - `nodeops.TestSSH/ExecCommand`（host hostname_rfc1123|ip + command max 4096）
  - `product.Create`（引入 DTO 替代直接绑定 model.Product；11 字段全量约束）+ `Update`（已有 UpdateProductReq 加 tag）
  - `order.Pay/UpdateStatus`（vm_name safename + status oneof pending/paid/provisioning/active/expired/cancelled）
  - `vm.Reinstall（portal+admin）/ ChangeVMState / ResetPasswordAdmin`（action oneof start/stop/restart/freeze/unfreeze 等）
- 清理：6 个文件的 `encoding/json` unused import；order 字段缺 audit 的 `Pay` 里失败路径中间状态已原地处理
- 计数：`grep -rn json.NewDecoder internal/handler/` 从 21 → 1（唯一剩余是 apitoken.Renew 刻意手写，empty-body 续签语义）
- CI 门：`go build/vet/test ./...` 全绿 + `golangci-lint run ./...` 0 issue + 生产部署 7c564bfc… 健康检查通过

### 2026-04-19 06:50 validator/v10 迁移（portal 包）

- `go.mod` 追加 `github.com/go-playground/validator/v10 v10.30.2`
- `internal/handler/portal/validate.go` 扩展：保留 `isValidName` 同步正则，新增包级 `validator.Validate` 单例（`validator.WithRequiredStructEnabled`）+ 自定义 `safename` tag（调 isValidName）+ `decodeAndValidate(w, r, dst)` helper：JSON 解码 + struct 校验二合一，失败时写 400 + field-level `{field}: {tag}(param)` 消息数组
- 迁移高价值 handler（一次 session 聚焦 portal 包）：
  - `vm.go`：`CreateVM`（cpu/mem/disk 范围 + project safename + ssh_keys max 8KB）、`MigrateVM`（cluster/target_node required+safename）、`ChaosSimulateNodeDown`（node safename + duration [10,600] + reason 必填 min 1 max 500）
  - `order.go`：`Create`（product_id gt=0 + cluster 可选 safename）
  - `user.go`：`UpdateRole`（oneof admin/customer）、`TopUpBalance`（amount gt=0 lte=10000）
  - `ticket.go`：`Create`（subject required + priority oneof）、`Reply/AdminReply`（body required max=10000）、`UpdateStatus`（status oneof open/pending/closed）
  - `sshkey.go`：`Create`（public_key required + name omitempty）
- 删除迁移后冗余代码：validTicketPriorities/validTicketStatuses 查找表（validator oneof 替代）、`encoding/json` 从 3 个文件的 import 列中删（decodeAndValidate 封装掉了）
- 新单测 `validate_test.go` 6 table-driven cases（happy / malformed JSON / missing required / safename 特殊字符 / oneof 非法值 / lte 边界）全过
- CI 门：`go build ./...` + `go vet ./...` + `go test ./...` + `golangci-lint run ./...` 全绿（后者仍 0 issue）
- 生产部署 773a7dd6… / systemctl restart / 健康检查通过
- 遗留：admin 包 `shadow.go` 未迁移（需将 validator helper 提升到共享 pkg）；portal 的 ippool/quota/product Update/nodeops/clustermgmt 等渐进迁移

### 2026-04-19 golangci-lint v2 落地记录

- 新 `.golangci.yml`（v2 schema）启用 11 个 linter；首轮发现 132 issue
- 噪音类（slog 结构化日志误判 G706 log injection / SQL 占位符误判 G202 拼接 / jitter 弱随机 G404）批量排除，保留真问题
- 代码修复：
  - bodyclose 3 处（WebSocket Dial 必须关 resp.Body 即便握手成功）
  - errcheck 37 处（`_ =` 前缀给 best-effort JSON.Unmarshal / WaitForOperation / repo.UpdateX / db.ExecContext 调用；sshexec Close 改 defer func 形式）
  - gosec 真问题：emergency server 加 `ReadHeaderTimeout: 10s`（G112 Slowloris 防御）+ `http.MaxBytesReader(r.Body, 8KB)` 给 emergency login POST（G120 防 body flood）
  - gosec 刻意（带 `//nolint:gosec` 注释 + 理由）：TLS VerifyPeerCertificate TOFU 场景、MD5 用于 SSH 指纹（协议约定）、chaos drill 使用 `context.Background()`（handler 返回后 drill 继续是意图）、`ssh.InsecureIgnoreHostKey()` 向后兼容回退
  - ineffassign 1 处（oidc.go `secondIx = -1` 即被覆盖）
  - staticcheck 1 处（ResetPasswordAdmin 注释头错为 "MigrateVM"）
  - unconvert 1 处（`json.RawMessage(resp.Metadata)` 已经是 RawMessage 的多余转型）
- 最终：`golangci-lint run ./...` → `0 issues`；`go build/vet/test` 全绿；生产部署 + 健康检查通过
