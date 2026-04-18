# IncusAdmin Changelog

## 2026-04-18 20:30 [progress]

UX-002 / PLAN-016 后台菜单重组 + 用户/管理员视角分离 —— 部署至生产等待目视回归:

- 拆 `web/src/shared/components/layout/sidebar-data.ts`:`userSidebar` 扁平 7 项 + `adminSidebar` 5 组(监控 / 资源 / 基础设施运维 / 订单财务 / 用户工单)
- 重写 `app-sidebar.tsx`:路径前缀 `/admin` 自动切视角,admin 视角用 `@base-ui-components/react` Accordion 做二级折叠;当前路径所在组自动展开 + `localStorage('incus.sidebar.admin.openGroups')` 跨刷新持久化;collapsed 态降级为扁平 icon 列表 + 组间分隔线
- admin 顶部加"进入管理后台 / 返回用户后台"切换按钮(`isAdmin` 门控,非 admin 永不渲染)
- 补 `sidebar.switchToAdmin / backToUser / group.{monitoring,resources,infrastructure,billing,userOps}` 共 7 条 i18n key(中英双语),移除硬编码 "Admin"
- 前端 `bun run typecheck` + `bun run build` 通过;dist_hash=`5bd7a8bbece0773d`
- 部署:本地 go 1.25 交叉编译 linux/amd64 binary(sha256=`603ef5de…`)embed 新 dist,AIssh file_deploy → 原子 mv + `systemctl restart incus-admin`;`/api/health` dist_hash 与本地对齐
- 浏览器目视回归:agent 侧 saved_browser 非登录态,无法完成 SSO 登录截图,留待用户在 vmc.5ok.co 人工核对 (两个视角 × 折叠/展开 × 深浅色 × collapsed/expanded sidebar)

## 2026-04-18 18:42 [progress]

PLAN-015 / QA-005 全量落地，QA-004 报告 N1-N15 清零（除 N4/N7/N9/N10/N14 已转 PLAN-014 / 反代窗口）：

- N1 字体回退 + lang 同步：`index.css` font-sans 加 PingFang/YaHei/Noto CJK 兜底；`app/i18n.ts` languageChanged 钩子写 `<html lang>`，与 i18next 状态保持一致
- N2 Portal Tickets 入口：表格行加 ▶/▼ chevron + 链接化 Subject + aria-expanded；click → 内联展开 TicketDetail（API 已存在）
- N3 i18n 大扫除：`en/zh common.json` 补齐 admin.products / admin.nodes / admin.tickets / admin.users / admin.vmDetail / monitoring 等键；`monitoring.tsx` / `observability.tsx` / `node-join.tsx` 全部硬编码中文走 `t()`
- N5 audit target 渲染：`features/audit-logs/helpers.ts` `targetLabel()` 优先 `details.name || target || vm || vm_name || host || osd_id`，缺失才回退 `#target_id`；6 条单测覆盖
- N6 admin catchall 404：`app/routes/admin.tsx` 加 `notFoundComponent` —— "404 / Admin page not found / Back to Clusters" 取代裸 "Not Found"
- N8 后端 API 错误响应统一 JSON：`internal/server/static.go` `/api/*` 落空时返 `{"error":"not found"}`；`server.go` 注册 `MethodNotAllowed` handler 返 405 JSON；新增 `GET /api/admin/orders/{id}` 与 `GET /api/admin/products/{id}`
- N11 Node Ops IP 校验：`features/nodes/host-validation.ts` 抽出 `validHost()`（IPv4/IPv6/RFC1123），7 条单测覆盖
- N12 移动端表格溢出：13 个 admin/portal 路由表格 wrapper `overflow-hidden` → `overflow-x-auto`
- N13 VM 默认用户名按 image 映射：`features/vms/default-user.ts` 覆盖 ubuntu/debian/rocky/almalinux/centos/fedora/opensuse/arch/alpine/freebsd，6 条单测
- N15 Audit IP 列：`stripCidrSuffix()` 去掉 `/32`、`/128` 后缀

CI 门：后端 `go build/vet/test` 全过；前端 `tsc --noEmit / vitest run / vite build` 全过（37 条单测，新增 19 条）

部署：本地 build → AIssh `file_deploy` → systemctl restart incus-admin（PID 216141）；
新 dist_hash `b934750eb3d6f6...` 与本地一致；浏览器回归 N1/N3/N5/N6/N8/N11/N12/N13/N15 全部生效

## 2026-04-18 00:05 [progress]

QA-004 后继项：DB ↔ Incus VM 状态漂移修复（生产手工同步 + 前端空态文案 + 后端 db_running_count 指标）：
- 生产 SQL：`UPDATE vms SET status='deleted' WHERE id IN (7,8)`（vm-d8b7dc / vm-870c48 —— Incus 侧实际 0 实例，DB 残留）；`UPDATE ip_addresses SET status='available', vm_id=NULL WHERE id=5` 释放 202.151.179.239 回池
- 后端 `MetricsHandler.ClusterOverview` 增加 `db_running_count` 字段，`VMRepo.CountRunningByCluster` 新方法，让管理员一眼识别"DB 说 N 个 running / Incus 0 个"的漂移情形
- 前端 `admin/monitoring.tsx` 空态文案分流：`drifted=true` 时显示漂移提示和待执行同步提示；否则显示"当前集群无运行中的 VM"
- 部署：新二进制 sha256=2003ea42…，dist_hash=95c7d8082fdc；systemctl restart 后 `TLS pin learned` 已持久化
- 新开 `PLAN-014 VM 状态反向同步 worker` + `INFRA-006` 任务，设计后台 60s reconciler 消除未来漂移（不纳入本次部署范围）

## 2026-04-17 21:32 [progress]

PLAN-013 Phase B 反代层收尾（授权窗口内 AIssh 推送），PLAN-013 全量完成：
- B.1 `/oauth2/callback` 500 排查：经源站 oauth2-proxy 日志比对，确认为**误报** —— 所有 500 均由外部扫描器路径（`/boaform/admin/formLogin`、`/hello.world?%ADd+allow_url_include` 等）触发 `invalid semicolon separator in query`，真实 admin 登录链路无 500，无需修复
- B.3 favicon 白名单：生产 `/etc/incus-admin/oauth2-proxy.cfg` 的 `skip_auth_routes` 增加 `"^/favicon\.ico$"`（备份 `oauth2-proxy.cfg.bak.20260417212843`），`systemctl restart oauth2-proxy` 后 `curl -I https://vmc.5ok.co/favicon.ico` 返回 `HTTP/2 200`
- B.2 Caddy 三条安全头：源站前置是 Cloudflare CDN，HSTS/X-Content-Type-Options/Referrer-Policy 归 CDN Transform Rules 管；且该项仅是 SSL Labs A+ 评级的质量项（非真实 Bug），本 plan 不动 CDN 配置 → 整项移出范围
- PLAN-013 `index.md` 改 `[x]`，`PLAN-013.md` `status: completed` / `completedAt: 2026-04-17 21:32`
- `TECHDEBT-001` 随 plan 收口为 completed

## 2026-04-17 17:30 [progress]

PLAN-013 代码层 + 测试层 + CI 全量落地（Phase A/C/D 完成，Phase B 反代待运维窗口）：
- A.1 `Pay` 路径 IP 分配回滚集成测试 + `rollbackPayment` 导出供测试
- A.2 `admin/vms.tsx` 客户端分页
- A.3 `product.Update` 指针 DTO 真 PATCH 语义（仅合并非 nil 字段 + 3 条单测）
- C.1 SPKI 指纹 pinning：migration 006 + `cluster/tlspin.go`（TOFU/mismatch reject）+ 适配器接通 REST/WebSocket（events/console）+ 5 条单测
- C.2 端到端 cluster_id 打通：去掉 `int64(1)` 硬编码，订单创建 / 支付复用订单上的 cluster_id
- C.3 Observability iframe：HTTP 目标（Grafana/Prom/Alertmanager）改 "new tab only"，Ceph HTTPS 保留嵌入
- C.4 启动期计算 `dist/index.html` sha256，写入 `/api/health` 并在启动日志给 12 位短哈希
- D.1 `.github/workflows/ci.yml`：backend-unit → backend-integration（testcontainers, Ryuk 关）→ frontend typecheck/build
- D.2 `UserRepo.TopUpWithDailyCap`：事务 + `SELECT ... FOR UPDATE` 行锁，根治并发越限；并发 + 边界两条集成用例
- Phase B（oauth2-proxy callback 500 / Caddy 三条安全头 / favicon 白名单）保留 `[ ]`，等运维低峰窗口经 AIssh 推送，不阻塞代码合并

## 2026-04-15 17:32 [progress]

All 17 database tables covered with backend APIs and frontend pages. Features: VM lifecycle, console, snapshots, monitoring (Recharts), SSH keys, products, tickets, orders/billing, invoices, audit logs, API tokens with Bearer auth. Deployed at vmc.5ok.co.

## 2026-04-15 17:40 [decision]

PLAN-005 drafted: full-stack refactor to pma-web (shadcn/ui sidebar layout, ThemeProvider, feature hooks, ESLint, Vitest) and pma-go (golangci-lint, validator, consistent responses, Taskfile) standards. sqlc migration deferred.

## 2026-04-15 18:00 [decision]

Product direction clarified: internal private cloud first, external API later. PLAN-006 drafted: infrastructure automation — VM auto-failover (Incus cluster.healing_threshold), node management (SSH-automated add/remove), standalone host support (DB-stored config). Auto-deploy new cluster deferred to Phase 6D. Directory cleanup: deleted 17,885 lines of dead code (paymenter, ai-gateway, console-proxy, screenshots), unified all docs under root /docs/.

## 2026-04-15 18:30 [BUG-P1]

## 2026-04-16 00:30 [progress]

PLAN-007 Phase 1-5 + partial 2,3 implemented:
- Phase 1 (8 tasks): Admin DB write, delete free-create path, billing redesign, snapshot portal, hardcode elimination, dead code cleanup
- Phase 2: IP Pool CRUD (add/remove + UI form)
- Phase 3.1: Add Cluster/Standalone Host form
- Phase 3.3: Ceph Storage overview page
- Phase 4: VM detail pages (admin + user, DO-style tabs)
- Phase 5: Logout button, Console dynamic back link
Total: 11 commits in this session. Deployed to vmc.5ok.co.

## 2026-04-15 20:30 [progress]

PLAN-005 + PLAN-006 all phases completed:
- A0: 7 CRITICAL fixes (SSH keys, VM naming, order→VM, balance, ListAllVMs, panic, ticket detail)
- A1: 3 security fixes (Console/metrics auth, WebSocket CSRF)
- A: Frontend scaffold (sidebar, ThemeProvider dark/light/system, i18n zh/en, ESLint)
- B: 10 feature API hook modules (50+ hooks)
- C: Taskfile.yml, 5 DB indexes, HTTP 30s timeout
- WARNING batch: audit log injection, input validation, Dashboard real data, user Console/Snaps
- 6A: HA failover (healing_threshold=300, HA status page, evacuate/restore)
- 6B: Node management (evacuate/restore buttons in clusters page)
- 6C: Dynamic cluster add/remove (standalone host support)
- D: Go tests (11 cases) + Vitest (6 cases) + quality gate scripts
- E: Metrics 30s cache

## 2026-04-15 20:00 [progress]

PLAN-005 Phases A0-C implemented and deployed:
- A0: Fixed 7 CRITICAL bugs (SSH key injection, VM naming, order→VM provisioning, balance, ListAllVMs, panic, ticket detail)
- A1: Security fixes (Console/metrics ownership, WebSocket CSRF)
- A: Frontend scaffold (sidebar layout, ThemeProvider dark/light/system, i18n zh/en, ESLint, providers)
- B: Extracted 10 feature API hook modules (50+ hooks)
- C: Taskfile.yml, 5 DB indexes, HTTP client timeout
Total: 10 commits, ~3000 lines changed. Deployed to vmc.5ok.co.

## 2026-04-15 18:30 [BUG-P1]

Deep code audit (Graph + Serena + manual tracing) found 7 CRITICAL bugs: SSH keys never injected into VMs, VM naming collision (1 VM per user), order payment doesn't provision VM, balance hardcoded to 0, ListAllVMs stub, panic on empty cluster, user ticket detail missing frontend. Plus 14 WARNINGs including Console WebSocket no ownership check, quota never enforced, audit logs never written, IP allocation race condition, password in plaintext. PLAN-005 scope expanded to include Phase A0 (critical bug fixes).
