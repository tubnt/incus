# PLAN-002：集群版完整方案 — 7 大模块架构设计

- **状态**: draft
- **创建**: 2026-04-05
- **更新**: 2026-04-05
- **关联任务**: CLUSTER-001

---

## 一、总体架构

```
┌─────────────────────────────────────────────────────────────┐
│              Paymenter（用户自助面板 + 计费）                  │
│   注册 → 选区域 → 下单 → 支付 → 自动开通                      │
│   VM 管理 / VNC / 防火墙 / SSH Key / 快照 / 重装 / 账单       │
│   工单系统 / 邮件通知 / 2FA                                   │
├─────────────────────────────────────────────────────────────┤
│              Incus Server Extension (PHP/Laravel)            │
│   Paymenter ↔ Incus REST API 桥梁                           │
│   创建/暂停/恢复/删除/重装/快照/升降配/附加盘/带宽限速          │
│   IP 池管理 / ACL 防火墙 / SSH Key 注入                      │
├─────────────┬─────────────┬─────────────┬───────────────────┤
│ Incus 集群A │ Incus 集群B │ Incus 单机C │ ...               │
│ (东京机柜1) │ (香港机柜3) │ (测试机)    │                   │
│ 3-20 节点   │ 3-20 节点   │ 1 节点      │                   │
│ Ceph 存储   │ Ceph 存储   │ 本地存储    │                   │
│ br-pub 桥接 │ br-pub 桥接 │ br-pub 桥接 │                   │
│ dmcrypt加密 │ dmcrypt加密 │ 可选        │                   │
├─────────────┴─────────────┴─────────────┴───────────────────┤
│              Prometheus + Grafana + Loki + Alertmanager      │
│              告警分级(P0电话/P1消息/P2邮件) → 自愈脚本         │
│              容量水位线监控 + IP 池余量告警                     │
└─────────────────────────────────────────────────────────────┘
```

**关键决策：**

| 决策 | 结论 | 理由 |
|------|------|------|
| OVN | **不引入** | 公网 IP 直通 + 同机柜同交换机，不需要 overlay |
| SR-IOV | **不采用** | 绕过 Incus 安全过滤层，与 IP 锁定需求冲突 |
| 用户面板 | **Paymenter** | 开源 Laravel，客户管理+计费+工单开箱即用 |
| 前端自建 | **不需要** | Paymenter 自带客户门户 |
| 计费 | **Paymenter 内置** | 订阅/按量均支持，Stripe/PayPal 等支付网关 |
| 多集群 | **Paymenter 多 Server** | 每个 Incus 集群/单机注册为一个 Server |
| 用户防火墙 | **Incus ACL** | per-VM 安全组，bridge 模式支持 |
| 专线 | **VM 内 WireGuard** | 无需 OVN，VM 层面解决 |
| VM 磁盘加密 | **Ceph dmcrypt** | AES-NI 硬件加速，性能损耗 ~5-10%，作为卖点 |
| DDoS 防护 | **上游处理** | null route 被攻击 IP，宿主机不处理 |
| IPv6 | **首期不做** | 后续按需添加 |
| 自定义 ISO | **不做** | 只提供预设镜像 |
| VM 间内网 | **首期不做** | 保持 port_isolation 全隔离 |
| 用户 API | **首期不做** | 未来考虑用户级 MCP/AI 对话控制 |
| SLA | **99.9%** | auto-healing ~5 分钟恢复，每月允许 43 分钟宕机 |

---

## 二、模块设计（7+3 模块）

### 模块 1：IP 自动绑定与防篡改

沿用单机版方案，集群模式下 VM 迁移时自动在新节点重建过滤规则。

```bash
incus config device override ${VM} eth0 \
  ipv4.address=${IP} \
  security.ipv4_filtering=true \
  security.mac_filtering=true \
  security.port_isolation=true
```

**集群新增**：
```bash
# 阻断 VM 访问内部网络
nft add rule bridge vm_filter forward ether type ip ip daddr 10.0.0.0/8 drop
nft add rule bridge vm_filter forward ether type ip ip daddr 172.16.0.0/12 drop
nft add rule bridge vm_filter forward ether type ip ip daddr 192.168.0.0/16 drop
```

### 模块 2：网卡硬件卸载

10G + vhost-net 默认已最优，仅需确认：

```bash
modprobe vhost_net
ethtool -K br-pub gro on gso on tso on
ip link set eno2 mtu 9000  # Ceph Cluster 网络 Jumbo Frame
```

### 模块 3：监控体系与自愈

Prometheus + Grafana + Loki + Alertmanager，Docker Compose 部署。

| 采集目标 | 端口 | 说明 |
|----------|------|------|
| Incus /1.0/metrics | :8444 (mTLS) | VM CPU/内存/磁盘/网络 |
| Ceph MGR prometheus | :9283 | OSD/PG/空间/延迟 |
| node_exporter | :9100 | 宿主机硬件指标 |
| Promtail → Loki | :3100 | 日志收集 |

**容量水位线告警**：

| 资源 | 警告阈值 | 严重阈值 | 说明 |
|------|---------|---------|------|
| CPU（节点） | >80% 持续 10m | >95% 持续 5m | 需要扩容或迁移 VM |
| 内存（节点） | >85% | >95% | 需要扩容 |
| Ceph 存储 | >75% | >85% | 需要加 OSD |
| 单 OSD | >80% | >90% | 需要 rebalance |
| IP 池余量 | <20% | <10% | 需要采购新 IP |

**自愈路由**：

| 告警 | 自动动作 |
|------|---------|
| OSD 进程崩溃 | `systemctl restart ceph-osd@N` |
| PG 降级 >5min | `ceph pg repair` |
| 宿主机磁盘 >85% | 清理 journald + apt 缓存 |
| Incus 节点离线 | Incus auto-healing 迁移 VM |

### 模块 4：网络隔离

双 10G 物理隔离 + VLAN 逻辑隔离：

```
NIC 1 (eno1, 10G):
├── untagged → br-pub (VM 公网, MTU 1500)
├── VLAN 10 → 管理网 (10.0.10.0/24, MTU 1500)
└── VLAN 20 → Ceph Public (10.0.20.0/24, MTU 9000)

NIC 2 (eno2, 10G):
└── 直连 → Ceph Cluster (10.0.30.0/24, MTU 9000, 完全专用)
```

安全规则：
- VM → 内部网络：bridge nftables 阻断 RFC1918
- Ceph 端口：仅限集群节点 IP
- Incus API：`core.https_address` 绑定管理网 IP
- Ceph 通信：msgr2 TLS 加密

### 模块 5：用户自助面板（Paymenter + Incus Extension）

**用户完整操作清单**：

| 操作 | 实现方式 |
|------|---------|
| 创建 VM | Paymenter 下单 → Extension 调 Incus API |
| 启停/重启 | Extension → `PUT /1.0/instances/{name}/state` |
| **重装系统** | 删除旧实例 → 同 IP 创建新实例（保留 IP，全部重置）|
| **VNC 控制台** | noVNC via Incus console WebSocket |
| **修改密码** | Extension → `incus exec` 执行 `chpasswd` |
| **SSH Key 管理** | 用户面板上传公钥 → 创建 VM 时 cloud-init 注入 |
| **防火墙（安全组）** | Extension 管理 Incus ACL |
| **快照** | Extension → `POST /1.0/instances/{name}/snapshots` |
| **升降配** | 停机 → 修改 `limits.cpu` / `limits.memory` → 启动 |
| **附加磁盘** | Extension → `incus storage volume create` + device add |
| **重装系统** | 删除实例 → 同 IP/同规格重建 |
| 查看用量 | Extension 读取 Incus metrics |
| 查看账单 | Paymenter 内置 |
| **工单** | Paymenter 内置工单系统 |

**带宽限速**（管理员在后台设置每台 VM 的带宽上限）：

```bash
# Incus 原生支持网络限速
incus config device set ${VM} eth0 limits.ingress=100Mbit
incus config device set ${VM} eth0 limits.egress=100Mbit
```

**用户级防火墙**（Incus ACL）：

```bash
incus network acl create user-${tenant_id}-acl
incus network acl rule add user-${tenant_id}-acl ingress \
  action=allow protocol=tcp destination_port=80,443
incus config device set ${vm} eth0 security.acls=user-${tenant_id}-acl
incus config device set ${vm} eth0 security.acls.default.ingress.action=drop
```

### 模块 6：计费系统

Paymenter 内置，支持：
- 包月产品（固定配置 × 月价）
- 按量计费（Usage Extension）
- Stripe / PayPal / Mollie / 支付宝（社区插件）
- 自动续费、到期暂停、余额管理、发票
- **退款**：未使用天数按比例退还（参考 Linode）

**参考定价（对标 Vultr/Linode/Hetzner 中间价位）**：

| 规格 | 月价 (USD) | 参考 |
|------|-----------|------|
| 1C 1G 25G SSD 1TB 流量 | $5 | Vultr $6, Linode $5, Hetzner €4.5 |
| 1C 2G 50G SSD 2TB 流量 | $10 | Vultr $12, Linode $12, Hetzner €4.5 |
| 2C 4G 80G SSD 3TB 流量 | $20 | Vultr $24, Linode $24, Hetzner €7 |
| 4C 8G 160G SSD 4TB 流量 | $40 | Vultr $48, Linode $48, Hetzner €15 |
| 8C 16G 320G SSD 5TB 流量 | $80 | Vultr $96, Linode $96, Hetzner €30 |

> 后台可调整。建议初期定在 Vultr 的 80-85% 水平吸引用户。

### 模块 7：多集群管理

Paymenter 原生多 Server：

```
Paymenter 后台 → Server 管理
├── Server: tokyo-cluster  (https://10.0.10.1:8443)  → 3-20 节点集群
├── Server: hk-cluster     (https://10.0.10.2:8443)  → 另一个集群
└── Server: standalone-1   (https://test:8443)        → 单台服务器（也纳入管理）
```

用户下单时选择区域 → Paymenter 路由到对应 Server。

### 模块 8（新增）：IP 池管理

```
IP 池数据库（Paymenter Extension 维护）：
├── pool_id: tokyo-1
│   ├── subnet: 43.239.84.0/26
│   ├── gateway: 43.239.84.1
│   ├── reserved: [43.239.84.1, 43.239.84.20]  # 网关+宿主机
│   ├── allocated: {43.239.84.21: vm-node01, ...}
│   └── available: [43.239.84.29, ...]
└── pool_id: hk-1
    └── ...
```

| 操作 | 逻辑 |
|------|------|
| 创建 VM | 从 available 取一个 IP → 标记 allocated |
| 删除 VM | IP 放入 cooldown（24h）→ 然后回到 available |
| IP 快用完 | 告警（<10% 余量）|
| IP 池管理 | 管理后台增删 IP 段 |

### 模块 9（新增）：运维流程

**滚动维护**（零停机更新）：

```bash
# 1. 疏散节点（VM 秒级迁移到其他节点，用户无感）
incus cluster evacuate node1

# 2. 更新系统/Incus/Ceph
apt update && apt upgrade -y
# 如果是 Ceph OSD 节点，设置 noout 防止数据迁移
ceph osd set noout

# 3. 恢复节点
incus cluster restore node1
ceph osd unset noout
```

**审计日志**：
- Paymenter 操作日志（用户创建/删除 VM、支付记录）
- Incus lifecycle 事件（`incus monitor --type=lifecycle`）→ Loki 存储
- 管理员 SSH 操作 → journald → Loki

**Incus/Ceph 大版本升级**：
1. 测试环境先验证（用 standalone 测试机）
2. 滚动升级（一个节点一个节点来）
3. 保留回滚能力（apt snapshot 或 LVM 快照系统盘）

### 模块 10（新增）：通知与策略

**邮件通知**：
- 欠费提醒：到期前 7 天、3 天、1 天
- VM 到期暂停通知
- 维护公告（手动发送）
- 故障公告（手动发送）

**经营策略（参考 Linode）**：
- 退款：未使用天数按比例退还到账户余额
- 到期处理：到期 → 暂停（保留数据 7 天）→ 删除
- ToS/AUP：禁止挖矿、DDoS、垃圾邮件、端口扫描
- 滥用处理：收到 abuse 投诉 → 人工审核 → 暂停/删除 VM

**SLA：99.9%**
- 每月允许 43 分钟宕机
- 超出部分按比例返还账户余额（非现金）
- 不含计划内维护和不可抗力

**管理后台 2FA**：
- Paymenter 管理员登录启用 TOTP 2FA
- 所有管理员操作记录审计日志

---

## 三、不纳入方案的技术

| 技术 | 原因 |
|------|------|
| OVN | 公网 IP 直通不需要 overlay |
| SR-IOV | 绕过安全过滤层 |
| 自建前端 | Paymenter 已提供 |
| XDP/eBPF | 10G + vhost-net 已足够 |
| 跨机柜集群 | 单一集群限同机柜，跨区域用多集群 |
| IPv6 | 首期不做，后续按需 |
| 自定义 ISO | 不做，只提供预设镜像 |
| Ceph 纠删码 | 首期不需要，3 副本足够 |
| rDNS | 首期不做 |

---

## 四、开发阶段

### Phase 1：基础集群（2-3 周）

- [ ] setup-cluster.sh（3 节点 Incus 集群初始化）
- [ ] deploy-ceph.sh（cephadm 部署 MON + MGR + OSD + dmcrypt）
- [ ] Ceph 存储池接入 Incus
- [ ] 网络配置模板（双 10G + VLAN 10/20/30）
- [ ] 防火墙统一下发（bridge + inet + ceph_security）
- [ ] IP 绑定 + RFC1918 阻断验证

### Phase 2：VM 管理 + HA（2 周）

- [ ] create-vm.sh 集群版（Ceph 存储 + 自动选节点 + 带宽限速）
- [ ] VM 热迁移 + 冷迁移工具
- [ ] 公网 IP 迁移（Gratuitous ARP）
- [ ] Incus auto-healing 配置
- [ ] join-node.sh（新节点加入）
- [ ] 滚动维护工具（evacuate + update + restore）

### Phase 3：监控 + 自愈（1-2 周）

- [ ] Prometheus + Grafana + Loki Docker Compose
- [ ] Incus metrics + Ceph prometheus module 接入
- [ ] 告警规则 + Alertmanager 分级路由
- [ ] 容量水位线告警（CPU/内存/存储/IP 池）
- [ ] 自愈 webhook 服务 + 脚本
- [ ] 审计日志（lifecycle → Loki）

### Phase 4：Paymenter + 计费（2-3 周）

- [ ] Paymenter Docker Compose 部署
- [ ] Incus Server Extension 核心（创建/暂停/恢复/删除）
- [ ] VNC 控制台集成（noVNC）
- [ ] 用户防火墙（Incus ACL 管理）
- [ ] SSH Key 管理（上传 → cloud-init 注入）
- [ ] 快照管理（创建/恢复/删除）
- [ ] 升降配（修改 CPU/内存）
- [ ] 附加磁盘（Ceph RBD 卷）
- [ ] 重装系统（删除重建，保留 IP）
- [ ] 带宽限速设置（管理后台）
- [ ] IP 池管理模块
- [ ] 产品配置（规格/定价/支付网关）
- [ ] 多集群/单机 Server 注册
- [ ] 邮件通知（欠费/到期/维护）
- [ ] 管理后台 2FA
- [ ] 工单系统确认/配置

### Phase 5：运维完善（持续）

- [ ] 备份策略（Ceph 快照 + 未来异地冷存储）
- [ ] 运维手册 + 故障处理手册
- [ ] ToS / AUP / SLA 文档
- [ ] 安全渗透测试
- [ ] 节点扩缩容工具

### Phase 6（远期）：增值功能

- [ ] 用户级 MCP/AI 对话控制（网页聊天窗口 + Claude API）
- [ ] 专线功能（WireGuard 自动化）
- [ ] 云应用/Serverless（类似 CF Workers）
- [ ] IPv6 双栈
- [ ] 异地灾备

---

## 五、风险

| 风险 | 缓解 |
|------|------|
| Paymenter 稳定性 | 每日数据库备份 + 测试环境充分验证 |
| Ceph 学习曲线 | cephadm 自动化 + 3 节点起步 |
| Incus healing 误触发 | `offline_threshold=30` + 双重检测 |
| Extension 开发周期 | Phase 4 功能多，优先核心（创建/删除/启停/计费），其他迭代 |
| IP 池耗尽 | 容量监控 + 提前采购告警 |
| dmcrypt 性能 | AES-NI 硬件加速，实测 ~5-10% 开销，可接受 |
