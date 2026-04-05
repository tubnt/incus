# PLAN-002：集群版完整方案 — 7 大模块架构设计

- **状态**: draft
- **创建**: 2026-04-05
- **关联任务**: CLUSTER-001

---

## 一、总体架构

```
┌─────────────────────────────────────────────────────────────┐
│              Paymenter（用户自助面板 + 计费）                  │
│         客户注册 → 选区域 → 下单 → 支付 → 自动开通             │
│         VM 管理 / VNC 控制台 / 防火墙 / 续费 / 账单           │
├─────────────────────────────────────────────────────────────┤
│              Incus Server Extension (PHP/Laravel)            │
│         Paymenter ↔ Incus REST API 桥梁                     │
│         创建/暂停/恢复/删除 VM + ACL 防火墙                   │
├─────────────┬─────────────┬─────────────┬───────────────────┤
│ Incus 集群A │ Incus 集群B │ Incus 单机C │ ...               │
│ (东京机柜1) │ (香港机柜3) │ (测试机)    │                   │
│ 3-20 节点   │ 3-20 节点   │ 1 节点      │                   │
│ Ceph 存储   │ Ceph 存储   │ 本地存储    │                   │
│ br-pub 桥接 │ br-pub 桥接 │ br-pub 桥接 │                   │
├─────────────┴─────────────┴─────────────┴───────────────────┤
│              Prometheus + Grafana + Loki + Alertmanager      │
│              告警分级(P0电话/P1消息/P2邮件) → 自愈脚本         │
└─────────────────────────────────────────────────────────────┘
```

**关键决策：**

| 决策 | 结论 | 理由 |
|------|------|------|
| OVN | **不引入** | 公网 IP 直通 + 同机柜同交换机，不需要 overlay |
| SR-IOV | **不采用** | 绕过 Incus 安全过滤层，与 IP 锁定需求冲突 |
| 用户面板 | **Paymenter** | 开源 Laravel，客户管理+计费开箱即用，写 Extension 对接 |
| 前端自建 | **不需要** | Paymenter 自带客户门户 |
| 后端 | **Paymenter Extension (PHP)** | 调用 Incus REST API，1-3 周开发 |
| 计费 | **Paymenter 内置** | 订阅/按量均支持，Stripe/PayPal 等支付网关 |
| 多集群 | **Paymenter 多 Server** | 每个 Incus 集群/单机注册为一个 Server |
| 用户防火墙 | **Incus ACL** | per-VM 安全组，bridge 模式支持 |
| 专线 | **VM 内 WireGuard** | 无需 OVN，VM 层面解决 |

---

## 二、7 大模块设计

### 模块 1：IP 自动绑定与防篡改

**沿用单机版方案**，Incus 集群模式下 VM 迁移时自动在新节点重建过滤规则。

```bash
# create-vm 时自动设置
incus config device override ${VM} eth0 \
  ipv4.address=${IP} \
  security.ipv4_filtering=true \
  security.mac_filtering=true \
  security.port_isolation=true
```

**集群新增**：nftables bridge 阻断 VM 访问内部网络
```bash
nft add rule bridge vm_filter forward ether type ip ip daddr 10.0.0.0/8 drop
nft add rule bridge vm_filter forward ether type ip ip daddr 172.16.0.0/12 drop
nft add rule bridge vm_filter forward ether type ip ip daddr 192.168.0.0/16 drop
```

### 模块 2：网卡硬件卸载

**10G + vhost-net 默认已最优**，仅需确认：

```bash
modprobe vhost_net                       # 确认加载
ethtool -K br-pub gro on gso on tso on  # 确认 offload
# Ceph 网络 Jumbo Frame
ip link set eno2 mtu 9000               # NIC2 专用 Ceph Cluster
```

### 模块 3：监控体系与自愈

**Prometheus + Grafana + Loki + Alertmanager**，Docker Compose 部署。

| 采集目标 | 端口 | 说明 |
|----------|------|------|
| Incus /1.0/metrics | :8444 (mTLS) | VM CPU/内存/磁盘/网络 |
| Ceph MGR prometheus | :9283 | OSD/PG/空间/延迟 |
| node_exporter | :9100 | 宿主机硬件指标 |
| Promtail → Loki | :3100 | 日志收集 |

**自愈路由**：

| 告警 | 自动动作 |
|------|---------|
| OSD 进程崩溃 | `systemctl restart ceph-osd@N` |
| PG 降级 >5min | `ceph pg repair` |
| 宿主机磁盘 >85% | 清理 journald + apt 缓存 |
| Incus 节点离线 | Incus auto-healing 迁移 VM（需 Ceph 共享存储）|

**Ceph 内置自愈**：
```bash
ceph config set mon mon_osd_down_out_interval 600
ceph config set mgr mgr/devicehealth/self_heal true
ceph config set osd osd_scrub_auto_repair true
```

### 模块 4：网络隔离

**双 10G 物理隔离 + VLAN 逻辑隔离**：

```
NIC 1 (eno1, 10G):
├── untagged → br-pub (VM 公网, MTU 1500)
├── VLAN 10 → 管理网 (10.0.10.0/24, MTU 1500)
└── VLAN 20 → Ceph Public (10.0.20.0/24, MTU 9000)

NIC 2 (eno2, 10G):
└── 直连 → Ceph Cluster (10.0.30.0/24, MTU 9000, 完全专用)
```

**安全规则**：
- VM → 内部网络：bridge nftables 阻断 RFC1918
- Ceph 端口：仅限集群节点 IP 访问
- Incus API：`core.https_address` 绑定管理网 IP
- Ceph 通信：msgr2 TLS 加密（`ms_cluster_mode=secure`）

### 模块 5：用户自助面板（Paymenter + Incus Extension）

**Paymenter** 提供完整的客户管理框架，我们只需开发 **Incus Server Extension**：

```php
class IncusExtension extends ServerExtension {
    public function createServer($order) {
        // 调用 Incus REST API 创建 VM
        // 绑定 IP + 安全过滤 + cloud-init
        // 设置 Incus ACL（用户防火墙）
    }
    public function suspendServer($order) { /* 停止 VM */ }
    public function unsuspendServer($order) { /* 启动 VM */ }
    public function terminateServer($order) { /* 删除 VM */ }
}
```

**用户可操作**：
- VM 生命周期（启停/重启/重装/删除）
- VNC 控制台（noVNC via Incus console WebSocket）
- 防火墙管理（Incus ACL，per-VM 安全组）
- 修改密码
- 查看资源使用量 + 账单

**用户级防火墙**（Incus ACL on bridge，nftables 驱动）：
```bash
# Paymenter Extension 通过 Incus API 管理 ACL
incus network acl create user-${tenant_id}-acl
incus network acl rule add user-${tenant_id}-acl ingress \
  action=allow protocol=tcp destination_port=80,443
incus config device set ${vm} eth0 security.acls=user-${tenant_id}-acl
incus config device set ${vm} eth0 security.acls.default.ingress.action=drop
```

### 模块 6：计费系统

**Paymenter 内置**，支持：
- 包月产品（固定配置 × 月价）
- 按量计费（Usage Extension）
- Stripe / PayPal / Mollie 等支付网关
- 自动续费、到期暂停、余额管理、发票

### 模块 7：多集群管理

**Paymenter 原生多 Server**：

```
Paymenter 后台 → Server 管理
├── Server: tokyo-cluster  (https://10.0.10.1:8443)  → 3-20 节点集群
├── Server: hk-cluster     (https://10.0.10.2:8443)  → 另一个集群
└── Server: test-machine   (https://test:8443)        → 单台服务器
```

Incus 单机和集群的 REST API 完全一致，Extension 无需区分。用户下单时选择区域 → Paymenter 路由到对应 Server。

---

## 三、不纳入方案的技术

| 技术 | 原因 |
|------|------|
| OVN | 公网 IP 直通不需要 overlay |
| SR-IOV | 绕过安全过滤层 |
| 自建前端 | Paymenter 已提供 |
| XDP/eBPF | 10G + vhost-net 已足够 |
| 跨机柜集群 | 单一集群限同机柜，跨区域用多集群 |

---

## 四、开发阶段

### Phase 1：基础集群（2-3 周）

- [ ] setup-cluster.sh（3 节点 Incus 集群初始化）
- [ ] deploy-ceph.sh（cephadm 部署 MON + MGR + OSD）
- [ ] Ceph 存储池接入 Incus（`incus storage create ceph-pool ceph`）
- [ ] 网络配置模板（双 10G + VLAN 10/20/30）
- [ ] 防火墙统一下发（bridge vm_filter + inet host_filter + ceph_security）
- [ ] IP 绑定 + RFC1918 阻断验证

### Phase 2：VM 管理 + HA（2 周）

- [ ] create-vm.sh 集群版（Ceph 存储 + 自动选节点）
- [ ] VM 热迁移 + 冷迁移工具
- [ ] 公网 IP 迁移（Gratuitous ARP）
- [ ] Incus auto-healing 配置（`cluster.healing_threshold=300`）
- [ ] join-node.sh（新节点加入）

### Phase 3：监控 + 自愈（1-2 周）

- [ ] Prometheus + Grafana + Loki Docker Compose
- [ ] Incus metrics + Ceph prometheus module 接入
- [ ] 告警规则 + Alertmanager 分级路由
- [ ] 自愈 webhook 服务 + 脚本
- [ ] Grafana 仪表盘定制

### Phase 4：Paymenter + 计费（2-3 周）

- [ ] Paymenter Docker Compose 部署
- [ ] Incus Server Extension 开发（PHP）
- [ ] VNC 控制台集成（noVNC）
- [ ] 用户防火墙（Incus ACL 管理）
- [ ] 产品配置（规格/定价/支付网关）
- [ ] 多集群 Server 注册

### Phase 5：运维完善（持续）

- [ ] 节点扩缩容工具
- [ ] 备份策略（Ceph 快照 + 异地）
- [ ] 运维手册 + 故障处理手册
- [ ] 安全渗透测试

---

## 五、风险

| 风险 | 缓解 |
|------|------|
| Paymenter 稳定性（有数据丢失报告） | 每日数据库备份 + 测试环境充分验证 |
| Ceph 学习曲线 | cephadm 自动化 + 3 节点起步积累经验 |
| Incus healing 误触发 | `offline_threshold=30` + 双重检测（heartbeat + ping） |
| Extension 开发周期 | 参考 Proxmox 扩展代码，1-3 周可完成 |
