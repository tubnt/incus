# PLAN-011 Frontend pma-web compliance cleanup

- **status**: completed
- **createdAt**: 2026-04-17 01:05
- **approvedAt**: 2026-04-17 02:30
- **completedAt**: 2026-04-17 14:40
- **relatedTask**: REFACTOR-001 + REFACTOR-002

## Context

Full-stack QA (QA-003) and PLAN-010 closed out production bugs. A pma-web
baseline audit run immediately after exposed the remaining compliance gaps
in `incus-admin/web/`:

- Provider graph, stack versions, router + query wiring, Tailwind v4 tokens,
  `ConfirmDialog`, kebab-case/PascalCase naming, `dangerouslySetInnerHTML`
  absence — all ✅.
- Gaps concentrate in **data-layer discipline**, **i18n coverage**,
  **feature extraction**, and a short list of **style/type drift**.

Evidence (file:line, counts verified in the audit):

1. **Routes call `http.*` directly — 78 sites in 25 files**, bypassing the
   feature `useXxxQuery/useXxxMutation` pattern required by
   `pma-web/baseline.md:116`. Some features already expose hooks
   (`features/vms/api.ts`, `features/clusters/api.ts`,
   `features/monitoring/api.ts`, `features/snapshots/api.ts`,
   `features/tickets/api.ts`), but many routes re-inline the fetch.
   `features/auth/`, `features/products/`, `features/dashboard/`,
   `features/console/` are empty.
2. **Hardcoded CJK strings — 16 files, ~220 occurrences.** i18n is fully
   configured; `t()` adoption is inconsistent. Worst offenders:
   `app/routes/admin/node-join.tsx` (43), `admin/products.tsx` (27),
   `admin/monitoring.tsx` (24), `admin/nodes.tsx` (21), `tickets.tsx` (20),
   `admin/tickets.tsx` (16), `admin/users.tsx` (14),
   `admin/invoices.tsx` (10), `ssh-keys.tsx` (8), `settings.tsx` (9),
   `api-tokens.tsx` (9), `features/monitoring/vm-metrics-panel.tsx` (6).
3. **`shared/lib/query-client.ts:6` `staleTime: 30_000`** vs baseline 60s.
4. **Business components live inside route files.** Top offenders:
   `admin/storage.tsx` (385 lines), `admin/vms.tsx` (361), `admin/monitoring.tsx`
   (360), `admin/nodes.tsx` (356), `admin/products.tsx` (283),
   `admin/clusters.tsx` (269). Each embeds multiple forms/dialogs that
   should live in `features/*/`.
5. **Hardcoded Tailwind color literals** — non-semantic `bg-yellow-500/20
   text-yellow-600` (status pills) in 8 files,
   `bg-black text-green-400` (terminal/log blocks) in 4 files,
   hex `#f59e0b` in `admin/monitoring.tsx:207`, xterm theme in
   `features/console/terminal.tsx:25-28` (four hex colors).
6. **`as any` escapes** outside generated code: `layout/app-header.tsx:19`
   (`theme as any`), `layout/app-sidebar.tsx:75` (`item.to as any`).
7. **`React.*` type annotations** where baseline prefers namespace or direct
   type imports: `shared/components/theme-provider.tsx:12`,
   `shared/components/ui/confirm-dialog.tsx:21`,
   `shared/components/layout/app-sidebar.tsx:11`,
   `app/routes/admin/monitoring.tsx:342`.
8. **Thin UI primitive shelf** — `shared/components/ui/` has only
   `confirm-dialog.tsx` + `skeleton.tsx`. No shadcn `Dialog`, `Select`,
   `Tabs`, `Tooltip`, `Popover`, `DropdownMenu` generated yet, forcing
   inline solutions.
9. **Tests near zero** — only `shared/lib/utils.test.ts`. Vitest 4 is
   configured but not exercised.
10. **`vite.config.ts` manual alias** (acceptable but inconsistent with the
    recommended `vite-tsconfig-paths`); `useIsMobile` in
    `app/routes/__root.tsx:29-39` duplicates what `md:` utilities can do.

## Proposal

Five ordered phases. Each phase is independently shippable; later phases
assume earlier ones merged.

### Phase A — Data-layer discipline (P1)

Goal: all network access in routes flows through feature hooks; no bare
`http.*` in `src/app/routes/**`.

1. For each feature referenced by a route, ensure `features/<feature>/api.ts`
   exists and exports the relevant `useXxxQuery` / `useXxxMutation` pairs.
   New files needed:
   - `features/billing/api.ts` (orders/invoices/topup)
   - `features/ssh-keys/api.ts`
   - `features/api-tokens/api.ts`
   - `features/users/api.ts` (admin user mgmt)
   - `features/storage/api.ts` (Ceph OSD/pool/health)
   - `features/nodes/api.ts` (nodes list, join, evacuate)
   - `features/products/api.ts` (plans CRUD)
   - `features/ippool/api.ts` (ip-pools, ip-registry)
   - `features/ha/api.ts`
   - `features/observability/api.ts`
   - `features/audit/api.ts`
   - `features/node-ops/api.ts`
2. Migrate routes one-by-one. Each route reduces to: route guard +
   layout + composed feature components + feature hook calls.
3. Set `shared/lib/query-client.ts` `staleTime` back to `60_000`.

**Acceptance**: `grep -rn "http\.\(get\|post\|put\|delete\)"
src/app/routes/` returns 0.

### Phase B — i18n coverage (P1)

1. Extract all hardcoded CJK strings in the 16 listed files into
   `public/locales/{en,zh}/common.json` (or per-feature namespaces when the
   translation file grows past ~150 keys).
2. Use one consistent key-path scheme: `<area>.<context>.<key>` — e.g.
   `sshKey.addTitle`, `nodeJoin.step1Title`, `product.createButton`,
   `ticket.statusPending`. Reuse existing scheme where it already matches.
3. Add ESLint rule `@eslint-react/no-literal-string` (or a narrower custom
   regex check) to catch regressions. Configure `allowedStrings` for
   punctuation-only and identifier-like literals.
4. Run pass: `grep -rnP "[\\x{4e00}-\\x{9fa5}]" src/` returns only comments.

**Acceptance**: zero CJK literals inside JSX across `src/`; language
toggle switches every visible string.

### Phase C — Feature extraction (P1 → P2)

1. Move embedded components out of route files into
   `features/<area>/components/`:
   - `admin/storage.tsx` → `features/storage/components/{PoolList,
     PoolCreateForm, OSDList, CephHealthPanel}.tsx`
   - `admin/vms.tsx` → `features/vms/components/{ClusterTabs,
     AdminVMList, AdminVMRow, EvacuateForm, MigrateDialog}.tsx`
   - `admin/nodes.tsx` → `features/nodes/components/{NodeList,
     EvacuateForm, JoinWizard}.tsx`
   - `admin/clusters.tsx` → `features/clusters/components/{ClusterList,
     AddClusterForm}.tsx`
   - `admin/products.tsx` → `features/products/components/*.tsx`
   - `admin/monitoring.tsx` → `features/monitoring/components/*.tsx`
2. Target: each route file ≤ 120 lines, contains only
   `createFileRoute` + composition.

**Acceptance**: no route file > 200 lines; domain components importable
from `features/*`.

### Phase D — UI primitive shelf + style drift (P2)

1. Run `bunx shadcn@latest add dialog select tabs tooltip popover
   dropdown-menu badge button card input textarea toast` (the subset the
   audit shows we need). Output lands in `src/shared/components/ui/`.
2. Replace hardcoded status-pill literals (`bg-yellow-500/20 ...`) with a
   `StatusBadge` component that maps
   `pending|warning|error|success|muted` → semantic tokens
   (`bg-warning/20 text-warning`, etc.). Add the missing warning token to
   `src/index.css` if absent.
3. Replace `bg-black text-green-400` terminal/log blocks with a
   `<CodeBlock variant="terminal" />` component; keep one definition.
4. `features/console/terminal.tsx`: read `getComputedStyle(root)` for
   `--color-background`, `--color-foreground`, `--color-primary`,
   `--color-muted` and convert to xterm theme colors; re-apply when theme
   changes via `MutationObserver` on `<html>` class list.
5. Replace `admin/monitoring.tsx:207` hex with a theme token.
6. Fix `as any` in `layout/app-header.tsx:19` and
   `layout/app-sidebar.tsx:75`. Use correct TanStack Router `LinkProps`
   type for the latter.
7. Convert the four `React.ReactNode`/`React.ElementType` usages to direct
   `import type` form.

**Acceptance**: no `as any` outside `routeTree.gen.ts`; no color hex in
non-generated TS; Tailwind grep returns no `bg-(red|yellow|green|blue|
amber)-\d00` literals.

### Phase E — Verification + tests (P2)

1. Add Vitest suites:
   - `shared/lib/http.test.ts` — success, 4xx/5xx JSON error body, network
     error, params encoding.
   - `shared/components/ui/confirm-dialog.test.tsx` — promise resolves
     `true` on confirm, `false` on cancel/close; focus trapped.
   - `features/vms/api.test.ts` — mock `http`, verify query keys and
     mutation invalidations.
2. Add `vite-tsconfig-paths` to `vite.config.ts`; remove manual alias
   block.
3. Replace handwritten `useIsMobile` in `__root.tsx` with Tailwind-only
   layout using `md:` utilities + CSS-driven drawer state.
4. Wire CI: `bun run lint && bun run typecheck && bun run test &&
   bun run build` must all pass.

**Acceptance**: CI green; coverage for data layer and confirm dialog > 0.

## Risks

- **Phase A–C blast radius is large.** Each route migration risks breaking
  cache key invalidation and subtle refetch timing. Mitigation: do it one
  feature at a time, keep query keys identical to the existing inline
  versions, ship per-feature PRs, and rely on the existing production
  smoke-test loop.
- **i18n JSON churn** will produce large diffs in `common.json`. Keep one
  PR per feature area to keep review sane.
- **shadcn add** mutates `components.json` and may pull in `@base-ui/react`
  primitive variants not already in `bun.lock`; run `bun install` before
  first commit and verify bundle size delta in `vite build` output.
- **Phase E CI wiring** may surface lint errors currently suppressed by
  `react-refresh/only-export-components: off`. Fix as encountered; do not
  widen the off-list further.

## Scope

- Files touched (estimate): 40–55 frontend files across 5 phases.
- New files: ~12 `features/<area>/api.ts`, ~25 extracted component files,
  ~8 shadcn primitives, 3 Vitest specs.
- No backend changes.
- No new runtime deps (shadcn primitives use `@base-ui-components/react`
  already present).
- Dev deps: `vite-tsconfig-paths` (Phase E).

## Alternatives

- **Big-bang refactor in one PR** — rejected: too large to review, high
  regression risk on a live cluster.
- **Skip Phase C (leave components in routes)** — rejected: would leave
  REFACTOR-001 partially unmet and keep 350-line routes that mix
  business logic with routing.
- **Add literal-string lint rule first (Phase B before A)** — viable but
  Phase A unblocks the hooks-first pattern which several new feature
  components will rely on; stick with A→B.

## Annotations

- 2026-04-17 01:05 — Plan drafted from the pma-web baseline audit
  performed after PLAN-010 / QA-003 shipped. Feeds into the still-open
  REFACTOR-001 task.

- 2026-04-17 01:45 — 深度审查（User Journey 维度）追加。以下内容按发现
  优先级罗列，后续落地时合并进各 Phase 或新增 Phase F。已用
  Serena / code-review-graph / grep 逐条追溯调用链，非猜测。

### 深度审查发现（User Journey 视角）

#### P0 — 生产缺陷（阻塞核心功能，必须先于 Phase A 修复）

**P0-1 门户 VM 详情页契约不匹配（长期损坏）**
- 位置：`incus-admin/web/src/app/routes/vm-detail.tsx:42`
  `http.get<{ vm: VMService }>(/portal/services/${id})` →
  `data?.vm` 取值。
- 后端：`internal/handler/portal/vm.go:145` 返回
  `{"service": vm}`（键名为 `service`，不是 `vm`）。
- 影响：`data.vm` 恒为 `undefined`，QA-003 B14 修复后所有 VM 点击
  均显示 “Not Found”（不论 VM 是否存在）。`git log` 显示该键
  一直是 `service`，前端从未对齐。
- 修法：统一为 `{"vm": vm}` 或前端读 `data.service`。建议
  Phase A 同时把 `vms` 的 list/detail 响应体改成
  `{ vms: [...] }` / `{ vm: ... }`。

**P0-2 `findClusterName` 丢弃 VM.ClusterID**
- 位置：`internal/handler/portal/vm.go:288-294`
  ```go
  func findClusterName(mgr *cluster.Manager, _ int64) string {
      clients := mgr.List()
      if len(clients) > 0 { return clients[0].Name }
      return ""
  }
  ```
- 调用点：`VMAction`（line 148）、`ResetPassword` 等。
- 影响：多集群部署下，所有用户 VM 的 start/stop/restart/reset
  操作都会被路由到第一个集群。若 VM 实际在第二个集群，操作
  静默打到错误集群（可能命中同名 instance 或 404）。
- 修法：引入 `cluster.Manager.GetByID(clusterID int64)`，
  `findClusterName(mgr, vm.ClusterID)` 返回真实名字。

**P0-3 用户 CreateService 硬编码 `ClusterID: 1`**
- 位置：`internal/handler/portal/vm.go:265`
  `vm := &model.VM{ ..., ClusterID: 1, ... }`。
- 影响：所有用户下单创建的 VM 在 DB 中 ClusterID 均为 1，与
  实际创建位置（`h.clusters.List()[0]` — 见 line 221）脱钩，
  且多集群环境下调度决策完全丢失。
- 修法：用 `mgr.List()[0].ID`（或调度器返回的 ClusterID）写入
  DB；长期需要实现 product → cluster 关联或调度策略。

#### P1 — 业务闭环缺口

**P1-1 `/portal/services` JSON 缺 `cluster_name` / `project`**
- 后端 `ListServices`（vm.go:118-131）直接返回
  `[]model.VM`，`model.VM` 结构体（`model/models.go`）只有
  `ClusterID int64`，没有 `Project`、没有 `ClusterName`。
- 导致前端 25 处硬编码 `cluster=cn-sz-01&project=customers`：
  `routes/vms.tsx:104,123`、`vm-detail.tsx:96,144`、
  admin 的 console 链接构造等。
- 必须作为 **Phase A 的前置改造 (A.0)**：
  1. 新建 `handler/portal/dto.go`，定义 `VMServiceDTO`，
     含 `cluster`, `cluster_display_name`, `project` 三字段；
  2. `ListServices` / `GetService` 返回 DTO；
  3. 前端 `features/vms/api.ts` 的 `VMService` 类型补齐；
  4. 所有 Console / Snapshot / Metrics 链接改读 DTO 字段。

**P1-2 TanStack Query 缓存键分裂**
- List: `["myServices"]`（`features/vms/api.ts:35`，
  `vms.tsx:32`，billing.tsx 4 处 invalidation）。
- Detail: `["myService", id]`（`vm-detail.tsx:41`，大写 S 单数）。
- VM action 后 `vm-detail.tsx:50` 仅 invalidate
  `["myService", id]`，不触发 list 刷新；`vms.tsx:75` 只
  invalidate list，不触发 detail 刷新；双向都 stale。
- 修法：**Phase A 必须统一命名**，不是 "keep keys identical"
  就完事。标准表：

  | 资源 | list | detail | 其他 |
  |------|------|--------|------|
  | 用户 VM | `["vms", "myList"]` | `["vms", "myDetail", id]` | — |
  | 管理 VM | `["vms", "adminList", clusterName]` | `["vms", "adminDetail", cluster, name]` | — |
  | 订单 | `["orders", "myList"]` | — | — |
  | 工单 | `["tickets", "myList"]` | `["tickets", "detail", id]` | — |
  | 节点 | `["nodes", "list"]` | `["nodes", "detail", cluster, name]` | — |
  | 集群 | `["clusters", "list"]` | — | — |
  | 快照 | `["snapshots", "list", vm, cluster, project]` | — | — |
  | 度量 | `["metrics", "vm", vm, apiBase, cluster]` | — | `["metrics", "adminOverview"]` |

  所有 mutation 均同时 invalidate 关联的 list + detail 前缀。

**P1-3 admin/create-vm 永远落到 `clusters[0]`**
- 位置：`admin/create-vm.tsx:35`
  `const clusterName = clustersData?.clusters?.[0]?.name ?? "";`
- 页面 UI 里 `Project` 有 select（硬编码 customers/default），但
  `Cluster` 没有 select，下单直接落到 `/admin/clusters/<first>/vms`。
- 修法：Phase C 抽出 `ClusterPicker` 组件，并把 project 从
  枚举改成基于后端 `/admin/clusters/<c>/projects` 拉取。

**P1-4 admin/ha 同样只看 `clusters[0]`**
- 位置：`admin/ha.tsx:31`。多集群 HA 无法切换。
- 修法：用 ClusterPicker 统一，存到 URL search 参数。

**P1-5 事件流仅 admin 可用**
- 后端 `handler/portal/events.go:31-33` 路由挂在 `AdminRoutes`，
  portal 用户打不到；`StreamEvents` 也没按用户过滤，即便开放
  给 portal 也会泄露其他用户事件。
- 前端仅 `admin/observability.tsx:86-102` 使用 WebSocket，其他
  VM/节点/任务状态页面全部依赖轮询。
- 修法：新增 `/api/portal/events/ws`，按 `vm_id IN (user's vms)`
  过滤 Incus lifecycle 事件；新增 `shared/lib/events.ts` 统一封装
  `useVMEvents({ cluster, project, vmName })` hook；前端在获得
  事件后本地 `queryClient.setQueryData` 或 `invalidateQueries`。
  建议并入 **Phase F（新增）** “事件流统一接入”。

**P1-6 billing → pay 后 detail 页未 invalidate**
- `billing.tsx:198-205` `payMutation.onSuccess`：invalidate
  了 `["myServices"]`、`["myInvoices"]`、`["currentUser"]`，
  但没 invalidate 对应 VM 的 detail（此时尚无 id 可 invalidate
  属实）。更麻烦的是 OrderRow 再支付时（line 250-253）也
  同样遗漏 detail。因为 Phase A 统一 key 后只需 invalidate
  前缀 `["vms"]`，这个问题会自然消失——这正是 P1-2 标准化
  的价值，需在 Phase A 用例中列出。

#### P2 — 性能与规范

**P2-1 轮询雪崩（13 处 refetchInterval）**
- 同时 15s/10s 间隔的查询：`vms.tsx` (15s)、`admin/vms.tsx`
  (15s)、`admin/nodes.tsx` (15s)、`admin/ha.tsx` (15s)、
  `admin/orders.tsx` (15s)、`admin/tickets.tsx` (15s)、
  `admin/audit-logs.tsx` (15s)、`features/vms/api.ts`
  list (15s) 和 cluster list (10s)、`features/tickets/api.ts`
  myTickets (15s)、`features/monitoring/*` (30s) 等。
- 问题：打开 admin 首页后峰值 QPS ≈ 10+ req/15s，Incus API
  侧是单集群 REST，长期会出现连接复用饱和。
- 修法（随 Phase F 一起）：关键实时资源（VM state、node
  online、task progress）改订阅事件流；非实时（产品、发票、
  工单列表、审计日志）调高 `staleTime` 到 120s，
  `refetchInterval` 删除或改成 `refetchOnWindowFocus: true`。

**P2-2 `billing.tsx` 三并发查询无门控**
- 页面加载瞬间同时发起 `/portal/orders`、`/portal/invoices`、
  `/portal/products` 三个请求。建议 Phase C 拆出
  `BillingPage`，把 products 查询交给 `<ProductGrid>`（可
  prefetch），invoices 延迟到 tab 切换时加载。

**P2-3 `useIsMobile` 与 `md:` 断点不一致**
- `__root.tsx:29-39` 自定义 `< 768` 即 mobile，但 Tailwind
  `md:` 断点是 `>= 768`。当前碰巧一致，但会随 CSS 变量迁移
  或设计稿调整漂移。Phase E 要求换成 CSS-only 方案，这条
  已在原计划里，保持。

**P2-4 `admin/vm-detail.tsx` 用 `adminClusterVMs` 作为详情来源**
- 位置：`admin/vm-detail.tsx:36` 复用 list 的 queryKey 后
  在组件内 `list.find(...)` 找单条。这会在 list 尚未加载
  时永远为空，也会被 15s 轮询重新拉整个 list。Phase A
  要求：admin 详情页改用 `/admin/vms/<cluster>/<name>` 单独
  endpoint（若后端没有就新增），key 走
  `["vms", "adminDetail", cluster, name]`。

**P2-5 事件上报中文字符串还在混用**
- `admin/vms.tsx:152` `toast.success(\`${vm.name}: ${action}
  ${t("vm.actionSubmitted")}\`)`，格式串由代码拼成，不利于
  中英切换。Phase B 要求统一 `t("vm.actionSubmittedFor",
  { name, action })` 占位符风格。

#### 对原 PLAN-011 的修正建议

1. **在 Phase A 之前插入 Phase A.0「后端契约增补」**：
   - 修 P0-1、P0-2、P0-3；
   - 为 `/portal/services` 增加 `cluster`、`cluster_display_name`、
     `project` 字段；
   - 为 Admin VM 详情提供
     `GET /admin/clusters/{cluster}/vms/{name}` 单体 endpoint；
   - 这一步没有就无法安全完成 Phase A 的钩子迁移（否则钩子
     内部仍要硬编码 cluster/project）。

2. **Phase A 增加“缓存键标准化表”**（上文 P1-2 表格），
   并把 acceptance 从 “grep 0 `http.*` in routes” 扩展为：
   - `grep` 无 `queryKey: \["my(Services|Service)"\]`；
   - 所有 mutation invalidation 至少覆盖 list 前缀。

3. **新增 Phase F「事件流统一接入」**（P2）：
   - 后端 `/api/portal/events/ws` 按 user_id 过滤；
   - 前端 `shared/lib/events.ts` 封装 `useVMEvents` /
     `useNodeEvents`；
   - 剔除轮询，改为事件驱动 invalidate；
   - 未接入事件的列表页 `refetchInterval` 至少抬到 60s。

4. **Phase C 扩展**：
   - 抽出 `ClusterPicker` 组件，`admin/ha.tsx` /
     `admin/create-vm.tsx` / admin 监控等使用；
   - `ProjectPicker` 组件基于 `/admin/clusters/{c}/projects`，
     弃用枚举。

5. **Phase D 细化**：
   - `StatusBadge` 的映射表要与 `features/vms/api.ts` 的
     `extractIP` 等辅助一起挪到 `shared/components/status`，
     避免 `admin/vms.tsx` 重复实现。

6. **Phase E 追加测试**：
   - `features/vms/api.test.ts`：验证 P1-2 新 key 标准化后
     list/detail invalidation 正确传播；
   - `handler/portal/vm_test.go`：断言 `ListServices` /
     `GetService` 响应包含 `cluster`、`project` 字段；
   - 端到端（Playwright）：用户下单 → 支付 → 列表出现 →
     点击详情 → 看到信息 → reset password 成功。

### 后端完整性审查（pma-go 基线，2026-04-17 02:30）

用 Serena / 直接 grep 追溯所有 `handler/portal/*.go` 调用链，并把
`internal/` 目录结构对照 pma-go `references/baseline.md` /
`config-and-data.md` / `http-and-runtime.md`。以下是原 PLAN-011 未
覆盖但属于"后端应做的所有改造"的硬缺陷。

#### G-P0 — clusters 表成为悬空 FK（契约级不一致）

- `db/migrations/001_initial.sql:3` 定义 `clusters(id SERIAL PK,
  name UNIQUE, display_name, api_url, status, ...)`，并在
  `vms.cluster_id`、`orders.cluster_id`、`ip_pools.cluster_id`、
  `product_clusters.cluster_id` 四处作为 FK。
- 全仓 `grep -rn "FROM clusters"` / `INSERT INTO clusters` 命中
  **零次**：没有 repository、没有 sqlc 查询、没有启动注入。
- 运行时 `cluster.Manager`（`internal/cluster/manager.go`）仅从
  koanf 配置（`config.Clusters`）构建，键为 `name string`，**没有
  ID 概念**。
- 结果：
  1. `vms.cluster_id` 列所有行都是 `1`（原 P0-3 的根因），且无
     人维护 clusters 表；
  2. admin `clustermgmt.go` 动态添加集群只写内存 + 配置文件，不
     落表，进一步放大数据漂移；
  3. `product_clusters` 形同虚设，真正多集群售卖没法按 product
     选目的集群。
- **PLAN-011 需补的 Phase A.0 子任务**（二选一、写入方案决策）：
  - 方案 A（保留 DB 主导）：启动时 upsert config → clusters 表；
    新增 `repository/cluster.go` 提供 `GetByName / GetByID /
    UpsertFromConfig`；`cluster.Manager` 暴露 `IDByName(name)
    int64` 与 `NameByID(id) string`；重写 `findClusterName(mgr,
    clusterID)`；PortalVM / AdminVM / OrderPay 写库时都用真实 ID。
  - 方案 B（简化为 name-only）：migration 004 把 `vms.cluster_id
    INT` 改为 `vms.cluster_name TEXT NOT NULL`，删 clusters / 
    product_clusters 表，`model.VM.ClusterName` 取代
    `ClusterID`；所有 handler 改用 name。
  - 建议方案 A（多集群商业化必选），工作量含 goose 迁移
    004_seed_clusters.sql 与 cluster repository。

#### G-P0 — handler 直接返回 DB 模型 / 响应键不一致

- `GetService` 返回 `{"service": vm}`，但 `ListServices` 返回
  `{"services": [...]}`，前端 vm-detail 因期望 `{"vm": ...}` 彻底
  读不到（原 P0-1）。真正症结在：后端没有 **DTO 层**，每个 handler
  自己拍键名。
- pma-go `http-and-runtime.md` "keep response mapping consistent"
  要求响应映射一致。建议：
  - 新建 `internal/handler/dto/`（或就近 `handler/portal/dto.go`）；
  - 约定：列表用 `{"items": [...], "total": n}` 或资源复数名；
    单体用资源单数名；
  - `VMServiceDTO { ID, Name, ClusterName, ClusterDisplayName,
    Project, IP, Status, CPU, MemoryMB, DiskGB, OSImage, Node,
    CreatedAt }`（剔除 `password` 字段，仅创建/重置时一次性返回）；
  - 所有 `map[string]any{}` 写响应改为结构体 + json tags。
- 把该任务列入 **Phase A.0**，与前端 `features/vms/api.ts`
  类型对齐。

#### G-P1 — 敏感字段 `password` 长期驻留数据库并随列表返回

- `model.VM.Password *string`、`repository/vm.go` 所有 SELECT 都把
  `password` 拉出来，`ListByUser` → `ListServices` → 前端
  `VMService.password` 完整暴露。
- pma-go baseline 明确要求 "Secrets: never log secrets"；目前
  passwords 不仅返回到前端 JSON，还有机会进 slog（尽管没直接
  `slog.*password*`，但 JSON body 可能被中间件捕获）。
- 修法（应在 Phase A.0 一并完成）：
  - schema migration 004：`ALTER TABLE vms DROP COLUMN password;`
    或至少改成加密列；
  - 密码仅在 `CreateVMResult` / `ResetPasswordResult` 当次响应中
    返回；后续不再存 DB；
  - DTO 彻底剥掉 password。
- 该 P0 级安全问题**必须列入 PLAN-011**，不能延后。

#### G-P1 — 订单支付 → VM 供应无事务 / 幂等保障

- `handler/portal/order.go:106-215` `Pay`：
  1. `PayWithBalance` 扣钱；
  2. `allocateIP` 分配 IP；
  3. `vmSvc.Create` 调 Incus 建机；
  4. `vmRepo.Create` 落 DB；
  5. `UpdateStatus` 标记 active。
  任一步失败都只回写 order status，不回滚余额，不释放 IP，不销毁
  Incus 残留实例。例：VM 创建成功但 DB 插入失败 → Incus 多一个
  孤儿 VM + 用户扣了钱看不到。
- pma-go baseline 要求 "Translate internal errors to safe HTTP
  responses at the edge"；目前 handler 把 5 步编排原地抛 500。
- **应加入 PLAN-011 Phase A.0** 的后端子任务：
  - 把编排搬到 `service/order.go`（新）或扩展 `service/vm.go`；
  - 引入补偿动作：IP release、Incus instance delete、balance
    refund；
  - 用 context + sentinel error 表达可重试/不可重试分支。
- 或在 PLAN-011 之外另立新 plan（REFACTOR-002 事务一致性），但
  至少要在 A.0 里声明"本 plan 不修，待 X 处理"。

#### G-P1 — handler 违反 pma-go 分层

- `handler/portal/vm.go = 1029 行`，超过 pma-go "files usually
  under 800 lines"；并且同一文件同时装 portal `VMHandler` 与
  `AdminVMHandler`，违反 baseline "focused files"。
- `handler/portal/` 包同时挂 portal + admin 路由（grep
  `AdminRoutes`、`AdminVMHandler`、`AdminClusterMgmt*`），与
  `server/server.go:143 /api/admin` 分组冲突。
- pma-go 期望 `internal/handler/{portal,admin}`：
  - Phase C 已涉及前端 feature extraction，**应对称地在后端
    拆包**：
    - `handler/admin/vm.go`、`handler/admin/clustermgmt.go` …
    - `handler/portal/vm.go`、`handler/portal/order.go` …
  - `server/server.go` 的路由挂载不变，改 import 路径。
- 将该重构纳入 PLAN-011 Phase C（定名 Phase C-backend）。

#### G-P1 — business logic 沉淀在 handler，service 层过薄

- `service/` 仅 `vm.go` 与 `vm_test.go`。order/product/ticket/
  sshkey/audit/ippool/ceph/metrics/quota/console/events/nodeops
  全部**直接 handler → repository**。
- 后端 handler 层混入调度、IP 分配、审计、余额扣款、Incus 调用、
  cloud-init 构造，违反 pma-go "handlers manage transport;
  services manage business rules"。
- 修法（可分阶段）：
  - Phase A.0：先抽 `service/order.go`，把 Pay 编排从 handler
    移出（因其与 P0 紧耦合）；
  - Phase C-backend：对称抽 `service/ticket.go`、
    `service/sshkey.go`、`service/quota.go`；
  - Phase E：在 service 层加单元测试（repo mock）。

#### G-P1 — 未使用 `go-playground/validator`

- `handler/portal/validate.go = 9 行`，形同空文件；所有入参校验
  都手写 `if req.Foo == "" { ... }`。
- pma-go baseline 明确：`Validation: go-playground/validator`。
- 影响：B15（node-ops IP 校验）/ B16（Add Cluster URL 校验）都是
  QA-003 打补丁的手写校验。未来新增 request struct 会继续漂移。
- Phase A.0 一起做：
  1. 在 `internal/handler/portal`（或提到 handler/validate）加
     共享 `validator.New()`；
  2. 对 `CreateOrderReq`、`CreateVMReq`、`AddClusterReq` 等加
     tag（`validate:"required,hostname|ip"` 等）；
  3. 统一 400 错误封装 `{error, fields}`。

#### G-P2 — 事件流未按用户过滤（隐私/权限）

- `handler/portal/events.go` 仅挂 `AdminRoutes`；即便如此，
  `StreamEvents` 不按 project/user 过滤即订阅 Incus 全集群
  lifecycle（line 51-58）。
- 如果按 P1-5（前述）把 `/events/ws` 开放给 portal，必须：
  - 在 `events.go` 读取 user VMs（repo 查询）→ 构造白名单；
  - 从 Incus 接收帧后在服务端过滤再转发浏览器；
  - 事件元数据校验（防伪造 vm_id）。
- Phase F 应包含该后端子任务。

#### G-P2 — `h.clusters.List()[0]` 惯用法散落 8 处

- grep 结果：
  - `handler/portal/order.go:139` Pay 选第一个集群；
  - `handler/portal/events.go:39` 默认第一个集群；
  - `handler/portal/vm.go:221` 类似；
  - `internal/server/server.go` 启动日志；
  - 多处 admin handler fallback。
- 后果：多集群部署退化为单集群，且 clusters map 无序，随进程重启
  可能变。
- 修法：
  - `cluster.Manager` 增加 `Primary() *Client`（按 config 顺序）
    或 `FindByProductID(pid)`；
  - 调用方显式声明选择策略，禁止 `List()[0]`。
- Phase A.0 或独立小 task 完成。

#### G-P2 — pma-go 数据访问层偏离

- 仓库使用 `database/sql` + 手写 SQL + 位置参数，**既没有 sqlc
  也没有 GORM**。
- pma-go baseline 列的是 "sqlc + pgx"（required default）或
  GORM（alternative）。当前选型介于两者之间，且无编译期安全。
- 不必纳入 PLAN-011，但应在 plan risks 里声明"后端 data access
  不符合 pma-go default，另立 REFACTOR-002"。

#### G-P2 — koanf 配置与 env 分散

- `grep os.Getenv` 未彻查，但 pma-go 要求 "never read process env
  directly inside domain logic"。Phase A.0 顺便在
  `internal/config` 统一所有 env 入口。

#### 修订后的 PLAN-011 结构建议

原 5 阶段扩为 7 阶段：

| 阶段 | 范围 | 层 |
|------|------|----|
| **Phase A.0 (新)** | 后端契约修复 | Go |
| Phase A | 前端数据层钩子化 + 缓存键标准化 | TS |
| Phase B | 前端 i18n 覆盖 | TS |
| Phase C-frontend | 前端组件从 route 抽到 features | TS |
| **Phase C-backend (新)** | handler/portal ↔ handler/admin 拆包 + service 抽取 | Go |
| Phase D | 前端 UI primitives + style drift | TS |
| **Phase F (新)** | 后端 `/api/portal/events/ws` + 前端事件 hooks + 轮询削减 | Go + TS |
| Phase E | 测试 + CI | Go + TS |

Phase A.0 具体交付物清单：
1. migration `004_drop_vm_password.sql` + repo 移除 password 列；
2. migration `005_seed_clusters.sql`（方案 A）或 `005_vm_cluster_name.sql`（方案 B）；
3. `internal/repository/cluster.go`（方案 A）；
4. `cluster.Manager` 增加 ID↔Name 映射；
5. `internal/handler/portal/dto.go`（VMServiceDTO、OrderDTO 等）；
6. `handler/portal/vm.go` 的 `ListServices` / `GetService` /
   `CreateService` / `Pay` 迁移到 DTO；
7. `findClusterName(mgr, clusterID)` 实现正确映射；
8. 删除 3 处 `ClusterID: 1` 硬编码，改用真实 ID；
9. `service/order.go` 编排 Pay → IP → VM → DB，带补偿；
10. `go-playground/validator` 引入与 `CreateOrderReq` /
    `AddClusterReq` / `NodeOpsReq` 的 tag 化；
11. 对应单元测试：`service/order_test.go`、
    `repository/cluster_test.go`、`handler/portal/vm_test.go`
    断言响应含 cluster_name / project 字段。

### 自查（三轮）

**二轮自查 2026-04-17 02:00（前端 / User Journey）**

- ✅ 覆盖所有 sidebar 6 + 17 条路径的核心查询键；
- ✅ 覆盖 WebSocket 通道（只有一条，仅 admin 使用）；
- ✅ 追溯 Portal VM 生命周期 UI → hook → http → Go handler →
  cluster manager；
- ✅ 审计 `refetchInterval` 全 20 处；
- ✅ 对比 `admin/create-vm` 与 `features/vms/api.ts` 的重复逻辑；
- ⚠️ 尚未执行：Ceph OSD out/in → VM 可用性链路、SSH key inject
  → VM cloud-init 注入细节。原因：前者是只读只观察，后者 Phase
  A.0 改造时会自然串起。

**三轮自查 2026-04-17 02:30（后端 / pma-go 基线对齐）**

- ✅ 对齐 pma-go baseline 的"文件/分层/错误/日志/验证/配置/数据
  访问"七项，发现 G-P0×2、G-P1×5、G-P2×3；
- ✅ 追溯 clusters 表 → FK 引用 → Manager → scheduler 全链路，
  证实运行时与 schema 脱钩；
- ✅ 追溯订单支付 → IP 分配 → VM 创建 → DB 插入 → 审计五步链，
  确认无事务 / 无补偿；
- ✅ grep 确认 `database/sql` 手写 SQL、未使用 sqlc / GORM /
  go-playground/validator；
- ✅ 确认 `h.clusters.List()[0]` 在 8 处构成多集群退化；
- ⚠️ 未做：admin/ceph、admin/ip-pools handler 的 service 层抽取
  细节、`cmd/server/main.go` 是否通过 goose 驱动 migration。
  两项归 REFACTOR-002，PLAN-011 仅在 Risks 声明"data access 
  选型偏离 pma-go default，另立 plan 处理"即可。

### 实施进度

- **Phase A.0（后端契约）— 核心闭环已完成 (2026-04-17)**
  - ✅ `model.Cluster` + `ClusterRepo.Upsert/GetByName/GetByID/List`
  - ✅ `cluster.Manager` 增加 `idByName` / `nameByID` 映射表，
    `SetID` / `IDByName` / `NameByID` / `DisplayNameByName` 方法
  - ✅ `cmd/server/main.go` 启动时 `Upsert` 每个配置集群、
    绑定 DB id ↔ 名称
  - ✅ `handler/portal/dto.go` 新增 `VMServiceDTO`（**省略 password
    字段**）、`NewVMServiceDTO` / `NewVMServiceDTOList` 构建器
  - ✅ `ListServices` / `GetService` 输出 `{"vms": [...]}` / `{"vm": ...}`
    信封（替代 `services` / `service`）
  - ✅ `findClusterName` 改为走 `Manager.NameByID(id)` + 回退，修复多集群
    VM 动作路由走错集群的 P0
  - ✅ 移除 3 处 `ClusterID: 1` 硬编码（vm.go CreateService、
    AdminVMHandler.CreateVM、order.go Pay）
  - ✅ `dto_test.go` 覆盖 password 脱敏、cluster 解析、项目回退、
    IP 指针解引、批量顺序
  - 🟡 延后：`migration 004 drop vms.password` 需先迁 
    `vm_credentials` 备份；`go-playground/validator` 可在 Phase
    C-backend 一起引入；`service/order.go` 支付编排与补偿抽取并入
    Phase C-backend。

- **Phase A（前端数据层）— 已完成 VM 钩子 + 契约对齐 (2026-04-17)**
  - ✅ `features/vms/api.ts` 新增 `vmKeys` 前缀键
    (`["vm", "list", ...]` / `["vm", "detail", id]`) 并统一
    mutation invalidation；新增 `useMyVMDetailQuery`
  - ✅ `VMService` 接口补 `cluster / cluster_display_name / project /
    updated_at`、**移除 password 字段**
  - ✅ `routes/vms.tsx` 改用 `useMyVMsQuery` / `useVMActionMutation`，
    删除内联 inline interface，Console / SnapshotPanel 的 cluster /
    project 从 DTO 读取，不再硬编码 `cn-sz-01 / customers`
  - ✅ `routes/vm-detail.tsx` 改用 `useMyVMDetailQuery` /
    `useVMActionMutation`，Console / SnapshotPanel 同样走动态值
  - ✅ `routes/index.tsx` `vmsData.services` → `vmsData.vms`
  - ✅ `bunx tsc --noEmit` + `vite build` 通过
  - ✅ **Phase A 全量迁移完成 (2026-04-17)** — 所有 admin / portal 路由
    不再直调 `http.*`，统一消费 feature 钩子：
    - `features/clusters/api.ts` 扩展 `clusterKeys.all`、增加
      `useEvacuateNodeMutation` / `useRestoreNodeMutation` /
      `useAddClusterMutation`；
    - `features/vms/api.ts` 增加 `ClusterVMsResponse` / `AdminCreateVMParams`
      / `useMigrateVMMutation` / `useAdminCreateVMMutation` /
      `useResetVMPasswordMutation`，`useClusterVMsQuery` 增 enabled 守卫；
    - `features/monitoring/api.ts` 增 `monitoringKeys` + `useHealthQuery`；
    - `features/billing/api.ts` 增 `AdminOrder` / `AdminInvoice` +
      `useAdminOrdersQuery` / `useAdminInvoicesQuery`；
    - `features/products/api.ts` 规范 `Product` / `ProductFormData` +
      `useAdminProductsQuery` / `useCreate|UpdateProductMutation`；
    - 新建 `features/ip-pools/api.ts`、`features/audit-logs/api.ts`、
      `features/nodes/api.ts`、`features/storage/api.ts`，覆盖
      IP Pools / IP Registry / Audit Logs / Nodes / HA / SSH /
      Ceph status / OSD tree / Pools / OSD in-out；
    - 涉及路由：`admin/{clusters, vms, vm-detail, create-vm, tickets,
      orders, invoices, ip-pools, ip-registry, audit-logs, ha, nodes,
      node-ops, node-join, products, storage, monitoring}` +
      portal `{index, vm-detail}`；
    - 顺便修复 `monitoring` `UsageBadge` 与 `storage` `HEALTH_WARN`
      的颜色漂移 (`yellow-500/20 text-yellow-600` /
      `text-yellow-500` → `bg-warning/20 text-warning` /
      `text-warning`)；
    - `bunx tsc --noEmit` + `bunx vite build` 通过；
    - `rg "http\.(get|post|put|delete)" src/app/routes` 返回 0 条。

### 第四轮审查 2026-04-17 04:10（Go 后端完整性追溯）

用 Serena/graph 追溯 `incus-admin/internal/` 全部 handler/service/repo/
migrations + `cmd/server/main.go`，对照 pma-go baseline
与原 PLAN-011 A.0/C-backend/F 的承诺清单逐项校验。结论：原 plan
**方向正确但覆盖面存在遗漏/延后未标记**，需要在 C-backend 与 F 中
补齐。以下按发现严重度列出。

#### A.0 已声明但未交付的残留项

| # | 项 | 位置/证据 | 影响 |
|---|---|---|---|
| A.0-R1 | `OrderHandler.Create` 仍硬编码 `clusterID := int64(1)` | `handler/portal/order.go:95` | Phase A.0 已声明"移除 3 处 ClusterID:1"，实际只改了 `Pay`、portal `CreateService`、admin `CreateVM`；**订单落库的 cluster_id 仍为 1**，多集群下订单历史与 VM 真实位置脱钩 |
| A.0-R2 | `vms.password` 列仍在 schema，`model.VM.Password *string` 带 `json:"password,omitempty"` | `db/migrations/001_initial.sql:91`, `model/models.go:41`, `handler/portal/vm.go:108` `UpdatePassword` 每次 reset 回写 | DTO 虽已脱敏，但 DB dump / 备份 / `ResetPassword` 仍持久化明文密码；migration 004 + `vm_credentials` 中间表未实施 |
| A.0-R3 | `go-playground/validator` 未引入 | `handler/portal/validate.go` 仅 9 行正则；全仓 `grep validator.` = 0 | A.0 清单 #10 未实施；新 request struct 仍手写 `if req.X == ""` |
| A.0-R4 | `service/order.go` 支付编排 + 补偿未抽 | `handler/portal/order.go:106-219` `Pay` 仍在 handler 原地 5 步编排（扣款 → allocateIP → vmSvc.Create → vmRepo.Create → UpdateStatus），失败仅 `slog.Error`，**无余额回滚 / IP 释放 / VM 销毁** | A.0 清单 #9 未实施；订单一致性风险长期挂账 |

#### B — 原 plan 未充分覆盖的 Go 后端缺口

**B-1. DTO 覆盖面仅 VMServiceDTO，其余资源全部裸返 `model.*`**

- `handler/portal/` 27 处 `writeJSON(w, ..., map[string]any{"<resource>": <model>})` 模式（grep 已枚举）；
- `orders` / `invoices` / `tickets` / `products` / `users` / `nodes` / `pools` / `logs` / `ip_addresses` / `osd_tree` 全部直接暴露 DB 列（`user_id`/`cluster_id`/`order_id` 等 FK 数字）；
- admin VM list (`AdminVMHandler.ListClusterVMs` / `ListAllVMs` / `AdminVMHandler.CreateVM`) 返回 raw `json.RawMessage`（Incus instance 原生 payload），前端 `IncusInstance` 不得不 `vm.project || "customers"` fallback（`admin/vms.tsx:118`）；
- 原 plan A.0 清单 #5 仅列 `VMServiceDTO + OrderDTO`，**缺口**：`OrderDTO / InvoiceDTO / AuditLogDTO / TicketDTO / ProductDTO / UserDTO（admin vs portal 两态）/ NodeDTO / IPPoolDTO / IPAddressDTO / AdminVMDTO / CephHealthDTO`。

**B-2. handler 包未按 pma-go 分层**

- `handler/portal/` 目录下 27 个文件同时承载 portal 与 admin 两类 handler：
  - `vm.go` 1052 行（> pma-go 建议 800 行阈值），`VMHandler` 与 `AdminVMHandler` 共用文件；
  - `ProductHandler / TicketHandler / OrderHandler / InvoiceHandler / QuotasHandler / MetricsHandler / SnapshotHandler` 通过 `PortalRoutes()` / `AdminRoutes()` 方法复用同一类型；
  - `EventsHandler / IPPoolHandler / AuditHandler / ClusterMgmtHandler / CephHandler / NodeOpsHandler / UserHandler` 仅挂 admin 但坐落于 `portal` 包；
- pma-go baseline "focused files / package boundaries" 要求 `handler/{portal,admin}` 分目录。
- 原 plan C-backend 已列此目标，但**未列 admin handler 清单 + 目标包路径 + 迁移步骤**。

**B-3. service 层单薄**

- `internal/service/` 仅 `vm.go` + `vm_test.go`；
- handler 内部沉积业务逻辑：
  - `OrderHandler.Pay` 编排 Pay + IP + VM + DB（见 A.0-R4）；
  - `TicketHandler.Reply` 直接 repo 写消息 + 改 ticket.status；
  - `SSHKeyHandler.Create` 手写 fingerprint + 指纹校验；
  - `CephHandler` 逐条 HTTP 调用 Ceph REST；
  - `NodeOpsHandler` 执行远程 SSH；
  - `QuotasHandler` 直接 repo；
- 原 plan 仅声明抽 `service/order.go`，**缺口**：`service/{order,ticket,sshkey,ceph,nodeops,quota,billing}.go`。

**B-4. migration 无自动驱动**

- `db/migrations/{001,002,003}.sql` 手写裸 SQL；
- `cmd/server/main.go` 全仓 `grep "migrate|goose"` = 0，启动时不跑 migrations；
- `Taskfile.yml` 无 `task migrate` 条目；
- pma-go baseline "migrations are first-class (goose / golang-migrate / Atlas)" 偏离。
- 原 plan **完全未提 migration 框架**。建议新增 Phase G 或并入 C-backend。

**B-5. 事件流 portal 端未提供，且无用户过滤**

- `handler/portal/events.go:31 EventsHandler.AdminRoutes` 仅挂 `/admin/events/ws`，portal 用户无路径；
- `StreamEvents` 透传整个 Incus lifecycle（`eventTypes = "lifecycle,operation"`）不按 user/vm 过滤；
- 原 plan F **仅提 "新增 /api/portal/events/ws + 按 user 过滤"**，缺清单：
  1. 新 handler 文件 `handler/portal/portal_events.go`（或迁 `handler/portal/events.go` + 新增 `handler/admin/events.go` 后对称新增 portal 版本）；
  2. 用户 → VM 白名单 repo query：`vmRepo.NamesByUser(ctx, userID) []string`；
  3. 服务端帧过滤 + 伪造校验（事件 source 的 `project` / `instance name` ∈ 白名单）；
  4. 前端 `shared/lib/events.ts` 封装 `useVMEvents({ userVMs })`，不再逐集群硬连。

**B-6. 前端契约闭环仍漏 4 处**

即便 Phase A 已迁 hooks，以下硬编码仍在（未被 VMServiceDTO 覆盖）：

- `app/routes/console.tsx:9` `project: (search.project as string) || "customers"`；
- `app/routes/admin/vm-detail.tsx:19` 同上 fallback；
- `app/routes/admin/create-vm.tsx:30,104` `useState("customers")` + `<option value="customers">`；
- `app/routes/admin/vms.tsx:118` `const project = vm.project || "customers"`（因 admin list 无 AdminVMDTO）。

根因在 **B-1（AdminVMDTO 缺失）+ B-2（admin handler 未分包导致 project 字段拍不到 DTO）**。

**B-7. `h.clusters.List()[0]` 在 8 处 fallback**

- `order.go:90,133` / `events.go:39` / `vm.go:216` / `AdminVMHandler.ChangeVMState:745` / `DeleteVM:788` / `server/server.go` 启动日志 / 监控；
- `cluster.Manager` 已有 `IDByName / NameByID / SetID`，但**没有 `Primary()` / `FindByProductID()`**，调用方仍沿用 `List()[0]`；
- 原 plan A.0 提过"cluster.Manager 增加 ID↔Name 映射"已做，但 `Primary()` 建议未做。

**B-8. 响应信封不统一**

- list 资源：`{"orders":[...]}` / `{"logs":[...], "total": n}` / `{"vms":[], "count": 0, "stale": true}` 三种信封；
- 单体：`{"order": o}` / `{"vm": v}` / `{"ticket": t, "messages": [...]}` 混合；
- 错误：有的 `map[string]any{"error": "..."}`、有的 `http.Error`；
- pma-go "consistent response mapping" 偏离。建议 C-backend 内统一约定 list 用 `{items, total, page?}` 或资源复数名 + 单体用资源单数名。

#### 对 PLAN-011 的修订建议

1. **Phase A.0 标记"部分完成 + 残留 4 项"**：
   - A.0-R1 `order.go:95 clusterID=1` 单独挂到 C-backend 起手的"断点修复"；
   - A.0-R2 `migration 004 drop password` + `vm_credentials(vm_id, encrypted_password, created_at)` 走独立安全 PR，需要先生产数据备份；
   - A.0-R3 / A.0-R4 并入 C-backend。

2. **Phase C-backend 完整交付物清单**（替换原粗粒度描述）：
   - **目录拆分**：新建 `internal/handler/admin/` 与 `internal/handler/portal/` 双目录，按归属迁移：
     - `handler/admin/`: `vm.go`（拆 `AdminVMHandler`）、`clustermgmt.go`、`ceph.go`、`nodeops.go`、`ippool.go`、`audit.go`、`users.go`、`events.go`、`orders_admin.go`、`invoices_admin.go`、`tickets_admin.go`、`products_admin.go`、`metrics_admin.go`
     - `handler/portal/`: `vm.go`、`order.go`、`ticket.go`、`invoice.go`、`product.go`、`snapshot.go`、`sshkey.go`、`apitoken.go`、`quota.go`、`metrics.go`、`console.go`、`portal_events.go`（新）
   - **DTO 清单**（新建 `handler/dto/`）：
     - `VMServiceDTO`（已存在，迁目录）
     - `AdminVMDTO`（新，替代 raw `json.RawMessage`；含 `cluster`, `project`, `node`, `user_email`）
     - `OrderDTO`（+ `cluster`, `product_name`, `user_email?`）
     - `InvoiceDTO`（+ `cluster`, `product_name`）
     - `TicketDTO` / `TicketMessageDTO`（+ `user_email`, `user_name`）
     - `AuditLogDTO`（+ `user_email`, `target_name`）
     - `UserDTO`（portal 仅 id/email/name/role；admin +balance）
     - `NodeDTO`、`IPPoolDTO`、`IPAddressDTO`（+ `cluster`, `vm_name`）
     - `CephHealthDTO`、`ClusterDTO`（+display_name）
   - **service 层抽取**：`service/{order,ticket,sshkey,ceph,nodeops,quota}.go`；`order.go` 包含 `Pay` 的事务 + 补偿（sentinel error `ErrPayRefund/ErrIPRelease/ErrVMDestroy`）；
   - **validator 引入**：新增 `internal/middleware/validate.go` 暴露共享 `*validator.Validate`；12 个 request struct 打 tag；400 错误统一封装 `{error, fields: {field: msg}}`；
   - **断点修复**：`order.go:95` clusterID=1；8 处 `List()[0]` → `Manager.Primary()` 或显式选择。
   - **Acceptance**：
     - `grep -rn "^func.*Admin.*" handler/portal/` = 0；
     - `grep -rn "map\[string\]any{\"" handler/` = 0（用 DTO 取代）；
     - 单元测试 `service/order_test.go` 覆盖 3 条补偿路径。

3. **Phase F 完整交付物清单**（替换原描述）：
   - **后端**：
     - `handler/portal/portal_events.go` 挂 `/api/portal/events/ws`（PortalRoutes）；
     - 新增 `repository/vm.go` 的 `NamesByUser(ctx, userID) ([]string, error)`（若无）；
     - 在 `portal_events.StreamEvents` 内订阅所有配置集群，帧过滤 `project + instance name ∈ userVMNames`；
     - 伪造防护：source 的 `project` 必须 ∈ user 可见项目集；
   - **前端**：
     - `shared/lib/events.ts` 封装 `useVMEvents(userVMs)` / `useNodeEvents(clusterName)` / `useOperationEvents(target)`；
     - 路由集成：`routes/vms.tsx` / `routes/vm-detail.tsx` / `admin/vms.tsx` / `admin/nodes.tsx` / `admin/monitoring.tsx` 订阅事件 → `queryClient.setQueryData/invalidateQueries`；
     - 拆除/降低 `refetchInterval` 表：
       | 文件 | 当前 | 目标 |
       |---|---|---|
       | `features/vms/api.ts` myList | 15s | 60s + 事件驱动 |
       | `features/vms/api.ts` clusterList | 10s | 60s + 事件驱动 |
       | `features/nodes/api.ts` list/detail | 15s | 60s + 事件驱动 |
       | `features/audit-logs/api.ts` | 15s | 60s（非实时） |
       | `features/tickets/api.ts` myTickets | 15s | `refetchOnWindowFocus` |
       | `features/billing/api.ts` | 15s/30s | 60s（列表）/ 事件（支付后） |
       | `features/monitoring/*` | 30s | 保留（Prom 采样周期本就 15-30s） |
   - **Acceptance**：admin 首页 15 分钟峰值 QPS ≤ 3 req/15s（当前 ≈ 10+）。

4. **Phase G（新增，P2）— 基础设施对齐 pma-go**：
   - 引入 goose 或 golang-migrate；`cmd/server/main.go` 启动时 `Up()`；Taskfile 新增 `task migrate:up|down|status`；
   - 评估 sqlc + pgx 迁移（单独 plan，不在 PLAN-011 范围内声明即可）；
   - 统一 env 入口 `internal/config`，禁止 handler/service 内 `os.Getenv`。

5. **Phase E 测试清单追加**：
   - `service/order_test.go` — Pay 补偿 3 路径（余额/IP/VM）；
   - `repository/cluster_test.go` — `Upsert` 幂等 + ID 回绑；
   - `handler/portal/vm_test.go` — `ListServices` / `GetService` 断言 cluster/project 字段（已部分有，需补 admin list DTO 版）；
   - `handler/admin/events_test.go` — 帧过滤白名单伪造测试（需先完成 F 后端部分）；
   - `handler/portal/validate_test.go` — validator tag 断言。

6. **Risks 追加**：
   - **C-backend 工作量**：handler 拆包 + DTO 迁移 + service 抽取 + validator 引入 + 断点修复，保守估计 2-3 周；建议拆 3 个 PR（包分层 → DTO → service）；
   - **A.0-R2 drop password 的生产数据路径**：需先落 `vm_credentials(vm_id, password_encrypted TEXT, created_at TIMESTAMPTZ)` → 回填 → 观测 1 周 → drop `vms.password`；加密方案必须先确定（推荐 AES-GCM，密钥走 `config.Auth.EncryptionKey`）；
   - **B-4 引入 goose**：首次启动时生产库已有 001-003 应用过的数据，需走 `goose up -no-versioning` 或预置 `goose_db_version` 行，避免重跑；
   - **B-5 事件流白名单**：若用户 VM 数量 > 100，每次订阅构造白名单会慢；需要 repo 增加 `NamesByUser` 索引（已有 `idx_vms_user_id`，可复用）。

### 第四轮自查结论

- ✅ 后端完整性追溯覆盖 27 个 handler 文件 + 12 个 repo + 1 个 service + 3 个 migration + `server.go` 路由挂载；
- ✅ 追溯 Phase A.0 的 10 项清单，发现 **已交付 6 项 + 残留 4 项**；
- ✅ 追溯 Pay / Create / Reset / Reinstall 4 条关键调用链，确认无事务 + password 持久化 + 信封不统一；
- ✅ 对比前端硬编码 `customers/cn-sz-01` 残留 4 处，证实根因在后端 AdminVMDTO 缺失；
- ✅ 证实 C-backend 需要显式列出目录迁移清单 + DTO 清单 + service 清单，而非粗粒度"handler 分包 + service 抽取"；
- ⚠️ 未做：Ceph / IPPool 的一致性（Ceph REST API 调用失败如何补偿），归入独立 REFACTOR plan；
- ⚠️ 未做：前端 ClusterPicker / ProjectPicker 组件的具体接口契约（归 Phase C-frontend）。

综上，**PLAN-011 方向正确，但 C-backend 与 F 的"交付物级"颗粒度不足**；按本节修订建议补齐后，前后端契约闭环可完整覆盖：
> user journey → TanStack Query key → feature hook → http → server.go route → handler (portal/admin) → service → repository / cluster.Manager → DTO → JSON → feature hook typing → UI。

### 风险补遗

- **Phase A.0 工作量被低估**：合并后端 DTO、password 脱库、
  cluster ID 体系、订单事务四件事，大概率需独立 PR，与前端
  Phase A 解耦；若同 PR 会阻塞前端进度。建议 Phase A.0 单独拉
  backend-only PR，前端 Phase A 基于 A.0 的响应契约（可先 mock）
  并行。
- **migration 004（drop password）的生产回滚路径**：现网
  `vms.password` 列有真实数据，如需保留历史密码需先迁到
  `vm_credentials(vm_id, encrypted_password, created_at)` 表，
  再做 drop，避免不可逆数据丢失。
- **方案 A（clusters 表主导）影响面**：一旦 `vms.cluster_id`
  改用真实 ID，所有历史 `cluster_id=1` 行要有数据修复脚本核对
  真实集群名，避免错映射。脚本需包含人工确认步骤。

### 第五轮审查 2026-04-17 05:00（User Journey 深度追溯）

> 以「用户旅程」为主线，通过 Serena 符号读取 + code-review-graph
> 依赖图 + 代码阅读，按 **Portal (P1/P2/P3) + Admin (A1/A2/A3/A4)**
> 共 7 条旅程追溯前后端调用链，聚焦：功能一致性、业务闭环、正确
> 性、性能瓶颈。结果整理成 8 大类 47 项新发现，已与前四轮去重。

#### J-P1 Portal 订单→支付→开通 旅程

| 代号 | 风险 | 具体位置 | 说明 |
|------|------|----------|------|
| J-P1.1 | P0 | `handler/portal/order.go:95` | `Pay` 中 `clusterID := int64(1)` 仍为硬编码 → 用户看似付款成功，实际永远绑定到 id=1 集群。与 A.0-R1 同源但未修复，需列入 Phase A.0。 |
| J-P1.2 | P0 | `order.go:106-219` | 5 步非事务编排（`PayWithBalance → AllocateIP → vmSvc.Create → UpdateStatus → vmRepo.Create`）。`vmRepo.Create` 失败仅 `slog.Error`，Incus 里已有 VM 但 DB 中不可见 → 用户付了钱、扣了配额、占了 IP，却在 portal 看不到 VM（孤儿资源）。需列入 Phase A.0，至少加 compensation：`vmRepo.Create` 失败时删除 Incus VM + 释放 IP + 退款。 |
| J-P1.3 | P1 | `order.go:183-184` | 失败路径写 `HTTP 200 + {"status":"paid","error":"..."}` → 前端 `usePayOrderMutation.onSuccess` 误判为成功，会刷新订单但留下失败订单。应返回 4xx/5xx + RFC 7807 envelope。 |
| J-P1.4 | P1 | `web/src/app/routes/billing.tsx:147-159` | 下单链 `orderMutation.mutate → onSuccess → payMutation.mutate(orderId)` 顺序调用。用户在 `orderMutation` 成功、`payMutation` 触发之间导航离开 → 产生 `status=pending` 的孤儿订单；backend 无 cron 清理。Phase C 应合并为 `/portal/orders/checkout` 原子接口。 |
| J-P1.5 | P2 | `billing.tsx:20-25` | `OS_IMAGES` 硬编码在前端，后端 `product.go` 的 `os_image_default` 字段被忽略。换镜像需发版前端。Phase C（产品契约）应暴露 `GET /portal/catalog/os-images`。 |
| J-P1.6 | P2 | `billing.tsx:170` / `admin/products.tsx:100` / `admin/invoices.tsx:66` | 价格统一用 `$` 前缀 + `.toFixed(2)`，中文 locale 应显示 `¥`。需要引入 `currency` 字段到 `ProductDTO` / `InvoiceDTO`，前端按 `Intl.NumberFormat` 本地化。 |
| J-P1.7 | P2 | `order.go` `Create` 与 `Pay` | 未做幂等：同一 `product_id` 可连续下单生成多条 `status=pending` 行，前端只要连点两次"立即购买"即可。后端需 `Idempotency-Key` 头或 DB 唯一键 `(user_id, product_id, status=pending)`。 |
| J-P1.8 | P2 | `ipallocator.go:34` | `allocateIP` 永远用 `cc.IPPools[0]`，多池场景无法负载均衡或按 VLAN 分池。Phase A.0 补"可选 pool_id"。 |

#### J-P2 Portal VM 生命周期 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-P2.1 | P0 | `web/src/features/snapshots/snapshot-panel.tsx:43,49` | `deleteMutation` / `restoreMutation` 硬编码 `/admin/vms/${vmName}/snapshots/${snap}`，即使 `apiBase="/portal"`。Portal 用户点"恢复/删除快照"必然 403。生产 Bug，需紧急修复：按 `apiBase` 拼前缀。 |
| J-P2.2 | P1 | `handler/portal/vm.go:36-42` | `VMHandler.Routes` 未注册 `CreateService`（第 187 行）。这是 **死代码**，与 order-driven flow 并存。建议删除 `CreateService` 以避免误导（同源 J-P1.4）。 |
| J-P2.3 | P1 | `vm.go:60,93,169,171` + `metrics.go:129` | 多处 `project = "customers"` 硬编码。P2 Portal VM 若未来支持 `project=default` 或多租户，这些路径都漏。Phase A 应引入 `UserScope.DefaultProject` 常量 + 迁移。 |
| J-P2.4 | P2 | `web/src/features/vms/api.ts:68-74` | `useMyVMDetailQuery` 无 `refetchInterval`。用户在详情页等 VM 状态变化（如 reset-password 后）需手动刷新。建议加 10s 轮询或 WebSocket 订阅。 |
| J-P2.5 | P2 | `api.ts:165-171` | `useResetVMPasswordMutation.onSuccess` 只 invalidate `vmKeys.myDetail(vmId)`，不动列表。列表若显示「凭据已重置」badge 会错过。按约定应 invalidate `vmKeys.all`。 |
| J-P2.6 | P2 | `web/src/app/routes/vm-detail.tsx:106` | `Username: "ubuntu"` 硬编码。后端 `reinstall` 返回 `username`，但详情页不展示。需要 `VMServiceDTO.username` 字段 + DB 列。 |
| J-P2.7 | P3 | `vm-detail.tsx:77` | Console 链接明文拼 `cluster` + `project`，与 PLAN-011 的「URL 去暴露 cluster/project」冲突；Phase C 前端需改成传 `vm_id`，Console handler 再内部解析。 |

#### J-P3 Portal Tickets / SSH-Key / API-Token / Settings 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-P3.1 | P1 | `handler/portal/ticket.go:107` Reply | 未检查 ticket 状态 → 用户可向 `status=closed` 工单继续回复（前端 UI 也允许，因为 close 按钮后 ticket 还可输入）。需后端返 409 + 前端禁用输入框。 |
| J-P3.2 | P1 | `ticket.go:155` UpdateStatus | 无枚举白名单。前端虽只传 `open/closed`，但 API 允许任意字符串 → 脏数据进 DB。需 `validate.In("open","closed","answered","pending")`。 |
| J-P3.3 | P2 | `sshkey.go:100` | `sshFingerprint` 用 **MD5**（旧格式 `xx:xx:...`），与 OpenSSH `ssh-keygen -lf` 默认 **SHA256** 不一致。用户核对指纹会困惑。需改为 SHA256 base64（`golang.org/x/crypto/ssh.FingerprintSHA256`）。 |
| J-P3.4 | P2 | `apitoken.go:52` | `h.repo.Create(ctx, userID, name, nil)` — expires_at 永远 nil → **令牌永不过期**。前端 UI 也没暴露过期选项。需 Phase C 同时补后端 + 前端。 |
| J-P3.5 | P2 | `web/src/app/routes/settings.tsx` | Settings 页仅展示身份信息，缺「修改密码 / 通知偏好 / 2FA / 账单邮箱」。Phase C 确定 Settings 最小功能集。 |
| J-P3.6 | P3 | `ticket.go` Create | `body` 字段未设 `required`，允许创建空正文工单。前端虽检查，但 API 绕过可通。需后端校验。 |

#### J-A1 Admin Cluster / Node / HA 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-A1.1 | P0 | `web/src/features/nodes/api.ts` vs `features/clusters/api.ts` | `useNodeEvacuateMutation` 打 `/admin/nodes/{node}/evacuate?cluster=X`（clustermgmt.go:193），`useEvacuateNodeMutation` 打 `/admin/clusters/{cluster}/nodes/{node}/evacuate`（vm.go AdminVMHandler）。**两条路径都在后端注册且实现并存**。若两个入口语义漂移（如只有 A 加了审计、B 没加），将难以排查。Phase C-backend 必须砍掉其中一个（推荐保留 `/admin/clusters/{cluster}/nodes/{node}/evacuate` RESTful 风格）。 |
| J-A1.2 | P1 | `web/src/app/routes/admin/ha.tsx:19` | `const clusterName = clusters[0]?.name ?? ""`。**只显示第一个集群**的 HA 状态，多集群用户看不到其他集群。需加 cluster picker。 |
| J-A1.3 | P1 | `web/src/app/routes/admin/clusters.tsx` AddClusterForm | 只有 `name + api_url` 校验。后端支持 `ca_file` 字段但前端无输入框 → 只能接受 insecure 连接，或手工改 DB。Phase C 前端表单要加 `ca_file` + `trust_password`。 |
| J-A1.4 | P2 | `web/src/app/routes/admin/node-join.tsx` | Wizard 在 step 3 通过 SSH `incus cluster add ${nodeName}` 生成 token，**未转义 nodeName**。若用户输入 `n1; rm -rf /`，将执行任意命令。需在 `sshexec.Runner` 层做 shell 参数安全转义，或改用 `exec.Command` 数组参数形式。 |
| J-A1.5 | P2 | `clustermgmt.go:193` Evacuate / Restore | 调用 `client.EvacuateMember` 直接推给 Incus，**没有 audit 日志 + 没有前端进度流**（长操作可能 >1 分钟）。Phase B 引入 SSE/WS 进度后应把这里串上。 |

#### J-A2 Admin VM CRUD / Migrate / Reinstall 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-A2.1 | P1 | `web/src/app/routes/admin/vms.tsx:118` | `const project = vm.project || "customers"` — 当 `vm.project` 为空字符串或 undefined 时 fallback 到 "customers"。非 customers project 的 VM 会被错路由。需后端 DTO 强制返回真实 project，前端删 fallback。 |
| J-A2.2 | P1 | `web/src/app/routes/admin/vm-detail.tsx:33-34` | 存在性检查靠 `useClusterVMsQuery` 拉全量再 O(N) 扫描。集群有 500 VM 时，每次进详情页要传 500 条 JSON。需后端补 `GET /admin/clusters/{cluster}/vms/{name}` 单条接口。 |
| J-A2.3 | P1 | `admin/vm-detail.tsx:133-139` Migrate | `target_node` 是自由文本输入，无下拉。用户可输入不存在/非本集群节点，后端才 404。需前端 `useClusterNodesQuery(clusterName)` 渲染 select。 |
| J-A2.4 | P2 | `handler/portal/vm.go` `MigrateVM:897` | 后端也未校验 `target_node` 是否在本集群，依赖 Incus 返回错误。应先过 `cluster.Members()` 白名单。 |
| J-A2.5 | P2 | `admin/vm-detail.tsx` Reset Password | 点击后只 toast 显示密码。无"复制到剪贴板"按钮，密码会被滑屏忘记（portal 端同理，`vm-detail.tsx:89`）。 |
| J-A2.6 | P3 | `vm.go` `ReinstallVM` | 成功返回新密码，但 **DB 中 `vms.password` 是否同步更新？** 需追溯 vmRepo.UpdatePassword，否则 user-detail 下次 reset 与 reinstall 返回的密码不一致。 |

#### J-A3 Admin Products / Orders / Invoices / Users 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-A3.1 | P0 | `handler/portal/product.go:83` `Update` | `json.NewDecoder(r.Body).Decode(existing)` 直接 decode 进已读的 `existing` 结构体 → 请求 body 中缺的字段会被 **零值覆盖**（e.g. PATCH 仅改 price 但 active=false 被清零）。需改为 decode 进 `req struct` + 显式字段拷贝，或接受部分字段并合并。 |
| J-A3.2 | P0 | `handler/portal/user.go:79-105` TopUp | 校验 `amount <= 0` 但 **无上限**。管理员可给自己冲值 1e18 → 相当于任意提现。需加 daily cap + admin-cannot-topup-self + audit log 二次确认。 |
| J-A3.3 | P1 | `web/src/app/routes/admin/orders.tsx` | 页面是只读。`pending` 订单无"重试开通"、`failed` 无"取消/退款"按钮。对于 J-P1.2 的孤儿订单无补偿入口。 |
| J-A3.4 | P1 | `web/src/app/routes/admin/users.tsx:77` | `onChange={(e) => roleMutation.mutate(e.target.value)}` — 直接下拉即改。降级 admin→user 无 confirm。建议 `useConfirm` 二次确认 + 审计日志（已有）。 |
| J-A3.5 | P1 | `handler/portal/invoice.go:38` `ListAll` | 无分页。历史发票过万时前端会卡死。需加 `limit/offset` + 总数。 |
| J-A3.6 | P2 | `admin/products.tsx` ProductForm | 缺 `access`（public/beta/private）下拉与 `os_image_default` 字段。当前只能改"配置/价格/排序"，无法按套餐绑定默认镜像。 |
| J-A3.7 | P2 | `admin/orders.tsx` + `admin/invoices.tsx` | 列表显示 `user_id: 数字`，无用户名/邮箱。需后端 join users.email 或 DTO 带 `user_email`。 |

#### J-A4 Admin Monitoring / Audit / Storage / IP / Ceph / NodeOps 旅程

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| J-A4.1 | P0 | `handler/portal/nodeops.go:107` | `isValidCommand` 是 **前缀匹配白名单**（`incus`、`ceph`、`systemctl status` 等）。危险点：<br>① `incus` 前缀可被扩展 → `incus-shell; rm -rf /` 若前缀匹配被绕。<br>② 缺命令参数解析，管道/分号 `;`、`&&`、`|` 未过滤。<br>③ `sshexec.Runner` 若走 shell 执行会被注入。<br>需改为「命令 + 参数数组」显式白名单 + 不经 shell（见 J-A1.4）。 |
| J-A4.2 | P0 | `handler/portal/ceph.go:193` `CreatePool` | `fmt.Sprintf("ceph osd pool create %s %d %s", req.Name, req.PGNum, req.Type)` — `req.Name` / `req.Type` 用户可控，拼进 shell。`name="p1; rm -rf /data"` 会执行危险命令。需 `validate.ShellSafe(req.Name)` + 固定枚举 `type`。 |
| J-A4.3 | P1 | `ippool.go:119` `AddPool` | `h.clusters.UpdateConfig` 只改内存，不写回 `config.toml` → 重启丢失。需持久化到 DB 或 config 文件。 |
| J-A4.4 | P1 | `ippool.go:194` `countIPs` | 假设 range 同网段最后一个八位组，如 `10.1.1.1-10.1.1.200`；跨段 `10.1.0.1-10.1.1.200` 静默失败。需用 `net.ParseIP` + 大整数计算。 |
| J-A4.5 | P1 | `handler/portal/console.go:107` | `InsecureSkipVerify: true` 静默启用；无会话审计、无超时、无心跳。Phase F 已提到，但需加"生产环境禁用 InsecureSkipVerify"开关。 |
| J-A4.6 | P2 | `handler/portal/metrics.go:113-114` ClusterOverview | `clusterName == "" && len(h.clusters.List()) > 0 → clusterName = List()[0]` —— portal 用户的 VM 必定在自己所属集群，但 fallback 到 List()[0] 可能错。需 `vmRepo.GetByName(name).ClusterID` 反查。 |
| J-A4.7 | P2 | `handler/portal/audit.go:31` | `repo.List` 无按 user/action/target/date 过滤，前端 `admin/audit-logs.tsx` 也无过滤器。量大时难定位。 |
| J-A4.8 | P2 | `admin/observability.tsx:8-13` | Grafana/Prometheus/Alertmanager/Ceph URL 前端硬编码。多集群或私有化部署时不可配。需 `GET /admin/observability/dashboards` 后端返回。 |
| J-A4.9 | P2 | `admin/observability.tsx:98` | WS URL `/api/admin/events/ws` 仅 admin。Portal 用户看不到 VM 创建/开通/快照等事件流。Phase F 要加 `/api/portal/events/ws?user_id=...`。 |
| J-A4.10 | P2 | `admin/ip-registry.tsx:13` / `admin/monitoring.tsx:19` | 前端无分页/无虚拟列表。VM >500 时 monitoring 页 render 数十个 BarChart 性能差。 |
| J-A4.11 | P3 | `admin/audit-logs.tsx:65,76` | `user_id` 直出数字、`details` 截断无展开。用户体验差。 |
| J-A4.12 | P3 | `handler/portal/nodeops.go:107` quickCommands | 写死 7 条命令，无"保存常用"。应提供管理员自定义 quick command 集。 |

#### 跨旅程横切发现（性能 / 并发 / 权限 / 契约）

| 代号 | 风险 | 位置 | 说明 |
|------|------|------|------|
| X-1 | P0 | `ceph.go` 全文 + `nodeops.go` | 命令拼接依赖 `sshexec.Runner` 是否经 shell。Phase A.0 要强制审计 `sshexec` 实现，若经 shell 必须切到 `/bin/sh -c` + 参数安全转义，或改 `ssh ... -- cmd arg1 arg2` 数组化。 |
| X-2 | P0 | 所有 portal handler | 5 轮审查共确认 **写接口普遍无事务**：order.Pay / vm.Create / snapshot.Create / apitoken.Create 等。Phase A.0 要统一引入 `repository.WithTx(ctx, func(tx))` 模式。 |
| X-3 | P1 | 所有管理员"列表类"API | `invoice.ListAll` / `order.ListAll` / `audit.List(limit<=100)` 除 audit 外均无分页。Phase C-backend 所有 `AdminRoutes` 列表接口必须 `{limit, offset, total}` 标准化。 |
| X-4 | P1 | `metrics.go:54` `cephCache` 30s TTL | 多实例部署（HA admin）时缓存是进程本地；另一实例热点集群可能陷入"每次都去拉 metrics"。需 Redis / in-process-shared + 定期后台刷新（sidecar）。 |
| X-5 | P1 | 前端所有 mutation `onSuccess: () => invalidateQueries({ queryKey: vmKeys.all })` | 粗粒度 invalidate 导致每次操作都刷 `myList + detail + clusterList`。Phase A 已经部分细化，但 vm action / reset / reinstall 仍统一用 `vmKeys.all`。需按操作类型精细化。 |
| X-6 | P1 | Portal "customers" project 硬编码 | `vm.go:60/93/169/171` + `metrics.go:129` + 前端 `admin/create-vm.tsx:30` + `admin/vms.tsx:118`。Phase A.0 要定义 `ProjectMapper(userID) → projectName`（见方案 A 的 `users.default_project_id`）。 |
| X-7 | P2 | DTO 缺失：`InvoiceDTO` / `OrderDTO` / `TicketDTO` / `AuditLogDTO` | 仅 `VMServiceDTO` 存在。Phase A.0 要一次性补齐 + `dto_test.go` 覆盖。前端 `AdminOrder.cluster_id` 字段继续直暴后端 model，破坏封装。 |
| X-8 | P2 | 文件系统：后端 `handler/portal/` 27 文件 | Admin 专用 handler（audit、ceph、ippool、metrics、quota、ha、clustermgmt、product、nodeops）混在 `portal/` 包。Phase C-backend 迁到 `handler/admin/`。 |
| X-9 | P2 | 权限分层 | portal handler 中 `PortalRoutes` 与 `AdminRoutes` 同 struct 方法注册，依赖路由 `middleware.RequireAdmin` 保护。若 server.go 路由树忘加中间件，admin API 会泄露到 portal。Phase A.0 要加路由级 lint：admin 路径必须挂 `RequireAdmin`。 |
| X-10 | P3 | `handler/portal/events.go` | 已有 WS events 流，但 events 实际触发点在哪些 handler 未列清。Phase F 要提供 events 发布清单。 |

#### 第五轮自查结论

- ✅ 覆盖 7 条完整用户旅程（P1+P2+P3 + A1+A2+A3+A4），与第 4 轮的「后端包结构审查」互补；
- ✅ 47 条新发现按 P0/P1/P2/P3 分级，其中 **P0 共 6 条**（J-P1.1 / J-P1.2 / J-P2.1 / J-A3.1 / J-A3.2 / J-A4.1-A4.2 / X-1 / X-2），应进入 Phase A.0 最高优先级；
- ✅ 识别出两类被前四轮忽略的风险：
  1. **前端 apiBase 漏拼导致功能失效**（J-P2.1 snapshot delete/restore 是明确生产 Bug）；
  2. **命令注入风险链**（ceph pool 创建、nodeops 前缀白名单、node-join wizard）—— 属于 P0 级安全问题，应独立 security patch PR；
- ✅ 识别出两个被低估的**业务正确性**问题：
  1. `product.Update` 被零值覆盖（J-A3.1）—— 管理员改价会清空 active/access 标志；
  2. `TopUp` 无上限（J-A3.2）—— admin 自冲值权限漏洞；
- ✅ 识别出两个**并发 / 缓存**瓶颈：metrics.cephCache 在 HA 多实例场景失效（X-4）；portal refetchInterval 15s 同时作用 N 个用户 → Incus 压力线性放大；
- ⚠️ 未做：
  - `sshexec.Runner` 实现是否经 shell（X-1 需查源码最终确认）；
  - `vmRepo.UpdatePassword` 是否在 Reinstall 后被调用（J-A2.6）；
  - Phase F 的 `portal events ws` 与 `admin events ws` 事件 schema 差异；
  - 分页 envelope 最终形态（`{items, total, limit, offset}` vs `{data, meta}`）。

#### 对 PLAN-011 本体的修订建议

按本轮新增发现，Phase 清单应再微调（**不是推翻已写方案，而是补强**）：

- **Phase A.0（后端契约闭环）** 新增 7 条前 4 轮未覆盖的项：
  - A.0-R11 portal/order.Pay 补 compensation + 幂等键（J-P1.2/J-P1.7）；
  - A.0-R12 sshexec 切换到参数数组模式，彻底解决命令注入（X-1 / J-A1.4 / J-A4.1 / J-A4.2）；
  - A.0-R13 `product.Update` 改为字段级 patch（J-A3.1）；
  - A.0-R14 `user.TopUp` 加 daily cap + self-lock + 二次确认（J-A3.2）；
  - A.0-R15 `ticket.Reply` / `ticket.UpdateStatus` 加状态与枚举校验（J-P3.1/J-P3.2）；
  - A.0-R16 `api_tokens.expires_at` 默认 7d，UI 与 DB 联动（J-P3.4）；
  - A.0-R17 所有 Admin 列表接口标准化 `{items, total, limit, offset}`（X-3）。
- **Phase A（前端数据层）** 追加：
  - `useMyVMDetailQuery` refetchInterval；
  - `useResetVMPasswordMutation` invalidate 粒度校正（J-P2.5）；
  - snapshot-panel 按 apiBase 动态拼路径（J-P2.1）。
- **Phase B（i18n）** 追加 currency 语义：`currency: "USD" | "CNY" | ...`，前端按 `Intl.NumberFormat` 渲染（J-P1.6）。
- **Phase C（feature 抽取）** 追加：
  - 抽 `OsImagePicker` / `NodePicker` / `ProjectPicker`（J-P1.5 / J-A2.3）；
  - `AdminVMDetail` 由 O(N) 扫描改为 `useAdminVMDetailQuery(clusterName, vmName)` 单条接口（J-A2.2）；
  - HA page 加 cluster picker（J-A1.2）；
  - Observability dashboard 接后端 `/observability/dashboards`（J-A4.8）。
- **Phase D（UI 一致）** 追加 `useConfirm` 统一覆盖 user 降级、TopUp、pool delete（J-A3.4 / J-A3.2）。
- **Phase E（验证）** 追加：snapshot delete/restore 在 portal 下 **必须** 跑 e2e 用例（J-P2.1）；order.Pay 补偿事务要单测；sshexec 切数组化后要回归 nodeops / ceph / node-join 三条路径。
- **新增 Phase G — 安全补丁（P0-only）**：合并 sshexec 数组化 + ceph pool 参数校验 + nodeops 命令白名单重构 + TopUp 上限 + console 会话审计。独立 PR，与 Phase A.0 并行推进但优先级最高。

综上，**PLAN-011 的方向依然正确，但前四轮侧重"数据契约 + 后端结构"，第五轮沿用户旅程暴露了「功能闭环缺口 + 安全 + 业务正确性」三类新风险**。建议把新增的 A.0-R11 ~ R17 + Phase G 安全补丁写进 Phase 表格，作为执行级清单。

### 第五轮风险补遗

- **snapshot delete/restore 在 portal 已死**：这是 **现网 Bug**，凡在 portal 创建过快照的用户都无法清理，形成存储膨胀。建议单独 hotfix，不等 Phase C 整体重构。
- **安全补丁独立 PR**：sshexec 数组化、ceph pool 参数校验、TopUp 上限三项若混入 Phase A.0 会拖慢节奏；按 Phase G 拆单独 PR + 逐项 CVE-level review。
- **孤儿订单清理**：Phase A.0-R11 上线前，需要一次性 DB 清理脚本把历史 `status=pending + created_at>1h` 的订单标 `failed` 并释放 IP，避免 A.0 上线后老数据持续告警。

---

## 批次 2 / Phase G — 安全补丁执行级实施计划（2026-04-17）

> 优先级 **P0**；独立 PR；不与 Phase A.0 交叉。范围严格收敛在「安全 + 业务正确性」。

### 背景

第五轮审计给出方向（sshexec / ceph / nodeops / TopUp / console），批次 2 将其落到具体文件与接口签名上。所有改动都要求**最小 diff**、**不顺手重构**。

### G-1 `sshexec.Runner.RunArgs`（基础）

- 文件：`incus-admin/internal/sshexec/runner.go`
- 新增方法：
  ```go
  func (r *Runner) RunArgs(ctx context.Context, program string, args ...string) (string, error)
  ```
  内部实现：对 `program` 与每个 `arg` 做 POSIX 单引号转义（`' → '\''`），拼接后复用既有 `Run`。保持 `session.CombinedOutput` 不变（一次会话一条命令，SSH 通道本身无额外风险）。
- 不改动 `HostKeyCallback`（本轮不处理 MITM，记入 TODO 由独立 Phase 处理）。
- 不破坏现有 `Run(ctx, cmd)` 调用点。

### G-2 Ceph 参数化

- 文件：`incus-admin/internal/handler/portal/ceph.go`
- `CreatePool`：
  - `req.Name` 正则 `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`；
  - `req.Type` enum `{"replicated", "erasure"}`，默认 `replicated`；
  - `req.PGNum` 范围 `[1, 65536]`；
  - 改用 `runner.RunArgs(ctx, "ceph", "osd", "pool", "create", name, strconv.Itoa(pg), poolType)`。
- `DeletePool`：同名校验 + `runner.RunArgs(ctx, "ceph", "osd", "pool", "delete", name, name, "--yes-i-really-really-mean-it")`。
- `OSDOut` / `OSDIn`：`osdID` 用 `strconv.Atoi` 转 int 后再格式化，杜绝字符串拼接；`RunArgs(..., strconv.Itoa(id))`。
- `application enable` / `set size` 同理参数化。
- 不改动 HTTP 路由签名、不改 response envelope。

### G-3 nodeops 命令白名单重构

- 文件：`incus-admin/internal/handler/portal/nodeops.go`
- `isValidCommand` 彻底重写：
  1. 按 `strings.Fields(cmd)` 切分 token。
  2. 第一 token 必须**完全匹配**（非前缀）白名单子命令集合（保持与旧 11 条等价的真命令集，如 `incus`、`systemctl`、`journalctl`…）。
  3. 整串出现 `;`、`&&`、`||`、`|`、`` ` ``、`$(`、`<`、`>`、`\n` 任一字符 → 拒绝。
  4. 参数白名单由每个子命令的 allow-list 决定（例：`systemctl` 只允许 `status|restart|reload` + service 名正则 `^[a-zA-Z0-9@._-]+$`）。
- `ExecNodeCommand` 处理器不改签名；内部若通过白名单，使用 `runner.RunArgs(ctx, program, args...)`。
- 返回码：白名单拒绝 → 400 `{"error":"command not allowed"}`，保持旧行为。

### G-4 `TopUpBalance` 上限 + 自锁

- 文件：`incus-admin/internal/handler/portal/user.go::TopUpBalance`
- 新增校验：
  - `amount > 0` 且 `amount <= 10000`（单次上限；常量 `MaxTopUpPerRequest`）。
  - 当前会话 `ctxUser.Role == "admin"` 且 `ctxUser.ID == targetUserID` → 返回 403 `admin cannot top up own balance`（防止特权账户自充）。
  - 追加审计：`audit(ctx, "user.topup", targetUserID, map[string]any{"amount": amount})`。
- 不引入日额度（避免改 schema），写 TODO 留给后续 Phase。

### G-5 console 会话审计

- 文件：`incus-admin/internal/handler/portal/console.go`
- 将 `InsecureSkipVerify: true` 改为读取配置 `cfg.Incus.InsecureSkipVerify`，默认 `false`；配置文件和 `.env.example` 同步新增键。
- 在 WS 升级成功 / 连接关闭处追加：
  - `audit(ctx, "console.session_open", vmID, map[string]any{"cluster":cluster, "project":project})`
  - `audit(ctx, "console.session_close", vmID, map[string]any{"duration_ms": d.Milliseconds()})`
- 不动现有 `slog.Info`。

### G-6（可选，降级）`product.Update` 字段级 PATCH

- 经二次核实 Go `json.Decode(existing)` 本身是**部分覆盖**（未出现的字段保持原值），风险低于 P0；本批次**不改**。
- 留 TODO：Phase C 改 DTO 化后再补 PATCH/PUT 语义区分。

### 变更清单

| # | 文件 | 变更 | 风险 |
|---|------|------|------|
| G-1 | `internal/sshexec/runner.go` | +`RunArgs` / +`shellQuote` | 新增，零破坏 |
| G-2 | `internal/handler/portal/ceph.go` | 5 处 `fmt.Sprintf` → `RunArgs` + 正则/枚举校验 | 返回值字符串格式一致 |
| G-3 | `internal/handler/portal/nodeops.go` | `isValidCommand` 重写 + `RunArgs` | 可能误杀非 ASCII 服务名，覆盖测试需加 |
| G-4 | `internal/handler/portal/user.go` | `TopUpBalance` 上限 + 自锁 + audit | 需要前端对 400/403 提示 |
| G-5 | `internal/handler/portal/console.go` + `config` | InsecureSkipVerify 配置化 + audit 两处 | 需要生产配置同步，否则 WS 握手失败 |

### 验证

- `task lint && task test ./internal/handler/portal/... ./internal/sshexec/...`
- 手测：
  1. Ceph 创建 `name = "a;rm -rf"` → 400。
  2. nodeops exec `incus list; rm -rf /` → 400。
  3. admin 自充 → 403。
  4. console 配置 `InsecureSkipVerify=false` + 无证书 → WS 失败并记录。
- 回归：G-2 / G-3 变更后 **node-join / HA failover / 原 pool 生命周期** 三条路径各过一次。

### 不做的事

- 不改 `HostKeyCallback`（延期）。
- 不做 TopUp 日额度（需 schema）。
- 不改 `product.Update`（风险低）。
- 不引入 audit schema 变更（复用现有 `audit()` 函数）。

---

## 批次 3 / Phase A.0 残留（2026-04-17）

> 优先级 **P1**；与批次 2 相互独立 PR；`R17` 由于涉及所有 Admin List 接口 + 前端联动，单独拆出 Phase B/C 内完成。

### 已落实

- **R11 `order.Pay` 补偿**：扣款成功后 IP 分配或 VM 创建失败 → 退款（`AdjustBalance` `type=refund`）+ 释放 IP（5min 冷却）+ 订单置 `cancelled`。通过 `SetUserRepo` 注入 `UserRepo`。
  - 幂等由 `OrderRepo.PayWithBalance` 的 `FOR UPDATE` + `status != pending` 保证，已有。
  - HTTP 状态码由 200 paid+error 改为 500，避免前端误判为成功。
- **R15 ticket 枚举**：`Create.Priority` ∈ `{low, normal, high, urgent}`；`UpdateStatus.Status` ∈ `{open, pending, closed}`；`Reply` 对 `closed` 工单返回 409。
- **R16 api_tokens 过期**：`Create` 默认 `expires_in_days = 7`；0 → 永不过期；范围 0–365。前端下周跟进支持显式填入。

### 延期

- **R17 Admin 列表 `{items,total,limit,offset}`**：改 OrderRepo/UserRepo/TicketRepo/ProductRepo 等所有 `ListAll` 签名 + handler 返回结构 + 前端 11 个列表页 → 改造面过大。移入 Phase C 批量重构，随 feature 抽取同时做。

---

## 批次 4 / Phase B · C · D 增量（2026-04-17）

> 优先级 **P2**；纯前端改造；与批次 2/3 独立 PR。

### 已落实

- **Phase D — `useConfirm` 覆盖**：`admin/users.tsx`
  - `admin → customer` 角色变更增加确认对话框（destructive）。
  - 余额充值改为确认后再调用 mutation，并将 amount 显示在确认文案中。
- **Phase B — `formatCurrency` helper**：`shared/lib/utils.ts` 新增 `formatCurrency(amount, currency='USD', locale?)`，基于 `Intl.NumberFormat`。
  - 当前调用方全量替换留到下个 PR（需同时新增 `Product.currency` 字段，Phase B 完整版延期）。
- **Phase C — `OsImagePicker` 抽取**：新增 `features/vms/os-image-picker.tsx`，导出 `OS_IMAGES`、`DEFAULT_OS_IMAGE`、`OsImagePicker`、`getOsImageLabel`。
  - 迁移 `app/routes/billing.tsx`：移除重复的 `OS_IMAGES` 常量，`<select>` 替换为 `<OsImagePicker>`。
  - 迁移 `app/routes/admin/create-vm.tsx`：同上，`Summary` 中 `OS_IMAGES.find().label` 改为 `getOsImageLabel(osImage)`。

### 验证

- `bun run typecheck` ✅
- `bun run build` ✅（2621 modules，538 ms，1.3 MB bundle — 无新告警）

### 延期（Phase E 及 R17）

- **Phase B 全量货币化**：需要 `Product.currency` + `Order.currency` schema 迁移，牵涉 13 个前端文件 + API DTO 扩展，独立 PR。
- **Phase C 剩余**：`NodePicker`、`ProjectPicker` 抽取、`AdminVMDetail` 单端点合并、HA 页集群选择器（J-A1.2）、可观测性看板接入 `/observability/dashboards` — 按优先级逐 PR 推进。
- **R17 列表分页**：随 Phase C 批量重构一并落地。
- **Phase E 自动化测试**：当前仓库无 Vitest / Go 集成测试基础设施，需单独 PR 引入 testing-library + `internal/testhelper` 后再加具体用例（`snapshot-panel`、`order.Pay` 补偿、`sshexec` 回归）。

---

## 批次 5 / Phase C ClusterPicker（2026-04-17）

> 优先级 **P2**；纯前端；与批次 4 同属 Phase C，单独 PR。

### 已落实

- **新增 `features/clusters/cluster-picker.tsx`**：基于 `useClustersQuery` 的单一下拉；支持 `allowEmpty` + `placeholder`，`disabled` 自动在加载中置灰，`display_name || name` 显示。
- **`admin/ip-pools.tsx`**：移除本地 `clusters.map` + `<select>`，改为 `<ClusterPicker allowEmpty placeholder=... />`；保留现有 `selectCluster` i18n 文案。
- **`admin/ha.tsx`**：`clusterName` 改为 `useState` + `useEffect` 初始化第一个集群；`clusters.length > 1` 时渲染选择器（J-A1.2）。
- **`admin/create-vm.tsx`**：同上多集群选择器；`Summary.Cluster` 根据当前 `clusterName` 查找 `display_name`，不再硬编码 `clusters[0]`。
- **i18n 补齐**：`vm.cluster / vm.size / vm.osImage / vm.project` 中英双语。

### 验证

- `bun run typecheck` ✅
- `bun run build` ✅（2622 modules / 564 ms）

### 延期

- `NodePicker`（跨集群过滤、Online/Evacuated 状态徽标）——等 PLAN-012。
- `ProjectPicker`（依赖后端 `/admin/clusters/{name}/projects` 端点，目前 `project` 硬编码下拉两项 `customers/default`）——等后端接口。
- `AdminVMDetail` 单端点合并——等 Phase C 后续拆包。

---

## 批次 6-10 执行级计划（2026-04-17 续）

> 关联 todo：批次 6 = R17 分页基础；批次 7 = currency schema；批次 8 = 后端服务扩展；批次 9 = 前端收尾；批次 10 = Phase E 测试。全部为 PLAN-011 「延期」栏里剩下的工作。

### 背景

批次 1-5 + Phase G 已完结（snapshot hotfix、`formatCurrency` helper、`OsImagePicker`、`ClusterPicker`、Phase D 确认弹窗、sshexec 数组化、ceph/nodeops 白名单、TopUp 上限+自锁、console 会话审计、`order.Pay` 补偿、ticket/apitoken 枚举+过期）。剩下四项靠后、相互独立，每一项拆一个 PR：

1. **R17 列表分页**：所有 Admin List 接口统一 `{items,total,limit,offset}` + 前端分页组件。
2. **货币字段闭环**：`products/orders/invoices.currency` 迁移 + DTO + `formatCurrency` 全量替换 UI。
3. **后端服务扩展**：`/admin/clusters/{name}/projects`、`AdminVMDetail` 单端点、`sshexec.HostKeyCallback` 严格校验。
4. **Phase E 测试基础**：`internal/testhelper`（Go）、Vitest 配置修正、关键用例（snapshot delete/restore、order.Pay 补偿、sshexec 回归）。

### 批次 6 / R17 分页基础（后端，P1）

- 已落实：新增 `internal/handler/portal/pagination.go` 定义 `Page[T any]` + `ParsePageParams(r)`；`User/Order/Invoice/Ticket/Product/VM` 仓库追加 `ListPaged(ctx, limit, offset) ([]T, int64, error)`，`ListAll` 退化为 `ListPaged(ctx, 0, 0)` 包装；`user/order/invoice/ticket/product/audit` handler 返回值已改为 `{items, total, limit, offset}` 同构结构（`items` 键名按业务沿用 `users/orders/...`）。
- 留尾：
  - `VMRepo.ListPaged` 目前**未被 handler 使用**（handler 仍遍历集群 API）——此条视为预留 API，批次 9 前端迁移时再决定是否接入。
  - `audit.List` 仍限幅 100 上限（与其他 200 不同），为保持合规留日志压力，保留。

### 批次 7 / 货币字段闭环（跨前后端，P1）

**后端**

- 已落实：迁移 `db/migrations/004_currency.sql` 对 `products/orders/invoices` 追加 `currency CHAR(3) NOT NULL DEFAULT 'USD'`；`model.Product/Order/Invoice` 结构增加 `Currency`；`ProductRepo` 全链路（SELECT/INSERT/UPDATE/Scan）带 currency；`OrderRepo.Create` 签名变为 `(...amount, currency string)`，`GetByID/ListByUser/ListPaged` SELECT+Scan 带 `COALESCE(currency, 'USD')`。
- 留尾（编译阻断点）：
  1. `handler/portal/order.go:105` 仍按 5 参调用 `h.orders.Create(...)`——补第六参 `product.Currency`（`product` 结构来自 `h.products.GetByID`，字段已存在）。
  2. `InvoiceRepo` SELECT/Scan 未携 currency——`ListByUser/ListPaged` 两处 + `Create` INSERT+RETURNING 同步加入 `currency`，默认取自 `orders.currency`（通过参数传入，或 Repo 查询 join；最小改动选择**参数注入**）。
  3. `OrderRepo.PayWithBalance` 在事务内创建 invoice 的那条 `INSERT INTO invoices (...) VALUES (...)` 未写 currency——补一列，值取自 `o.Currency`（`Scan` 也需同步加入）。
- 验证：`task lint && task test ./internal/repository/... ./internal/handler/portal/...`（若 `task` 不可用，退化为 `gofmt -l && go vet`）。

**前端**（依赖后端迁移发版）

- `shared/lib/utils.ts` 已有 `formatCurrency(amount, currency='USD', locale?)`。
- 13 处仍硬编码 `¥{amount}` / `$` 前缀的文件需改为 `formatCurrency(amount, currency)`：
  - `app/routes/billing.tsx`（订单/发票列表、topup、order summary）
  - `app/routes/admin/{products,orders,invoices,users}.tsx`
  - `features/billing/*`（若已抽出）
  - 其余点由 `rg -n "¥|[$][0-9]"` 锁定后逐个替换。
- 后端 DTO 已携带 `currency`，前端类型补 `currency?: string` 字段，默认 `USD`。
- i18n 不变（格式器内置 locale）。

### 批次 8 / 后端服务扩展（P2）

**B-1 `/admin/clusters/{name}/projects`**

- 文件：新增 `handler/portal/cluster.go` 处理器方法 `ListProjects`；路由挂载到 `/admin/clusters/{name}/projects`。
- 实现：`cluster.Manager` 按 name 拿 `client`，调用 `client.GetProjectNames()`（incus API 已提供）返回 `{projects: [...]}`。
- 失败返回 502 + error 字段，不降级静默返回空列表（避免前端误判）。

**B-2 AdminVMDetail 单端点**

- 文件：新增 `handler/portal/admin_vms.go` 处理器方法 `GetAdminVMDetail`；路由 `/admin/clusters/{name}/vms/{vmName}`。
- 实现：
  1. `cluster.Manager` 取 client。
  2. `client.GetInstanceFull(vmName)`：取 instance + state + snapshots（一个接口）。
  3. 合并 DB 侧 `vms` 表同名行（owner、order_id、ip、notes）。
  4. 返回 `{vm, db, snapshots, state}` 结构，前端 `AdminVMDetail` 直接用，删除原来 `O(N)` 遍历集群列表找一台的逻辑。
- 权限：`admin` only；`portal-admin` 中间件已有。

**B-3 `sshexec.HostKeyCallback` 严格校验**

- 文件：`internal/sshexec/runner.go`。
- 方案：`Runner` 新增 `knownHostsFile string` 字段（默认 `~/.ssh/known_hosts`）；若文件存在，改用 `ssh.FixedHostKey` / `knownhosts.New(file)` 构造 callback；若不存在**不要**悄悄回退 `InsecureIgnoreHostKey`——改为返回配置错误，强制运维在部署时 `ssh-keyscan` 注入主机。
- 配置：`cfg.SSH.KnownHostsFile`；`.env.example`、`config.yaml.example` 同步增加。
- 风险：现有部署若未配置会启动失败——发版前在 tmp 脚本里预填 `ssh-keyscan`。

### 批次 9 / 前端收尾（P2）

**C-1 R17 分页组件**

- 新增 `shared/components/ui/pagination.tsx`（基于现有 `@base-ui/react` 或 shadcn 模板）：props `{ total, limit, offset, onChange(limit, offset) }`。
- 每个 admin list 页接入：
  - `admin/users.tsx`、`admin/orders.tsx`（若存在）、`admin/invoices.tsx`、`admin/tickets.tsx`、`admin/products.tsx`、`admin/audit.tsx`、`admin/vms.tsx`。
  - query hook 改为接受 `{ limit, offset }` 参数；query key 统一 `[resource, 'list', { limit, offset }]`。
- i18n 文案 `pagination.{prev,next,page,of,total}` 中英双语。

**C-2 formatCurrency 全量替换**（批次 7 前端部分）

- 同批次 7 「前端」小节，依赖后端 DTO 先发版。

**C-3 NodePicker / ProjectPicker / AdminVMDetail**

- `ProjectPicker`：依赖 B-1 endpoint；`features/projects/api.ts` 新增 `useProjectsQuery(clusterName)`；`features/projects/project-picker.tsx`。
- `NodePicker`：现有 `/admin/clusters/{name}/nodes` 基础上，封装；徽标依 `node.status` 渲染 online/evacuated。
- `AdminVMDetail`：改用 B-2 endpoint；删除父列表 `find(vm => vm.name === name)` 逻辑；query key `[admin-vm, clusterName, vmName]`。

### 批次 10 / Phase E 测试基础（P2）

**Go 侧**

- 新增 `internal/testhelper/db.go`：`NewTestDB(t *testing.T)` → 启动 `pgcontainer`（Testcontainers）/ 或读 `POSTGRES_TEST_DSN` → 执行 `db/migrations/*.sql`；返回 `*sql.DB` + cleanup。
- 新增 `internal/testhelper/cluster.go`：fake incus server（httptest.Server）返回固定 JSON，给 `cluster.Manager` 注入。
- 用例：
  - `repository/order_test.go::TestPayWithBalance_RollbackOnInvoiceFail`：扣款后 invoice 失败 → 余额回滚、订单 pending。
  - `handler/portal/order_test.go::TestPay_RollbackOnIPAllocFail`：覆盖 `rollbackPayment` 路径。
  - `sshexec/runner_test.go::TestShellQuote`：覆盖边界字符串。
  - `handler/portal/nodeops_test.go::TestValidateCommand`：注入向量（`;`、`|`、反引号、换行）全部 400。

**前端侧**

- `vitest.config.ts`（已存在则补）加入 `setupFiles`、`environment: 'jsdom'`、`globals: true`。
- `features/snapshots/snapshot-panel.test.tsx`：portal 模式下 delete/restore 请求路径应为 `/api/...` 而非 `/admin/...`（覆盖 J-P2.1 回归）。
- `shared/lib/utils.test.ts` 追加 `formatCurrency` 用例（USD / CNY / 0 / NaN）。

### 交付顺序（自下而上，最小阻塞）

1. 批次 7 留尾三点（编译阻断）→ 单独 PR「fix: currency schema 收尾」。
2. 批次 8 B-1、B-2、B-3 → 三个独立 PR；B-3 由于部署强约束，先走 staging。
3. 批次 9 C-2 依赖批次 7 + C-3 的 ProjectPicker 依赖 B-1；其余 C-1 / NodePicker / AdminVMDetail 可先走。
4. 批次 10 测试基础独立 PR，与功能 PR 并行审核。

### 风险与不做

- 不引入 TopUp 日额度（需 schema + admin 面板；延后到 PLAN-012）。
- 不改 `product.Update` 字段级 PATCH（风险低，第五轮结论已记）。
- 不迁移 `vms` 列表到 `ListPaged`（前端仍靠集群 API，迁移成本高）。
- 不清理 `ListAll`（仍被 job 与非 admin 路径使用，保持兼容）。

## 执行小结（2026-04-17 续）

### 批次 7 / currency schema 闭环 —— 完成
- `repository/invoice.go`：`Create` 加 `currency`；`ListByUser`、`ListPaged` SELECT 用 `COALESCE(currency,'USD')`。
- `repository/order.go::PayWithBalance`：`SELECT ... FOR UPDATE` 携带 currency，写 invoice 时带入。
- `handler/portal/order.go:105`：`Create` 调用端补传 `product.Currency`，解除编译阻断。
- 前端：`features/billing/api.ts` / `features/products/api.ts` 补 `currency?: string`；所有金额展示点改走 `formatCurrency` —— `admin/users.tsx` / `admin/orders.tsx` / `admin/invoices.tsx` / `admin/products.tsx` / `billing.tsx`（3 处）。

### 批次 8 / 后端扩展 —— 完成
- **B-1** `handler/portal/vm.go::ListProjects` 暴露 `GET /admin/clusters/{name}/projects`，直通 Incus `/1.0/projects?recursion=1`；失败 502。
- **B-2** `GetClusterVMDetail` 暴露 `GET /admin/clusters/{name}/vms/{vmName}[?project=...]`，聚合 instance + state + snapshots + DB vms 一次性返回；`?project=` 缺省时在配置项目里回退搜索。
- **B-3** `sshexec/runner.go` 新增 `knownHostsFile` + `WithKnownHosts` + `hostKeyCallback()`：文件存在走 `knownhosts.New` 严格校验；为空则 `InsecureIgnoreHostKey` + WARN 日志（避免历史部署炸启动；生产应显式配置）。`config.MonitorConfig` 加 `SSHKnownHostsFile`（env `SSH_KNOWN_HOSTS_FILE`）；`CephHandler` / `NodeOpsHandler` 构造器与 `cmd/server/main.go` 同步透传。

### 批次 9 / 前端收尾 —— 完成
- **C-1 分页基础**：`shared/lib/pagination.ts`（`PageParams` 类型 + 查询串/key helper），`shared/components/ui/pagination.tsx`（i18n 文案、上一页/下一页/每页选择）。
  - i18n：`locales/{zh,en}/common.json` 新增 `pagination.{range,pageSize,prev,next,pageOf}`。
  - 已接入：`admin/users.tsx`、`admin/orders.tsx`、`admin/invoices.tsx`、`admin/tickets.tsx`、`admin/products.tsx`、`admin/audit-logs.tsx`（替换手写分页）。各 hook 接受 `PageParams`，query key 标准化为 `[resource,'list','admin',PageParams|'all']`。
- **C-2 formatCurrency**：同批次 7，全量替换完成。
- **C-3 ProjectPicker / NodePicker / AdminVMDetail**
  - `features/projects/{api,project-picker}.tsx`：使用 B-1，支持自动选择 customers → default → 首个。
  - `features/nodes/node-picker.tsx`：基于 `useAdminNodesQuery`，支持 `excludeNodes`，状态标注 evacuated/offline。
  - `features/vms/api.ts::useAdminVMDetailQuery`：基于 B-2；`admin/vm-detail.tsx` 删除原「拉整个集群列表再 find」的 O(N) 逻辑，migrate 目标输入改为 `NodePicker`（自动排除当前节点）。
  - `admin/create-vm.tsx`：原硬编码 `customers/default` select 替换为 `ProjectPicker`；submit 添加 `!project` 约束。

### 批次 10 / Phase E 测试基础 —— 完成（简版）
- Go 测试：
  - `internal/sshexec/runner_test.go::TestShellQuote`：空串/引号嵌套/分号/反引号/`$()` 全覆盖。
  - `internal/handler/portal/pagination_test.go::TestParsePageParams`：默认值、负数钳制、`>200` 上限、非法字符串回退。
  - `internal/handler/portal/nodeops_test.go::TestValidateCommand`：shell 元字符、白名单外程序、`systemctl restart`、`cat /root/.ssh/id_rsa` 全拒；`uptime` / `systemctl status` / `cat /etc/hosts` / `cat /proc/meminfo` 通过。
- 前端测试：
  - `features/snapshots/snapshot-utils.ts` 抽取纯 `snapshotPath(apiBase, vmName, snap?)`；`snapshot-panel.test.ts` 覆盖 admin/portal × list/single 四个分支（J-P2.1 回归）。
  - `shared/lib/utils.test.ts` 补 `formatCurrency` USD/CNY/undefined/0 四用例。
- 未做（延后 PLAN-012）：pgcontainer + order.PayWithBalance 回滚集成测试（依赖 testcontainers + DSN 基础设施，单独迭代）；fake Incus httptest server（ListProjects / AdminVMDetail）。

### 验证
- `go build ./...` ✓；`go test ./internal/sshexec/... ./internal/handler/portal/... ./internal/service/...` ✓（14 pass，新增 3 个文件）。
- `bun run typecheck` ✓；`bun run build` ✓（1.35 MB bundle）；`bunx vitest run` ✓（2 files / 14 tests，新增 5 用例）。

### 未覆盖 / 后续（PLAN-012 候选）
- `handler/portal/order_test.go::TestPay_RollbackOnIPAllocFail`（需 fake cluster + DB testhelper）。
- `admin/vms.tsx` 接入分页（当前靠 Incus 集群 API，不走 DB `ListPaged`）。
- `.env.example` / `config.yaml.example` 补 `SSH_KNOWN_HOSTS_FILE` 示例（生产部署强依赖）。
- TopUp 日额度 + `product.Update` 字段级 PATCH（原计划即标记延后）。

### 状态
- 批次 6-10 全部完成；PLAN-011 关闭主路径，剩余纯扩展项（上文 "未覆盖"）转交 PLAN-012。
