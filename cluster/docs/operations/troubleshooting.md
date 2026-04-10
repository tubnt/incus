# 故障处理手册

> 适用范围：5 节点 Incus + Ceph 集群（202.151.179.224/27）

---

## 恢复时间目标（RTO）汇总

| 故障场景 | RTO | 自动恢复 | 备注 |
|----------|-----|----------|------|
| 单节点宕机 | ≤ 6 分钟 | 是 | Auto-healing 自动迁移 VM |
| 单 OSD 故障 | ≤ 2 分钟 | 是 | Ceph 自动标记 out + recovery |
| 多 OSD 故障（同节点） | ≤ 6 分钟 | 是 | 随节点故障一同恢复 |
| PG degraded | ≤ 10 分钟 | 是 | 自动 recovery |
| PG stuck | 30-60 分钟 | 否 | 需人工介入 |
| 网络故障（单节点） | ≤ 6 分钟 | 是 | 等同节点宕机 |
| 网络故障（全局） | 视情况 | 否 | 需人工排查 |
| VM 无法启动 | 15-30 分钟 | 否 | 人工排查 |
| Paymenter 故障 | ≤ 10 分钟 | 否 | Docker 重启 |
| 存储空间不足 | 1-4 小时 | 否 | 紧急清理或扩容 |
| 双节点同时宕机 | 30-60 分钟 | 部分 | Ceph min_size=2 仍可 IO |
| 三节点同时宕机 | 数小时 | 否 | 丢失 quorum，需人工恢复 |

---

## 1. 节点宕机处理

### 1.1 自动恢复流程

集群配置了自动恢复机制：
- **20 秒**：Incus 标记节点为 offline（`cluster.offline_threshold=20`）
- **5 分钟**：触发 auto-healing（`cluster.healing_threshold=300`）
- VM 自动在其他节点冷启动（依赖 Ceph 共享存储）

**总恢复时间约 5-6 分钟**，满足 99.9% SLA（月停机 ≤ 43 分钟）。

### 1.2 验证自动恢复

```bash
# 检查集群状态
incus cluster list
# 预期：故障节点显示 OFFLINE 或 EVACUATED

# 检查 VM 是否已在其他节点启动
incus list --project customers -c n,s,l
# 预期：所有 VM 显示 RUNNING，location 为非故障节点

# 检查 Ceph 状态
ceph -s
# 预期：可能有 degraded PG，但 IO 正常
```

### 1.3 自动恢复未触发时的手动处理

```bash
# 1. 确认节点确实不可达
ping 202.151.179.XXX   # 公网
ping 10.0.10.X         # 管理网

# 2. 尝试通过 IPMI/BMC 重启（如果有）
# IPMI_PASSWORD 从密码管理器获取，避免明文出现在 bash 历史
export IPMI_PASSWORD='<从密码管理器获取>'
ipmitool -H <bmc-ip> -U admin -E power cycle

# 3. 如果无法恢复，手动疏散
incus cluster evacuate <故障节点>

# 4. 如果疏散也失败，手动迁移关键 VM
incus move <vm-name> --target <健康节点> --project customers

# 5. 长期无法恢复，移除节点
incus cluster remove <故障节点> --force
```

### 1.4 节点恢复后处理

```bash
# 1. 节点重启后检查服务
systemctl status incusd
systemctl status ceph-osd@*

# 2. 确认节点重新加入集群
incus cluster list

# 3. 将 VM 迁回（可选，视负载均衡需要）
bash scripts/batch-migrate.sh <临时节点> <恢复节点>

# 4. 验证 Ceph 回到 HEALTH_OK
ceph -w
```

---

## 2. Ceph OSD 故障处理

### 2.1 单 OSD 宕机

```bash
# 1. 确认故障 OSD
ceph osd tree | grep down

# 2. 检查 OSD 日志
journalctl -u ceph-osd@<id> --since "10 minutes ago"

# 3. 尝试重启 OSD
systemctl restart ceph-osd@<id>

# 4. 如果重启失败，检查磁盘健康
smartctl -a /dev/sdX

# 5. 如果磁盘故障，标记 OSD 为 out 并等待 recovery
ceph osd out osd.<id>
ceph -w  # 观察 recovery 进度
```

### 2.2 磁盘更换流程

```bash
# 1. 确保 OSD 已标记为 out
ceph osd out osd.<id>

# 2. 等待数据重新分布完成（所有 PG 回到 active+clean，超时 4 小时）
TIMEOUT=14400; ELAPSED=0
while ! ceph health | grep -q HEALTH_OK; do
  sleep 10; ELAPSED=$((ELAPSED+10))
  if [ "$ELAPSED" -ge "$TIMEOUT" ]; then
    echo "ERROR: 等待 HEALTH_OK 超时（${TIMEOUT}s），请人工介入"
    break
  fi
done

# 3. 停止并删除 OSD
systemctl stop ceph-osd@<id>
ceph osd purge osd.<id> --yes-i-really-mean-it

# 4. 物理更换磁盘

# 5. 部署新 OSD
cephadm shell -- ceph orch daemon add osd <host>:/dev/<new-disk>

# 6. 验证新 OSD 正常
ceph osd tree
ceph -w
```

### 2.3 多 OSD 故障

如果同时有多个 OSD 故障：

```bash
# 1. 立即检查是否丢失数据（min_size=2）
ceph health detail

# 2. 设置 noout 防止雪崩式 recovery
ceph osd set noout

# 3. 逐个排查故障 OSD
# 优先恢复能恢复的 OSD

# 4. 恢复后取消 noout
ceph osd unset noout
```

**关键阈值：**
- 1 个 OSD 故障：数据安全（3 副本 → 2 副本）
- 2 个 OSD 故障（不同节点）：IO 可能暂停（min_size=2）
- 3 个 OSD 故障（不同节点且承载相同 PG）：**数据丢失风险**

---

## 3. PG degraded/stuck 处理

### 3.1 PG degraded

```bash
# 1. 查看降级 PG 数量
ceph pg stat

# 2. 查看具体哪些 PG 降级
ceph pg dump_stuck degraded

# 3. 通常等待自动 recovery 即可，观察进度
ceph -w

# 4. 如果 recovery 进度停滞，检查原因
ceph health detail
```

### 3.2 PG stuck

```bash
# 查看各种类型的 stuck PG
ceph pg dump_stuck unclean    # 未清理
ceph pg dump_stuck inactive   # 不活跃（最严重，无法 IO）
ceph pg dump_stuck stale      # 过期（PG 无 OSD 报告）

# inactive PG 处理
# 1. 检查承载该 PG 的 OSD 是否都不可用
ceph pg <pg-id> query

# 2. 如果是 OSD 问题，先恢复 OSD
# 3. 如果 OSD 正常但 PG 仍然 stuck
ceph pg repair <pg-id>

# stale PG 处理
# 通常由于承载 OSD 长时间不可用
# 恢复对应 OSD 或 force-create-pg（危险操作，会丢数据）
```

### 3.3 PG inconsistent

```bash
# 1. 发现 inconsistent PG
ceph health detail | grep inconsistent

# 2. 执行 deep-scrub
ceph pg deep-scrub <pg-id>

# 3. 如果 deep-scrub 后仍然 inconsistent
ceph pg repair <pg-id>

# 4. 检查修复结果
ceph pg <pg-id> query
```

---

## 4. 网络故障排查

### 4.1 br-pub（公网桥）故障

**症状**：VM 无法访问公网 / 外部无法访问 VM

```bash
# 1. 检查网桥状态
ip link show br-pub
bridge link show

# 2. 检查物理网卡
ethtool eno1

# 3. 检查 VM 网卡是否连接到网桥
bridge link show | grep <vm-tap>

# 4. 检查 nftables 规则是否正常
nft list chain bridge vm_filter forward

# 5. 检查上游网关可达性
ping 202.151.179.225

# 6. 如果网桥异常，重新应用网络配置
bash scripts/apply-network.sh
```

### 4.2 VLAN 故障

**症状**：管理网不通（VLAN 10）或 Ceph 网络不通（VLAN 20）

```bash
# 1. 检查 VLAN 接口
ip link show eno1.10  # 管理网 VLAN
ip link show eno1.20  # Ceph Public VLAN

# 2. 检查 IP 配置
ip addr show eno1.10
ip addr show eno1.20

# 3. 测试 VLAN 连通性
ping 10.0.10.1  # 管理网
ping 10.0.20.1  # Ceph Public

# 4. 检查交换机 trunk 配置
# 确认 VLAN 10 和 20 在 trunk 端口上已标记

# 5. 重新应用网络配置
netplan apply
```

### 4.3 Ceph Cluster 网络（eno2）故障

**症状**：Ceph recovery/replication 缓慢或停滞

```bash
# 1. 检查 eno2 状态
ethtool eno2

# 2. 测试 Cluster 网络连通性
for i in 1 2 3 4 5; do ping -c1 -W1 10.0.30.$i && echo "node$i OK" || echo "node$i FAIL"; done

# 3. 检查 MTU 配置（应为 9000）
ip link show eno2 | grep mtu

# 4. Ceph 会自动回退到 Public 网络进行 recovery
# 但性能会严重下降，应尽快修复 Cluster 网络
```

### 4.4 WireGuard 隧道故障

**症状**：Paymenter 无法连接 Incus API

```bash
# 1. 检查 WireGuard 接口
wg show

# 2. 检查隧道连通性
ping 10.100.0.1   # Paymenter 端
ping 10.100.0.10  # 集群端

# 3. 检查最近握手时间
wg show wg0 | grep "latest handshake"
# 如果超过 2 分钟没有握手，说明隧道中断

# 4. 重启 WireGuard
systemctl restart wg-quick@wg0

# 5. 检查密钥配置是否一致
# 两端 PublicKey 必须互相匹配
```

---

## 5. VM 无法启动排查

### 5.1 排查步骤

```bash
# 1. 查看 VM 状态和错误信息
incus info <vm-name> --project customers

# 2. 查看 Incus 日志
journalctl -u incusd --since "5 minutes ago" | grep <vm-name>

# 3. 检查 Ceph 存储是否可用
rbd ls incus-pool
rbd info incus-pool/<vm-disk-image>

# 4. 检查目标节点资源是否充足
incus info --resources

# 5. 尝试启动并查看控制台输出
incus start <vm-name> --project customers --console
```

### 5.2 常见原因

| 原因 | 表现 | 解决方案 |
|------|------|----------|
| Ceph 不可用 | 启动超时 | 先恢复 Ceph |
| 资源不足 | OCI 错误 | 迁移到有资源的节点 |
| 磁盘镜像损坏 | QEMU 启动失败 | 从快照恢复 |
| 配置错误 | 参数校验失败 | 检查 VM 配置 |
| 节点故障 | 调度失败 | 迁移到其他节点 |

---

## 6. Paymenter 故障处理

### 6.1 Paymenter 服务无法访问

```bash
# 1. 检查 Docker 容器状态
docker compose -f paymenter/docker-compose.yml ps

# 2. 查看应用日志
docker compose -f paymenter/docker-compose.yml logs --tail=100 app

# 3. 检查 MySQL 状态
docker compose -f paymenter/docker-compose.yml logs --tail=50 db

# 4. 检查 Redis 状态
docker compose -f paymenter/docker-compose.yml logs --tail=50 redis

# 5. 尝试重启所有服务
docker compose -f paymenter/docker-compose.yml restart
```

### 6.2 VM 创建失败

```bash
# 1. 检查 Paymenter 队列 Worker 日志
docker compose -f paymenter/docker-compose.yml logs --tail=100 queue

# 2. 检查 WireGuard 隧道到集群的连通性
ping 10.100.0.10

# 3. 测试 Incus API 可达性
curl -k --cert /path/to/paymenter.crt --key /path/to/paymenter.key \
  https://10.0.10.1:8443/1.0

# 4. 检查 IP 池是否有可用 IP
# 查询 Paymenter 数据库 ip_pools 表

# 5. 检查 Incus 项目资源配额
incus project info customers
```

### 6.3 数据库故障

```bash
# 1. MySQL 无法启动
docker compose -f paymenter/docker-compose.yml logs db
# 常见：磁盘满、权限问题、崩溃恢复

# 2. 磁盘满处理
docker system prune  # 清理未使用的 Docker 资源
# 扩展数据卷

# 3. 从备份恢复
docker compose -f paymenter/docker-compose.yml stop db
# 恢复数据目录或导入 SQL 备份
docker compose -f paymenter/docker-compose.yml start db
```

---

## 7. 存储空间不足紧急处理

### 7.1 Ceph 存储空间不足

**告警阈值：**
- Warning：> 75% 使用率
- Critical：> 85% 使用率
- Ceph 硬限制：> 85% 进入 nearfull，> 95% 进入 full（**停止写入**）

```bash
# 1. 检查当前使用率
ceph df detail

# 2. 查看各 OSD 使用率
ceph osd df tree

# 3. 紧急释放空间
# a) 清理不需要的 VM 快照
rbd snap ls incus-pool/<image> | head -20
rbd snap rm incus-pool/<image>@<snap>

# b) 删除已停止的废弃 VM（确认后操作！）
incus list --project customers status=STOPPED

# 4. 如果接近 full，设置 OSD backfill 限制避免雪上加霜
ceph osd set nobackfill

# 5. 长期方案：添加新 OSD 或新节点扩容
```

### 7.2 节点根分区空间不足

```bash
# 1. 检查磁盘使用
df -h /

# 2. 清理 journald 日志
journalctl --vacuum-time=3d
journalctl --vacuum-size=500M

# 3. 清理 apt 缓存
apt clean

# 4. 清理旧内核
apt autoremove --purge

# 5. 检查大文件
du -sh /var/log/* | sort -rh | head -10
```

---

## 8. 紧急联系和升级

### 升级矩阵

| 严重等级 | 定义 | 响应时间 | 处理要求 |
|----------|------|----------|----------|
| P0 | 生产全局故障/安全漏洞 | 立即 | 确认后立即行动，通知所有相关人员 |
| P1 | 核心功能不可用 | 15 分钟内 | 提出方案并确认后执行 |
| P2 | 非核心功能异常 | 1 小时内 | 可自行修复 |
| P3 | 体验优化 | 下一工作日 | 排入待办 |

### 故障处理通用流程

1. **发现** — 监控告警或用户报告
2. **评估** — 确定影响范围和严重等级
3. **通知** — 按升级矩阵通知相关人员
4. **止血** — 采取最小化影响的紧急措施
5. **修复** — 根本原因修复
6. **验证** — 确认服务完全恢复
7. **复盘** — 记录事件报告，更新运维手册
