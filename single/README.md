# Incus 单机版 — 公网 IP 虚拟机管理

一键在单台物理服务器上部署多台公网 IP 虚拟机（KVM），支持 12 种 Linux 发行版 + Windows。

## 功能特性

- **一键环境初始化**：自动检测网络参数、配置网桥、防火墙、Profile
- **一键创建 VM**：自动分配密码、配置网络、安全加固
- **IP 锁定**：每台 VM 只能使用绑定的 IP，无法伪造
- **宿主机统一防火墙**：`bridge` 层过滤所有 VM 流量，支持白名单
- **VM→宿主机隔离**：VM 无法访问宿主机任何端口
- **多系统支持**：Ubuntu、Debian、CentOS、Rocky、Alma、Fedora、Arch、Windows

## 前提条件

- 已安装 Incus（建议 6.14+）
- 物理服务器有可用的公网 IP 段
- Ubuntu 22.04+ 或 Debian 12+（宿主机系统）

## 快速开始

### 1. 部署脚本到服务器

```bash
# 复制脚本到服务器
scp scripts/*.sh root@your-server:/opt/incus-scripts/

# 创建快捷命令
ssh root@your-server
ln -sf /opt/incus-scripts/setup-env.sh /usr/local/bin/setup-env
ln -sf /opt/incus-scripts/create-vm.sh /usr/local/bin/create-vm
ln -sf /opt/incus-scripts/vm-firewall.sh /usr/local/bin/vm-firewall
chmod +x /opt/incus-scripts/*.sh
```

### 2. 初始化环境（只需运行一次）

```bash
setup-env
```

脚本会：
1. 自动检测物理网卡、IP、子网、网关（也可手动填写）
2. 配置网桥（含 5 分钟自动回滚安全网）
3. 创建 Incus Profile
4. 配置宿主机防火墙
5. 生成 SSH 密钥

如果你的网络参数特殊，编辑 `setup-env.sh` 顶部配置区：

```bash
HOST_IP="203.0.113.10"         # 手动填写宿主机 IP
SUBNET_MASK="/26"              # 手动填写子网掩码
GATEWAY="203.0.113.1"          # 手动填写网关
PHYS_IFACE="eno1"              # 手动填写物理网卡名
```

### 3. 创建虚拟机

```bash
create-vm <名称> <IP> [镜像]
```

#### Linux 系统（全自动）

```bash
create-vm vm-web    203.0.113.11                   # Ubuntu 24.04（默认）
create-vm vm-db     203.0.113.12 debian12           # Debian 12
create-vm vm-app    203.0.113.13 rocky9             # Rocky Linux 9
create-vm vm-cache  203.0.113.14 centos10           # CentOS 10
create-vm vm-build  203.0.113.15 alma9              # AlmaLinux 9
create-vm vm-ci     203.0.113.16 fedora42           # Fedora 42
create-vm vm-dev    203.0.113.17 arch               # Arch Linux
```

运行 `create-vm` 不带参数可查看全部可用镜像。

#### Windows 系统（半自动）

```bash
create-vm vm-win01 203.0.113.18 windows              # 不带 ISO
create-vm vm-win01 203.0.113.18 windows /root/win.iso # 带 ISO
```

Windows 安装流程：
1. 脚本自动创建空 VM、下载 virtio 驱动
2. `incus start vm-win01 && incus console vm-win01 --type=vga`
3. 磁盘选择界面加载 virtio 驱动 (`viostor/w11/amd64`)
4. 安装完成后手动配置静态 IP
5. `incus config device remove vm-win01 install` 卸载 ISO

### 4. 连接

```bash
# 密码登录（创建时会显示密码）
ssh root@203.0.113.11

# 查看所有凭据
cat /root/.vm-credentials

# 应急访问（不经过网络）
incus exec vm-web -- bash
```

## 防火墙管理

### 宿主机统一防火墙

所有 VM 入站流量经过宿主机 nftables `bridge` 层统一过滤：

```bash
vm-firewall status                          # 查看状态
vm-firewall open 80                         # 所有 VM 开放 HTTP
vm-firewall open 443                        # 所有 VM 开放 HTTPS
vm-firewall close 80                        # 关闭全局 HTTP
vm-firewall open-for 203.0.113.11 3306      # 仅某台 VM 开放 MySQL
vm-firewall close-for 203.0.113.11 3306     # 关闭
vm-firewall save                            # 持久化（不 save 重启丢失！）
```

### IP 白名单

全锁模式下，仅白名单 IP 可访问 VM：

```bash
vm-firewall allow 116.23.45.0/24            # 添加网段
vm-firewall allow 8.8.8.8                   # 添加单 IP
vm-firewall deny 116.23.45.0/24             # 移除
vm-firewall whitelist                        # 查看白名单
vm-firewall save                             # 持久化
```

### VM 管理

```bash
vm-firewall add-vm 203.0.113.19             # 注册新 VM IP
vm-firewall remove-vm 203.0.113.19          # 移除
vm-firewall list                             # 查看完整规则
```

## 常用运维

```bash
incus list                                    # 查看所有 VM
incus stop/start/restart vm-web               # 停止/启动/重启
incus config show vm-web                      # 查看配置
incus config device show vm-web               # 查看安全配置
incus console vm-web --type=vga               # 图形控制台 (Windows)

# 删除 VM
incus config set vm-web security.protection.delete=false
incus delete vm-web --force
```

## 密码管理

```bash
# 从宿主机改密码（无需登录 VM）
incus exec vm-web -- bash -c "echo 'root:新密码' | chpasswd"

# 忘记密码
incus exec vm-web -- passwd root

# 查看所有凭据
cat /root/.vm-credentials
```

## 安全架构

```
┌─────────────────────────────────────────────┐
│ L8  KVM 硬件级隔离（独立内核）                 │
│ L7  inet nftables（宿主机自身保护）            │
│ L6  bridge nftables（VM 流量统一过滤/白名单）  │
│ L5  VM 内防火墙（UFW/firewalld）              │
│ L4  UEFI Secure Boot                         │
│ L3  port_isolation（VM 间二层隔离）            │
│ L2  mac_filtering（MAC 锁定）                 │
│ L1  ipv4_filtering（IP 源地址锁定）            │
└─────────────────────────────────────────────┘
```

| 攻击场景 | 防护机制 |
|----------|---------|
| VM 伪造 IP | ipv4_filtering 拦截 |
| VM 伪造 MAC | mac_filtering 拦截 |
| VM 攻击其他 VM | port_isolation 二层隔离 |
| VM 攻击宿主机 | bridge input 全阻断 |
| VM 信息泄露 | guest API 已关闭 |
| 外部扫描 VM | bridge forward 白名单/端口过滤 |

## 脚本列表

| 脚本 | 说明 |
|------|------|
| `scripts/setup-env.sh` | 环境初始化（网桥、Profile、防火墙） |
| `scripts/create-vm.sh` | 创建虚拟机（Linux/Windows） |
| `scripts/vm-firewall.sh` | 防火墙管理（端口、白名单、VM 注册） |

## 注意事项

1. **cloud-init 仅首次运行**：所有配置须在 `incus start` 前完成
2. **IP 双重配置**：Incus 设备层 (`ipv4.address`) + cloud-init 网络层必须一致
3. **Windows 无 cloud-init**：网络需在系统内手动配置
4. **删除前关保护**：`security.protection.delete=false`
5. **bridge 规则必须加 `ether type ip`**：否则 IP 匹配在 bridge 层不生效
6. **VM 隔离放在 bridge input**：不能放 inet input（会干扰桥接返回流量）
7. **Ubuntu 24.04 PermitRootLogin**：默认 `without-password`，需追加 `PermitRootLogin yes`
