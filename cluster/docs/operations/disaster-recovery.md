# 异地灾备运维手册

## 1. 架构说明

### 1.1 总体架构

本灾备方案基于 **Ceph RBD Mirroring** 实现跨站点数据复制，采用 **Journal-based 异步镜像**模式。

```
┌─────────────────────┐          ┌─────────────────────┐
│   主站点 (Primary)   │  journal  │  从站点 (Secondary)  │
│                     │ ───────> │                     │
│  Ceph Cluster A     │  async   │  Ceph Cluster B     │
│  ┌───────────────┐  │          │  ┌───────────────┐  │
│  │ incus-pool    │  │          │  │ incus-pool    │  │
│  │ (RBD images)  │──┼──────────┼─>│ (RBD mirror)  │  │
│  └───────────────┘  │          │  └───────────────┘  │
│                     │          │                     │
│  rbd-mirror daemon  │          │  rbd-mirror daemon  │
└─────────────────────┘          └─────────────────────┘
```

### 1.2 关键组件

| 组件 | 说明 |
|------|------|
| **rbd-mirror** | 守护进程，负责在主从站点之间同步 RBD 镜像 |
| **Journal** | 每个 RBD 镜像的日志，记录所有写操作 |
| **Peer** | 主从集群之间的信任关系 |
| **Bootstrap Token** | 用于建立 Peer 关系的令牌 |

### 1.3 数据流

1. 应用写入数据到主站点 RBD 镜像
2. 写操作同时记录到 journal
3. 从站点的 `rbd-mirror` 守护进程异步回放 journal
4. 从站点镜像与主站点保持近实时同步

### 1.4 RPO / RTO 目标

| 指标 | 目标值 | 说明 |
|------|--------|------|
| **RPO** | < 1 分钟 | 取决于网络带宽和写入负载 |
| **RTO** | < 10 分钟 | 镜像提升 + DNS 切换 |


## 2. 日常验证

### 2.1 状态检查（建议每日执行）

```bash
# 查看全部灾备状态
./cluster/scripts/disaster-recovery-status.sh all

# 仅检查 RPO
./cluster/scripts/disaster-recovery-status.sh rpo

# 检查 Peer 连接
./cluster/scripts/disaster-recovery-status.sh peers
```

### 2.2 健康判断标准

| 状态 | 含义 | 处置 |
|------|------|------|
| `up+replaying` | 正常同步中 | 无需操作 |
| `up+syncing` | 初始全量同步中 | 等待完成 |
| `up+stopped` | 同步已暂停 | 检查原因 |
| `down+...` | 守护进程异常 | 立即排查 |

### 2.3 告警阈值

| 级别 | 同步延迟 | 操作 |
|------|----------|------|
| 正常 | < 60 秒 | 无需操作 |
| 警告 | 60 - 300 秒 | 检查网络带宽，观察趋势 |
| 严重 | > 300 秒 | 立即排查，考虑扩容带宽 |

### 2.4 定期演练

**建议每季度执行一次灾备切换演练**，步骤如下：

1. 选择低峰时段
2. 通知相关人员
3. 执行切换（参见第 3 节）
4. 验证业务可用性
5. 执行回切（参见第 4 节）
6. 记录演练报告


## 3. 故障切换步骤

### 3.1 前提条件

- 确认主站点确实不可用
- 确认从站点 Ceph 集群健康
- 确认已获得运维负责人授权

### 3.2 自动切换（推荐）

```bash
# 设置必要的环境变量
export DR_POOL="incus-pool"
export DR_NEW_VIP="10.0.1.100"           # 从站点 VIP
export DR_DNS_ZONE="storage.example.com"  # 服务域名
export DR_DNS_SERVER="ns1.example.com"    # DNS 服务器

# 执行完整故障切换
./cluster/scripts/disaster-recovery-failover.sh failover
```

该命令将依次执行：
1. **前置检查** — 验证集群连通性和镜像状态
2. **镜像提升** — 将所有 Secondary 镜像提升为 Primary
3. **DNS 更新** — 将服务域名指向从站点 VIP
4. **可用性验证** — 检查 Ceph 健康、RBD 读写、DNS 解析

### 3.3 手动分步切换

如果自动切换失败，可手动执行各步骤：

```bash
# 步骤 1: 提升镜像
./cluster/scripts/disaster-recovery-failover.sh promote

# 步骤 2: 更新 DNS（手动方式）
# 登录 DNS 管理后台，将 A 记录指向从站点 VIP

# 步骤 3: 验证
./cluster/scripts/disaster-recovery-failover.sh verify
```

### 3.4 切换后检查清单

- [ ] 所有 RBD 镜像状态为 Primary
- [ ] DNS 解析指向从站点 VIP
- [ ] 应用可以正常读写存储
- [ ] 监控系统已切换到从站点
- [ ] 通知相关人员切换已完成


## 4. 回切步骤

当主站点恢复后，需要将数据同步回主站点并切换回来。

### 4.1 前提条件

- 主站点 Ceph 集群已恢复并健康
- 主从站点网络连通
- 已获得运维负责人授权

### 4.2 回切流程

```bash
# 步骤 1: 在当前 Primary（原从站点）上 demote 镜像
rbd mirror image demote incus-pool/<image-name>
# 对所有镜像执行

# 步骤 2: 在原主站点上 promote 镜像
rbd mirror image promote incus-pool/<image-name>
# 对所有镜像执行

# 步骤 3: 等待数据同步完成
./cluster/scripts/disaster-recovery-status.sh images
# 确认所有镜像状态为 up+replaying 且延迟接近 0

# 步骤 4: 更新 DNS 回原主站点 VIP
export DR_NEW_VIP="10.0.0.100"  # 原主站点 VIP
./cluster/scripts/disaster-recovery-failover.sh update-dns

# 步骤 5: 验证
./cluster/scripts/disaster-recovery-failover.sh verify
```

### 4.3 批量回切脚本

```bash
# 在原从站点（当前 Primary）执行 demote
POOL="incus-pool"
FAILED=0
while IFS= read -r img; do
    echo "Demoting: ${POOL}/${img}"
    if ! rbd mirror image demote "${POOL}/${img}"; then
        echo "ERROR: demote 失败 — ${POOL}/${img}"
        FAILED=$((FAILED+1))
    fi
done < <(rbd ls "${POOL}")
if [ "$FAILED" -gt 0 ]; then
    echo "严重: ${FAILED} 个镜像 demote 失败，中止操作！请人工处理后再继续 promote"
    exit 1
fi
echo "所有镜像 demote 成功，请在原主站点执行 promote"

# --- 在原主站点执行 promote（确认所有 demote 成功后再执行） ---
POOL="incus-pool"
FAILED=0
while IFS= read -r img; do
    echo "Promoting: ${POOL}/${img}"
    if ! rbd mirror image promote "${POOL}/${img}"; then
        echo "ERROR: promote 失败 — ${POOL}/${img}"
        FAILED=$((FAILED+1))
    fi
done < <(rbd ls "${POOL}")
if [ "$FAILED" -gt 0 ]; then
    echo "警告: ${FAILED} 个镜像 promote 失败，请人工检查"
fi
```

### 4.4 回切后检查清单

- [ ] 原主站点所有镜像状态为 Primary
- [ ] 原从站点所有镜像恢复为 Secondary 并正常同步
- [ ] DNS 已切回原主站点
- [ ] 应用正常运行
- [ ] 灾备状态监控显示健康
- [ ] 通知相关人员回切已完成


## 5. 故障排查

### 5.1 常见问题

#### 镜像同步延迟持续增大

```bash
# 检查网络带宽
iperf3 -c <remote-mon-ip>

# 检查 rbd-mirror 守护进程日志
ceph log last 100 --channel=cluster

# 检查 OSD 负载
ceph osd perf
```

#### rbd-mirror 守护进程异常

```bash
# 查看守护进程状态
ceph orch ls --service-type=rbd-mirror

# 重启守护进程
ceph orch restart rbd-mirror

# 查看日志
ceph orch logs --service-name=rbd-mirror
```

#### Peer 连接断开

```bash
# 检查 Peer 列表
rbd mirror pool peer list incus-pool

# 移除并重新添加 Peer
rbd mirror pool peer remove incus-pool <peer-uuid>
# 重新执行 bootstrap 流程
./cluster/scripts/setup-disaster-recovery.sh setup-primary
```

### 5.2 紧急联系

| 角色 | 职责 |
|------|------|
| 存储运维 | Ceph 集群维护、灾备操作 |
| 网络运维 | DNS 更新、网络排查 |
| 应用运维 | 业务验证、用户通知 |
