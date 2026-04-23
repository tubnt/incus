# IncusAdmin HA Runbook

面向运维的 HA（高可用）链路操作手册。覆盖 PLAN-020 落地的事件流、`healing_events` 表、Chaos drill、`/admin/ha` 管理面板的日常健康检查、故障诊断与常见场景处置。

---

## 架构一览

```
Incus 集群  ──► /1.0/events (WebSocket)  ──► incus-admin event_listener
                                              │
                                              ├─ instance-deleted  ──► vms.status='gone' (MarkGoneByName)
                                              ├─ instance-updated  ──► vms.node 更新 + AppendEvacuatedVM
                                              └─ cluster-member-updated
                                                   ├─ offline/evacuated ──► healing_events(trigger=auto) Create
                                                   └─ online            ──► CompleteByNode

                            60s 轮询兜底  ──► vm_reconciler (DB ↔ Incus)

                            15min 扫描    ──► healing_expire_stale (in_progress → partial)

                            /admin/ha ──► 节点 Status + healing 历史事件表
```

### 数据落地

- `vms` —— 增加 `status='gone'` 表示 Incus 侧已消失但 DB 保留审计痕迹
- `healing_events` —— 节点 evacuation/healing 生命周期，字段：
  - `trigger` ∈ `manual | auto | chaos`
  - `status` ∈ `in_progress | completed | failed | partial`
  - `evacuated_vms` JSONB：每个 VM 的 `{vm_id, name, from_node, to_node}`
  - `actor_id` —— 手动/chaos 时填触发的 admin user_id，自动路径为 NULL
- `audit_logs` —— 所有 `/api/admin` 写操作自动落 `http.<METHOD>` 行；手动 evacuate / chaos drill 另写业务 action（`node.evacuate` / `node.chaos_drill.start`），details 含 `healing_event_id` 交叉引用

---

## 日常健康检查

### 1. 事件流在线

**期望**：每个集群一个 goroutine 持续订阅 `/1.0/events?type=lifecycle`，disconnected 计数长期为 0。

```bash
# 启动日志：确认 worker 全部就位
journalctl -u incus-admin --since "5 minutes ago" | grep -E "event listener starting|vm reconciler started|healing expire worker started"

# 近 10 分钟断链次数
journalctl -u incus-admin --since "10 minutes ago" | grep -c "event listener disconnected"
# 期望：0；如果 > 3 → 告警（见"故障诊断 › 事件流断链"）
```

启动期望日志（一条 cluster 对应一条 event listener starting 项）：

```
{"msg":"vm reconciler started","interval":60000000000,"create_buffer":10000000000,"drift_alert_threshold":5}
{"msg":"event listener starting","clusters":1,"types":["lifecycle"]}
{"msg":"healing expire worker started","max_age":900000000000,"tick":300000000000}
```

### 2. Reconciler drift 统计

```bash
# 是否有 drift 校正在发生（正常 = 0）
journalctl -u incus-admin --since "1 hour ago" | grep -c "vm reconciler: drift corrected"

# 是否有 drift 告警（drift > 5 一次性 WARN）
journalctl -u incus-admin --since "1 hour ago" | grep "drift above threshold"
```

DB 侧查漂移 VM：

```sql
SELECT id, name, cluster_id, status, updated_at
FROM vms
WHERE status = 'gone'
ORDER BY updated_at DESC
LIMIT 20;
```

### 3. healing_events 状态分布

```sql
SELECT status, trigger, COUNT(*) AS n, MAX(started_at) AS latest
FROM healing_events
WHERE started_at > NOW() - INTERVAL '7 days'
GROUP BY status, trigger
ORDER BY status, trigger;
```

异常信号：
- `in_progress` 行 **超过 15 分钟未完成** → ExpireStale worker 下次 tick 应转 `partial`；若仍不转，查 sweeper 日志
- `partial` 激增 → 事件流频繁漏事件，结合 disconnected 计数一起看
- `failed` 激增 → Incus 调用持续失败（证书过期/网络/权限），看 `error` 字段

---

## 故障诊断

### 事件流断链

**症状**：`event listener disconnected` 日志出现；`/admin/ha` History Tab 数据停滞。

**排查顺序**：

1. 确认 Incus 侧 API 可达：
   ```bash
   # 从 incus-admin 主机
   curl -v --cacert /path/to/cluster-ca.pem --cert /path/to/client.crt --key /path/to/client.key https://<incus-node>:8443/1.0 | head -20
   ```

2. 若 WebSocket 升级失败（`bad handshake` + HTTP/2 proto），确认 TLS ALPN 走 `http/1.1`。代码已强制：`tlsCfg.NextProtos = []string{"http/1.1"}`（`internal/cluster/events.go`）。Incus 6.23 不实现 RFC 8441，不支持 WS over h2。

3. 若返回 `400 "<type> isn't a supported event type"`，说明订阅了 Incus 不认识的类型。当前代码只订阅 `lifecycle`（按 `metadata.source` 区分 instance vs cluster-member），不要加回 `type=cluster`。

4. 看 backoff 是否到顶：重连退避 5s → 60s + 25% jitter；长时间失败不会 crash 进程，但需要人工干预。

**兜底**：reconciler 仍然跑，数据最终一致；重连时 listener 会先触发一次全量 reconcile 对齐 drift。

### healing_events 卡在 in_progress

**原因**：节点 online 事件漏收，或 evacuate 操作中途异常未走 Fail 路径。

**处置**：

1. 查当前 in_progress 行：
   ```sql
   SELECT id, cluster_id, node_name, trigger, started_at, NOW() - started_at AS age
   FROM healing_events WHERE status = 'in_progress';
   ```

2. 若 `age > 15min` → 等下一个 sweeper tick（5min）自动转 `partial`

3. 若确认节点早已 online 或 drill 早已结束，手工完成：
   ```sql
   UPDATE healing_events
   SET status = 'completed', completed_at = NOW()
   WHERE id = <ID> AND status = 'in_progress';
   ```

4. 手工完成的行建议补一条审计说明：
   ```sql
   -- 假设 actor 为运维 user_id=XX
   INSERT INTO audit_logs (user_id, action, target_type, target_id, details, ip_address)
   VALUES (<XX>, 'healing.manual_complete', 'healing_event', <ID>,
           '{"reason":"<描述>"}'::jsonb, NULL);
   ```

### Reconciler drift 异常激增

**症状**：单次 reconcile 标记 `gone` 的 VM 数 > 5（`drift above threshold` WARN）。

**排查**：

1. 确认 Incus 集群是否批量重启/迁移导致 VM 短暂不可见 —— 这种情况次轮 reconcile 会自动纠正回来

2. 若是生产事故（节点宕机），先走节点恢复 runbook，VM 会随 healing 被 evacuate 到其他节点；healing 完成后 reconciler 会停止对这些 VM 标 gone

3. 若是误标，**不要直接** `UPDATE vms SET status = 'running'`，先确认 Incus 侧确实存在该实例：
   ```bash
   incus --project customers list <vm-name>
   ```
   存在才恢复，否则保留 `gone`

### VM 被多次反复标 gone

**原因**：Incus 侧实例存在但 cluster client TLS/权限断续；reconciler 误判。

**注意**：reconciler **本身不会** 对 "cluster unreachable" 触发 gone（per-cluster error 隔离，整个 cluster skip）。若看到反复 gone，通常是单个 VM 名字在两集群项目间冲突，或 project 参数错误。

**检查**：
```bash
grep "marked gone" journalctl-output | awk -F'vm_id=' '{print $2}' | cut -d' ' -f1 | sort | uniq -c | sort -rn | head -5
# 同一 vm_id 短时间内多次 gone → 数据异常
```

---

## 常见操作

### 手动 evacuate 节点

**入口**：`/admin/ha` → Status Tab → 目标节点 → "Evacuate"按钮；或 API：

```bash
# step-up 认证 + admin 身份
curl -X POST https://vmc.5ok.co/api/admin/clusters/<cluster>/nodes/<node>/evacuate \
  -H "Cookie: <session>"
```

**行为**：
1. 先写 `healing_events(trigger='manual', actor_id=<admin>)` → 返回 `healing_event_id`
2. 调 Incus `POST /1.0/cluster/members/<node>/state {"action":"evacuate"}`
3. 异步等待 operation 完成 → `Complete` 或 `Fail` 对应 healing 行
4. `audit_logs` 写 `node.evacuate` + `healing_event_id` 交叉引用

**VM 落地新节点记录**：evacuate 过程中 `instance-updated` 事件触发 `AppendEvacuatedVM`，在 `healing_events.evacuated_vms` JSONB 里追加 `{vm_id, name, from_node, to_node}`。

**验证**：
- `/admin/ha` History Tab 该行状态从 `进行中` → `已完成`，VM 数 > 0
- 点击明细 Drawer 展示每个 VM 的迁移路径

### 恢复节点（restore）

```bash
curl -X POST https://vmc.5ok.co/api/admin/clusters/<cluster>/nodes/<node>/restore
```

修复节点后调 restore 让它重新接收新 VM。注意：**不会**把原先 evacuate 走的 VM 迁回来 —— 如需回迁，手工 `incus move <vm> --target <node>`（PLAN-020 Phase F 范围之外）。

### Chaos drill（故障演练）

**仅允许非 production 环境**。handler 顶部硬拒 `INCUS_ADMIN_ENV=production`。

```bash
# 前置：服务端必须以 INCUS_ADMIN_ENV=staging 或 =dev 启动
curl -X POST https://<staging>/api/admin/clusters/<cluster>/ha/chaos/simulate-node-down \
  -H "Content-Type: application/json" \
  -d '{"node":"node1","duration_seconds":60,"reason":"drill 2026-04-19"}'
```

**字段约束**：
- `node` 合法 hostname
- `duration_seconds` ∈ `[10, 600]`
- `reason` 必填（审计要求）

**执行流**：handler 立即返 202 + `healing_event_id`；后台 goroutine 用 `context.Background()` + duration+5min 超时 cap 执行 evacuate → sleep → restore，全程更新 `healing_events(trigger='chaos')`。

**中止**：当前无 "cancel drill" API；如需紧急中止，直接 `systemctl restart incus-admin`，drill 的 healing 行会被 ExpireStale 转 `partial`；之后手工 `incus cluster member set <node> state=restore`。

### 切环境变量开启 chaos

```bash
# 临时（测试）
sudo systemctl edit incus-admin --full
# 编辑 [Service] 段加：
# Environment="INCUS_ADMIN_ENV=staging"
sudo systemctl daemon-reload && sudo systemctl restart incus-admin

# 还原
sudo systemctl revert incus-admin && sudo systemctl restart incus-admin
```

**警告**：切到 staging 后 chaos 可被任意 admin 触发，只在隔离集群使用；drill 结束 **必须** 切回 production。

---

## 告警建议（未落地，留给 SRE）

| 信号 | 阈值 | 建议路由 |
|------|------|----------|
| `event listener disconnected` | 10min 内 > 3 次 | Slack/Lark 企业 IM |
| `vm reconciler: drift above threshold` | 每日 > 1 次 | 邮件 summary |
| `healing_events.status='failed'` 新增 | 任一条 | IM 高优先 |
| `healing_events.status='partial'` | 每周 > 0 | 周报 |
| `in_progress` 行龄 > 30min | 任一条 | IM 高优先（sweeper 应在 15min 内转 partial） |

Alertmanager / Prometheus 集成属于 PLAN-018 之后的独立专题，本 runbook 不展开。

---

## 版本约束

- Incus ≥ 6.0（events 订阅 `type=lifecycle` + `metadata.source` 前缀稳定）
- Incus 6.23 实测：`type=cluster` **不被接受**，只用 lifecycle
- WebSocket 必须走 HTTP/1.1 ALPN（gorilla websocket + Incus 当前版本的兼容约束）
- `healing_events` 保留 365 天（同审计），保留时间由 `AUDIT_RETENTION_DAYS` 间接控制（目前 healing 独立保留策略未实现）

---

## 相关文档

- PLAN-020（计划与决策）—— `docs/plan/PLAN-020.md`
- HA-001（任务跟踪）—— `docs/task/HA-001.md`
- INFRA-006（VM 反向同步 worker）—— `docs/task/INFRA-006.md`
- PLAN-019（Step-up auth / Shadow login / 审计全覆盖）—— `docs/plan/PLAN-019.md`
