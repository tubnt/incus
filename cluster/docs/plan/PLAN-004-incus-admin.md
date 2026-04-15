# PLAN-004 IncusAdmin — Self-hosted Cloud Platform Management System

- **status**: implementing
- **createdAt**: 2026-04-15 02:00
- **approvedAt**: 2026-04-15 09:00
- **relatedTask**: ADMIN-001, ADMIN-002, ADMIN-003, ADMIN-004

## 背景

当前 Paymenter v1.4.7 + Incus Extension 完成了基本的 VM 售卖链路，但存在以下限制：
- Paymenter 是 PHP/Laravel 生态，管理端和售卖端耦合
- 旧 Extension 代码（50+ 文件）接口不兼容 v1.4.7，需要重写
- 不支持多集群管理
- 无法区分内部使用和外部售卖
- 运维能力弱（无节点管理、容量规划、批量操作）

## 目标

构建 **IncusAdmin** — 自研的 Incus 集群云平台管理系统：

1. **管理运维一体**：集群管理 + VM 管理 + 监控告警 + 用户权限
2. **多集群**：管理多个 Incus 集群（内部专用 / 公共售卖 / 不同机房）
3. **多售卖端**：通过统一 API 支持多个前端品牌
4. **内部优先**：公司员工自助使用，多余容量对外售卖

## 架构

```
┌──────────────────────────────────────────────────────────────┐
│                   IncusAdmin（Go 单体服务）                     │
│                                                               │
│  ┌─ HTTP API ────────────────────────────────────────────┐   │
│  │  /api/admin/*    Admin API (admin role)                │   │
│  │  /api/portal/*   Portal API (all authenticated users) │   │
│  └───────────────────────────────────────────────────────┘   │
│                                                               │
│  ┌─ 核心模块 ────────────────────────────────────────────┐   │
│  │  cluster    多集群连接管理（Incus client SDK）          │   │
│  │  vm         VM 生命周期（创建/迁移/快照/重装/删除）       │   │
│  │  network    IP 池 / VLAN / 防火墙 / rDNS               │   │
│  │  storage    存储池 / 磁盘管理 / Ceph 状态               │   │
│  │  billing    订单 / 计费 / 支付网关 / 内部记账            │   │
│  │  auth       用户 / 角色 / RBAC / API Token / SSO       │   │
│  │  monitor    Prometheus 查询 / Grafana 嵌入 / 告警       │   │
│  │  console    WebSocket 终端代理（xterm.js ↔ Incus console）│   │
│  └───────────────────────────────────────────────────────┘   │
│                                                               │
│  ┌─ 数据库 ──────────────────────────────────────────────┐   │
│  │  PostgreSQL                                            │   │
│  │  表：users, clusters, vms, ips, orders, invoices...    │   │
│  └───────────────────────────────────────────────────────┘   │
└──────────┬───────────────┬───────────────┬───────────────────┘
           │               │               │
     Incus API        Prometheus      Ceph API
     (mTLS)           (HTTP)          (REST)
           │               │               │
    ┌──────┴──────┐  ┌─────┴────┐  ┌──────┴──────┐
    │ Cluster A   │  │ Grafana  │  │ Ceph MON    │
    │ 5 nodes     │  │ Loki     │  │ 29 OSD      │
    │ internal    │  │ Alert    │  │ 25TiB       │
    └─────────────┘  └──────────┘  └─────────────┘
    ┌─────────────┐
    │ Cluster B   │  (未来扩展)
    │ HK/US nodes │
    └─────────────┘
```

## 前端

```
web/
├── admin/              # 管理运维面板（React）
│   ├── Dashboard       集群概览 / 节点状态 / 资源用量
│   ├── Clusters        多集群列表 / 切换
│   ├── VMs             VM 列表 / 详情 / 操作 / 创建
│   ├── Network         IP 池 / VLAN / 防火墙规则
│   ├── Storage         存储池 / 磁盘 / Ceph 状态
│   ├── Users           用户管理 / 角色 / 权限
│   ├── Billing         订单 / 发票 / 收入统计
│   ├── Monitor         嵌入 Grafana Dashboard
│   └── Settings        系统配置 / 集群连接 / 支付网关
│
└── portal/             # 售卖端面板（React，可多品牌）
    ├── Home            产品展示
    ├── Products        VPS 规格选择 / 配置器
    ├── Checkout        购物车 / 支付
    ├── Dashboard       用户 Dashboard
    ├── Services        VM 列表 / 详情 / 操作
    ├── Console         Web 终端（xterm.js）
    ├── Billing         账单 / 发票
    ├── Tickets         工单
    └── Account         个人信息 / SSH Key / API Token
```

## 技术栈

| 层 | 技术 | 原因 |
|----|------|------|
| 后端 | Go 1.23+ | Incus 官方 client SDK；单二进制部署 |
| HTTP | Chi 或 stdlib | 轻量路由 |
| 数据库 | PostgreSQL | sqlc 生成类型安全查询 |
| 认证 | Logto SSO (OIDC) + JWT | 统一身份，支持 API Token |
| 前端 | React 19 + TypeScript + Vite | 现代 SPA |
| UI | shadcn/ui + Tailwind CSS v4 | 美观 + 可定制 |
| 路由 | TanStack Router | 类型安全文件路由 |
| 状态 | TanStack Query + Zustand | 服务端状态 + 客户端状态 |
| 终端 | xterm.js | Web 终端 |
| 图表 | Recharts 或嵌入 Grafana | 监控可视化 |
| 部署 | 单二进制（嵌入前端静态文件） | `go:embed` |

## 项目结构

```
incus-admin/
├── cmd/
│   └── server/
│       └── main.go              # 入口
├── internal/
│   ├── api/
│   │   ├── admin/               # 管理端 handlers
│   │   ├── portal/              # 售卖端 handlers
│   │   ├── middleware/           # 认证 / 限流 / CORS
│   │   └── router.go            # 路由注册
│   ├── cluster/
│   │   ├── manager.go           # 多集群连接管理
│   │   ├── client.go            # Incus client 封装
│   │   └── scheduler.go         # 节点调度（负载均衡选节点）
│   ├── vm/
│   │   ├── lifecycle.go         # 创建 / 启动 / 停止 / 删除
│   │   ├── migrate.go           # 迁移
│   │   ├── snapshot.go          # 快照
│   │   ├── reinstall.go         # 重装系统
│   │   └── console.go           # WebSocket 终端代理
│   ├── network/
│   │   ├── ippool.go            # IP 池 CRUD + 自动分配
│   │   ├── firewall.go          # VM 防火墙规则
│   │   └── rdns.go              # 反向 DNS
│   ├── storage/
│   │   ├── pool.go              # 存储池管理
│   │   └── ceph.go              # Ceph 状态查询
│   ├── billing/
│   │   ├── order.go             # 订单管理
│   │   ├── invoice.go           # 发票生成
│   │   ├── payment.go           # 支付网关（Stripe）
│   │   └── internal.go          # 内部记账（无需支付）
│   ├── auth/
│   │   ├── logto.go             # Logto OIDC 授权码流程（登录/回调/登出）
│   │   ├── session.go           # Session cookie 管理
│   │   ├── user.go              # 用户同步 + CRUD
│   │   ├── role.go              # 角色 / RBAC
│   │   └── apitoken.go          # API Token 签发 / 验证
│   ├── monitor/
│   │   ├── prometheus.go        # PromQL 查询
│   │   └── alert.go             # 告警管理
│   ├── store/
│   │   ├── db.go                # 数据库连接
│   │   ├── migrations/          # SQL 迁移
│   │   └── queries/             # sqlc 查询
│   └── config/
│       └── config.go            # 配置加载（koanf）
├── web/
│   ├── admin/                   # React 管理面板
│   └── portal/                  # React 售卖面板
├── deploy/
│   ├── Dockerfile
│   ├── docker-compose.yml
│   └── systemd/
└── docs/
```

## 多集群配置

```yaml
# config.yaml
server:
  listen: ":8080"
  domain: "admin.incuscloud.com"

database:
  driver: postgres
  dsn: "${DATABASE_URL}"  # postgres://incusadmin:xxx@localhost/incusadmin

clusters:
  - name: "cn-sz-01"
    display_name: "深圳机房 A"
    api_url: "https://10.0.20.1:8443"
    cert_file: "/etc/incus-admin/certs/sz01-client.crt"
    key_file: "/etc/incus-admin/certs/sz01-client.key"
    projects:
      - name: "default"
        access: "internal"
        description: "内部研发环境"
      - name: "customers"
        access: "public"
        description: "公共售卖"
    ip_pools:
      - cidr: "202.151.179.224/27"
        gateway: "202.151.179.225"
        range: "202.151.179.234-202.151.179.254"
        vlan: 376

  - name: "hk-01"
    display_name: "香港机房"
    api_url: "https://wg-hk:8443"
    # ...

auth:
  # IncusAdmin does NOT handle OIDC directly — oauth2-proxy does.
  # IncusAdmin reads X-Auth-Email header from oauth2-proxy.
  admin_emails: "${ADMIN_EMAILS}"          # auto-promote to admin on first login
  session_secret: "${SESSION_SECRET}"
  session_ttl: "24h"
  emergency_token: "${EMERGENCY_TOKEN}"    # 64-char fallback when Logto is down

billing:
  stripe_key: "${STRIPE_SECRET_KEY}"
  currency: "USD"

monitor:
  prometheus_url: "http://localhost:9090"
  grafana_url: "http://localhost:3000"
```

## User Roles (Phase 1: 2 roles only)

```
admin
  └── Full access: clusters, all VMs, users, billing, IP pools, monitoring, settings

customer (default)
  └── Own VMs only: create, manage, console, snapshots, tickets, account
  └── No billing limits in Phase 1 (internal use)
  └── Balance-based billing added in Phase 2
```

Future roles (add when needed): `operator` (VM ops + monitoring, no billing), `viewer` (read-only).

## API Design

Auth is handled entirely by oauth2-proxy + Logto. IncusAdmin reads `X-Auth-Email`
header. No `/auth/login` or `/auth/register` endpoints — those happen at Logto.

```
System endpoints (no auth):
  GET    /api/health                  Health check
  GET    /auth/emergency              Emergency login form (skip oauth2-proxy)
  POST   /auth/emergency              Verify emergency token → issue session
  GET    /auth/me                     Current user info (from proxy header)

Portal API (/api/portal/) — all users:
  GET    /products                    Product list (filtered by role/region)
  GET    /products/:slug              Product detail
  POST   /orders                      Place order (deduct balance)
  GET    /services                    My services (VMs)
  GET    /services/:id                Service detail (VM info: IP/user/pass)
  POST   /services/:id/actions/:act   VM action (start/stop/restart)
  WS     /services/:id/console        WebSocket terminal
  POST   /services/:id/reinstall      Reinstall OS
  GET    /services/:id/snapshots      List snapshots
  POST   /services/:id/snapshots      Create snapshot
  DELETE /services/:id/snapshots/:sid Delete snapshot
  POST   /services/:id/snapshots/:sid/restore  Restore snapshot
  GET    /billing/balance             My balance
  GET    /billing/invoices            My invoices
  GET    /billing/transactions        My transactions
  GET    /ssh-keys                    My SSH keys
  POST   /ssh-keys                    Add SSH key
  DELETE /ssh-keys/:id                Delete SSH key
  GET    /tickets                     My tickets
  POST   /tickets                     Create ticket
  GET    /tickets/:id                 Ticket detail
  POST   /tickets/:id/messages        Reply to ticket
  GET    /account                     My profile
  PUT    /account                     Update profile
  GET    /account/api-tokens          My API tokens
  POST   /account/api-tokens          Create API token
  DELETE /account/api-tokens/:id      Revoke API token

Admin API (/api/admin/) — admin role only:
  GET    /clusters                    List all clusters
  GET    /clusters/:id                Cluster detail
  GET    /clusters/:id/nodes          Node list
  GET    /clusters/:id/nodes/:name    Node detail (resources)
  GET    /clusters/:id/vms            All VMs in cluster
  POST   /clusters/:id/vms            Create VM (admin, skip billing)
  GET    /vms                         All VMs across clusters
  GET    /vms/:id                     VM detail
  PUT    /vms/:id/state               Change VM state (start/stop/force-stop)
  POST   /vms/:id/migrate             Migrate VM to another node
  DELETE /vms/:id                     Delete VM
  POST   /vms/bulk-action             Bulk start/stop/delete
  GET    /ip-pools                    List IP pools
  POST   /ip-pools                    Create IP pool
  PUT    /ip-pools/:id                Update IP pool
  GET    /ip-pools/:id/addresses      List IPs in pool
  GET    /users                       List all users
  GET    /users/:id                   User detail
  PUT    /users/:id/role              Change user role
  POST   /users/:id/balance           Top up user balance
  GET    /users/:id/quota             Get user quota
  PUT    /users/:id/quota             Update user quota
  GET    /products                    All products
  POST   /products                    Create product
  PUT    /products/:id                Update product
  DELETE /products/:id                Delete product
  GET    /orders                      All orders
  GET    /invoices                    All invoices
  GET    /monitor/dashboard           Cluster metrics (Prometheus)
  GET    /audit-log                   Audit log
  GET    /tickets                     All tickets
  PUT    /tickets/:id/status          Close/assign ticket
```

## Database Schema

```sql
-- Clusters (loaded from YAML config, DB stores runtime state)
CREATE TABLE clusters (
    id           SERIAL PRIMARY KEY,
    name         TEXT UNIQUE NOT NULL,
    display_name TEXT,
    api_url      TEXT NOT NULL,
    status       TEXT DEFAULT 'active',
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Users (no password — all auth via Logto SSO)
CREATE TABLE users (
    id           SERIAL PRIMARY KEY,
    email        TEXT UNIQUE NOT NULL,
    name         TEXT,
    role         TEXT DEFAULT 'customer',  -- customer / admin
    logto_sub    TEXT UNIQUE,              -- Logto ID token `sub` claim
    balance      DECIMAL(10,2) DEFAULT 0,  -- account balance
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Quotas (per user, multi-dimension)
CREATE TABLE quotas (
    id            SERIAL PRIMARY KEY,
    user_id       INT UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    max_vms       INT DEFAULT 5,
    max_vcpus     INT DEFAULT 8,
    max_ram_mb    INT DEFAULT 16384,
    max_disk_gb   INT DEFAULT 200,
    max_ips       INT DEFAULT 3,
    max_snapshots INT DEFAULT 10
);

-- SSH Keys
CREATE TABLE ssh_keys (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    public_key   TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- VMs (tracks Incus instances ↔ users/orders)
CREATE TABLE vms (
    id           SERIAL PRIMARY KEY,
    name         TEXT NOT NULL,            -- incus instance name (e.g. vm-42)
    cluster_id   INT REFERENCES clusters(id),
    user_id      INT REFERENCES users(id),
    order_id     INT REFERENCES orders(id),
    ip           INET,
    status       TEXT DEFAULT 'creating',  -- creating/running/stopped/suspended/error/deleted
    cpu          INT,
    memory_mb    INT,
    disk_gb      INT,
    os_image     TEXT,
    node         TEXT,                     -- cluster node name
    password     TEXT,                     -- initial password (encrypted at rest)
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Snapshots
CREATE TABLE snapshots (
    id           SERIAL PRIMARY KEY,
    vm_id        INT REFERENCES vms(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    size_bytes   BIGINT DEFAULT 0,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- IP Pools
CREATE TABLE ip_pools (
    id           SERIAL PRIMARY KEY,
    cluster_id   INT REFERENCES clusters(id),
    cidr         CIDR NOT NULL,
    gateway      INET NOT NULL,
    vlan_id      INT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE ip_addresses (
    id           SERIAL PRIMARY KEY,
    pool_id      INT REFERENCES ip_pools(id),
    ip           INET UNIQUE NOT NULL,
    vm_id        INT REFERENCES vms(id),
    status       TEXT DEFAULT 'available', -- available/assigned/reserved/cooldown
    cooldown_until TIMESTAMPTZ,            -- when cooldown expires
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Products
CREATE TABLE products (
    id             SERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    slug           TEXT UNIQUE,
    cpu            INT,
    memory_mb      INT,
    disk_gb        INT,
    bandwidth_tb   INT,
    price_monthly  DECIMAL(10,2),
    access         TEXT DEFAULT 'public',   -- public / internal
    active         BOOLEAN DEFAULT TRUE,
    sort_order     INT DEFAULT 0,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    updated_at     TIMESTAMPTZ DEFAULT NOW()
);

-- Product ↔ Cluster availability (join table, replaces INT[])
CREATE TABLE product_clusters (
    product_id   INT REFERENCES products(id) ON DELETE CASCADE,
    cluster_id   INT REFERENCES clusters(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, cluster_id)
);

-- Orders (lifecycle: pending → paid → provisioning → active → expired → cancelled)
CREATE TABLE orders (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id),
    product_id   INT REFERENCES products(id),
    cluster_id   INT REFERENCES clusters(id),
    status       TEXT DEFAULT 'pending',
    amount       DECIMAL(10,2),
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Invoices (N:1 with orders — initial + renewals)
CREATE TABLE invoices (
    id           SERIAL PRIMARY KEY,
    order_id     INT REFERENCES orders(id),
    user_id      INT REFERENCES users(id),
    amount       DECIMAL(10,2),
    status       TEXT DEFAULT 'unpaid',    -- unpaid / paid / cancelled
    due_at       TIMESTAMPTZ,
    paid_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Transactions (balance ledger — deposits and deductions)
CREATE TABLE transactions (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id),
    amount       DECIMAL(10,2) NOT NULL,   -- positive = deposit, negative = deduction
    type         TEXT NOT NULL,            -- deposit / purchase / renewal / refund
    description  TEXT,
    invoice_id   INT REFERENCES invoices(id),
    created_by   INT REFERENCES users(id), -- admin who made the deposit
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Tickets
CREATE TABLE tickets (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id),
    subject      TEXT NOT NULL,
    status       TEXT DEFAULT 'open',      -- open / replied / closed
    priority     TEXT DEFAULT 'normal',    -- low / normal / high
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE ticket_messages (
    id           SERIAL PRIMARY KEY,
    ticket_id    INT REFERENCES tickets(id) ON DELETE CASCADE,
    user_id      INT REFERENCES users(id),
    body         TEXT NOT NULL,
    is_staff     BOOLEAN DEFAULT FALSE,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Audit Log
CREATE TABLE audit_logs (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id),
    action       TEXT NOT NULL,            -- vm.create, user.role_change, balance.deposit, etc.
    target_type  TEXT,                     -- vm / user / order / etc.
    target_id    INT,
    details      JSONB,
    ip_address   INET,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

-- API Tokens
CREATE TABLE api_tokens (
    id           SERIAL PRIMARY KEY,
    user_id      INT REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT UNIQUE NOT NULL,     -- SHA-256 of the token
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);
```

## 开发计划

### Phase 1：后端 MVP（1-2 周）

- [ ] Go 项目脚手架（cmd/internal/config）
- [ ] 数据库 migration（sqlc + PostgreSQL）
- [ ] 多集群 Incus client 管理
- [ ] VM CRUD API（创建/列表/详情/操作）
- [ ] IP 池自动分配/回收
- [ ] 用户认证（Logto SSO OIDC + session cookie + API Token）
- [ ] cloud-init 模板（静态 IP + SSH Key + 密码）
- [ ] 节点调度（按负载选节点）
- [ ] 基本 RBAC（admin/customer）

### Phase 2：管理面板前端（1 周）

- [ ] React + Vite + shadcn/ui 脚手架
- [ ] 登录页
- [ ] Dashboard（集群概览 / 节点状态 / VM 统计）
- [ ] VM 管理（列表/详情/创建/操作）
- [ ] IP 池管理
- [ ] 用户管理
- [ ] 嵌入 Grafana 监控

### Phase 3：售卖端前端（1 周）

- [ ] 用户注册/登录
- [ ] 产品浏览 + 配置选择器（CPU/RAM/磁盘/OS/SSH Key）
- [ ] 购物车 + Checkout
- [ ] Stripe 支付集成
- [ ] 用户 Dashboard + VM 管理
- [ ] 账单/发票

### Phase 4：功能补全（持续）

- [ ] Web Console（xterm.js + Incus console API）
- [ ] 快照管理
- [ ] 重装系统
- [ ] 防火墙 UI
- [ ] 流量监控
- [ ] 多 OS 镜像选择
- [ ] 内部审批流（员工申请 VM）
- [ ] rDNS 管理
- [ ] 自动续费 + 到期处理
- [ ] SSO 集成
- [ ] 多售卖端品牌支持

## 与现有系统的关系

- **Incus 集群**：继续使用，IncusAdmin 通过 mTLS 连接 Incus REST API
- **Ceph 存储**：继续使用，IncusAdmin 查询 Ceph 状态
- **监控栈**：继续使用，Grafana 嵌入管理面板
- **nftables 防火墙**：继续使用，IncusAdmin 可通过 API 管理规则
- **Paymenter**：废弃，被 IncusAdmin 的售卖端替代
- **旧 Extension 代码**：废弃，逻辑参考重写为 Go
- **运维文档**：保留更新

---

## 调研补充（2026-04-15）

### 行业配额对标

| 平台 | 默认配额 | 维度 |
|------|----------|------|
| Hetzner | 8 vCPU / 账户 | vCPU 总量 |
| DigitalOcean | 25 Droplets / 账户 | 实例数 + 同时创建速率 |
| Vultr | 月消费额度 ~$100 | 费用上限 |
| OpenStack | 10 instances / 20 vCPU / 50GB RAM | 多维度 per project |
| Google Cloud | 24 vCPU / 区域 | vCPU + GPU + IP + 磁盘 |

### 确认设计决策

#### 1. 内部员工 = 外部用户，统一余额计费

```
管理员 → 后台给员工充余额
员工   → 余额购买 VM（自动扣费）
外部客户 → Stripe 充余额 → 余额购买 VM（Phase 3 才做）
```

计费逻辑统一，不分两套流程。内部/外部只是**余额来源不同**和**可见产品不同**。

#### 2. 计费分层实施

```
Phase 1: 管理员手动充值余额 → 余额自动扣费（内部先跑起来）
Phase 2: 自动续费 + 到期暂停/删除
Phase 3: Stripe 在线充值（对外售卖时接入）
```

#### 3. 多维度配额

```sql
CREATE TABLE quotas (
    id            SERIAL PRIMARY KEY,
    user_id       INT UNIQUE REFERENCES users(id),
    max_vms       INT DEFAULT 5,
    max_vcpus     INT DEFAULT 8,
    max_ram_mb    INT DEFAULT 16384,     -- 16 GB
    max_disk_gb   INT DEFAULT 200,
    max_ips       INT DEFAULT 3,
    max_snapshots INT DEFAULT 10
);
```

创建 VM 时检查所有维度，任一超限则拒绝并提示具体哪个配额不够。

#### 4. 安全隔离（公有云标准）

| 层 | 措施 | 状态 |
|----|------|------|
| 计算 | QEMU/KVM 硬件虚拟化 | ✅ 已有 |
| 网络 L2 | MAC/IP 过滤 + port_isolation | ✅ 需确认所有 VM |
| 网络 L3 | RFC1918 阻断 + 元数据阻断 (169.254) | ⚠️ 元数据阻断待加 |
| 存储 | Ceph RBD 独立 volume + dmcrypt | ✅ 已有 |
| API | JWT RBAC + 用户只能操作自己的资源 | 待实现 |
| 管理平面 | VM 不能访问管理网/Ceph 网 | ✅ 已有 |

### 参考来源

- [Droplet Limits - DigitalOcean](https://docs.digitalocean.com/products/droplets/details/limits/)
- [Manage Account Limits - Vultr](https://docs.vultr.com/platform/billing/manage-account-limits)
- [FAQ - Hetzner Cloud](https://docs.hetzner.com/cloud/servers/faq/)
- [Quota Management - Red Hat OpenStack](https://docs.redhat.com/en/documentation/red_hat_openstack_platform/15/html/users_and_identity_management_guide/quota_management)
- [Allocation quotas - Google Cloud](https://cloud.google.com/compute/resource-usage)
- [Firewall Isolation with nftables on Proxmox VE 9](https://www.dataplugs.com/en/firewall-isolation-techniques-using-nftables-on-proxmox-ve-9/)
- [Incus - Linux Containers](https://linuxcontainers.org/incus/)
- [Azure Public Cloud Isolation](https://learn.microsoft.com/en-us/azure/security/fundamentals/isolation-choices)

---

## 部署架构（确认 2026-04-15）

```
User Browser
  │ HTTPS
  ▼
Cloudflare CDN (vmc.5ok.co)
  │ HTTP
  ▼
oauth2-proxy (:4180)
  │ X-Auth-Email header
  ▼
IncusAdmin Go (:8080)
  ├── PostgreSQL
  └── WireGuard tunnel (10.100.0.1)
       │
       ▼
Incus Cluster node1 (10.100.0.10 / 10.0.20.1:8443)
  ├── 5-node Incus + Ceph
  ├── Prometheus + Grafana
  └── br-pub VLAN 376
```

### 关键端点

| 端点 | 说明 |
|------|------|
| `https://vmc.5ok.co/` | 用户/管理面板入口 |
| `https://vmc.5ok.co/auth/callback` | Logto SSO 回调 |
| `https://vmc.5ok.co/api/admin/*` | 管理 API |
| `https://vmc.5ok.co/api/portal/*` | 售卖端 API |
| `https://auth.l.5ok.co/` | Logto 身份认证 |

## Authentication Architecture (Confirmed)

### Flow: oauth2-proxy + Logto OIDC + Emergency Fallback

```
Normal path:
  User → CF CDN (HTTPS) → oauth2-proxy (:4180)
    → Redirect to Logto login
    → Logto authenticates + verifies org 23hldzpnetw6
    → Callback → oauth2-proxy sets cookie
    → Proxy to IncusAdmin (:8080) with X-Auth-Email header
    → IncusAdmin finds/creates local user → serves page by role

Emergency path (Logto down):
  Admin → SSH tunnel to server → http://localhost:8081/auth/emergency
    → IncusAdmin secondary port (:8081, localhost only, no oauth2-proxy)
    → Shows token input form
    → Admin enters EMERGENCY_TOKEN (from .env)
    → Verifies token + checks ADMIN_EMAILS
    → Issues admin session directly on :8081
    → Admin can manage cluster via localhost:8081
```

### oauth2-proxy Configuration

```ini
provider = "oidc"
client_id = "${LOGTO_CLIENT_ID}"
client_secret = "${LOGTO_CLIENT_SECRET}"
oidc_issuer_url = "https://auth.l.5ok.co/oidc"
allowed_groups = ["23hldzpnetw6"]
set_xauthrequest = true
pass_access_token = true
proxy_websockets = true                    # required for VM console WebSocket
skip_auth_routes = ["/api/health"]         # emergency login on :8081, not here
upstreams = ["http://127.0.0.1:8080"]
http_address = "0.0.0.0:4180"
cookie_secure = false                      # CF CDN terminates HTTPS
```

### Role Assignment

1. First login via SSO → create local user with role `customer`
2. If email matches `ADMIN_EMAILS` env → auto-promote to `admin`
3. Admin can manually change other users' roles in the dashboard
4. `customer` role: create/manage own VMs, no billing limits, no admin access
5. `admin` role: full access (cluster management, all VMs, users, billing)

### Environment Variables

```bash
LOGTO_CLIENT_ID=q8yhergm6ljnm5x1zlip0
LOGTO_CLIENT_SECRET=qSSossrHfr1e18Npw7ii6310c0r2mLhW
LOGTO_ISSUER=https://auth.l.5ok.co/oidc
LOGTO_ORG_ID=23hldzpnetw6
ADMIN_EMAILS=ai@5ok.co
SESSION_SECRET=<random-generated>
EMERGENCY_TOKEN=<random-64-char>
DATABASE_URL=postgres://incusadmin:xxx@localhost/incusadmin
```

### Frontend: Single SPA (Confirmed)

One React application at `vmc.5ok.co`. Menu items rendered by user role:

| Role | Visible Menu |
|------|-------------|
| customer | My VMs, Create VM, Tickets, Account |
| admin | + Clusters, All VMs, Users, Billing, IP Pools, Monitoring, Settings |

## Performance Considerations

| Area | Issue | Mitigation |
|------|-------|------------|
| VM creation | Image download on first create: 30s+ | Pre-cache images on all nodes during setup |
| Node scheduling | Per-create API queries to all nodes is slow | Cache node resources in memory, refresh every 60s |
| VM list (admin) | N API calls for N VMs across clusters | Use Incus `?recursion=2` to batch, cache 10s TTL |
| WireGuard latency | ~30ms RTT per Incus API call (SG↔TW) | Cache aggressively for read operations |
| VM status sync | DB status can drift from Incus reality | Background goroutine reconciles every 60s, or query Incus on detail page with short cache |

## Review Findings Log (2026-04-15)

Issues found during user journey audit, tracked for implementation:

**Fixed in this revision:**
- Removed dead `/api/portal/auth/*` endpoints (auth is via Logto, not IncusAdmin)
- Removed `/api/internal/*` namespace (internal = external, unified flow)
- Removed `password` column from users table
- Renamed `sso_id` to `logto_sub`
- Simplified roles: 5 → 2 (customer / admin)
- Added missing tables: quotas, ssh_keys, snapshots, transactions, tickets, ticket_messages, audit_logs, api_tokens
- Replaced `cluster_ids INT[]` with `product_clusters` join table
- Changed memory/disk from TEXT to INT (MB/GB) for quota arithmetic
- Fixed deployment diagram: MySQL→PostgreSQL, added oauth2-proxy
- Fixed oauth2-proxy config: added `proxy_websockets`, removed auth `redirect_uri` from IncusAdmin config
- Emergency login moved to secondary port (:8081 localhost only)
- Added admin balance top-up and quota management endpoints
- Added full admin VM management endpoints (force-stop, migrate, delete, bulk)
- Added order lifecycle states: pending→paid→provisioning→active→expired→cancelled
- Added `updated_at` to all tables

**Deferred to implementation phase:**
- Billing scheduler (cron for renewals/suspensions) — design in Phase 2
- Image cache management across clusters
- API rate limiting middleware
- Notifications (email/webhook)
- IncusAdmin PostgreSQL backup strategy
