# 运维手册

> 适用范围：5 节点 Incus + Ceph 集群（202.151.179.224/27）

---

## 1. 日常巡检流程

### 1.1 每日巡检

| 序号 | 检查项 | 命令 | 预期结果 |
|------|--------|------|----------|
| 1 | Ceph 集群状态 | `ceph health` | `HEALTH_OK` |
| 2 | OSD 状态 | `ceph osd tree` | 20 个 OSD 均为 `up` |
| 3 | Incus 集群状态 | `incus cluster list` | 5 节点均 ONLINE |
| 4 | Prometheus 告警 | 检查 Alertmanager 面板 | 无未确认 critical 告警 |
| 5 | 磁盘空间 | `df -h /` （各节点） | 根分区 < 85% |
| 6 | VM 运行状态 | `incus list --project customers` | 所有已购 VM 为 RUNNING |

### 1.2 每周巡检

| 序号 | 检查项 | 命令 | 预期结果 |
|------|--------|------|----------|
| 1 | Ceph PG 状态 | `ceph pg stat` | 全部 `active+clean` |
| 2 | OSD 空间均衡 | `ceph osd df tree` | 各 OSD 偏差 < 10% |
| 3 | 安全更新检查 | `apt list --upgradable` | 无 critical CVE |
| 4 | 证书有效期 | `openssl x509 -enddate -noout -in <cert>` | 距过期 > 30 天 |
| 5 | IP 池余量 | 查询 Paymenter 数据库或管理面板 | 可用 IP > 20% |
| 6 | 备份完整性 | 验证最新备份可恢复 | 备份文件存在且校验通过 |

### 1.3 每月巡检

| 序号 | 检查项 | 操作 |
|------|--------|------|
| 1 | 系统内核版本 | 评估是否需要升级，安排维护窗口 |
| 2 | Ceph 版本 | 检查是否有安全补丁 |
| 3 | 容量趋势 | 分析 Prometheus 30 天趋势，评估扩容需求 |
| 4 | 日志审计 | 审查 SSH 登录日志、失败认证记录 |
| 5 | 密钥轮换 | 检查是否有到期密钥需要轮换 |

---

## 2. 集群健康检查命令速查

### 2.1 Incus 集群

```bash
# 集群节点状态
incus cluster list

# 集群节点详细信息
incus cluster show <node>

# 查看所有 VM（customers 项目）
incus list --project customers

# 查看 VM 详情
incus info <vm> --project customers

# 查看节点资源总览
incus info --resources
# 查看指定 VM 资源使用
incus info <vm> --project customers --resources
```

### 2.2 Ceph 存储

```bash
# 集群健康摘要
ceph status           # 或 ceph -s

# 详细健康信息
ceph health detail

# OSD 状态树
ceph osd tree

# OSD 空间使用
ceph osd df tree

# PG 状态
ceph pg stat
ceph pg dump_stuck    # 查看卡住的 PG

# 存储池状态
ceph osd pool stats incus-pool
rados df

# 实时性能
ceph osd perf
ceph osd pool get incus-pool all
```

### 2.3 网络检查

```bash
# 管理网络连通性（从任意节点）
for i in 1 2 3 4 5; do ping -c1 -W1 10.0.10.$i; done

# Ceph 网络连通性
for i in 1 2 3 4 5; do ping -c1 -W1 10.0.20.$i; done

# 检查 br-pub 网桥
bridge link show

# nftables 规则
nft list ruleset

# WireGuard 隧道状态
wg show
```

### 2.4 监控系统

```bash
# Prometheus 目标状态
curl -s http://localhost:9090/api/v1/targets | jq '.data.activeTargets[] | {job: .labels.job, health: .health}'

# 当前告警
curl -s http://localhost:9093/api/v2/alerts | jq '.[].labels'
```

---

## 3. VM 创建/迁移/删除标准流程

### 3.1 VM 创建（通过 Paymenter 自动化）

正常流程由 Paymenter Incus Extension 自动完成：
1. 用户下单 → Paymenter 分配 IP
2. 调用 Incus API 创建 VM（cloud-init 初始化）
3. 配置安全过滤（IPv4/MAC/端口隔离）
4. 设置带宽限制
5. 启动 VM

**手动创建（仅紧急/调试用）：**

```bash
# 1. 创建 VM
incus launch images:ubuntu/24.04/cloud <vm-name> \
  --project customers \
  --vm \
  -c limits.cpu=2 \
  -c limits.memory=2GiB \
  -d root,size=50GiB

# 2. 绑定公网 IP
incus config device set <vm-name> eth0 \
  ipv4.address=202.151.179.XXX \
  --project customers

# 3. 启用安全过滤
incus config device set <vm-name> eth0 \
  security.ipv4_filtering=true \
  security.mac_filtering=true \
  security.port_isolation=true \
  --project customers

# 4. 设置带宽限制
incus config device set <vm-name> eth0 \
  limits.ingress=100Mbit \
  limits.egress=100Mbit \
  --project customers
```

### 3.2 VM 迁移

```bash
# 实时迁移（默认，接近零停机）
bash scripts/migrate-vm.sh <vm-name> --target <目标节点>

# 冷迁移（需要停机）
bash scripts/migrate-vm.sh <vm-name> --target <目标节点> --cold

# 批量迁移（节点维护前）
bash scripts/batch-migrate.sh <源节点> <目标节点>
```

迁移后自动发送 GARP 更新交换机 MAC 表。

### 3.3 VM 删除

```bash
# 通过 Paymenter 自动化：
# 用户取消订阅 → Extension 停机 → 删除实例 → IP 进入 24h 冷却期

# 手动删除（紧急情况）：
incus stop <vm-name> --project customers
incus delete <vm-name> --project customers
# 注意：手动删除后需要在 Paymenter 数据库中释放对应 IP
```

---

## 4. Ceph 常用运维命令

### 4.1 OSD 管理

```bash
# 查看 OSD 状态
ceph osd status
ceph osd tree

# 手动标记 OSD 为 out（准备替换磁盘）
ceph osd out osd.<id>

# 等待数据迁移完成
ceph -w  # 观察 recovery 进度

# 移除 OSD
ceph osd purge osd.<id> --yes-i-really-mean-it

# 添加新 OSD（在目标节点上执行）
cephadm shell -- ceph orch daemon add osd <host>:/dev/sdX

# 设置 OSD 维护标志（暂停 recovery）
ceph osd set noout
ceph osd set norecover
# 维护完成后取消
ceph osd unset noout
ceph osd unset norecover
```

### 4.2 存储池管理

```bash
# 查看池配置
ceph osd pool get incus-pool all

# 调整副本数（谨慎操作！）
ceph osd pool set incus-pool size 3
ceph osd pool set incus-pool min_size 2

# 查看池 IO
ceph osd pool stats incus-pool

# 查看空间使用
rados df
ceph df detail
```

### 4.3 PG 维护

```bash
# 查看 PG 分布
ceph pg ls-by-pool incus-pool

# 查看不健康的 PG
ceph pg dump_stuck unclean
ceph pg dump_stuck inactive
ceph pg dump_stuck stale

# 修复降级 PG
ceph pg repair <pg-id>

# 强制重新均衡
ceph osd reweight-by-utilization
```

### 4.4 dmcrypt 密钥备份

```bash
# 导出 OSD 加密密钥（定期备份到安全位置）
bash scripts/backup-dmcrypt-keys.sh
```

---

## 5. 备份恢复流程

### 5.1 备份策略

| 备份对象 | 频率 | 保留周期 | 存储位置 |
|----------|------|----------|----------|
| VM 快照 | 用户按需 | 跟随 VM 生命周期 | Ceph 池内 |
| Paymenter 数据库 | 每日 | 30 天 | 异地存储 |
| Ceph OSD 密钥 | 每次 OSD 变更后 | 永久 | 离线安全存储 |
| Incus 数据库 | 每日 | 30 天 | 异地存储 |
| 配置文件 | Git 版本控制 | 永久 | Git 仓库 |

### 5.2 Paymenter 数据库备份

```bash
# 备份（在 Paymenter 所在机器上）
# 先加载环境变量（MYSQL_ROOT_PASSWORD 在 Paymenter 的 .env 中定义）
source /opt/paymenter/.env
docker exec paymenter-db mysqldump -u root -p"${MYSQL_ROOT_PASSWORD}" \
  paymenter > /backup/paymenter-$(date +%Y%m%d).sql

# 恢复
docker exec -i paymenter-db mysql -u root -p"${MYSQL_ROOT_PASSWORD}" \
  paymenter < /backup/paymenter-YYYYMMDD.sql
```

### 5.3 Incus 数据库备份

```bash
# 导出 Incus 数据库（在 leader 节点上执行）
incus admin sql global .dump > /backup/incus-global-$(date +%Y%m%d).sql

# 恢复需要在集群完全停止后操作，请联系高级运维
```

### 5.4 VM 快照

```bash
# 创建快照
incus snapshot create <vm-name> <snap-name> --project customers

# 查看快照列表
incus snapshot list <vm-name> --project customers

# 恢复快照（会覆盖当前状态）
incus snapshot restore <vm-name> <snap-name> --project customers

# 删除快照
incus snapshot delete <vm-name> <snap-name> --project customers
```

---

## 6. 密钥轮换流程

### 6.1 Paymenter mTLS 证书轮换

```bash
# 1. 生成新证书
bash scripts/setup-restricted-cert.sh

# 2. 在 Incus 中添加新证书
incus config trust add <new-cert.crt> --restricted \
  --projects customers

# 3. 更新 Paymenter 配置指向新证书
# 修改 .env 或 Docker volume 中的证书路径

# 4. 重启 Paymenter 服务
docker compose -f paymenter/docker-compose.yml restart

# 5. 验证连接正常后，删除旧证书
incus config trust remove <old-cert-fingerprint>
```

### 6.2 Console Proxy 证书轮换

```bash
# 1. 生成新证书
bash scripts/setup-console-cert.sh

# 2. 添加到 Incus 信任列表
incus config trust add <new-cert.crt> --restricted \
  --projects customers

# 3. 更新 console-proxy 配置并重启

# 4. 验证 WebSocket 连接正常后删除旧证书
```

### 6.3 WireGuard 密钥轮换

```bash
# 1. 在 Paymenter 端生成新密钥对（注意设置私钥文件权限）
(umask 077 && wg genkey | tee /etc/wireguard/new-private.key | wg pubkey > /etc/wireguard/new-public.key)

# 2. 更新集群端 peer 配置
# 修改 wg0.conf 中 Paymenter 的 PublicKey

# 3. 同步重启两端 WireGuard
systemctl restart wg-quick@wg0  # 集群端
systemctl restart wg-quick@wg0  # Paymenter 端

# 4. 验证隧道连通
ping 10.100.0.10
```

---

## 7. 证书更新流程

### 7.1 Incus 集群证书

Incus 集群证书在初始化时自动生成，有效期 10 年。如需提前更新：

```bash
# 在集群 leader 节点上
incus admin cluster-cert set <new-cert.pem> <new-key.pem>
# 此命令会自动同步到所有集群成员
```

### 7.2 Ceph msgr2 TLS 证书

```bash
# 查看当前证书
ceph config get mon auth_client_required

# Ceph 使用 msgr2 内置加密，证书由 cephadm 自动管理
# 如需手动更新，参考 Ceph 文档的 cephadm cert rotation 章节
```

### 7.3 Prometheus mTLS（Incus metrics）

```bash
# Incus metrics 端点使用集群 TLS 证书
# 更新 Incus 集群证书后，同步更新 Prometheus 配置中的 CA 证书
# 修改 prometheus.yml 中 tls_config.ca_file 指向新 CA
systemctl restart prometheus
```

---

## 8. 滚动维护流程

对集群节点进行系统更新或硬件维护时，使用滚动维护流程避免服务中断：

```bash
# 使用滚动维护脚本
bash scripts/rolling-maintenance.sh <节点名>

# 脚本自动执行：
# 1. 设置 Ceph noout 标志
# 2. 疏散节点上的 VM 到其他节点
# 3. 执行维护操作（系统更新等）
# 4. 恢复节点
# 5. 迁回 VM
# 6. 取消 Ceph noout 标志
```

**注意事项：**
- 同一时间仅维护 1 个节点
- Voter 节点（node1-3）维护前确保另外 2 个 voter 正常
- 维护窗口建议选择业务低峰期
- 维护前通知受影响用户
