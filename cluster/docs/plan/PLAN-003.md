# PLAN-003：业务平台 — Paymenter + Incus 云主机销售系统

- **状态**: draft
- **创建**: 2026-04-09
- **关联**: PLAN-002（集群基础设施）

---

## 一、核心架构：业务面与控制面分离

```
┌─────────────────────────────────────────────────────────┐
│  业务面（面向公网）                                        │
│                                                         │
│  独立服务器 / 独立 VM（不在 Incus 集群内）                  │
│  ┌───────────────────────────────────────────┐          │
│  │  Nginx + WAF + Let's Encrypt              │ ← :443  │
│  │  Paymenter v1.x (Docker Compose)          │          │
│  │  ├── 客户门户（注册/下单/VM管理/工单/账单） │          │
│  │  ├── 管理后台（产品/定价/客户/2FA）         │          │
│  │  ├── Incus Extension (PHP)                │          │
│  │  └── MySQL/PostgreSQL                     │          │
│  └──────────────────┬────────────────────────┘          │
│                     │                                    │
│        WireGuard 隧道（10.100.0.0/24）                   │
│                     │                                    │
├─────────────────────┼────────────────────────────────────┤
│  控制面（仅内网）     │                                    │
│                     ▼                                    │
│  ┌──────────────────────────────────────────────┐       │
│  │  Incus 集群 A        Incus 集群 B    单机 C   │       │
│  │  API :8443           API :8443      :8443    │       │
│  │  (管理网 VLAN 10)    (管理网)        (公网)   │       │
│  │                                              │       │
│  │  Paymenter 证书：受限（customers project）     │       │
│  │  管理员证书：完整权限（运维专用）               │       │
│  └──────────────────────────────────────────────┘       │
│                                                         │
│  ┌──────────────────────────────────────────────┐       │
│  │  Prometheus + Grafana + Loki (监控，内网)       │       │
│  └──────────────────────────────────────────────┘       │
└─────────────────────────────────────────────────────────┘
```

### 为什么分离

| 场景 | 不分离的后果 | 分离后 |
|------|------------|--------|
| Paymenter 被入侵 | 攻击者拿到 Incus admin，删除所有 VM | 只能操作 customers project，有资源配额限制 |
| 集群全挂 | 客户面板也挂，什么都看不到 | Paymenter 正常，客户可看状态、提工单 |
| 集群维护 | 面板跟着断 | 零影响 |
| Paymenter 被 DDoS | VM 也受影响 | VM 正常运行 |

### 安全隔离三层

```
第 1 层：网络隔离
  Incus API 只监听管理网（10.0.10.X:8443）
  Paymenter 通过 WireGuard 隧道连接，公网无法直达 Incus API

第 2 层：Incus 证书权限
  Paymenter 证书：--projects customers --restricted
  → 只能操作 customers project
  → 不能管理集群节点、存储池、网络配置
  → 资源配额在 project 级别强制

第 3 层：Extension 逻辑校验
  Extension 代码内二次校验（防止证书权限泄露后滥用）
  → 操作审计日志
  → 限速（每分钟最多创建 N 台 VM）
  → 规格白名单（只允许创建预定义的规格）
```

---

## 二、Paymenter 部署

### 部署位置选择

| 方案 | 成本 | 安全 | 可用性 | 推荐 |
|------|------|------|--------|------|
| 外部小 VPS（Vultr/Linode $5/月）| 低 | 最好（完全独立）| 高 | ✅ 首选 |
| 你已有的单机（43.239.84.20）上的 VM | 零 | 好（同机房但独立 VM）| 中 | ✅ 可接受 |
| 集群内 VM | 零 | 差（集群挂面板也挂）| 低 | ❌ 不推荐 |

### Docker Compose

```yaml
# paymenter/docker-compose.yml
version: "3.8"
services:
  paymenter:
    image: paymenter/paymenter:v1
    restart: unless-stopped
    environment:
      - APP_URL=https://panel.example.com
      - DB_HOST=db
      - DB_DATABASE=paymenter
      - DB_USERNAME=paymenter
      - DB_PASSWORD=${DB_PASSWORD}
      - MAIL_MAILER=smtp
      - MAIL_HOST=${SMTP_HOST}
      - MAIL_PORT=587
      - MAIL_USERNAME=${SMTP_USER}
      - MAIL_PASSWORD=${SMTP_PASS}
    volumes:
      - ./storage:/var/www/html/storage
      - ./extensions:/var/www/html/extensions  # Incus Extension
    ports:
      - "127.0.0.1:8080:80"
    depends_on:
      - db

  db:
    image: mysql:8.0
    restart: unless-stopped
    environment:
      - MYSQL_ROOT_PASSWORD=${MYSQL_ROOT_PASSWORD}
      - MYSQL_DATABASE=paymenter
      - MYSQL_USER=paymenter
      - MYSQL_PASSWORD=${DB_PASSWORD}
    volumes:
      - mysql_data:/var/lib/mysql

  nginx:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./nginx/conf.d:/etc/nginx/conf.d:ro
      - ./certbot/conf:/etc/letsencrypt:ro
    depends_on:
      - paymenter

  # VNC WebSocket 代理（Incus console 需要 mTLS，浏览器无法直连）
  vnc-proxy:
    image: golang:1.22-alpine
    build: ./vnc-proxy
    restart: unless-stopped
    environment:
      - INCUS_CERT=/certs/client.crt
      - INCUS_KEY=/certs/client.key
    volumes:
      - ./certs:/certs:ro
    ports:
      - "127.0.0.1:6080:6080"

volumes:
  mysql_data:
```

---

## 三、Incus Server Extension 设计

### 核心接口

```php
<?php
// extensions/incus/IncusExtension.php

class IncusExtension extends ServerExtension
{
    // === 生命周期 ===
    public function createServer($user, $params, $order, $product, $options)
    {
        // 1. 从 IP 池分配 IP
        // 2. 调用 Incus API 创建 VM（cloud-init 注入网络+密码+SSH Key）
        // 3. 绑定 ipv4_filtering + mac_filtering + port_isolation
        // 4. 设置带宽限速（limits.ingress/egress）
        // 5. 创建用户级 ACL（默认 ingress drop）
        // 6. 记录审计日志
    }

    public function suspendServer($user, $params, $order, $product, $options)
    {
        // incus stop <vm> --stateful（保留状态暂停）
    }

    public function unsuspendServer($user, $params, $order, $product, $options)
    {
        // incus start <vm>
    }

    public function terminateServer($user, $params, $order, $product, $options)
    {
        // 1. incus delete <vm> --force
        // 2. 回收 IP 到 cooldown 池（24h 后释放）
        // 3. 删除 ACL
        // 4. 删除附加磁盘卷
    }

    // === 用户操作 ===
    public function reboot($params)      { /* PUT state action=restart */ }
    public function reinstall($params)   { /* 删除 → 同 IP 重建 */ }
    public function changePassword($params) { /* incus exec chpasswd */ }
    public function getConsoleUrl($params)  { /* 返回 VNC 代理 URL */ }

    // === 快照 ===
    public function createSnapshot($params)  { /* POST snapshots */ }
    public function restoreSnapshot($params) { /* PUT snapshots/{name} */ }
    public function deleteSnapshot($params)  { /* DELETE snapshots/{name} */ }

    // === 升降配 ===
    public function upgrade($params)
    {
        // 升配：热操作（不停机），修改 limits.cpu / limits.memory
    }
    public function downgrade($params)
    {
        // 降配：停机 → 修改 → 启动
    }

    // === 防火墙 ===
    public function getFirewallRules($params)    { /* GET /1.0/network-acls/{name} */ }
    public function addFirewallRule($params)      { /* PATCH /1.0/network-acls/{name} */ }
    public function removeFirewallRule($params)   { /* PATCH /1.0/network-acls/{name} */ }

    // === 附加磁盘 ===
    public function addDisk($params)
    {
        // incus storage volume create ceph-pool vol-xxx size=50GiB
        // incus config device add <vm> data-disk disk pool=ceph-pool source=vol-xxx
        // ★ 返回提示：VM 内需手动格式化挂载 /dev/sdb
    }
    public function removeDisk($params)
    {
        // incus config device remove <vm> data-disk
        // incus storage volume delete ceph-pool vol-xxx
    }

    // === SSH Key ===
    public function addSshKey($params)
    {
        // incus exec <vm> -- bash -c "echo 'key' >> /root/.ssh/authorized_keys"
    }

    // === 监控 ===
    public function getMetrics($params)
    {
        // GET /1.0/instances/{name}/state → CPU/内存/磁盘/网络
    }
}
```

### 连接 Incus 集群

```php
// Extension 内部的 Incus API 客户端
class IncusClient
{
    private string $endpoint;  // https://10.0.10.1:8443（通过 WireGuard）
    private string $certFile;  // 受限证书
    private string $keyFile;

    public function request(string $method, string $path, array $data = []): array
    {
        $ch = curl_init();
        curl_setopt($ch, CURLOPT_URL, $this->endpoint . $path);
        curl_setopt($ch, CURLOPT_SSLCERT, $this->certFile);
        curl_setopt($ch, CURLOPT_SSLKEY, $this->keyFile);
        curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, true);
        // ...
    }
}
```

---

## 四、IP 池管理

### 数据模型（MySQL，在 Paymenter 数据库中）

```sql
CREATE TABLE ip_pools (
    id INT AUTO_INCREMENT PRIMARY KEY,
    server_id INT NOT NULL,           -- 关联 Paymenter Server
    name VARCHAR(64),                  -- "tokyo-pool-1"
    subnet VARCHAR(18) NOT NULL,       -- "202.151.179.224/27"
    gateway VARCHAR(15) NOT NULL,      -- "202.151.179.225"
    netmask VARCHAR(15) NOT NULL,      -- "255.255.255.224"
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE ip_addresses (
    id INT AUTO_INCREMENT PRIMARY KEY,
    pool_id INT NOT NULL,
    ip VARCHAR(15) NOT NULL UNIQUE,    -- "202.151.179.232"
    status ENUM('available', 'allocated', 'cooldown', 'reserved') DEFAULT 'available',
    vm_name VARCHAR(64),               -- "vm-cust-001"
    order_id INT,                      -- 关联 Paymenter 订单
    allocated_at TIMESTAMP,
    released_at TIMESTAMP,
    cooldown_until TIMESTAMP,          -- 释放后 24h 冷却期
    FOREIGN KEY (pool_id) REFERENCES ip_pools(id)
);

-- 初始化 IP 池
INSERT INTO ip_pools (server_id, name, subnet, gateway, netmask)
VALUES (1, 'test-pool', '202.151.179.224/27', '202.151.179.225', '255.255.255.224');

-- 批量导入可用 IP
INSERT INTO ip_addresses (pool_id, ip, status) VALUES
(1, '202.151.179.232', 'available'),
(1, '202.151.179.233', 'available'),
-- ... 232-254
(1, '202.151.179.254', 'available');

-- 宿主机 IP 标记为保留
UPDATE ip_addresses SET status='reserved' WHERE ip IN
('202.151.179.226','202.151.179.227','202.151.179.228','202.151.179.229','202.151.179.230');
```

### 分配/回收逻辑

```
创建 VM:
  SELECT ip FROM ip_addresses
    WHERE pool_id=? AND status='available'
    ORDER BY INET_ATON(ip) LIMIT 1 FOR UPDATE;
  → UPDATE status='allocated', vm_name=?, order_id=?, allocated_at=NOW()

删除 VM:
  UPDATE status='cooldown', vm_name=NULL, released_at=NOW(),
    cooldown_until=DATE_ADD(NOW(), INTERVAL 24 HOUR)
  WHERE ip=?

Cron（每小时）:
  UPDATE status='available', cooldown_until=NULL
  WHERE status='cooldown' AND cooldown_until < NOW()

告警:
  SELECT COUNT(*) FROM ip_addresses WHERE pool_id=? AND status='available'
  → 如果 < 总数 10% 则发告警邮件
```

---

## 五、VNC WebSocket 代理

Incus console API 需要 mTLS（客户端证书），浏览器无法直接建立 mTLS WebSocket 连接。需要一个代理：

```
浏览器 (noVNC) → wss://panel.example.com/vnc/{vm}
                       ↓
                 VNC 代理服务 (Go)
                       ↓ mTLS
                 Incus API /1.0/instances/{vm}/console
```

### 代理服务（Go，~200 行）

```go
// vnc-proxy/main.go
package main

// 核心逻辑：
// 1. 接收浏览器 WebSocket 连接（带 session token 认证）
// 2. 验证 token（从 Paymenter session 或 JWT 验证）
// 3. 验证用户有权访问该 VM
// 4. 用 mTLS 建立到 Incus 的 WebSocket 连接
// 5. 双向转发数据
```

### noVNC 前端集成

```html
<!-- Paymenter Extension 的 VM 管理页面 -->
<div id="vnc-container">
    <iframe src="/vnc-console?vm={{vm_name}}&token={{session_token}}"
            width="100%" height="600px" frameborder="0"></iframe>
</div>
```

---

## 六、用户防火墙（安全组）

### 用户操作流程

```
用户面板：我的 VM → 防火墙
┌──────────────────────────────────────┐
│  入站规则                             │
│  ┌─────┬──────┬───────┬────────────┐ │
│  │ #   │ 协议  │ 端口   │ 来源       │ │
│  ├─────┼──────┼───────┼────────────┤ │
│  │ 1   │ TCP  │ 22    │ 0.0.0.0/0  │ │
│  │ 2   │ TCP  │ 80    │ 0.0.0.0/0  │ │
│  │ 3   │ TCP  │ 443   │ 0.0.0.0/0  │ │
│  │ 4   │ TCP  │ 3306  │ 10.0.0.0/8 │ │
│  └─────┴──────┴───────┴────────────┘ │
│  [+ 添加规则]                         │
│                                      │
│  默认策略: [拒绝所有入站 ▼]            │
└──────────────────────────────────────┘
```

### 底层映射到 Incus ACL

```bash
# Extension 调用 Incus REST API
# 创建 ACL
POST /1.0/network-acls
{
  "name": "acl-order-12345",
  "ingress": [
    {"action": "allow", "protocol": "tcp", "destination_port": "22", "source": "0.0.0.0/0"},
    {"action": "allow", "protocol": "tcp", "destination_port": "80,443", "source": "0.0.0.0/0"},
    {"action": "allow", "protocol": "tcp", "destination_port": "3306", "source": "10.0.0.0/8"}
  ],
  "egress": []
}

# 绑定到 VM
PATCH /1.0/instances/{vm}
{
  "devices": {
    "eth0": {
      "security.acls": "acl-order-12345",
      "security.acls.default.ingress.action": "drop",
      "security.acls.default.egress.action": "allow"
    }
  }
}
```

> ★ Incus ACL 在 unmanaged bridge + nftables 驱动下可用（已在 PLAN-002 审查中确认），但需在测试环境实测高密度场景。

---

## 七、产品配置

### Paymenter 产品定义

```
产品组: 云主机
├── 区域: 东京 (Server: tokyo-cluster)
│   ├── 产品: 1C-1G (1核/1G/25G SSD/1TB流量) — $5/月
│   ├── 产品: 1C-2G (1核/2G/50G SSD/2TB流量) — $10/月
│   ├── 产品: 2C-4G (2核/4G/80G SSD/3TB流量) — $20/月
│   ├── 产品: 4C-8G (4核/8G/160G SSD/4TB流量) — $40/月
│   └── 产品: 8C-16G (8核/16G/320G SSD/5TB流量) — $80/月
├── 区域: 香港 (Server: hk-cluster)
│   └── ... 同上规格，价格可能不同
└── 区域: 测试 (Server: standalone-1)
    └── ... 只放低价测试产品
```

### 下单可选项（Configurable Options）

| 选项 | 类型 | 值 |
|------|------|------|
| 操作系统 | 下拉 | Ubuntu 24.04 / Debian 12 / Rocky 9 / CentOS 10 / Alma 9 / Fedora 42 / Arch |
| SSH 公钥 | 文本框 | 用户粘贴公钥 |
| 初始密码 | 自动生成 | 不显示，邮件通知 |
| 附加磁盘 | 下拉 | 无 / 50G / 100G / 200G / 500G（额外计费）|

---

## 八、邮件通知

### 通知类型

| 事件 | 触发 | 模板 |
|------|------|------|
| 注册成功 | 用户注册 | 欢迎邮件 + 登录链接 |
| 订单确认 | 支付成功 | VM 信息（IP/密码/SSH Key 指纹）|
| VM 创建完成 | Extension 回调 | IP + 连接方式 + 初始密码 |
| 发票生成 | 每月 | 账单详情 + 支付链接 |
| 到期提醒 | 到期前 7/3/1 天 | 续费提醒（★ 可能需自定义 Cron 实现）|
| VM 暂停 | 到期未续费 | 数据保留 7 天警告 |
| VM 删除 | 暂停 7 天后 | 数据已删除通知 |
| 维护公告 | 管理员手动 | 维护时间 + 影响范围 |
| 密码重置 | 用户操作 | 新密码 |

### 到期处理流程

```
到期日 D
  D-7: 邮件提醒"您的 VM 将于 7 天后到期"
  D-3: 邮件提醒"请尽快续费"
  D-1: 邮件提醒"明天到期，未续费将暂停"
  D+0: 暂停 VM（incus stop），邮件通知"已暂停，数据保留 7 天"
  D+7: 删除 VM + 回收 IP，邮件通知"数据已删除"
```

---

## 九、经营策略

### 参考 Linode/Vultr

| 策略 | 做法 |
|------|------|
| **定价** | Vultr 80-85% 水平，后台可调 |
| **退款** | 未使用天数按比例退到账户余额（非现金）|
| **SLA** | 99.9%，超出按比例返账户余额 |
| **ToS** | 禁止挖矿、DDoS、垃圾邮件、端口扫描、违法内容 |
| **滥用处理** | 收到投诉 → 人工审核 → 暂停/删除 + 邮件通知 |
| **DDoS** | 上游 null route 被攻击 IP |
| **支付方式** | Stripe（首选）、PayPal、支付宝（如需国内客户）|

---

## 十、开发阶段

### Phase 4A：Paymenter 基础部署（1 周）

- [ ] Paymenter Docker Compose 部署
- [ ] Nginx + Let's Encrypt 配置
- [ ] WireGuard 隧道连接到 Incus 集群
- [ ] Incus 受限证书签发（customers project）
- [ ] 管理后台 2FA 启用
- [ ] 基础产品/定价配置

### Phase 4B：Incus Extension 核心（2 周）

- [ ] Extension 骨架（IncusClient + 生命周期方法）
- [ ] createServer（IP 池分配 + cloud-init + 安全过滤 + 带宽限速）
- [ ] suspendServer / unsuspendServer / terminateServer
- [ ] IP 池管理（分配/回收/冷却/告警）
- [ ] 密码修改 / SSH Key 注入
- [ ] 重装系统（删除重建保留 IP）
- [ ] 审计日志

### Phase 4C：用户功能（2 周）

- [ ] VNC WebSocket 代理服务（Go，~200 行）
- [ ] noVNC 前端集成
- [ ] 用户防火墙 UI（Incus ACL 管理）
- [ ] 快照管理（创建/恢复/删除）
- [ ] 升降配（热升配/冷降配）
- [ ] 附加磁盘管理
- [ ] 资源监控图表（CPU/内存/带宽）

### Phase 4D：运营功能（1 周）

- [ ] 邮件通知模板（注册/下单/到期/暂停/删除）
- [ ] 到期自动处理 Cron（7/3/1 天提醒 → 暂停 → 删除）
- [ ] 多集群/单机 Server 注册
- [ ] 工单系统配置
- [ ] 支付网关配置（Stripe/PayPal）

---

## 十一、风险

| 风险 | 缓解 |
|------|------|
| Paymenter 被入侵 | WireGuard 隔离 + 受限证书 + 操作审计 |
| Paymenter 数据丢失 | MySQL 每日备份到独立存储 |
| VNC 代理安全 | Session token 验证 + 用户只能访问自己的 VM |
| Extension 开发周期 | 分 4 个子阶段，核心功能优先 |
| Paymenter v1 停止维护 | 关注 v2 进展，预留迁移能力 |
| ACL 高密度性能 | 测试环境验证 100+ VM 下的 nftables 规则性能 |
| 到期提醒精细化 | Paymenter 内置可能不够，准备自定义 Cron |
