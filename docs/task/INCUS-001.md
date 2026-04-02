# INCUS-001: Incus VM 公网IP网桥环境搭建与安全加固

- **状态**: completed
- **owner**: claude-main
- **创建时间**: 2026-04-02
- **计划**: PLAN-001

## 描述

在宿主机 43.239.84.20 上搭建 Incus VM 虚拟化环境，通过网桥模式为每台 VM 分配独立公网 IP，实现自动配置、安全隔离和 IP 锁定。先创建 2 台 VM 验证。

## 验收标准

- [ ] 网桥 br-pub 正确桥接 eno1，宿主机网络正常
- [ ] 2 台 VM 运行 Ubuntu 24.04 cloud，各自拥有独立公网 IP
- [ ] VM 规格：4核8G内存8G swap
- [ ] IP 锁定：VM 内部无法伪造/修改 IP
- [ ] 安全隔离：secureboot、MAC/IP 过滤均启用
- [ ] 密码使用随机生成的强密码
- [ ] 宿主机 swappiness 优化
