# PLAN-002: Incus 集群云平台完整方案

- **状态**: draft
- **创建**: 2026-04-05
- **关联任务**: CLUSTER-001

## 一、需求总览

| # | 需求 | 核心挑战 |
|---|------|---------|
| 1 | IP 自动绑定 + 防篡改 | 集群模式下迁移时规则重建 |
| 2 | 网卡硬件卸载 | vhost-net/multiqueue/Jumbo Frame |
| 3 | 监控 + 自愈 | Prometheus 全栈 + webhook 自动修复 |
| 4 | 网络隔离 | 三网分离 + VM 无法触及管理/存储网 |
| 5 | 用户自助面板 | 基于 Incus API + Project 多租户 |
| 6 | 账单系统 | 按时长计费 + 自动扣费 |
| 7 | 多集群管理 | API 聚合层，每集群 ≤20 台 |

## 二、整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                       云管理平台 (Go + Vue3)                      │
│  ┌────────────┐  ┌────────────┐  ┌──────────┐  ┌──────────────┐ │
│  │ 用户自助门户 │  │ 管理后台    │  │ 计费模块  │  │ 多集群聚合    │ │
│  │ VM 生命周期 │  │ 节点/存储  │  │ Lago/    │  │ REST API    │ │
│  │ 控制台/VNC │  │ 网络/安全  │  │ 自建      │  │ 路由        │ │
│  └─────┬──────┘  └─────┬──────┘  └────┬─────┘  └──────┬───────┘ │
│        └───────────────┴───────────────┴───────────────┘         │
│                              │                                    │
│              ┌───────────────┼───────────────┐                   │
│              │    Keycloak OIDC + OpenFGA     │                   │
│              └───────────────┼───────────────┘                   │
└──────────────────────────────┼───────────────────────────────────┘
                               │
          ┌────────────────────┼────────────────────┐
          │                    │                     │
   ┌──────┴──────┐     ┌──────┴──────┐      ┌──────┴──────┐
   │ 集群 A       │     │ 集群 B       │      │ 集群 C       │
   │ 区域: 东京   │     │ 区域: 香港   │      │ 区域: 新加坡 │
   │ 3-10 节点    │     │ 3-10 节点    │      │ 3-10 节点    │
   │ Incus+Ceph  │     │ Incus+Ceph  │      │ Incus+Ceph  │
   └─────────────┘     └─────────────┘      └─────────────┘
```

## 三、各模块方案

### 模块 1：IP 自动绑定 + 防篡改

**调研结论：Incus `security.ipv4_filtering` 在集群模式下完全可用，VM 迁移时自动在目标节点重建过滤规则。**

**方案：三层锁定**

| 层 | 机制 | 集群行为 |
|----|------|---------|
| L1 | `security.ipv4_filtering` | 迁移时自动重建 ebtables/nftables 规则 |
| L2 | `security.mac_filtering` | MAC 地址跟随 VM 迁移 |
| L3 | nftables bridge 阻断 RFC1918 | 每节点部署，防止 VM 探测内网 |

```bash
# 每节点 nftables 兜底规则
nft add rule bridge vm_filter forward ip daddr 10.0.0.0/8 drop
nft add rule bridge vm_filter forward ip daddr 172.16.0.0/12 drop
nft add rule bridge vm_filter forward ip daddr 192.168.0.0/16 drop
```

**注意事项：**
- 迁移瞬间（数秒）可能存在规则空白窗口
- OVN port security 可在 Phase 4 完全解决此问题

---

### 模块 2：网卡硬件卸载

**调研结论：10G + vhost-net + multiqueue 默认配置已接近最优，无需手动调优。SR-IOV 不适合（绕过安全过滤层）。**

**方案：确认默认配置 + Jumbo Frame**

```bash
# 每节点检查清单
modprobe vhost_net                      # 确保加载
ethtool -K eno1 gro on gso on tso on   # 确保开启
ethtool -K br-pub gro on gso on tso on

# Ceph 网络启用 Jumbo Frame (MTU 9000)
# VLAN 20 (Ceph Public): MTU 9000
# VLAN 30 (Ceph Cluster/NIC2): MTU 9000
# br-pub (VM 公网): MTU 1500（不变）
```

**性能对比（10G 网络）：**

| 配置 | 单流吞吐 | 延迟 |
|------|---------|------|
| 默认 vhost-net | ~9.2 Gbps | ~30μs |
| + multiqueue (4队列) | ~9.4 Gbps | ~25μs |
| + Jumbo Frame (Ceph) | Ceph +15% 吞吐 | 减少 CPU 中断 |

---

### 模块 3：监控 + 自愈

**调研结论：Incus 原生支持 /1.0/metrics（Prometheus 格式），Ceph 有 prometheus module，社区有完整的 Grafana 仪表盘。**

**方案：Prometheus + Grafana + Loki + Alertmanager + 自愈 Webhook**

```
┌─────────────┐    ┌──────────────┐    ┌──────────┐
│ Prometheus   │───→│ Alertmanager │───→│ 自愈服务  │
│ 采集:        │    │ 分级路由:    │    │ 路由表:   │
│ - Incus :8444│    │ P0→电话+IM  │    │ OSD挂→重启│
│ - Ceph :9283 │    │ P1→IM       │    │ PG降级→修复│
│ - Node :9100 │    │ P2→邮件     │    │ 磁盘满→清理│
└──────┬───────┘    └──────────────┘    └──────────┘
       │
┌──────┴───────┐    ┌──────────────┐
│ Grafana      │    │ Loki         │
│ 仪表盘 #19727│    │ Promtail     │
│ + Ceph 面板  │    │ 每节点部署    │
└──────────────┘    └──────────────┘
```

**关键告警规则：**

| 级别 | 告警 | 自愈动作 |
|------|------|---------|
| P0 | Incus 节点离线 | 通知（Incus auto-healing 处理） |
| P0 | Ceph HEALTH_ERR | 通知 + 检查 OSD |
| P0 | Ceph OSD down | 自动重启 OSD 进程 |
| P1 | Ceph PG degraded > 5min | 自动 `ceph pg repair` |
| P1 | 宿主机 CPU/内存 > 90% | 通知 |
| P2 | 磁盘 > 85% | 自动清理日志/缓存 |
| P2 | Ceph 空间 > 75% | 通知扩容 |

**部署方式：** Docker Compose，部署在管理节点上，仅监听管理网 VLAN 10。

---

### 模块 4：网络隔离

**调研结论：双 10G 物理隔离 + VLAN 逻辑隔离 + nftables 规则 + Ceph msgr2 TLS = 四层防护。VM 无法触及管理/存储网络。**

**方案：三网物理+逻辑隔离**

```
NIC 1 (eno1, 10G):
├── untagged → br-pub (VM 公网 IP, MTU 1500)
├── VLAN 10 → 管理网 (Incus API + SSH, 10.0.10.0/24, MTU 1500)
└── VLAN 20 → Ceph Public (客户端IO, 10.0.20.0/24, MTU 9000)

NIC 2 (eno2, 10G):
└── 直连 → Ceph Cluster (OSD复制, 10.0.30.0/24, MTU 9000)
```

**安全规则：**

```bash
# 1. VM 无法访问内部网络（每节点 bridge nftables）
nft add rule bridge vm_filter forward ether type ip ip daddr 10.0.0.0/8 drop

# 2. Ceph 端口仅限集群节点访问（每节点 inet nftables）
nft add rule inet host_filter input tcp dport 6789 ip saddr != { 10.0.20.1-10.0.20.20 } drop
nft add rule inet host_filter input tcp dport 6800-7300 ip saddr != { 10.0.20.1-10.0.20.20 } drop

# 3. Incus API 仅限管理网
incus config set core.https_address 10.0.10.X:8443

# 4. Ceph 通信加密
ceph config set global ms_cluster_mode secure
ceph config set global ms_service_mode secure
ceph config set global ms_client_mode secure
```

---

### 模块 5：用户自助面板

**调研结论：无现成方案，需自建。Incus REST API 完整（创建/删除/启停/快照/迁移/控制台），Project 支持多租户隔离和资源配额。**

**技术选型：**

| 层 | 选择 | 理由 |
|----|------|------|
| 前端 | Vue3 + Vite | 轻量、生态好 |
| 后端 | Go | 与 Incus client 库天然兼容 |
| 认证 | Keycloak OIDC | Incus 原生支持 |
| 权限 | Incus Project + OpenFGA | 细粒度 RBAC |
| 控制台 | WebSocket VNC/Terminal | Incus /1.0/instances/{name}/console |

**用户可操作：**

| 操作 | API 端点 | 权限 |
|------|---------|------|
| 创建 VM | POST /1.0/instances | project 内 |
| 删除 VM | DELETE /1.0/instances/{name} | 仅自己的 |
| 启停重启 | PUT /1.0/instances/{name}/state | 仅自己的 |
| 快照/恢复 | POST/DELETE snapshots | 仅自己的 |
| 修改密码 | POST /1.0/instances/{name}/exec | 仅自己的 |
| 控制台 | WebSocket console | 仅自己的 |
| 查看监控 | Grafana 嵌入 | 仅自己的 VM |
| 重装系统 | 删除+重建 | 仅自己的 |

**Incus Project 多租户隔离：**

```bash
# 为每个租户创建 project
incus project create tenant-001 \
  -c limits.instances=10 \
  -c limits.cpu=32 \
  -c limits.memory=64GiB \
  -c limits.disk=500GiB \
  -c restricted=true

# 创建受限 token（绑定到 project）
incus config trust add --name tenant-001-token \
  --projects tenant-001 --restricted
```

---

### 模块 6：账单系统

**调研结论：推荐 Lago（开源 usage-based billing）用于中期，自建简易计费用于首期。**

**首期方案（自建简易计费）：**

```
定时任务 (每小时) → 查询所有 VM 运行状态
  → 记录: VM_ID, 规格, 运行时长, 时间戳
  → 月底汇总: Σ(规格单价 × 运行小时数)
  → 生成账单 → 扣费 → 余额不足停机
```

**价格模型：**

```
费用 = CPU单价×核数×小时 + 内存单价×GB×小时 + 磁盘单价×GB×天 + 流量单价×GB
```

**数据表设计（PostgreSQL）：**

```sql
-- 规格定价
CREATE TABLE pricing (
    cpu_per_core_hour    DECIMAL,  -- 每核每小时
    memory_per_gb_hour   DECIMAL,  -- 每GB内存每小时
    disk_per_gb_day      DECIMAL,  -- 每GB磁盘每天
    traffic_per_gb       DECIMAL   -- 每GB流量
);

-- 用量记录（每小时采集）
CREATE TABLE usage_records (
    id          SERIAL PRIMARY KEY,
    tenant_id   VARCHAR,
    instance_id VARCHAR,
    cpu_cores   INT,
    memory_gb   INT,
    disk_gb     INT,
    is_running  BOOLEAN,
    recorded_at TIMESTAMP
);

-- 账单
CREATE TABLE invoices (
    id         SERIAL PRIMARY KEY,
    tenant_id  VARCHAR,
    period     VARCHAR,   -- "2026-04"
    amount     DECIMAL,
    status     VARCHAR    -- pending/paid/overdue
);

-- 余额
CREATE TABLE balances (
    tenant_id VARCHAR PRIMARY KEY,
    balance   DECIMAL
);
```

**中期方案：** 接入 Lago，通过 Lago Events API 上报用量，Lago 负责计费、发票、webhook 通知。

---

### 模块 7：多集群管理

**调研结论：Incus 无原生多集群联邦，需自建 API 聚合层。**

**方案：Go API Gateway 聚合多 Incus 集群**

```go
// 集群注册表
type Cluster struct {
    ID       string   // "cluster-tokyo"
    Region   string   // "ap-northeast-1"
    Endpoint string   // "https://10.0.10.1:8443"
    CertFile string   // TLS 客户端证书
    KeyFile  string
    CAFile   string
}

// API 路由: /api/v1/clusters/{cluster_id}/instances/...
// 透传到对应 Incus 集群的 REST API
```

**核心功能：**

| 功能 | 实现 |
|------|------|
| 集群列表 | 本地数据库存储集群连接信息 |
| 透传 API | 按 cluster_id 路由到对应 Incus API |
| 聚合视图 | 并发查询所有集群，合并结果 |
| 统一认证 | Keycloak → 映射到各集群的 Project/Token |
| 跨集群操作 | 导出 → 传输 → 导入（不支持热迁移） |

---

## 四、开发阶段（6 个 Phase）

### Phase 1：基础集群（2-3 周）

```
目标：3 节点 Incus + Ceph 集群可用
├── setup-cluster.sh（Incus 集群初始化）
├── deploy-ceph.sh（Ceph MON + MGR + OSD）
├── 网络配置（双 10G + VLAN 10/20/30）
├── Ceph 存储池接入 Incus
├── 防火墙统一下发（复用 single/ 的 vm-firewall 升级版）
└── IP 绑定 + 防篡改验证
```

### Phase 2：VM 管理（1-2 周）

```
目标：集群版 create-vm 可用
├── create-vm.sh 集群版（--target 指定节点或自动调度）
├── VM 迁移工具（热迁移 + 冷迁移 + Gratuitous ARP）
├── 统一凭据管理
└── 密码修改工具
```

### Phase 3：监控 + 自愈（1-2 周）

```
目标：全栈监控可观测
├── Prometheus + Grafana + Loki（Docker Compose）
├── Incus metrics + Ceph prometheus module 接入
├── Node Exporter + Promtail 每节点部署
├── 告警规则 + Alertmanager 分级路由
└── 自愈 webhook 服务 + 脚本
```

### Phase 4：用户自助面板（4-6 周）

```
目标：租户可自助管理 VM
├── Go 后端（Incus API 封装 + 认证 + 审计）
├── Vue3 前端（VM 列表/创建/控制台/快照）
├── Keycloak OIDC 集成
├── Project 多租户隔离
└── API 文档（OpenAPI）
```

### Phase 5：账单系统（2-3 周）

```
目标：按时长自动计费
├── 用量采集服务（每小时轮询 Incus API）
├── 账单生成（月结）
├── 余额管理 + 自动扣费
├── 欠费停机 + 充值恢复
└── 账单查询 API + 前端页面
```

### Phase 6：多集群管理（2-3 周）

```
目标：统一管理多个区域集群
├── 集群注册 + 连接管理
├── API 聚合层（透传 + 聚合视图）
├── 统一认证映射
├── 跨集群导入/导出
└── 区域选择 UI
```

## 五、风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| Ceph 学习曲线高 | Phase 1 延期 | 先用 cephadm 自动化部署 |
| 自建面板工作量大 | Phase 4 耗时 | 先 CLI 管理，面板渐进式开发 |
| Incus 生态不成熟 | 缺乏参考 | 多测试，关注社区动态 |
| 网络隔离遗漏 | 安全事故 | 上线前渗透测试 |
| VM 迁移时 IP 中断 | 用户感知 | 同交换机 GARP + 提前通知 |

## 六、技术栈总结

| 层 | 技术 |
|----|------|
| 虚拟化 | Incus (KVM/QEMU) |
| 存储 | Ceph (RBD + BlueStore) |
| 网络 | Bridge + VLAN + nftables（Phase 4 可选 OVN）|
| 集群数据库 | Cowsql (Raft) |
| 监控 | Prometheus + Grafana + Loki |
| 告警 | Alertmanager + Webhook |
| 认证 | Keycloak OIDC |
| 权限 | Incus Project + OpenFGA |
| 后端 | Go |
| 前端 | Vue3 + Vite |
| 计费 | 自建（首期）→ Lago（中期）|
| 部署 | Docker Compose（管理组件）|
| 数据库 | PostgreSQL（管理平台数据）|
