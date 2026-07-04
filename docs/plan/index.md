# Incus Cloud Platform - Plan Index

> Updated: 2026-05-09

## Usage

Each plan is a single line linking to its detail file. All detailed information lives in `docs/plan/PLAN-NNN.md`.

### Format

- [ ] [**PLAN-001 Short plan title**](PLAN-001.md) `YYYY-MM-DD`

### Status Markers

| Marker | Meaning |
|--------|---------|
| `[ ]`  | Draft / Pending review |
| `[-]`  | Approved / Implementing |
| `[x]`  | Completed |
| `[~]`  | Rejected / Abandoned |

### Rules

- Only update the checkbox marker; never delete the line.
- New plans append to the end.
- See each `PLAN-NNN.md` for full details.

---

## Plans

- [x] [**PLAN-001 Single-node Incus setup with public IP bridge**](PLAN-001.md) `2026-04-02`
- [x] [**PLAN-002 Cluster Infrastructure — Incus + Ceph + Network + Monitoring**](PLAN-002.md) `2026-04-09`
- [~] [**PLAN-003 Business Platform — Paymenter + Incus Extension + Billing**](PLAN-003.md) `2026-04-09`
- [x] [**PLAN-004 IncusAdmin — Self-hosted Cloud Platform Management System**](PLAN-004-incus-admin.md) `2026-04-15`
- [x] [**PLAN-005 Full-stack refactor to pma-web and pma-go standards**](PLAN-005.md) `2026-04-15`
- [x] [**PLAN-006 Infrastructure automation — HA failover, node management, standalone host**](PLAN-006.md) `2026-04-15`
- [x] [**PLAN-007 UI/UX completeness — compete with DigitalOcean/Vultr/Hetzner**](PLAN-007.md) `2026-04-15`
- [x] [**PLAN-008 QA fixes + three-persona user journey gaps**](PLAN-008.md) `2026-04-16`
- [x] [**PLAN-009 Code review follow-up — PLAN-008 findings**](PLAN-009.md) `2026-04-16`
- [x] [**PLAN-010 Production QA bug fixes (post-PLAN-009)**](PLAN-010.md) `2026-04-16`
- [x] [**PLAN-011 Frontend pma-web compliance cleanup**](PLAN-011.md) `2026-04-17`
- [x] [**PLAN-012 后端核算收尾 —— PayWithBalance 回滚集成测试 + TopUp 日额度**](PLAN-012.md) `2026-04-17`
- [x] [**PLAN-013 历史延期项全量清零 —— PLAN-009 架构级 + PLAN-010 反代 + PLAN-011 收尾 + PLAN-012 限制**](PLAN-013.md) `2026-04-17`
- [~] [**PLAN-014 VM 状态反向同步 worker —— 消除 DB ↔ Incus 漂移**](PLAN-014.md) `2026-04-18` *(已合并到 PLAN-020)*
- [x] [**PLAN-015 QA-004 后续修复 —— N1-N15 清单**](PLAN-015.md) `2026-04-18`
- [x] [**PLAN-016 后台菜单重组 + 用户/管理员视角分离**](PLAN-016.md) `2026-04-18`
- [x] [**PLAN-017 QA-006 用户端 E2E N1-N11 修复**](PLAN-017.md) `2026-04-18`
- [x] [**PLAN-018 用户端功能缺口填补(G1-G5)**](PLAN-018.md) `2026-04-18`
- [x] [**PLAN-019 安全与审计基线（Step-up / 审计全覆盖 / Token TTL / Shadow Login）**](PLAN-019.md) `2026-04-19`
- [x] [**PLAN-020 HA 真正化 + VM 状态反向同步（合并 PLAN-014）**](PLAN-020.md) `2026-04-19`
- [x] [**PLAN-021 运维 Must-Have（多 OS / 重装 / Rescue / 防火墙组 / 新段 / Floating IP）**](PLAN-021.md) `2026-04-24`
- [x] [**PLAN-022 前端 Linear 重设计 + 交互范式重构**](PLAN-022.md) `2026-04-29`
- [x] [**PLAN-023 后端批量操作 API（多选删除/启停 etc.）**](PLAN-023.md) `2026-04-29`
- [x] [**PLAN-024 Linear 风交互三件套（浮层 toolbar / 列宽持久化 / 行内详情抽屉）**](PLAN-024.md) `2026-04-30`
- [x] [**PLAN-025 VM provisioning 异步化 + SSE 进度流**](PLAN-025.md) `2026-04-30`
- [x] [**PLAN-026 集群节点管理 UI + 自动化（INFRA-002 落地）**](PLAN-026.md) `2026-04-30`
- [x] [**PLAN-027 Standalone Incus host 管理 + DB-driven cluster config（INFRA-003）**](PLAN-027.md) `2026-04-30`
- [x] [**PLAN-028 join-node.sh 兼容 bonded NIC + skip-network（OPS-026）**](PLAN-028.md) `2026-04-30`
- [x] [**PLAN-029 admin/vms 跨 project 列表 0 VM 修复（OPS-027）**](PLAN-029.md) `2026-04-30`
- [x] [**PLAN-030 全功能 UI 测试遗留打磨（OPS-028）**](PLAN-030.md) `2026-04-30`
- [x] [**PLAN-031 EN 语言包系统化补全（OPS-029）**](PLAN-031.md) `2026-04-30`
- [x] [**PLAN-032 DESIGN.md 严格合规清零（OPS-030）**](PLAN-032.md) `2026-05-01`
- [x] [**PLAN-033 添加节点自动化 —— 凭据多形态 + 自动探测向导（OPS-039）**](PLAN-033.md) `2026-05-03`
- [x] [**PLAN-037 VM 部署位置可观测 + 后台批量迁移（OPS-040）**](PLAN-037.md) `2026-05-06`
- [x] [**PLAN-038 节点加入 + 集群部署 AI 辅助（OPS-041）**](PLAN-038.md) `2026-05-06`
- [x] [**PLAN-039 调度三件套：多维 PickNode + Live Migration + 不均衡 Watchdog（OPS-042/043/044）**](PLAN-039.md) `2026-05-06`
- [~] [**PLAN-040 备份与灾备一期 —— 策略 / 保留 / S3 后端（INFRA-008）**](PLAN-040.md) `2026-05-09`
- [x] [**PLAN-041 监控告警闭环 —— Prometheus 端点 + 阈值规则 + Webhook 通道（INFRA-009）**](PLAN-041.md) `2026-05-09`
- [x] [**PLAN-042 OpenAPI 规范 + Terraform Provider（INFRA-010）**](PLAN-042.md) `2026-05-09`
- [x] [**PLAN-043 一键 Bootstrap CLI —— 5 分钟出私有云（INFRA-011）**](PLAN-043.md) `2026-05-09`
- [~] [**PLAN-051 全平台收口包 —— Session-1/2/3 + QA-009 一次性闭环（71/74 项落地，余项→OPS-047）**](PLAN-051.md) `2026-05-09`
- [ ] [**PLAN-053 cloud-gateway /v1 适配层（INFRA-012）**](PLAN-053.md) `2026-05-26`
- [ ] [**PLAN-054 按天付费 billing engine（INFRA-013）**](PLAN-054.md) `2026-05-26`
- [~] [**PLAN-055 深度代码审查整改批量修复（OPS-052）**](PLAN-055.md) `2026-07-04`
