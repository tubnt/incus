# Incus VM 部署与运维指南

## 环境概览

| 项目 | 值 |
|------|------|
| 宿主机 IP | 43.239.84.20 |
| 网桥 | br-pub (eno1 桥接) |
| 子网 | 43.239.84.0/26 (掩码 255.255.255.192) |
| 网关 | 43.239.84.1 |
| 可用 IP | 43.239.84.23 ~ 43.239.84.36 (14个) |
| 已用 IP | .21 (vm-node01), .22 (vm-node02) |
| 凭据文件 | /root/.vm-credentials |
| 脚本目录 | /opt/incus-scripts/ |

## 连接

```bash
# 密码登录（ubuntu 或 root 均可）
ssh root@43.239.84.21
ssh ubuntu@43.239.84.22

# 应急（Incus 控制台）
incus exec vm-node01 -- bash
```

凭据查看：`cat /root/.vm-credentials`

## 脚本

### 1. 环境初始化（全新宿主机运行一次）

```bash
setup-env
# 或
/opt/incus-scripts/setup-env.sh
```

自动完成：swappiness 调优 → 网桥配置（含安全网）→ Profile 创建 → nftables → SSH 密钥

### 2. 创建虚拟机

```bash
create-vm <名称> <IP> [镜像]
```

#### Linux（全自动）

```bash
create-vm vm-node03 43.239.84.23              # Ubuntu 24.04（默认）
create-vm vm-web    43.239.84.24 debian12     # Debian 12
create-vm vm-app    43.239.84.25 rocky9       # Rocky Linux 9
create-vm vm-dev    43.239.84.26 centos10     # CentOS 10
create-vm vm-test   43.239.84.27 alma9        # AlmaLinux 9
create-vm vm-build  43.239.84.28 fedora42     # Fedora 42
create-vm vm-arch   43.239.84.29 arch         # Arch Linux
```

完整镜像列表：运行 `create-vm` 不带参数查看

#### Windows（半自动）

```bash
# 不带 ISO（后续手动挂载）
create-vm vm-win01 43.239.84.27 windows

# 带 ISO 路径（自动挂载）
create-vm vm-win01 43.239.84.27 windows /root/win11.iso
```

Windows 安装流程：
1. 脚本自动创建空 VM、绑定 IP、下载 virtio 驱动
2. `incus start vm-win01 && incus console vm-win01 --type=vga` 进入图形安装
3. 磁盘选择界面加载 virtio 存储驱动 (`viostor/w11/amd64`)
4. 安装完成后在 Windows 内配置静态 IP
5. 卸载 ISO：`incus config device remove vm-win01 install`

## 常用运维

```bash
incus list                                    # 查看所有 VM
incus stop/start/restart vm-node01            # 停止/启动/重启
incus config show vm-node01                   # 查看配置
incus config device show vm-node01            # 查看安全配置
incus console vm-node01 --type=vga            # 图形控制台 (Windows)

# 删除 VM（需先关闭保护）
incus config set vm-node01 security.protection.delete=false
incus delete vm-node01 --force
```

## 安全架构

| 层级 | 机制 | 说明 |
|------|------|------|
| L1 | ipv4_filtering | IP 源地址锁定，每台 VM 只能用绑定的 IP |
| L2 | mac_filtering | MAC 地址锁定 |
| L3 | port_isolation | VM 间二层隔离，互相不可达 |
| L4 | secureboot | UEFI 安全启动 (Linux) |
| L5 | VM 内防火墙 | UFW (Debian/Ubuntu) 或 firewalld (RHEL系) |
| L6 | 宿主机 bridge nftables | 统一过滤所有 VM 出入流量（`vm-firewall` 管理） |
| L7 | 宿主机 inet nftables | 保护宿主机自身 |
| L8 | KVM | 硬件级隔离，独立内核 |

## 宿主机统一防火墙（推荐）

所有 VM 的入站流量经过宿主机 nftables `bridge` 层统一过滤，**无需进入 VM 操作**。

```bash
vm-firewall status                          # 查看状态
vm-firewall open 80                         # 所有 VM 开放 HTTP
vm-firewall open 443                        # 所有 VM 开放 HTTPS
vm-firewall close 80                        # 关闭全局 HTTP
vm-firewall open-for 43.239.84.21 3306      # 仅 vm-node01 开放 MySQL
vm-firewall close-for 43.239.84.21 3306     # 关闭
vm-firewall add-vm 43.239.84.23             # 注册新 VM
vm-firewall save                            # 持久化
vm-firewall list                            # 查看完整规则
```

默认策略：所有 VM 仅开放 SSH(22)，出站全放行。

## VM 内防火墙管理（可选，纵深防御）

### Ubuntu / Debian (ufw)

```bash
ufw status                           # 查看状态
ufw allow 80/tcp                     # 开放 HTTP
ufw allow 443/tcp                    # 开放 HTTPS
ufw allow 3306/tcp                   # 开放 MySQL
ufw allow from 43.239.84.20 to any   # 仅允许宿主机访问
ufw delete allow 80/tcp              # 关闭端口
ufw reload                           # 重载规则
```

### CentOS / Rocky / Alma / Fedora (firewalld)

```bash
firewall-cmd --list-all                             # 查看状态
firewall-cmd --permanent --add-port=80/tcp          # 开放 HTTP
firewall-cmd --permanent --add-port=443/tcp         # 开放 HTTPS
firewall-cmd --permanent --add-service=http         # 按服务名开放
firewall-cmd --permanent --remove-port=80/tcp       # 关闭端口
firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="43.239.84.20" accept'  # 仅允许宿主机
firewall-cmd --reload                               # 重载规则
```

### Arch Linux (ufw)

与 Ubuntu 相同。

### Windows

```powershell
# PowerShell 管理员
New-NetFirewallRule -DisplayName "HTTP" -Direction Inbound -Protocol TCP -LocalPort 80 -Action Allow
Remove-NetFirewallRule -DisplayName "HTTP"
Get-NetFirewallRule | Where-Object {$_.Enabled -eq 'True' -and $_.Direction -eq 'Inbound'} | Format-Table
```

## 密码管理

```bash
# 修改 VM 密码（从宿主机执行，无需登录 VM）
incus exec vm-node01 -- bash -c "echo 'root:新密码' | chpasswd"
incus exec vm-node01 -- bash -c "echo 'ubuntu:新密码' | chpasswd"

# 忘记密码重置
incus exec vm-node01 -- passwd root
incus exec vm-node01 -- passwd ubuntu

# SSH 登录后自己改
passwd              # 改当前用户
sudo passwd root    # 改 root
```

凭据文件：`cat /root/.vm-credentials`

## 注意事项

1. **cloud-init 仅首次运行**：所有配置须在 `incus start` 前完成
2. **IP 双重配置**：Incus 设备层 (`ipv4.address`) + cloud-init 网络层必须一致
3. **Windows 无 cloud-init**：网络需在系统内手动配置
4. **删除前关保护**：`security.protection.delete=false`
