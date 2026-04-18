# IncusAdmin Changelog

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
