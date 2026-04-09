# Incus 集群版 — Incus + Ceph 高可用虚拟机集群

> 状态：规划中

基于 Incus 集群 + Ceph 分布式存储，实现 VM 故障自动切换和数据冗余。

## 硬件规划

### 单节点配置

| 项目 | 规格 |
|------|------|
| CPU | 按需（建议 16+ 核）|
| 内存 | 按需（建议 64+ GB）|
| 系统盘 | 2x SSD RAID1（OS + Ceph MON/MGR）|
| OSD 盘 | 4x 1TB SSD（独占给 Ceph，不分区）|
| 网卡 | 2x 10Gbps |

### 网络架构（双 10G 物理隔离）

```
网卡 1 (10G) — bond0/eno1
├── VLAN 10: 管理网络（Incus 集群通信 + SSH）
├── VLAN 20: Ceph Public 网络（客户端 ↔ OSD）
└── 桥接: VM 公网 IP 流量

网卡 2 (10G) — bond1/eno2
└── Ceph Cluster 网络（OSD ↔ OSD 复制/恢复，专用）
```

**为什么这样分：**
- 网卡 2 **完全专用于 Ceph 集群内部复制**，OSD 恢复时不影响任何业务
- 网卡 1 承载管理 + VM 流量 + Ceph 客户端 IO，通过 VLAN 逻辑隔离
- 即使 Ceph 全速恢复（跑满 10G），VM 网络零影响

### 集群规模

| 阶段 | 节点数 | 说明 |
|------|--------|------|
| 最小起步 | 3 | Ceph + Incus 最小法定人数 |
| 推荐规模 | 5-6 | 容忍 2 节点同时故障 |
| 目标规模 | 10 | 同机柜满配 |

### 存储容量估算（3 节点起步）

```
裸容量: 3 节点 × 4 OSD × 1TB = 12 TB
Ceph 3 副本可用: 12 / 3 × 0.80 = 3.2 TB
每台 VM 50GB: 可开 ~64 台 VM

扩展到 10 节点: 40 TB 裸容量 → 10.7 TB 可用 → ~213 台 VM

★ 选择 3 副本而非 2 副本：单点故障不影响 IO 且不丢数据（业界标准）
```

## 架构方案

### 阶段一：Incus 集群 + Ceph + 桥接（首期实施）

```
┌─────────────────────────────────────────────────────┐
│                    Incus 集群                        │
│              (Cowsql 分布式数据库)                    │
├──────────┬──────────┬──────────┬──────────┬─────────┤
│  Node 1  │  Node 2  │  Node 3  │  Node 4  │  ...    │
│ Incus    │ Incus    │ Incus    │ Incus    │         │
│ MON+MGR  │ MON+MGR  │ MON+MGR  │          │         │
│ 4x OSD   │ 4x OSD   │ 4x OSD   │ 4x OSD   │         │
│ br-pub   │ br-pub   │ br-pub   │ br-pub   │         │
│ VM 1..N  │ VM 1..N  │ VM 1..N  │ VM 1..N  │         │
└──────────┴──────────┴──────────┴──────────┴─────────┘
     │  NIC1: 管理+Ceph Public+VM       │
     │  NIC2: Ceph Cluster（专用）        │
```

**特点：**
- VM 数据存在 Ceph 上，所有节点可见
- 节点故障后，VM 可在其他节点重新启动（共享存储）
- 桥接模式直通公网 IP，延续单机版方案
- **IP 迁移**：同机柜同交换机，通过 Gratuitous ARP 实现

### 阶段二（可选）：引入 OVN

当需要 VM 迁移时 IP 完全自动跟随时，引入 OVN overlay 网络。

## Ceph 网络流量控制

### 物理隔离（首选，你的硬件支持）

```
NIC 1 (eno1/10G):
  → public_network = 10.0.20.0/24  (Ceph 客户端)
  → 管理网 + VM 公网流量

NIC 2 (eno2/10G):
  → cluster_network = 10.0.30.0/24 (OSD 复制，专用)
```

```ini
# /etc/ceph/ceph.conf
[global]
public_network = 10.0.20.0/24
cluster_network = 10.0.30.0/24
```

### Ceph 恢复限速（保守配置）

```bash
# 限制恢复带宽，保护业务 IO
ceph config set osd osd_recovery_max_active_ssd 3
ceph config set osd osd_max_backfills 1
ceph config set osd osd_recovery_sleep_ssd 0.02
ceph config set osd osd_recovery_op_priority 3
ceph config set osd osd_mclock_profile high_client_ops
```

有专用 10G 集群网络，恢复不影响业务，这些参数可以适当放宽。

## HA 故障切换

### 自动切换流程

```
节点宕机
  → 20s: Incus 标记 offline (cluster.offline_threshold)
  → 5min: 开始自动疏散 (cluster.healing_threshold=300)
  → 数秒: VM 在其他节点启动（Ceph 共享存储，无需拷贝磁盘）
  → 总计: ~5-6 分钟自动恢复
```

### 关键配置

```bash
incus config set cluster.offline_threshold 20
incus config set cluster.healing_threshold 300
```

### 前提条件

- [x] VM 使用 Ceph 存储池（共享存储）
- [x] 至少 3 个集群节点（Cowsql 法定人数）
- [x] VM 无本地设备绑定

## 每节点资源预算

| 组件 | CPU | 内存 | 说明 |
|------|-----|------|------|
| OS + Incus | 1 核 | 2 GB | |
| Ceph MON+MGR | 1 核 | 4 GB | 仅前 3 节点 |
| Ceph OSD × 4 | 4 核 | 8 GB | 每 OSD 约 1C/2G |
| 系统预留 | 2 核 | 4 GB | |
| **可分配给 VM** | **剩余** | **剩余** | 64G 节点约 46G 可用 |

## 开发计划

### Phase 1：基础集群

- [ ] 集群初始化脚本（3 节点组建 Incus 集群）
- [ ] Ceph 部署脚本（MON + MGR + OSD）
- [ ] Ceph 存储池接入 Incus
- [ ] 网络配置（双 10G VLAN 隔离）
- [ ] 防火墙策略统一下发

### Phase 2：VM 管理

- [ ] 跨节点 create-vm（指定/自动选择目标节点）
- [ ] VM 迁移工具（热迁移 + 冷迁移）
- [ ] 公网 IP 迁移（Gratuitous ARP）
- [ ] 统一凭据管理

### Phase 3：高可用

- [ ] 自动 healing 配置
- [ ] 节点维护模式（evacuate/restore）
- [ ] 监控告警（节点状态、Ceph 健康度）
- [ ] 自动扩缩容（新节点加入流程）

### Phase 4（可选）：OVN

- [ ] OVN overlay 网络部署
- [ ] network forward（公网 IP → VM 内部 IP）
- [ ] VM 迁移 IP 自动跟随

## 脚本目录（规划）

```
cluster/
├── scripts/
│   ├── setup-cluster.sh      # 集群初始化（Incus + Ceph）
│   ├── join-node.sh           # 新节点加入集群
│   ├── create-vm.sh           # 集群版创建 VM
│   ├── migrate-vm.sh          # VM 迁移工具
│   ├── vm-firewall.sh         # 集群统一防火墙
│   └── ceph-status.sh         # Ceph 状态监控
├── configs/
│   ├── ceph.conf.template     # Ceph 配置模板
│   ├── netplan-nic1.yaml      # 网卡 1 配置模板
│   └── netplan-nic2.yaml      # 网卡 2 配置模板
└── docs/
    └── plan/
```
