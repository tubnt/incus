# Incus VM 管理工具集

基于 [Incus](https://linuxcontainers.org/incus/) 的公网 IP 虚拟机管理方案，提供一键部署、安全加固、统一防火墙。

## 版本

| 版本 | 目录 | 状态 | 说明 |
|------|------|------|------|
| 单机版 | [`single/`](single/) | 已完成 | 单台物理机部署多台公网 IP 虚拟机 |
| 集群版 | [`cluster/`](cluster/) | 开发中 | 多节点集群，支持迁移和高可用 |

## 单机版快速开始

```bash
# 1. 初始化环境
setup-env

# 2. 创建虚拟机
create-vm vm-web 203.0.113.11              # Ubuntu 24.04
create-vm vm-db  203.0.113.12 debian12     # Debian 12
create-vm vm-win 203.0.113.13 windows      # Windows

# 3. 防火墙管理
vm-firewall allow 1.2.3.4                  # 添加白名单
vm-firewall open 80                        # 开放端口
vm-firewall save                           # 持久化
```

详细文档见 [`single/README.md`](single/README.md)
