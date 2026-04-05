# 变更日志

## 2026-04-02 19:30 [进度]

完成 Incus VM 公网IP网桥环境搭建（PLAN-001）：

- 宿主机 swappiness 调整为 10
- 网桥 br-pub 配置完成（eno1 桥接，MAC 继承，STP 关闭）
- 使用 systemd-run 多层安全防护完成网桥切换（无 IPMI 场景）
- 创建 vm-public profile（4C8G, secureboot, MAC 过滤, port_isolation）
- 部署 vm-node01 (43.239.84.21) 和 vm-node02 (43.239.84.22)
- 安全验证通过：IP 锁定、VM 间隔离、UFW 防火墙
- 宿主机 nftables 防火墙已配置并持久化

已知问题：Ubuntu cloud image 未预装 openssh-server，需在 cloud-init packages 中显式添加
