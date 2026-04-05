# PLAN-001: Incus VM 公网IP网桥环境搭建

- **状态**: completed
- **任务**: INCUS-001
- **创建时间**: 2026-04-02
- **审查轮次**: R3（部署安全专项审查）

---

## 一、当前状态

| 项目 | 状态 |
|------|------|
| 宿主机 | Ubuntu 24.04.4 LTS, 24核 62GB RAM, 878GB 单磁盘 |
| Incus | v6.23 已安装，KVM 正常，CVE-2025-52890 已修复 |
| 网桥 br-pub | 已存在（netplan 之前创建），当前状态 DOWN、无成员、无 IP，networkctl 显示 `unmanaged` |
| 物理网卡 | eno1: 43.239.84.20/26，网关 43.239.84.1 |
| Swap | 已配置 136GB（8G + 128G），vm.swappiness 未优化 |
| 存储池 | default (dir 驱动)，保留使用 |
| 防火墙 | 规则为空 |
| 已有实例 | 无 |
| netplan | `/etc/netplan/50-cloud-init.yaml` 仅配置 eno1；另有 `.bak` 备份 |
| 关键工具 | tmux ✓ / at ✗（未安装）/ IPMI ✗（无带外管理） |

### ⚠️ 关键约束：无带外管理

本服务器**没有 IPMI/BMC/iDRAC**，一旦网络断开且自动恢复失败，将**完全失联**。所有网络变更操作必须有多层自动恢复机制。

## 二、方案设计

### 2.1 网络架构

```
互联网
  │
  ├── 网关 43.239.84.1
  │
  ├── br-pub (unmanaged bridge, STP=off, forward-delay=0)
  │     │
  │     ├── eno1 (物理网卡，无IP，MAC 继承到 br-pub)
  │     │
  │     ├── 宿主机 IP: 43.239.84.20/26  (配置在 br-pub 上)
  │     ├── VM-1 (vm-node01): 43.239.84.21/26  [IP锁定+MAC过滤]
  │     ├── VM-2 (vm-node02): 43.239.84.22/26  [IP锁定+MAC过滤]
  │     └── (预留 .23-.36 共14个IP)
  │
  子网: 43.239.84.0/26
  掩码: 255.255.255.192 (/26)
```

**选择 Unmanaged Bridge 方案的原因：**
- br-pub 已存在（netplan 之前创建），直接复用
- 公网 IP 直接桥接，L2 层转发，无需 IP forwarding
- cloud-init 静态配置 IP，无需 Incus DHCP/dnsmasq
- `ipv4.address` + `security.ipv4_filtering` 实现 IP 锁定（已验证在 unmanaged bridge 上有效，但必须手动指定 ipv4.address）
- 必须使用 `nictype: bridged`（`network:` 仅用于 managed network）

### 2.2 IP 分配规划

| IP | 用途 | 状态 |
|----|------|------|
| 43.239.84.20 | 宿主机（br-pub） | 已用 |
| 43.239.84.21 | vm-node01 | 待分配 |
| 43.239.84.22 | vm-node02 | 待分配 |
| 43.239.84.23-36 | 预留扩容 | 未使用（共14个） |

### 2.3 VM 规格

| 配置项 | 值 |
|--------|------|
| CPU | 4 核 |
| 内存 | 8 GiB |
| Swap（VM内） | 8 GiB（cloud-init 创建 /swapfile） |
| 系统盘 | 50 GiB |
| 镜像 | images:ubuntu/24.04/cloud |
| 模式 | VM（--vm，KVM 硬件隔离） |

### 2.4 安全策略

| 安全层级 | 措施 | 配置 |
|----------|------|------|
| **L2 网络** | MAC 锁定 | `security.mac_filtering=true` |
| **L3 网络** | IP 锁定 | `security.ipv4_filtering=true` + `ipv4.address` 绑定 |
| **L3 网络** | IPv6 过滤 | `security.ipv6_filtering=true` |
| **L2 隔离** | VM 间端口隔离 | `security.port_isolation=true` |
| **固件** | 安全启动 | `security.secureboot=true`（与 Ubuntu cloud image 兼容） |
| **实例保护** | 防误删 | `security.protection.delete=true` |
| **认证** | SSH Key | cloud-init 注入 ed25519 公钥，禁用密码 SSH |
| **认证** | 应急密码 | `openssl passwd -6` 哈希，`chpasswd type: hash` |
| **VM 内防火墙** | 纵深防御 | cloud-init 安装 ufw，仅放行 22 |
| **宿主机防火墙** | 访问控制 | nftables 限制 VM 访问宿主机管理端口 |

### 2.5 存储方案

**保留 `dir` 存储池**：轻便快捷，无需预制空间，按需使用磁盘。后续如有性能需求可迁移到 ZFS。

## 三、实施步骤

### 步骤 1：宿主机基础优化（低风险）

**1.1 调整 swappiness**
```bash
sysctl vm.swappiness=10
echo "vm.swappiness=10" > /etc/sysctl.d/99-vm-host.conf
sysctl -p /etc/sysctl.d/99-vm-host.conf
```

**1.2 验证**
```bash
cat /proc/sys/vm/swappiness  # 应输出 10
swapon --show
```

### 步骤 2：配置网桥 netplan（⚠️⚠️⚠️ 最高风险操作）

> **危险等级：极高。** 无 IPMI 带外管理，网络断开 = 完全失联。
>
> **`netplan try` 不可靠的原因（Launchpad Bug #2083029 等）：**
> - 回滚是**进程内实现**（Python 超时），不是 systemd timer
> - SSH 断开时进程可能被 SIGHUP 杀死，**回滚代码不执行**
> - 多个 bug 报告 bridge 场景下回滚失败
>
> **因此采用 `systemd-run` + 定时重启的多层防护方案。**

#### 3.1 前置准备

```bash
# 记录 eno1 的 MAC 地址（网桥需要继承，防止上游交换机 MAC 不匹配）
ENO1_MAC=$(ip link show eno1 | awk '/ether/{print $2}')
echo "eno1 MAC: $ENO1_MAC"

# 备份当前 netplan
mkdir -p /root/netplan-backup
cp /etc/netplan/*.yaml /root/netplan-backup/

# br-pub 是 netplan 之前创建的（networkctl 日志确认），
# 不需要手动删除，netplan 会重新接管管理
```

#### 3.2 编写新 netplan 配置

```yaml
# /etc/netplan/01-bridge.yaml
network:
  version: 2
  renderer: networkd
  ethernets:
    eno1:
      dhcp4: false
      dhcp6: false
  bridges:
    br-pub:
      macaddress: "<ENO1_MAC>"
      interfaces:
        - eno1
      addresses:
        - "43.239.84.20/26"
      routes:
        - to: "default"
          via: "43.239.84.1"
      nameservers:
        addresses:
          - 8.8.8.8
          - 1.1.1.1
      parameters:
        stp: false
        forward-delay: 0
      dhcp4: false
      dhcp6: false
```

**关键参数说明：**
- `macaddress: <ENO1_MAC>` — **必须设置**，确保网桥继承物理网卡 MAC，防止上游交换机/路由器因 MAC 变化丢弃流量
- `stp: false` — 单物理网卡无环路风险，避免 30 秒 STP 学习延迟
- `forward-delay: 0` — 配合 STP 关闭
- 不需要设置 `mtu`，网桥自动继承 eno1 的 MTU 1500

#### 3.3 编写网桥切换脚本

```bash
#!/bin/bash
# /root/bridge-switch.sh — 通过 systemd-run 执行，独立于 SSH 会话
set -euo pipefail

LOG="/var/log/bridge-switch.log"
CONFIRM="/tmp/bridge-switch-confirmed"
BACKUP="/root/netplan-backup"
TIMEOUT=180  # 3 分钟等待确认

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG"; }

rm -f "$CONFIRM"
log "===== 网桥切换开始 ====="

# 第一步：语法预检
log "1. netplan 语法预检..."
if ! netplan generate 2>>"$LOG"; then
    log "语法错误，中止操作"
    exit 1
fi

# 第二步：删除旧的 netplan 配置，应用新配置
log "2. 应用新 netplan 配置..."
rm -f /etc/netplan/50-cloud-init.yaml
netplan apply 2>>"$LOG"

# 第三步：等待网络恢复（bridge 生效需要几秒）
log "3. 等待网络恢复..."
sleep 5

# 第四步：自检网络
log "4. 网络自检..."
GATEWAY_OK=false
for i in $(seq 1 30); do
    if ping -c 1 -W 2 43.239.84.1 >/dev/null 2>&1; then
        GATEWAY_OK=true
        log "   网关可达 (第 ${i} 次尝试)"
        break
    fi
    sleep 2
done

if [ "$GATEWAY_OK" = false ]; then
    log "网关不可达，立即回滚！"
    cp "$BACKUP"/*.yaml /etc/netplan/
    rm -f /etc/netplan/01-bridge.yaml
    netplan apply 2>>"$LOG"
    log "已自动回滚（网关不可达）"
    exit 1
fi

# 第五步：等待人工确认
log "5. 网络正常！等待确认（${TIMEOUT}秒超时）..."
log "   请执行: touch $CONFIRM"
for i in $(seq 1 $TIMEOUT); do
    if [ -f "$CONFIRM" ]; then
        log "已确认！网桥切换成功。"
        # 清理旧配置的 .bak 文件
        rm -f /etc/netplan/50-cloud-init.yaml.bak
        rm -f "$CONFIRM"
        exit 0
    fi
    sleep 1
done

# 第六步：超时未确认，回滚
log "超时未确认，执行回滚..."
cp "$BACKUP"/*.yaml /etc/netplan/
rm -f /etc/netplan/01-bridge.yaml
netplan apply 2>>"$LOG"
log "已自动回滚（超时）"
exit 1
```

#### 3.4 四层安全防护执行流程

```
┌─────────────────────────────────────────────────────────────┐
│  第 1 层：脚本内自检（即时）                                  │
│  → 网关 ping 不通，立即回滚                                   │
├─────────────────────────────────────────────────────────────┤
│  第 2 层：脚本内超时（3 分钟）                                │
│  → 人工未确认 touch /tmp/bridge-switch-confirmed，自动回滚    │
├─────────────────────────────────────────────────────────────┤
│  第 3 层：systemd-run 定时回滚（5 分钟）                      │
│  → 独立的 systemd transient unit，与脚本无关                  │
│  → 恢复备份 + netplan apply                                  │
├─────────────────────────────────────────────────────────────┤
│  第 4 层：定时重启（10 分钟）— 终极保险                       │
│  → reboot 后加载磁盘上的 netplan 配置                        │
│  → 如果回滚已写回原配置，重启后恢复原网络                      │
└─────────────────────────────────────────────────────────────┘
```

**完整执行命令序列：**

```bash
# ===== 在 tmux 中执行所有操作 =====
tmux new -s bridge-switch

# ----- A. 部署脚本和新 netplan 配置 -----
# （此时新配置文件已写好但未应用，旧配置仍在生效）
chmod +x /root/bridge-switch.sh

# ----- B. 设置第 3 层安全网：5 分钟后 systemd-run 回滚 -----
systemd-run --on-active=300 --unit=netplan-rollback \
  bash -c 'cp /root/netplan-backup/*.yaml /etc/netplan/ && rm -f /etc/netplan/01-bridge.yaml && netplan apply && echo "[$(date)] systemd-run rollback executed" >> /var/log/bridge-switch.log'

# ----- C. 设置第 4 层安全网：10 分钟后定时重启 -----
systemd-run --on-active=600 --unit=netplan-reboot \
  bash -c 'cp /root/netplan-backup/*.yaml /etc/netplan/ && rm -f /etc/netplan/01-bridge.yaml && reboot'

# ----- D. 确认安全网已就位 -----
systemctl list-timers --all | grep netplan  # 应看到两个 timer

# ----- E. 执行网桥切换（通过 systemd-run 独立于 tmux） -----
systemd-run --unit=bridge-switch /root/bridge-switch.sh

# ----- F. 监控日志 -----
journalctl -u bridge-switch -f
# 或
tail -f /var/log/bridge-switch.log
```

**SSH 可能在此处断开（正常，持续 5-60 秒）**

```bash
# ----- G. SSH 重连后验证 -----
ip addr show br-pub        # 确认 43.239.84.20/26
ip addr show eno1           # 确认无 IP
bridge link show            # 确认 eno1 是 br-pub 成员
ip route show               # 确认默认路由
ping -c 3 43.239.84.1      # 网关
ping -c 3 8.8.8.8          # 外网

# ----- H. 一切正常 → 确认并取消所有安全网 -----
touch /tmp/bridge-switch-confirmed         # 确认脚本
systemctl stop netplan-rollback.timer      # 取消第 3 层
systemctl stop netplan-reboot.timer        # 取消第 4 层

# ----- I. 验证失败 → 什么都不做，等待自动恢复 -----
# 3 分钟内：脚本自身超时回滚
# 5 分钟内：systemd-run 回滚
# 10 分钟内：系统重启
```

#### 3.5 故障场景分析

| 故障场景 | 自动恢复路径 | 预计恢复时间 |
|----------|-------------|-------------|
| 网桥配置正确，短暂断连后恢复 | SSH 重连 → 确认 | 5-60 秒 |
| 网关不可达（配置错误） | 脚本自检失败 → 立即回滚 | 1-2 分钟 |
| 网桥配置后网络通但 SSH 卡住 | 脚本 3 分钟超时 → 回滚 | ~3 分钟 |
| 脚本被 kill（系统异常） | systemd-run 5 分钟 → 回滚 | ~5 分钟 |
| netplan apply 回滚也失败 | 10 分钟后 reboot → 加载磁盘原配置 | ~10 分钟 |
| 系统完全卡死 | 无法自动恢复 → 需联系 IDC | ∞（极端情况） |

> **第 4 层 reboot 的原理**：在 reboot 定时器触发前，第 3 层已把原始配置写回 `/etc/netplan/`。重启后 systemd-networkd 读取磁盘配置，恢复 eno1 直连网络。

### 步骤 3：创建 Incus Profile（低风险）

```yaml
config:
  limits.cpu: "4"
  limits.memory: 8GiB
  security.secureboot: "true"
  security.protection.delete: "true"
description: "公网IP虚拟机模板 - 4C8G, 安全加固"
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: br-pub
    security.mac_filtering: "true"
    security.ipv4_filtering: "true"
    security.ipv6_filtering: "true"
    security.port_isolation: "true"
    type: nic
  root:
    path: /
    pool: default
    size: 50GiB
    type: disk
name: vm-public
```

```bash
incus profile create vm-public
incus profile edit vm-public < vm-public.yaml
incus profile show vm-public  # 验证
```

### 步骤 4：创建 VM 并配置（中风险）

**先创建 vm-node01 验证，确认无误后再创建 vm-node02。**

#### 5.1 生成凭据

```bash
# 生成随机强密码
VM01_PASS=$(openssl rand -base64 24)
VM01_PASS_HASH=$(echo "$VM01_PASS" | openssl passwd -6 -stdin)
echo "vm-node01: $VM01_PASS" >> /root/.vm-credentials
chmod 600 /root/.vm-credentials

# SSH 密钥（如果没有则生成）
[ -f /root/.ssh/id_ed25519 ] || ssh-keygen -t ed25519 -N "" -f /root/.ssh/id_ed25519
SSH_PUBKEY=$(cat /root/.ssh/id_ed25519.pub)
```

#### 5.2 创建实例（不启动）

```bash
incus init images:ubuntu/24.04/cloud vm-node01 --vm --profile vm-public
```

#### 5.3 绑定 IP（Incus 层面锁定）

```bash
incus config device override vm-node01 eth0 ipv4.address=43.239.84.21
```

> `ipv4.address` 在 unmanaged bridge 上仅作为安全过滤依据（ebtables/nftables 规则），不会自动分配 IP。VM 内必须通过 cloud-init 配置相同 IP。

#### 5.4 cloud-init network-config

```yaml
network:
  version: 2
  ethernets:
    all-en:
      match:
        name: "en*"
      dhcp4: false
      dhcp6: false
      addresses:
        - 43.239.84.21/26
      routes:
        - to: default
          via: 43.239.84.1
      nameservers:
        addresses:
          - 8.8.8.8
          - 1.1.1.1
```

> 使用 `match: name: "en*"` 而非硬编码 `enp5s0`。VM 网卡名取决于 QEMU PCI 插槽，不同版本可能变化。Ubuntu cloud 使用 systemd-networkd，支持 name glob。

#### 5.5 cloud-init user-data

```yaml
#cloud-config
package_update: true
package_upgrade: true
packages:
  - curl
  - wget
  - vim
  - htop
  - ufw

swap:
  filename: /swapfile
  size: 8G
  maxsize: 8G

users:
  - name: ubuntu
    groups: sudo
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    lock_passwd: false
    ssh_authorized_keys:
      - <SSH_PUBKEY>

chpasswd:
  expire: false
  users:
    - name: ubuntu
      password: <VM01_PASS_HASH>
      type: hash

ssh_pwauth: false
disable_root: true

runcmd:
  - ufw default deny incoming
  - ufw default allow outgoing
  - ufw allow 22/tcp
  - ufw --force enable
```

#### 5.6 应用并启动

```bash
incus config set vm-node01 cloud-init.network-config - < network-config-node01.yaml
incus config set vm-node01 cloud-init.user-data - < user-data-node01.yaml
incus start vm-node01
```

#### 5.7 验证

```bash
# 等待 cloud-init 完成（1-3 分钟）
incus exec vm-node01 -- cloud-init status --wait

# 网络
incus exec vm-node01 -- ip addr show
incus exec vm-node01 -- ping -c 3 43.239.84.1
incus exec vm-node01 -- ping -c 3 8.8.8.8

# 规格
incus exec vm-node01 -- nproc            # → 4
incus exec vm-node01 -- free -h          # → ~8G
incus exec vm-node01 -- swapon --show    # → 8G /swapfile

# SSH
ssh ubuntu@43.239.84.21

# 防火墙
incus exec vm-node01 -- ufw status
```

> **cloud-init 排错**：如果配置有误，无需重建实例。可以：
> `incus stop vm-node01 → 修改 cloud-init 配置 → incus start vm-node01 → incus exec vm-node01 -- cloud-init clean --logs --reboot`

#### 5.8 vm-node01 通过后创建 vm-node02

重复 5.1-5.7，替换 IP 为 `43.239.84.22`，重新生成独立密码。

### 步骤 5：安全验证（低风险）

**6.1 IP 锁定验证**
```bash
# VM 内尝试添加伪造 IP — 命令可执行但流量被 Incus 过滤层阻断
incus exec vm-node01 -- ip addr add 43.239.84.30/26 dev $(incus exec vm-node01 -- ls /sys/class/net/ | grep en)
# 从宿主机 ping 43.239.84.30 → 应不通
```

**6.2 MAC 伪造验证**
```bash
incus exec vm-node01 -- ip link set dev $(incus exec vm-node01 -- ls /sys/class/net/ | grep en) address 00:11:22:33:44:55
# 修改后网络应中断
```

**6.3 VM 间隔离验证**
```bash
incus exec vm-node01 -- ping -c 3 43.239.84.22  # → 应不通（port_isolation）
```

**6.4 规格确认**
```bash
for vm in vm-node01 vm-node02; do
  echo "=== $vm ==="
  incus exec $vm -- nproc
  incus exec $vm -- free -h
  incus exec $vm -- swapon --show
  incus exec $vm -- df -h /
  incus exec $vm -- cat /etc/os-release | head -3
done
```

### 步骤 6：宿主机防火墙加固（中风险）

> **注意**：防火墙配置同样需要安全网，错误规则会锁死 SSH。

```bash
# 写入 nftables 规则文件
cat > /etc/nftables-incus.conf << 'EOF'
#!/usr/sbin/nft -f
flush ruleset

table inet filter {
    chain input {
        type filter hook input priority 0; policy drop;

        iif "lo" accept
        ct state established,related accept

        # ICMP
        ip protocol icmp accept
        ip6 nexthdr icmpv6 accept

        # SSH（从任何来源，后续可收窄）
        tcp dport 22 accept

        # 允许来自 br-pub 的 bridge 管理流量
        iif "br-pub" accept

        log prefix "nft-drop: " drop
    }

    chain forward {
        type filter hook forward priority 0; policy accept;
    }

    chain output {
        type filter hook output priority 0; policy accept;
    }
}
EOF

# 安全应用：先设 5 分钟后清空规则的安全网
systemd-run --on-active=300 --unit=nft-rollback \
  bash -c 'nft flush ruleset && echo "[$(date)] nft rolled back" >> /var/log/bridge-switch.log'

# 应用规则
nft -f /etc/nftables-incus.conf

# 验证 SSH 仍然可用后，取消安全网
systemctl stop nft-rollback.timer

# 持久化
systemctl enable nftables
cp /etc/nftables-incus.conf /etc/nftables.conf
```

## 四、风险评估

| 风险 | 级别 | 缓解措施 |
|------|------|----------|
| **netplan 网桥切换导致失联** | **极高** | 四层防护：脚本自检→超时回滚→systemd-run 回滚→定时 reboot |
| **`netplan try` 回滚失败** | 高 | **已弃用** `netplan try`，改用自定义脚本 + systemd-run |
| **无 IPMI 无法救援** | 高 | 多层自动恢复 + 终极 reboot 兜底 |
| **MAC 地址变化导致上游丢包** | 高 | netplan 显式设置 `macaddress` 继承 eno1 MAC |
| **nftables 锁死 SSH** | 中 | systemd-run 5 分钟后 flush ruleset |
| **dir 存储性能不足** | 低 | 后续可迁移到 ZFS，当前无实例迁移成本为零 |
| **cloud-init 配置错误** | 中 | 先建 1 台验证；`cloud-init clean --reboot` 重置 |
| **VM 内 IP 伪造** | 低 | `security.ipv4_filtering` + `ipv4.address` 硬锁 |
| **VM 逃逸宿主机** | 极低 | KVM 硬件隔离 + secureboot + 独立内核 |

## 五、替代方案（未采用）

### 方案 B：Managed Bridge + DHCP
- 未采用：公网场景多一层 dnsmasq，br-pub 已存在为 unmanaged

### 方案 C：Macvlan
- 未采用：宿主机与 VM 无法通信

### 方案 D：Routed NIC
- 未采用：需上游路由器配合

### 方案 E：ZFS 存储池
- 优点：即时快照、数据校验、lz4 压缩、zvol 原生块设备性能更好
- 缺点：需额外安装、loopback 方式仍有开销、ARC 占用内存
- **未采用原因**：用户选择 dir 轻便快捷方案，后续可按需迁移

## 六、参考来源

- [Incus NIC 设备文档](https://linuxcontainers.org/incus/docs/main/reference/devices_nic/) — unmanaged bridge + security filtering
- [Incus 安全文档](https://linuxcontainers.org/incus/docs/main/explanation/security/) — VM 隔离机制
- [Incus cloud-init](https://linuxcontainers.org/incus/docs/main/cloud-init/) — 配置键
- [Incus IP filtering commit](https://gitea.swigg.net/dustins/incus/commit/82f93b5) — unmanaged bridge 必须手动指定 ipv4.address
- [Launchpad Bug #2083029](https://bugs.launchpad.net/netplan/+bug/2083029) — netplan try 回滚失败
- [ArchWiki: Network bridge](https://wiki.archlinux.org/title/Network_bridge) — bridge 切换顺序
- [Hetzner bridge 失联案例](https://superuser.com/questions/1389309) — MAC 绑定问题
- [systemd-run 文档](https://www.freedesktop.org/software/systemd/man/systemd-run.html) — transient timer
