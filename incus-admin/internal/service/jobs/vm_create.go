package jobs

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/service"
)

// vmCreateExecutor 执行一次 vm.create 的全部步骤。前置假设：
//   - handler 已同步完成 IP 分配 + INSERT vms row(status='creating')，并把 vm_id 回写 job
//   - handler 已 PayWithBalance 把订单推到 paid → provisioning，余额已扣
//
// executor 只负责"提交 Incus → 等创建 → 等启动 → 写终态"。
type vmCreateExecutor struct{}

const (
	stepSubmit        = "submit_instance"
	stepWaitCreate    = "wait_create"
	stepStart         = "start_instance"
	stepWaitStart     = "wait_start"
	stepWaitCloudInit = "wait_cloud_init" // OPS-051 / PLAN-052
	stepVerifyReady   = "verify_ready"    // OPS-051 / PLAN-052
	stepFinalize      = "finalize"
)

func (e *vmCreateExecutor) Run(ctx context.Context, rt *Runtime, job *model.ProvisioningJob) error {
	params := rt.peekParams(job.ID)
	if params == nil {
		return fmt.Errorf("params missing for job %d (process restart?)", job.ID)
	}

	clusterName := rt.clusterName(job.ClusterID)
	client, ok := rt.deps.Clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not registered", clusterName)
	}

	// WP-I1：root_pass 优先用用户指定值（handler 已按 openapi minLength:8 校验），
	// 空则随机生成。password 走既有统一 root 凭据链路（cloud-init 注入 + 落库加密）。
	password := service.GeneratePassword()
	if strings.TrimSpace(params.RootPass) != "" {
		password = params.RootPass
	}

	imageAlias := params.OSImage
	if len(imageAlias) > 7 && imageAlias[:7] == "images:" {
		imageAlias = imageAlias[7:]
	}

	// OPS-051 / PLAN-052：cloud-init OS-aware + 统一 root + apt proxy + 合并
	// os_templates.cloud_init_template 高级字段。template lookup 失败 → 空
	// ExtraYAML（不阻塞 job，admin 模板配置错应在 UI 校验阶段挡住）。
	loginUser := rt.deps.DefaultLoginUser
	if loginUser == "" {
		loginUser = "root"
	}
	extraYAML := ""
	if rt.deps.OSTemplates != nil {
		// imageAlias 来自 os_templates.source，先找精确匹配的 enabled 模板
		if tpl, terr := rt.deps.OSTemplates.GetBySource(ctx, imageAlias); terr == nil && tpl != nil {
			extraYAML = tpl.CloudInitTemplate
			if tpl.DefaultUser != "" {
				loginUser = tpl.DefaultUser
			}
		}
	}
	cloudInit := service.BuildCloudInit(service.CloudInitInput{
		OSFamily:    service.ClassifyOSFamily(imageAlias),
		LoginUser:   loginUser,
		Password:    password,
		SSHKeys:     params.SSHKeys,
		AptProxyURL: rt.deps.AptProxyURL,
		ExtraYAML:   extraYAML,
		// WP-I1：用户 user_data 合并进 OS-aware 基础配置（cloud-config 追加合并；
		// 非 cloud-config 走 multipart MIME），不覆盖既有基础段。
		UserData: params.UserData,
	})

	// OS-aware：Windows / Linux / CoreOS 走不同 cloud-init 形态。
	// - Linux：netplan v2 + 接口名 enp5s0
	// - Windows：cloudbase-init / PowerShell exec
	// - CoreOS (Fedora CoreOS / Flatcar)：用 Ignition JSON 通过 fw_cfg 注入，
	//   不支持 cloud-init datasource。SSH key only (无密码)，default user = core。
	osKind := "linux"
	if isWindowsAlias(imageAlias) {
		osKind = "windows"
	} else if isCoreOSAlias(imageAlias) {
		osKind = "coreos"
	}

	// Windows 路径需要预先固定 MAC 才能写进 v1 network-config 让 cloudbase-init
	// 通过 mac match 找到唯一 NIC；Linux 路径让 Incus 默认生成 MAC（无所谓）。
	hwaddr := ""
	if osKind == "windows" {
		hwaddr = generateMAC()
	}

	var netCfg string
	if osKind == "windows" {
		netCfg = buildWindowsNetworkConfigV1(hwaddr, params.IP, params.SubnetCIDR, params.Gateway)
	} else {
		netCfg = service.BuildNetworkConfig(params.IP, params.SubnetCIDR, params.Gateway)
	}

	imageSource := map[string]any{
		"type":  "image",
		"alias": imageAlias,
	}
	// linuxcontainers.org simplestreams alias 形如 "ubuntu/24.04/cloud"，必带 "/"；
	// 不带 "/" 的视作管理员 incus image import 的本地 alias（如 windows-server-2022），
	// 让 Incus 走本地 image store 查找，不去上游拉。
	if strings.Contains(imageAlias, "/") {
		imageSource["server"] = "https://images.linuxcontainers.org"
		imageSource["protocol"] = "simplestreams"
	} else {
		// 本地 incus image alias：incus-admin 用受限 cert，alias 解析对受限
		// cert 不可见。先 GET /1.0/images/aliases/<name> 拿 fingerprint，
		// 再用 fingerprint 创建（受限 cert 可以读 alias，但创建实例时 alias→
		// fingerprint 内部解析路径走 admin-only 校验）。
		aliasResp, aerr := client.APIGet(ctx, fmt.Sprintf("/1.0/images/aliases/%s?project=%s", imageAlias, params.Project))
		if aerr == nil && aliasResp != nil && len(aliasResp.Metadata) > 0 {
			var meta struct {
				Target string `json:"target"`
			}
			if jerr := json.Unmarshal(aliasResp.Metadata, &meta); jerr == nil && meta.Target != "" {
				delete(imageSource, "alias")
				imageSource["fingerprint"] = meta.Target
				slog.Info("vm_create: resolved local alias to fingerprint", "alias", imageAlias, "fingerprint", meta.Target)
			}
		}
	}

	configMap := map[string]any{
		"limits.cpu":    fmt.Sprintf("%d", params.CPU),
		"limits.memory": fmt.Sprintf("%dMiB", params.MemoryMB),
		// Incus 标准 key 是 cloud-init.user-data / cloud-init.network-config
		// （legacy alias: user.user-data / user.network-config）。
		// 历史代码用 user.cloud-init，不是任何 cloud-init datasource 认识的
		// key，导致 NoCloud 找不到种子 → cloud-init-generator 自禁 →
		// netplan 不写入 → enp5s0 永远 DOWN。所有自 PLAN-005 起新建的
		// VM 都中招（admin VM 看似正常是因为没人测过出站）。
		"cloud-init.user-data":      cloudInit,
		"cloud-init.network-config": netCfg,
		"security.secureboot":       "false",
		"migration.stateful":        "true",
	}
	// WP-I1：tags 落到 incus 实例 user.tags 配置（vms 表当前无 tags 列，禁止改
	// schema，故落 incus 实例标签）。逗号分隔，空 tag 过滤。
	if tagStr := joinTags(params.Tags); tagStr != "" {
		configMap["user.tags"] = tagStr
	}
	// CoreOS 路径：Ignition JSON 通过 qemu fw_cfg name=opt/com.coreos/config
	// 注入。QEMU 的 OPTS 解析把 `,` 视为子参数分隔符 → JSON 内 `,` 必须
	// 用 `,,` 转义（QEMU 标准 escape）。需要 customers project 设
	// `restricted.virtual-machines.lowlevel=allow` 允许 raw.qemu。
	// CoreOS 不读 cloud-init，删 user-data + network-config 避免混淆。
	if osKind == "coreos" {
		ignition := buildIgnitionJSON(params.SSHKeys, params.IP, params.SubnetCIDR, params.Gateway)
		// incus raw.qemu 走 shlex.split。SSH key 含空格、JSON 含 `"` 都会
		// 被 split 破坏。用 single quote 包整段 fw_cfg arg —— shlex 单引号
		// 内的内容原样保留（不 split 空格、不处理 `\`、不吃 `"`）。
		// `,` → `,,` 仍要（QEMU OPTS 解析层 escape）。
		escapedIgnition := strings.ReplaceAll(ignition, ",", ",,")
		configMap["raw.qemu"] = fmt.Sprintf(`-fw_cfg 'name=opt/com.coreos/config,string=%s'`, escapedIgnition)
		delete(configMap, "cloud-init.user-data")
		delete(configMap, "cloud-init.network-config")
	}
	// 我们用的 antifob/incus-windows 构建的 Win Server 2022 镜像是 UEFI 原生
	// 且通过 image properties `requirements.cdrom_agent=true` 让 Incus 自动注入
	// incus-agent CDROM（agent:config）。不需要 CSM，secureboot 由 backend
	// 上面的 security.secureboot=false 覆盖。如果未来引入 BIOS-only Windows
	// 镜像，把 image properties 加 requirements.csm=true 让 Incus 自处理。
	body := map[string]any{
		"name":   job.TargetName,
		"type":   "virtual-machine",
		"source": imageSource,
		"config": configMap,
		"devices": map[string]any{
			"root": map[string]any{
				"type": "disk",
				"pool": params.StoragePool,
				"path": "/",
				"size": fmt.Sprintf("%dGiB", params.DiskGB),
			},
			"eth0": eth0Device(params.Network, params.IP, hwaddr),
			// 必加 cidata 盘，否则不带 incus-agent 的镜像（Windows / 自定义
			// 镜像）在 guest 内根本看不到 Incus 注入的 user-data /
			// network-config。Linux images: 镜像也可以挂这个盘，是冗余兜底但
			// 无副作用。来源：lxc/incus/doc/cloud-init.md。
			"cloud-init": map[string]any{
				"type":   "disk",
				"source": "cloud-init:config",
			},
		},
	}
	// OPS-051 follow-up：任何镜像 properties 含 requirements.cdrom_agent=true
	// 都需要显式挂 agent:config。原代码只覆盖 windows 路径，导致
	// images:rockylinux/9/cloud（也带 cdrom_agent）start 失败：
	// "This virtual machine image requires an agent:config disk be added"。
	// 修法：所有 VM 都挂；未被 image 引用的盘 incus 不会 mount 进 guest，
	// 无功能副作用、CDROM 资源开销可忽略（一个空 ISO 描述符）。
	body["devices"].(map[string]any)["incusagent"] = map[string]any{
		"type":   "disk",
		"source": "agent:config",
	}

	bodyJSON, _ := json.Marshal(body)

	// Step submit_instance
	rt.step(ctx, job.ID, 0, stepSubmit, model.StepStatusRunning, "提交 VM 创建请求")
	resp, err := client.APIPost(ctx, fmt.Sprintf("/1.0/instances?project=%s", params.Project), bytes.NewReader(bodyJSON))
	if err != nil {
		rt.finishStep(ctx, job.ID, 0, stepSubmit, model.StepStatusFailed, err.Error())
		return fmt.Errorf("submit instance: %w", err)
	}
	rt.finishStep(ctx, job.ID, 0, stepSubmit, model.StepStatusSucceeded, "")

	// Step wait_create — 真正的镜像拉取 + 创建发生在这里，可能 30s–5min
	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			rt.step(ctx, job.ID, 1, stepWaitCreate, model.StepStatusRunning, "等待镜像拉取与实例创建")
			if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
				rt.finishStep(ctx, job.ID, 1, stepWaitCreate, model.StepStatusFailed, werr.Error())
				return fmt.Errorf("wait create: %w", werr)
			}
			rt.finishStep(ctx, job.ID, 1, stepWaitCreate, model.StepStatusSucceeded, "")
		} else {
			rt.step(ctx, job.ID, 1, stepWaitCreate, model.StepStatusSkipped, "no operation id")
		}
	} else {
		rt.step(ctx, job.ID, 1, stepWaitCreate, model.StepStatusSkipped, "sync response")
	}

	// Step start_instance
	rt.step(ctx, job.ID, 2, stepStart, model.StepStatusRunning, "启动实例")
	startBody, _ := json.Marshal(map[string]any{"action": "start", "timeout": 60})
	startResp, err := client.APIPut(ctx, fmt.Sprintf("/1.0/instances/%s/state?project=%s", job.TargetName, params.Project), bytes.NewReader(startBody))
	if err != nil {
		rt.finishStep(ctx, job.ID, 2, stepStart, model.StepStatusFailed, err.Error())
		return fmt.Errorf("start instance: %w", err)
	}
	rt.finishStep(ctx, job.ID, 2, stepStart, model.StepStatusSucceeded, "")

	// Step wait_start
	if startResp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(startResp.Metadata, &op)
		if op.ID != "" {
			rt.step(ctx, job.ID, 3, stepWaitStart, model.StepStatusRunning, "等待 boot 完成")
			if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
				rt.finishStep(ctx, job.ID, 3, stepWaitStart, model.StepStatusFailed, werr.Error())
				return fmt.Errorf("wait start: %w", werr)
			}
			rt.finishStep(ctx, job.ID, 3, stepWaitStart, model.StepStatusSucceeded, "")
		} else {
			rt.step(ctx, job.ID, 3, stepWaitStart, model.StepStatusSkipped, "no operation id")
		}
	} else {
		rt.step(ctx, job.ID, 3, stepWaitStart, model.StepStatusSkipped, "sync response")
	}

	// Windows 镜像（如 antifob/incus-windows 构建版）只装了 incus-agent，没装
	// cloudbase-init。Incus 的 cloud-init NoCloud 数据 guest 内不会被消费，
	// VM 起来默认 APIPA 169.254.x。这里走 incus exec PowerShell 兜底：
	//   - 静态 IP/掩码/网关
	//   - DNS
	//   - 启用 RDP（fDenyTSConnections=0 + 防火墙 Remote Desktop 组）
	//   - Administrator 密码 = 我们生成的随机 password
	// 对 Linux 镜像不跑（已经走 cloud-init.network-config + cidata 自配）。
	if osKind == "windows" {
		applyWindowsCloudInit(ctx, client, params.Project, job.TargetName, params.IP, params.SubnetCIDR, params.Gateway, password)
	}

	// OPS-051 / PLAN-052 Step 4 wait_cloud_init：等 guest 内 cloud-init 完成
	// （apt 装 openssh-server + qemu-guest-agent，5min cap）。soft fail-open
	// （Q4=A）：超时不让 job=failed，避免误退款 + 删 VM。
	if osKind != "windows" {
		rt.step(ctx, job.ID, 4, stepWaitCloudInit, model.StepStatusRunning, "等待 cloud-init 完成（安装 SSH 服务）")
		ciCtx, ciCancel := context.WithTimeout(ctx, 5*time.Minute)
		ciRet, ciErr := client.ExecNonInteractive(ciCtx, params.Project, job.TargetName,
			[]string{"cloud-init", "status", "--wait"})
		ciCancel()
		switch {
		case ciErr != nil:
			rt.finishStep(ctx, job.ID, 4, stepWaitCloudInit, model.StepStatusWarning,
				fmt.Sprintf("cloud-init exec 失败 (err=%v)；VM 已创建，请稍后重试 SSH 或在详情页查看", ciErr))
		case ciRet != 0:
			rt.finishStep(ctx, job.ID, 4, stepWaitCloudInit, model.StepStatusWarning,
				fmt.Sprintf("cloud-init 退出码 %d（可能仍在装包）；VM 已创建，请等 1-2 分钟后重试 SSH", ciRet))
		default:
			rt.finishStep(ctx, job.ID, 4, stepWaitCloudInit, model.StepStatusSucceeded, "")
		}
	} else {
		rt.step(ctx, job.ID, 4, stepWaitCloudInit, model.StepStatusSkipped, "windows uses applyWindowsCloudInit path")
	}

	// OPS-051 / PLAN-052 Step 5 verify_ready：探活 22 / 3389 端口。同样
	// soft fail-open（Q4=A）。
	port, svc := 22, "SSH"
	if osKind == "windows" {
		port, svc = 3389, "RDP"
	}
	rt.step(ctx, job.ID, 5, stepVerifyReady, model.StepStatusRunning,
		fmt.Sprintf("验证 %s/%d 端口监听", svc, port))
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	var verifyCmd []string
	if osKind == "windows" {
		// cloudbase-init 已经在 applyWindowsCloudInit 内挂 RDP；这里用
		// PowerShell Test-NetConnection 自检（不依赖外部）。
		verifyCmd = []string{"powershell.exe", "-NoProfile", "-Command",
			"if ((Test-NetConnection -ComputerName 127.0.0.1 -Port 3389 -InformationLevel Quiet)) { exit 0 } else { exit 1 }"}
	} else {
		verifyCmd = []string{"sh", "-c", fmt.Sprintf("ss -ltn | grep -qE ':%d[[:space:]]'", port)}
	}
	verifyRet, verifyErr := client.ExecNonInteractive(verifyCtx, params.Project, job.TargetName, verifyCmd)
	verifyCancel()
	switch {
	case verifyErr != nil:
		rt.finishStep(ctx, job.ID, 5, stepVerifyReady, model.StepStatusWarning,
			fmt.Sprintf("%s 端口探活 exec 失败 (err=%v)；VM 已创建，请稍后试", svc, verifyErr))
	case verifyRet != 0:
		rt.finishStep(ctx, job.ID, 5, stepVerifyReady, model.StepStatusWarning,
			fmt.Sprintf("%s/%d 端口尚未监听 (exit=%d)；VM 已创建，请等 cloud-init 完成或在详情页查看", svc, port, verifyRet))
	default:
		rt.finishStep(ctx, job.ID, 5, stepVerifyReady, model.StepStatusSucceeded, "")
	}

	// Step 6 finalize：拉 instance 元数据取 node 名，写回 vm row + 写密码
	rt.step(ctx, job.ID, 6, stepFinalize, model.StepStatusRunning, "记录运行节点与凭据")
	node := ""
	if instanceData, gerr := client.GetInstance(ctx, params.Project, job.TargetName); gerr == nil {
		var inst struct{ Location string }
		_ = json.Unmarshal(instanceData, &inst)
		node = inst.Location
	}

	if job.VMID != nil {
		if err := rt.deps.VMs.UpdateAfterProvision(ctx, *job.VMID, node, password); err != nil {
			rt.finishStep(ctx, job.ID, 6, stepFinalize, model.StepStatusFailed, err.Error())
			return fmt.Errorf("update vm row: %w", err)
		}
	}
	// 订单状态推到 active
	if job.OrderID != nil {
		if err := rt.deps.Orders.UpdateStatus(ctx, *job.OrderID, model.OrderActive); err != nil {
			slog.Error("order activate failed", "job_id", job.ID, "order_id", *job.OrderID, "error", err)
		}
	}
	rt.finishStep(ctx, job.ID, 6, stepFinalize, model.StepStatusSucceeded, "")

	// PLAN-036 默认 firewall_groups 软失败应用：读用户 default 列表，
	// 串行 attach。任一失败仅 log + audit，不阻塞 finalize。VM 已 active，
	// 用户事后可通过 /firewall 集中管理页或 vm-detail 手动补绑。
	//
	// Windows VM 跳过：用户 default 组通常是 SSH-only / Web，不含 RDP 3389。
	// 直接套上去会让用户刚开机就连不上。Windows 默认放空，用户自己去 /firewall
	// 加 "Windows RDP" 组（含 3389）或选择性允许。
	if rt.deps.Firewall != nil && job.VMID != nil && osKind != "windows" {
		applyUserDefaultFirewallGroups(ctx, rt, job, rt.clusterName(job.ClusterID), params.Project, job.TargetName)
	}

	// 消费 params + pma-cr H-3：显式 Wipe credential（vm_create 不带 SSH cred，
	// 但 takeParams 返回的 Params 含密码 plaintext；保险起见 Wipe）
	if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
	return nil
}

// Rollback 释放 IP / 删 instance / 退款 / 取消订单 / 标 vm row error。每步幂等：
//   - MarkRefunded 用 refund_done_at IS NULL guard 保证只退一次
//   - 释放 IP 用 IP 字符串幂等（已 cooldown 状态再 Release 不报错）
//   - 删 instance 在 Incus 不存在时也忽略 404
//
// pma-cr MEDIUM：进程崩溃 / sweeper 路径下 params 已丢失，必须从 DB 兜底
// 读 IP（vms.ip）+ amount（orders.amount）。否则 stale recovery 会"finalize
// 但不退款"，跟 CRITICAL 同一根因。
func (e *vmCreateExecutor) Rollback(ctx context.Context, rt *Runtime, job *model.ProvisioningJob, reason string) {
	params := rt.peekParams(job.ID)
	clusterName := rt.clusterName(job.ClusterID)

	// 兜底从 DB 复原 project / IP / amount。params 在线时这些字段会被覆盖
	// 它的值（更准确，因 params 是 handler 当时的事实）。
	project := ""
	ip := ""
	amount := 0.0
	if params != nil {
		project = params.Project
		ip = params.IP
		amount = params.OrderAmount
	}
	if job.VMID != nil {
		if vm, _ := rt.deps.VMs.GetByID(ctx, *job.VMID); vm != nil {
			if ip == "" && vm.IP != nil {
				ip = *vm.IP
			}
		}
	}
	if amount == 0 && job.OrderID != nil {
		if order, _ := rt.deps.Orders.GetByID(ctx, *job.OrderID); order != nil {
			amount = order.Amount
		}
	}
	if project == "" {
		project = "customers" // 与 OrderHandler.Pay 默认一致
	}

	// 1) 删 Incus 上可能残留的 instance（幂等：404 忽略）
	if client, ok := rt.deps.Clusters.Get(clusterName); ok {
		_ = e.bestEffortDeleteInstance(ctx, client, project, job.TargetName)
	}

	// 2) 释放 IP（cooldown）
	if ip != "" {
		if err := rt.deps.IPAddrs.Release(ctx, ip); err != nil {
			slog.Warn("rollback release IP failed", "job_id", job.ID, "ip", ip, "error", err)
		}
	}

	// 3) 退款（原子幂等）—— RefundOnce 在单事务内完成 mark + balance + transactions，
	//    任一步失败整体回滚，下次重试。返回 (false, nil) 说明已退过 → no-op。
	if job.OrderID != nil && amount > 0 {
		desc := fmt.Sprintf("订单 #%d 失败退款: %s", *job.OrderID, reason)
		if _, err := rt.deps.Jobs.RefundOnce(ctx, job.ID, job.UserID, amount, desc); err != nil {
			slog.Error("rollback refund failed; will retry on next sweep", "job_id", job.ID, "error", err)
		}
	}

	// 4) 订单 cancelled
	if job.OrderID != nil {
		if err := rt.deps.Orders.UpdateStatus(ctx, *job.OrderID, model.OrderCancelled); err != nil {
			slog.Error("rollback cancel order failed", "job_id", job.ID, "error", err)
		}
	}

	// 5) vm row 标 error（保留行供 admin 调查；reverse-sync worker 后续可清理）
	if job.VMID != nil {
		if err := rt.deps.VMs.UpdateStatus(ctx, *job.VMID, model.VMStatusError); err != nil {
			slog.Error("rollback mark vm error failed", "job_id", job.ID, "vm_id", *job.VMID, "error", err)
		}
	}

	// 6) P1-4：取消该 VM 的订阅（PLAN-054 计费）。异步创建失败时若不销订阅，
	//    billing worker 仍会按周期扣费产生"幽灵扣费"。CancelByVM 只动 active 行
	//    且幂等（0 行也不报错）。订单已在步骤 4 cancelled，订阅随之作废。
	if rt.deps.Subscriptions != nil && job.VMID != nil {
		if _, err := rt.deps.Subscriptions.CancelByVM(ctx, *job.VMID); err != nil {
			slog.Error("rollback cancel subscription failed", "job_id", job.ID, "vm_id", *job.VMID, "error", err)
		}
	}

	// pma-cr H-3：rollback 路径同样显式 Wipe
	if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
}

func (e *vmCreateExecutor) bestEffortDeleteInstance(ctx context.Context, client *cluster.Client, project, name string) error {
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// 先 force stop（已 stopped 不报错）
	stopBody, _ := json.Marshal(map[string]any{"action": "stop", "timeout": 10, "force": true})
	_, _ = client.APIPut(stopCtx, fmt.Sprintf("/1.0/instances/%s/state?project=%s", name, project), bytes.NewReader(stopBody))

	delResp, err := client.APIDelete(stopCtx, fmt.Sprintf("/1.0/instances/%s?project=%s", name, project))
	if err != nil {
		return err
	}
	if delResp != nil && delResp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(delResp.Metadata, &op)
		if op.ID != "" {
			_ = client.WaitForOperation(stopCtx, op.ID)
		}
	}
	return nil
}

// clusterName 反查 cluster_id → name。runtime 集中放这里供 executor 复用。
func (r *Runtime) clusterName(clusterID int64) string {
	if r.deps.Clusters == nil {
		return ""
	}
	return r.deps.Clusters.NameByID(clusterID)
}

// applyUserDefaultFirewallGroups 在 vm.create finalize 后串行 attach 用户的
// 默认 firewall_groups（PLAN-036 D3 软失败口径）。任一失败 log + audit，但
// 整体不阻塞——VM 已 active，用户事后可补绑。
func applyUserDefaultFirewallGroups(
	ctx context.Context,
	rt *Runtime,
	job *model.ProvisioningJob,
	clusterName, project, vmName string,
) {
	groups, err := rt.deps.Firewall.ListDefaultGroups(ctx, job.UserID)
	if err != nil {
		slog.Error("default firewall: list failed", "job_id", job.ID, "user_id", job.UserID, "error", err)
		rt.deps.Audit.Log(ctx, &job.UserID, "firewall.default_apply_failed", "vm", *job.VMID,
			map[string]any{"reason": "list_failed", "error": err.Error()}, "")
		return
	}
	if len(groups) == 0 {
		return
	}
	if clusterName == "" || vmName == "" {
		slog.Warn("default firewall: missing cluster/vm name", "job_id", job.ID)
		return
	}
	failed := []map[string]any{}
	ok := 0
	for i := range groups {
		g := &groups[i]
		if err := rt.deps.Firewall.Attach(ctx, clusterName, project, vmName, g); err != nil {
			failed = append(failed, map[string]any{"group_id": g.ID, "slug": g.Slug, "error": err.Error()})
			continue
		}
		if err := rt.deps.Firewall.Bind(ctx, *job.VMID, g.ID); err != nil {
			failed = append(failed, map[string]any{"group_id": g.ID, "slug": g.Slug, "error": "bind: " + err.Error()})
			continue
		}
		ok++
	}
	if len(failed) > 0 {
		slog.Warn("default firewall: partial apply", "vm_id", *job.VMID, "ok", ok, "failed", len(failed))
		rt.deps.Audit.Log(ctx, &job.UserID, "firewall.default_apply_failed", "vm", *job.VMID,
			map[string]any{"ok": ok, "failed_count": len(failed), "failed": failed}, "")
	} else {
		rt.deps.Audit.Log(ctx, &job.UserID, "firewall.default_apply_ok", "vm", *job.VMID,
			map[string]any{"group_count": ok}, "")
	}
}

// joinTags 把 tags 数组规整为 incus user.tags 配置值（逗号分隔）。逐项 Trim 并
// 过滤空串；全空返回 ""（调用方据此决定是否写 config）。
func joinTags(tags []string) string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if s := strings.TrimSpace(t); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

// isWindowsAlias 判断 image alias 是否 Windows。约定：alias 以 "windows" 开头
// 或包含 "windows-" 段。镜像目录是 admin 维护，slug 可控，约定即可。
func isWindowsAlias(alias string) bool {
	a := strings.ToLower(alias)
	return strings.HasPrefix(a, "windows") || strings.Contains(a, "/windows-") || strings.Contains(a, "-windows")
}

// isCoreOSAlias 判断 image alias 是否 CoreOS（Fedora CoreOS / Flatcar）。
// CoreOS 用 Ignition 而非 cloud-init，必须走专用配置注入路径。
func isCoreOSAlias(alias string) bool {
	a := strings.ToLower(alias)
	return strings.Contains(a, "coreos") || strings.Contains(a, "flatcar")
}

// buildIgnitionJSON 生成 CoreOS Ignition 配置（spec 3.3.0）。CoreOS 默认
// `core` 用户不能用密码登录，必须有 SSH key。如果用户没传 SSH key，VM 仍
// 创建但无登录方式（建议前端在选 CoreOS 时强制要求 SSH key）。
//
// 静态 IP 用 systemd-networkd unit 写入 /etc/systemd/network/00-eth0.network。
// CoreOS 内置 systemd-networkd，启动时自动加载。
func buildIgnitionJSON(sshKeys []string, ip, cidr, gateway string) string {
	prefix := cidr
	if i := strings.LastIndex(cidr, "/"); i >= 0 {
		prefix = cidr[i+1:]
	}
	keysJSON, _ := json.Marshal(sshKeys)
	// Fedora CoreOS 44 默认用 NetworkManager（不是 systemd-networkd），
	// 写 NM keyfile 格式 /etc/NetworkManager/system-connections/eth0.nmconnection。
	// mode 0600 (384 decimal)，NetworkManager 拒绝 0644 keyfile。
	// interface-name=enp5s0 是 incus virtio-net 在 systemd predictable name 下
	// 的实际名（console log 已验证）。
	nmKeyfile := fmt.Sprintf(`[connection]
id=eth0
type=ethernet
interface-name=enp5s0

[ipv4]
method=manual
addresses=%s/%s
gateway=%s
dns=1.1.1.1;8.8.8.8;
may-fail=false

[ipv6]
method=disabled
`, ip, prefix, gateway)
	dataURL := "data:;base64," + base64.StdEncoding.EncodeToString([]byte(nmKeyfile))
	ignition := fmt.Sprintf(`{"ignition":{"version":"3.3.0"},"passwd":{"users":[{"name":"core","sshAuthorizedKeys":%s}]},"storage":{"files":[{"path":"/etc/NetworkManager/system-connections/eth0.nmconnection","mode":384,"overwrite":true,"contents":{"source":"%s"}}]}}`, keysJSON, dataURL)
	return ignition
}

// generateMAC 给 Windows VM 预生成 MAC。固定 OUI 10:66:6a（Incus / linuxcontainers
// 注册的 OUI，与 Linux VM 自动生成的 MAC 同前缀），后 3 字节随机。
// 不用 QEMU 标准 52:54:00 是因为某些机房上游交换机 MAC ACL 仅放行
// Incus OUI（线上观测：52:54:00 的 ARP 永远没回包，10:66:6a 正常）。
// 必须在 instance 创建之前定，因为要写进 cloud-init network-config v1
// 让 cloudbase-init/incus-agent 通过 MAC match 找到唯一 NIC。
func generateMAC() string {
	b := make([]byte, 3)
	if _, err := cryptorand.Read(b); err != nil {
		// 兜底用 time-based，安全性无关紧要（只是 NIC ID）
		now := time.Now().UnixNano()
		b[0] = byte(now)
		b[1] = byte(now >> 8)
		b[2] = byte(now >> 16)
	}
	return fmt.Sprintf("10:66:6a:%02x:%02x:%02x", b[0], b[1], b[2])
}

// buildWindowsNetworkConfigV1 生成 cloud-init network-config v1（cloudbase-init
// NoCloud 唯一支持的格式）。用 mac_address 匹配比 name/index 更可靠。
//
//	subnetCIDR 形如 "26"（仅前缀位数）或 "192.168.1.0/26"；
//	兼容历史调用，本函数仅取 prefix length。
//
// Cloudbase-init NoCloudConfigDriveService 引用：
//
//	https://cloudbase-init.readthedocs.io/en/latest/services.html#nocloud-configuration-drive
func buildWindowsNetworkConfigV1(mac, ip, subnetCIDR, gateway string) string {
	prefix := subnetCIDR
	if i := strings.LastIndex(subnetCIDR, "/"); i >= 0 {
		prefix = subnetCIDR[i+1:]
	}
	return fmt.Sprintf(`version: 1
config:
  - type: physical
    name: Ethernet
    mac_address: "%s"
    subnets:
      - type: static
        address: %s/%s
        gateway: %s
        dns_nameservers:
          - 1.1.1.1
          - 8.8.8.8
  - type: nameserver
    address:
      - 1.1.1.1
      - 8.8.8.8
`, mac, ip, prefix, gateway)
}

// eth0Device 装 nic device。Windows VM 必须传 hwaddr 锁定 MAC（与 network-config
// 里的 mac_address 对齐）；Linux 留空让 Incus 默认生成。
// utf16LE 把 UTF-8 字符串转 UTF-16LE 字节（PowerShell -EncodedCommand 要求）。
func utf16LE(s string) []byte {
	codepoints := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(codepoints)*2)
	for _, c := range codepoints {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func eth0Device(parent, ip, hwaddr string) map[string]any {
	dev := map[string]any{
		"type":                    "nic",
		"nictype":                 "bridged",
		"parent":                  parent,
		"ipv4.address":            ip,
		"security.ipv4_filtering": "true",
		"security.mac_filtering":  "true",
	}
	if hwaddr != "" {
		dev["hwaddr"] = hwaddr
	}
	return dev
}

// applyWindowsCloudInit 在 Windows VM 起来 + agent 在线后，通过 incus exec
// 跑 PowerShell 把 cloud-init 等价配置（IP/网关/DNS/RDP/Admin 密码）下发。
//
// 为什么不依赖镜像里的 cloudbase-init：antifob/incus-windows 构建版只有
// incus-agent + virtio + qemu-ga，没装 cloudbase-init；要装等于多一层维护
// （cloudbase-init 1.x MSI 安装 → sysprep → 重导出），收益不大。直接 RPC
// 进 guest 跑 PowerShell 是最短路径。
//
// 关键时序：wait_start 只等 QEMU 进程启动，不等 guest OS boot 完。Windows
// 冷启动 + incus-agent 自启 ≈ 60-180s。必须先轮询 instance state 等
// agent 在线（Processes != -1）才发 exec，否则 Incus 把请求队列住又超时。
//
// 失败不阻塞 finalize：哪怕这步出错，订单仍会成功，用户可通过 vm-detail
// "重置密码" 或 admin/vms 触发 reinstall 重试。这是软失败设计，与
// PLAN-036 默认防火墙软失败一致。
func applyWindowsCloudInit(ctx context.Context, client *cluster.Client, project, name, ip, subnetCIDR, gateway, password string) {
	// Step 1: 等 agent 在线（最长 5 min，每 10s 探）。
	if !waitWindowsAgent(ctx, client, project, name, 30, 10*time.Second) {
		slog.Error("windows cloud-init: agent never came online; user can reinstall", "vm", name)
		return
	}

	// Step 1.5: agent online 不等于 NetSetup / Windows 网络栈完全 ready。
	// 实测 antifob image 在 agent 上线后立刻发 New-NetIPAddress，IP 不会持久化
	// （Windows 还在 OOBE 收尾，会被自动 reset 回 APIPA）。睡 60s 让 Windows
	// 走完 OOBE 收尾 + DHCP 客户端兜底。
	select {
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
	}

	// Step 2: 发 PowerShell exec
	prefix := subnetCIDR
	if i := strings.LastIndex(subnetCIDR, "/"); i >= 0 {
		prefix = subnetCIDR[i+1:]
	}
	// PowerShell 单 here-string，依次：清旧 IP → 新静态 IP → DNS →
	// 启用 RDP service + reg + firewall → 改 Administrator 密码（admin 用户名兜底）。
	// OPS-051 测试发现：Windows OOBE 收尾后会重置 New-NetIPAddress 设的静态 IP
	// → APIPA 169.254.x → 外网 RDP 不通。把 watchdog 脚本写到磁盘 +
	// scheduled task 每分钟 reapply IP 直到目标 IP 稳定（1 小时窗口）。
	// 用 base64 encoded command 避开 quoting 嵌套问题。
	// watchdogPS 是 OOBE 后周期性自愈逻辑（不依赖外部参数，全部 inline）。
	watchdogPS := fmt.Sprintf(`$ok = Get-NetIPAddress -InterfaceAlias 'Ethernet' -IPAddress '%s' -ErrorAction SilentlyContinue
$route = Get-NetRoute -InterfaceAlias 'Ethernet' -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -eq '%s' }
if (-not $ok -or -not $route) {
  Get-NetIPAddress -InterfaceAlias 'Ethernet' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue
  Get-NetRoute -InterfaceAlias 'Ethernet' -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
  New-NetIPAddress -InterfaceAlias 'Ethernet' -IPAddress '%s' -PrefixLength '%s' -ErrorAction SilentlyContinue | Out-Null
  New-NetRoute -InterfaceAlias 'Ethernet' -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -NextHop '%s' -ErrorAction SilentlyContinue | Out-Null
  Set-DnsClientServerAddress -InterfaceAlias 'Ethernet' -ServerAddresses 1.1.1.1,8.8.8.8 -ErrorAction SilentlyContinue
}`, ip, gateway, ip, prefix, gateway)

	// UTF-16LE + base64 = PowerShell -EncodedCommand 标准格式
	watchdogB64 := base64.StdEncoding.EncodeToString(utf16LE(watchdogPS))

	ps := fmt.Sprintf(`$ErrorActionPreference='Continue';
$nic='Ethernet';
Get-NetIPAddress -InterfaceAlias $nic -AddressFamily IPv4 -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue;
Get-NetRoute -InterfaceAlias $nic -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue;
New-NetIPAddress -InterfaceAlias $nic -IPAddress %s -PrefixLength %s -ErrorAction Continue | Out-Null;
New-NetRoute -InterfaceAlias $nic -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -NextHop %s -ErrorAction Continue | Out-Null;
Set-DnsClientServerAddress -InterfaceAlias $nic -ServerAddresses 1.1.1.1,8.8.8.8 -ErrorAction Continue;
Set-ItemProperty -Path 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 0 -ErrorAction Continue;
Enable-NetFirewallRule -DisplayGroup 'Remote Desktop' -ErrorAction Continue;
Set-Service -Name TermService -StartupType Automatic -ErrorAction Continue;
Start-Service -Name TermService -ErrorAction Continue;
$pw = ConvertTo-SecureString '%s' -AsPlainText -Force;
Set-LocalUser -Name Administrator -Password $pw -ErrorAction SilentlyContinue;
Set-LocalUser -Name admin -Password $pw -ErrorAction SilentlyContinue;
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '-NoProfile -WindowStyle Hidden -EncodedCommand %s';
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddSeconds(30) -RepetitionInterval (New-TimeSpan -Minutes 1) -RepetitionDuration (New-TimeSpan -Hours 1);
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest;
Register-ScheduledTask -TaskName 'ops051-ip-watchdog' -Action $action -Trigger $trigger -Principal $principal -Force -ErrorAction SilentlyContinue | Out-Null;
Write-Output 'incus-admin: windows cloud-init OK + ip watchdog scheduled'
`, ip, prefix, gateway, password, watchdogB64)
	body := map[string]any{
		"command":            []string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps},
		"wait-for-websocket": false,
		"interactive":        false,
		"width":              80,
		"height":             25,
	}
	// OPS-051 测试发现：PS 多行 here-string 经 incus exec 传入时含 `$false`
	// 等被某层 shell/JSON 转义破坏 → New-NetIPAddress 静默失败 →
	// 网络永远是 APIPA。改用 -EncodedCommand base64 UTF-16LE，绕开所有
	// quoting 问题。
	psEncoded := base64.StdEncoding.EncodeToString(utf16LE(ps))
	body["command"] = []string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", psEncoded}
	bodyJSON, _ := json.Marshal(body)
	resp, err := client.APIPost(ctx, fmt.Sprintf("/1.0/instances/%s/exec?project=%s", name, project), bytes.NewReader(bodyJSON))
	if err != nil {
		slog.Error("windows cloud-init exec failed", "vm", name, "error", err)
		return
	}
	// Wait for exec operation to finish so PowerShell actually runs before we return.
	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
				slog.Warn("windows cloud-init exec wait failed (cmd may still have run)", "vm", name, "error", werr)
			}
		}
	}
	slog.Info("windows cloud-init applied", "vm", name, "ip", ip)
}

// waitWindowsAgent 轮询 incus instance state 等 incus-agent 上线（Processes != -1）。
// 每次间隔 wait，最多 attempts 次。返回 true=agent 就绪 / false=超时。
func waitWindowsAgent(ctx context.Context, client *cluster.Client, project, name string, attempts int, wait time.Duration) bool {
	type stateResp struct {
		Processes int `json:"processes"`
	}
	for i := 0; i < attempts; i++ {
		resp, err := client.APIGet(ctx, fmt.Sprintf("/1.0/instances/%s/state?project=%s", name, project))
		if err == nil && resp != nil {
			var s stateResp
			if jerr := json.Unmarshal(resp.Metadata, &s); jerr == nil && s.Processes > 0 {
				slog.Info("windows agent online", "vm", name, "attempt", i+1, "processes", s.Processes)
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
	return false
}
