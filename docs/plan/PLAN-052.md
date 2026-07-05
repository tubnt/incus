# PLAN-052 VM 创建 SSH 不可达根因修复 + 凭据/UX 闭环

- **status**: completed
- **priority**: P1
- **owner**: claude
- **createdAt**: 2026-05-15
- **completedAt**: 2026-05-15
- **task**: OPS-051

---

## 0. 摘要

vm-08f9d5（ubuntu/24.04/cloud）开机后默认凭据连不上 → 调查发现
linuxcontainers cloud variant 不预装 openssh-server，`buildCloudInit`
也没注入 `packages:`，导致**所有 Linux VM 都无法默认 SSH**。
本计划在修主因之外，把用户旅程从「显示密码 → ssh 失败 → 不知道为啥」
改为「等 cloud-init 完成 → smoke verify 通 → 凭据明文 + 一键复制 →
密码即刻可用」。

## 1. 用户旅程闭环（修后）

```
用户 /launch 点创建
  ↓
order → pay → enqueue
  ↓ jobs runtime
  step0 submit_instance         (~1s)
  step1 wait_create             (镜像拉取 0-30s，已缓存 0s)
  step2 start_instance          (~2s)
  step3 wait_start              (boot operation 完成 ~5s)
  step4 wait_cloud_init         (新增；apt 装 sshd+qga ~30-90s，hard cap 5min)
  step5 verify_ssh              (新增；ss -ltn 0.5s，soft fail-open)
  step6 finalize                (写 DB + audit + firewall binding)
  ↓ SSE terminal=succeeded
DonePanel
  - VM/IP/User=root/Password 全部明文
  - 一键 "复制全部" 按钮（含 ssh 命令）
  - sshd 状态指示由 verify_ssh step 结果决定
  ↓
用户复制密码 → ssh root@<ip> 一次通
```

## 2. Phase 拆分

### Phase A — 基础设施

**A-1 部署 apt-cacher-ng**

- 在 vmc.5ok.co（incusadmin 主控，139.162.24.177）部署 docker compose
- 监听内网 10.x.x.x:3142（不暴露公网；客户 VM 通过私网或主控 IP 访问）
- 因主控与 VM IP（202.151.179.x）在不同子网，先用主控公网 IP 走 3142
  端口；后期可加 nginx 缓存代理收敛带宽
- 缓存目录 `/var/cache/apt-cacher-ng`，限制 20 GB
- 健康检查：`curl http://139.162.24.177:3142/acng-report.html`

**A-2 buildCloudInit 注入 apt proxy**

cloud-config:
```yaml
apt:
  http_proxy: http://139.162.24.177:3142/
  https_proxy: http://139.162.24.177:3142/
```

注：上游不可达时 apt-cacher-ng 会 404 而非失败（passthrough 模式），
仍能正常装包；为防止 mirror 完全宕机硬阻塞，cloud-init 加 fallback：

```yaml
runcmd:
  - 'if ! timeout 5 curl -sf http://139.162.24.177:3142/acng-report.html >/dev/null; then sed -i "/http_proxy/d;/https_proxy/d" /etc/apt/apt.conf.d/*; fi'
```

### Phase B — buildCloudInit 全新重写（OS-aware）

**核心函数**：`incus-admin/internal/service/vm.go::buildCloudInit`

签名变更：
```go
type CloudInitInput struct {
    OSKind       OSFamily      // apt|dnf|pacman|alpine
    Password     string
    SSHKeys      []string
    ExtraYAML    string         // 来自 os_templates.cloud_init_template
    AptProxy     string         // 来自 cfg.AptCacher.URL
}
func BuildCloudInit(in CloudInitInput) string
```

**输出模板**（apt 系范例）：
```yaml
#cloud-config
hostname: ${name}   # cloud-init 内置变量 v.instance_id 不可用; 由 incus 自动注入
preserve_hostname: false
manage_etc_hosts: true

apt:
  http_proxy: http://139.162.24.177:3142/
  https_proxy: http://139.162.24.177:3142/

package_update: true
package_upgrade: false   # 首装不全量升级，由 unattended-upgrades 异步做
packages:
  - openssh-server
  - qemu-guest-agent
  - unattended-upgrades

users:
  - name: root
    lock_passwd: false
    ssh_authorized_keys:
      ${SSH_KEYS_YAML}

disable_root: false
ssh_pwauth: true

chpasswd:
  expire: false
  users:
    - name: root
      password: ${PASSWORD}
      type: text

write_files:
  - path: /etc/ssh/sshd_config.d/99-incusadmin.conf
    content: |
      PermitRootLogin yes
      PasswordAuthentication yes
  - path: /etc/apt/apt.conf.d/20auto-upgrades
    content: |
      APT::Periodic::Update-Package-Lists "1";
      APT::Periodic::Unattended-Upgrade "7";

runcmd:
  - 'systemctl enable --now ssh.service || systemctl enable --now sshd.service'
  - 'systemctl enable --now qemu-guest-agent.service'
  - 'systemctl reload ssh.service || systemctl reload sshd.service'
  - 'systemctl enable --now unattended-upgrades.service'

# 来自 os_templates.cloud_init_template 的字段（YAML merge）：
${EXTRA_YAML_MERGED}
```

**OS 包名差异**：
- `apt` (ubuntu/debian): `openssh-server, qemu-guest-agent, unattended-upgrades`
- `dnf` (rocky/almalinux/fedora): `openssh-server, qemu-guest-agent, dnf-automatic`
- `pacman` (arch): `openssh, qemu-guest-agent`（无 unattended-upgrades 对应）
- runcmd 服务名 `ssh.service` vs `sshd.service` 用 `||` 兼容

**YAML merge 规则**：
- 系统字段（users.root / packages 中的 openssh-server / sshd_config）**强制保留**
- ExtraYAML 中其它字段（write_files / runcmd / hostname 等）追加合并
- 用 `yaml.v3` 解析两个 Node，递归合并 mapping，list 追加去重
- 校验：ExtraYAML 解析失败 → buildCloudInit 返 error，job 失败
  （这是 admin 端配置错误，应明确暴露）

### Phase C — jobs runtime 加 step 5/6

**vm_create.go** 在 finalize 之前插入：

```go
// step 5: wait_cloud_init
rt.step(ctx, job.ID, 5, stepWaitCloudInit, model.StepStatusRunning, "等待 cloud-init 完成")
cloudInitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
defer cancel()
ret, err := client.ExecNonInteractive(cloudInitCtx, params.Project, job.TargetName,
    []string{"cloud-init", "status", "--wait"})
if err != nil || ret != 0 {
    rt.finishStep(ctx, job.ID, 5, stepWaitCloudInit, model.StepStatusWarning,
        fmt.Sprintf("cloud-init 未在 5 分钟内完成 (ret=%d, err=%v)，VM 仍可用但可能需要等待", ret, err))
    // soft fail-open（Q4=A）
} else {
    rt.finishStep(ctx, job.ID, 5, stepWaitCloudInit, model.StepStatusSucceeded, "")
}

// step 6: verify_ssh (Linux) / verify_rdp (Windows)
port, svc := 22, "ssh"
if osKind == "windows" { port, svc = 3389, "rdp" }
rt.step(ctx, job.ID, 6, stepVerifyReady, model.StepStatusRunning,
    fmt.Sprintf("验证 %s/%d 可用", svc, port))
verifyCtx, vcancel := context.WithTimeout(ctx, 10*time.Second)
defer vcancel()
ret, _ = client.ExecNonInteractive(verifyCtx, params.Project, job.TargetName,
    []string{"sh", "-c", fmt.Sprintf("ss -ltn | grep -qE ':%d\\s'", port)})
if ret == 0 {
    rt.finishStep(ctx, job.ID, 6, stepVerifyReady, model.StepStatusSucceeded, "")
} else {
    rt.finishStep(ctx, job.ID, 6, stepVerifyReady, model.StepStatusWarning,
        fmt.Sprintf("%d 端口尚未监听，VM 已创建可手动排查", port))
}
```

Windows 路径用 PowerShell 替代：
```
Test-NetConnection -ComputerName 127.0.0.1 -Port 3389 -InformationLevel Quiet
```

**`StepStatusWarning`**：新增 step 状态，不让 job=failed。需要 model + repository + SSE 编码兼容。

**关闭异步路径**：旧 finalize 在 step 4，新顺序：4→5→6→7（finalize 改 step7）。

**vm_reinstall.go** 同步加 step 5/6（在 recreate 之后）。

### Phase D — 删僵尸同步路径

**清单**：
- `internal/service/vm.go::VMService.Create` (line 121-234，~110 行)
- `internal/service/vm.go::VMService.Reinstall` (line 523-653，~130 行)
- `internal/service/vm.go::buildCloudInit` (lowercase 版本)
  → 改 `BuildCloudInit` 实现成主版（删 wrapper）
- `internal/handler/portal/vm.go::VMHandler.Reinstall:203-221`
  （`h.jobs == nil` 兜底分支删 ~20 行）
- `internal/handler/portal/vm.go::AdminVMHandler.CreateVM:1221-1273`
  （兜底分支删 ~55 行）
- `internal/handler/portal/vm.go::AdminVMHandler.ReinstallVM:1666-1680`
  （兜底）
- `cmd/server/main.go`：runtime 注入永远不为 nil 的 invariant 加 panic 兜底
- 所有调用 `h.vmSvc.Create/Reinstall` 的 worker（event_listener / clustermgmt）
  graph 误识别（healing.Create / vmRepo.Create 同名），实际无依赖，确认后清理

**风险**：测试代码可能调 VMService.Create — 全文搜替换为 jobs runtime 注入或
直接调 jobs.Run。

### Phase E — cloud_init_template 接入消费

**后端**：
- `repository/os_template.go` 不变（已读字段）
- `service/jobs/vm_create.go`：通过 `rt.deps.OSTemplates.GetBySlug(...)`
  取 cloud_init_template（按 image alias 反查 template）。若取不到则空串
- `service/vm.go::BuildCloudInit` 用 yaml.v3 合并 ExtraYAML
- `handler/portal/template.go::validateCloudInitTemplate` 加危险字段黑名单：
  - 拒绝顶层 `disable_root: true`（与系统默认冲突）
  - 拒绝顶层 `ssh_pwauth: false`（破坏初始登录）
  - 拒绝 `users` 顶层完全替换（系统 users.root 必须保留）→ 改用追加策略

**前端**：
- `admin/os-templates.tsx` 加 Textarea + 校验报错显示 + "高级 cloud-init 注入"
  说明文案
- 预留 "🤖 让 AI 帮我写"按钮（disabled + tooltip "PLAN-053 接入"）
- 校验在 onChange 触发 `await api.validateTemplate(...)`，错误下方红色提示

### Phase F — 统一 root 账号

**DB**：
```sql
UPDATE os_templates SET default_user='root'
WHERE source LIKE '%/cloud' OR slug LIKE 'ubuntu%' OR slug LIKE 'debian%'
   OR slug LIKE 'rocky%' OR slug LIKE 'almalinux%' OR slug LIKE 'fedora%'
   OR slug LIKE 'archlinux';
-- 不动 Windows (Administrator)
```

**前端**：
- `features/vms/default-user.ts::defaultUserForImage` 所有 Linux 分支返 root
- `features/vms/default-user.test.ts` 同步改

**后端**：
- buildCloudInit 用 `users.root` 替代镜像默认 user
- VMHandler.Reinstall 返回 `username: "root"`（替代 "ubuntu"）
- 注意：cloud variant 镜像自带的 ubuntu/debian 用户**保留**（cloud-init 不会
  删 user，磁盘空间忽略），仅默认登录目标改 root

### Phase G — firewall binding 漂移修复

**预防侧**（创建时）：
- `vm_create.go::applyUserDefaultFirewallGroups` 内 attach 成功后调
  `rt.deps.Firewall.Bind(ctx, vmID, groupID)` 写 binding 表
- 失败也仅 log，不阻塞 job

**修复侧**（admin endpoint）：
- `internal/handler/portal/firewall.go` 加 `AdminVMHandler.ReconcileBindings`
- 路径 `POST /admin/firewall/reconcile-bindings?dry_run=1`
- 逻辑：扫所有 cluster 的 incus 实例（跨 project），读 `security.acls`，
  对比 vm_firewall_bindings 表，缺失则写入；删除孤儿 binding
- dry_run=1 返回差异不写库
- audit log: `admin.firewall.reconcile_bindings`

### Phase H — 前端 UX 改造

**SecretReveal 增强**：
- 新增 prop `alwaysReveal?: boolean`，默认 false 保持兼容
- 当 alwaysReveal=true 时省略 Eye 按钮，永远明文显示，仅留 Copy 按钮

**DonePanel 改造**：
- 凭据 4 个 SecretReveal 全部 `alwaysReveal`（明文）
- 新增 "复制全部凭据" 按钮，剪贴板格式：
  ```
  VM: ${vm_name}
  IP: ${ip}
  Username: ${username}
  Password: ${password}

  ssh ${username}@${ip}
  ```
- 顶部 sshd ready 指示：新增 `verify_ready` 字段从 SSE stream.steps 取 step6
  的 status，渲染 🟢/🟡/🔴 标记 + i18n 文案
- 一键复制成功 toast

**ProvisioningPanel 文案优化**：
- i18n key 优化：
  - `submit_instance` → "提交创建请求"
  - `wait_create` → "等待镜像拉取与实例创建"
  - `start_instance` → "启动实例"
  - `wait_start` → "等待 boot 完成"
  - `wait_cloud_init` → "正在安装 SSH 服务和系统组件"
  - `verify_ssh` → "验证连通性"
  - `verify_rdp` → "验证远程桌面连通性"
  - `finalize` → "记录运行节点与凭据"

**i18n 双语**：
- `web/public/locales/zh/common.json` + `en/common.json` 同步 step keys

### Phase I — 旧 cluster/single 模板同步

`cluster/configs/cloud-init/vm-user-data.yaml.template`：

```yaml
packages:
  - openssh-server          # 新增
  - qemu-guest-agent
```

（保持 root 化与代码侧一致；该文件 IncusAdmin 服务不读，仅为单机部署模式
保留）

### Phase J — 验证与部署

1. **本地编译验证**：
   - `cd incus-admin && go vet ./... && go test ./... -count=1`
   - `cd incus-admin/web && bun run typecheck && bun run lint`
   - `task web-build`
   - `cd incus-admin && go build -trimpath ./cmd/server`

2. **部署到 vmc.5ok.co**：
   - DB migration（drop column or update default_user）
   - apt-cacher-ng docker compose up
   - 上传新 binary `/usr/local/bin/incus-admin`
   - `systemctl restart incus-admin`

3. **现场验证**：
   - portal 用 5ok 账号新建 1 台 ubuntu-24-04 VM
   - 完成态密码出现 + `ssh root@<ip>` 一次通
   - admin/vms 看到 firewall binding 已 attach
   - `incus exec ... -- systemctl status unattended-upgrades` 是 active

## 3. 不做（明确边界）

- CoreOS/Flatcar/Fedora-CoreOS 镜像 → PLAN-053
- AI LLM 调 API 自动生成 cloud-init → PLAN-053
- 舰队级 patch 集中调度（Ansible/Salt） → OPS-052 backlog
- apt-cacher-ng HA 高可用 → 单实例够，主控故障 VM 仍能直连上游（fallback）
- 现存 vm row 批量回填 root —— 历史 VM 全部已 deleted，无需迁移

## 4. 风险

| 风险 | 缓解 |
|------|------|
| apt-cacher-ng 主控宕机 → 新建 VM 卡住 | runcmd 健康探测 → 自动剥离 proxy |
| cloud-init `packages` 装包失败但 `status` 仍 done（issue #6281） | step 6 ss 探活兜底；warning 提示用户 |
| 5 分钟 cloud-init 阻塞 worker 拖慢批量 | OPS-050 已可调 PoolSize；建议生产 8/256 |
| YAML merge 撞键导致系统字段被 ExtraYAML 覆盖 | 黑名单 + 系统字段强制 override 策略 |
| 统一 root 但有用户已配 ssh_keys 给 ubuntu user | cloud-init 同时把 keys 写入 root + 保留 ubuntu user 的 keys（双重保险） |
| DonePanel 凭据明文显示，肩窥风险 | 用户决策（UX-007 已开 step-up 重看入口；本次取一致 = 明文 + 复制） |

## 5. 估算

- 后端 Go：~1,200 行（含测试）
- 前端 TS：~400 行
- SQL：1 migration（drop or update default_user）
- Docker compose：1 文件（apt-cacher-ng）+ env.example 增 `APT_CACHER_URL`
- 文档：OPS-051.md / PLAN-052.md / changelog 条目

## 6. 关联

- 上游：PLAN-051 §2-E F-05；docs/Session-2.md F-05 round-trip 测试
- 横向：OPS-022（密码加密）/ OPS-024（admin batch）/ PLAN-035-036（firewall）
- 下游：PLAN-053（CoreOS + AI gen cloud-init）/ OPS-052（patch 仪表盘）
