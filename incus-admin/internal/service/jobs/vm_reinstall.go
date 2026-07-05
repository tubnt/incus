package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/service"
)

// vmReinstallExecutor 执行一次 vm.reinstall 的步骤。前置假设（handler 已同步）：
//   - probe + prePullImage 通过 —— 镜像服务可达且镜像已落本地缓存
//     （这一步必须保留 sync 在 handler 里以保护用户原有 VM 数据）
type vmReinstallExecutor struct{}

const (
	stepFetchInstance = "fetch_instance"
	stepStop          = "stop"
	stepDelete        = "delete"
	stepRecreate      = "recreate"
	stepWaitRecreate  = "wait_recreate"
	stepStartReinstall = "start_after_reinstall"
)

func (e *vmReinstallExecutor) Run(ctx context.Context, rt *Runtime, job *model.ProvisioningJob) error {
	params := rt.peekParams(job.ID)
	if params == nil {
		return fmt.Errorf("params missing for job %d", job.ID)
	}

	clusterName := rt.clusterName(job.ClusterID)
	client, ok := rt.deps.Clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not registered", clusterName)
	}

	// step 0: 拉原 instance config（保留 devices 配置，避免重装后 IP / 磁盘消失）
	rt.step(ctx, job.ID, 0, stepFetchInstance, model.StepStatusRunning, "读取原实例配置")
	instData, err := client.GetInstance(ctx, params.Project, job.TargetName)
	if err != nil {
		rt.finishStep(ctx, job.ID, 0, stepFetchInstance, model.StepStatusFailed, err.Error())
		return fmt.Errorf("get instance: %w", err)
	}
	var inst struct {
		Config   map[string]string         `json:"config"`
		Devices  map[string]map[string]any `json:"devices"`
		Location string                    `json:"location"`
	}
	if err := json.Unmarshal(instData, &inst); err != nil {
		rt.finishStep(ctx, job.ID, 0, stepFetchInstance, model.StepStatusFailed, err.Error())
		return fmt.Errorf("parse instance: %w", err)
	}
	rt.finishStep(ctx, job.ID, 0, stepFetchInstance, model.StepStatusSucceeded, "")

	// step 1: stop（best-effort，已 stopped 不报错）
	// OPS-051 测试发现：原版只发 stop PUT 不等 async op 完成，下一步 delete
	// 立刻撞 "Instance is running" race。这里同步等 stop op 完成（force=true
	// + timeout=30，最坏 30s），失败转 skipped 不阻塞重装链。
	rt.step(ctx, job.ID, 1, stepStop, model.StepStatusRunning, "停止当前实例")
	stopBody, _ := json.Marshal(map[string]any{"action": "stop", "timeout": 30, "force": true})
	stopResp, stopErr := client.APIPut(ctx, fmt.Sprintf("/1.0/instances/%s/state?project=%s", job.TargetName, params.Project), bytes.NewReader(stopBody))
	if stopErr != nil {
		rt.finishStep(ctx, job.ID, 1, stepStop, model.StepStatusSkipped, stopErr.Error())
	} else {
		if stopResp != nil && stopResp.Type == "async" {
			var op struct{ ID string }
			if uerr := json.Unmarshal(stopResp.Metadata, &op); uerr == nil && op.ID != "" {
				if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
					rt.finishStep(ctx, job.ID, 1, stepStop, model.StepStatusWarning, "wait stop op: "+werr.Error())
					goto afterStop
				}
			}
		}
		rt.finishStep(ctx, job.ID, 1, stepStop, model.StepStatusSucceeded, "")
	}
afterStop:

	// step 2: delete + 等删完
	rt.step(ctx, job.ID, 2, stepDelete, model.StepStatusRunning, "删除原实例")
	delResp, err := client.APIDelete(ctx, fmt.Sprintf("/1.0/instances/%s?project=%s", job.TargetName, params.Project))
	if err != nil {
		rt.finishStep(ctx, job.ID, 2, stepDelete, model.StepStatusFailed, err.Error())
		return fmt.Errorf("delete instance: %w", err)
	}
	if delResp != nil && delResp.Type == "async" {
		var op struct{ ID string }
		// Session-2 F-37 / PLAN-051 §2-E：原版用 `_ = json.Unmarshal(...)` 吞错，
		// 解析失败导致 op.ID 为空 → 跳过 WaitForOperation → recreate 撞 "instance
		// still exists" 用户看到 cryptic error。明确判 err 后视为失败。
		if uerr := json.Unmarshal(delResp.Metadata, &op); uerr != nil {
			rt.finishStep(ctx, job.ID, 2, stepDelete, model.StepStatusFailed, uerr.Error())
			return fmt.Errorf("parse delete async metadata: %w", uerr)
		}
		if op.ID == "" {
			rt.finishStep(ctx, job.ID, 2, stepDelete, model.StepStatusFailed, "incus async response missing operation id")
			return fmt.Errorf("delete: incus async response missing operation id")
		}
		if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
			rt.finishStep(ctx, job.ID, 2, stepDelete, model.StepStatusFailed, werr.Error())
			return fmt.Errorf("wait delete: %w", werr)
		}
	}
	rt.finishStep(ctx, job.ID, 2, stepDelete, model.StepStatusSucceeded, "")

	// step 3: recreate（生成新密码 / cloud-init）
	password := service.GeneratePassword()
	// OPS-051 / PLAN-052：与 vm_create 一致 —— OS-aware + 统一 root +
	// apt proxy + 合并 cloud_init_template。
	loginUser := rt.deps.DefaultLoginUser
	if loginUser == "" {
		loginUser = "root"
	}
	extraYAML := ""
	if rt.deps.OSTemplates != nil {
		if tpl, terr := rt.deps.OSTemplates.GetBySource(ctx, params.ImageSource); terr == nil && tpl != nil {
			extraYAML = tpl.CloudInitTemplate
			if tpl.DefaultUser != "" {
				loginUser = tpl.DefaultUser
			}
		}
	}
	cloudInit := service.BuildCloudInit(service.CloudInitInput{
		OSFamily:    service.ClassifyOSFamily(params.ImageSource),
		LoginUser:   loginUser,
		Password:    password,
		SSHKeys:     nil, // 重装路径不带 ssh_keys（admin 重装统一）
		AptProxyURL: rt.deps.AptProxyURL,
		ExtraYAML:   extraYAML,
	})
	service.StripVolatileConfig(inst.Config)
	// Session-2 F-05 / PLAN-051 §2-E：与 vm_create 同步—— 用 Incus 标准
	// cloud-init key，legacy `user.cloud-init` 不被识别。
	delete(inst.Config, "user.cloud-init")
	inst.Config["cloud-init.user-data"] = cloudInit
	// OPS-051 测试发现：老 VM 的 cloud-init.network-config 是 v2 格式，
	// 在 RHEL 系 NetworkManager 后端兼容性差（enp5s0 拿不到 IPv4）。
	// 重装时从老 v2 配置正则提取 ip/cidr/gateway，调 BuildNetworkConfig 重生
	// v1 格式（所有 distro 通用）。解析失败保留原样不阻塞重装链。
	if nc, ok := inst.Config["cloud-init.network-config"]; ok {
		if v1cfg := convertV2NetworkConfigToV1(nc); v1cfg != "" {
			inst.Config["cloud-init.network-config"] = v1cfg
		}
	}

	body := map[string]any{
		"name": job.TargetName,
		"type": "virtual-machine",
		"source": map[string]any{
			"type":     "image",
			"alias":    params.ImageSource,
			"server":   params.ServerURL,
			"protocol": params.Protocol,
		},
		"config":  inst.Config,
		"devices": inst.Devices,
	}
	bodyJSON, _ := json.Marshal(body)

	rt.step(ctx, job.ID, 3, stepRecreate, model.StepStatusRunning, "重建实例")
	createPath := fmt.Sprintf("/1.0/instances?project=%s&target=%s", params.Project, inst.Location)
	resp, err := client.APIPost(ctx, createPath, bytes.NewReader(bodyJSON))
	if err != nil {
		rt.finishStep(ctx, job.ID, 3, stepRecreate, model.StepStatusFailed, err.Error())
		return fmt.Errorf("recreate: %w", err)
	}
	rt.finishStep(ctx, job.ID, 3, stepRecreate, model.StepStatusSucceeded, "")

	if resp.Type == "async" {
		var op struct{ ID string }
		// Session-2 F-37
		if uerr := json.Unmarshal(resp.Metadata, &op); uerr != nil {
			rt.finishStep(ctx, job.ID, 4, stepWaitRecreate, model.StepStatusFailed, uerr.Error())
			return fmt.Errorf("parse recreate async metadata: %w", uerr)
		}
		if op.ID == "" {
			rt.finishStep(ctx, job.ID, 4, stepWaitRecreate, model.StepStatusFailed, "incus async response missing operation id")
			return fmt.Errorf("recreate: incus async response missing operation id")
		}
		rt.step(ctx, job.ID, 4, stepWaitRecreate, model.StepStatusRunning, "等待镜像与磁盘就绪")
		if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
			rt.finishStep(ctx, job.ID, 4, stepWaitRecreate, model.StepStatusFailed, werr.Error())
			return fmt.Errorf("wait recreate: %w", werr)
		}
		rt.finishStep(ctx, job.ID, 4, stepWaitRecreate, model.StepStatusSucceeded, "")
	}

	// step 5: start
	rt.step(ctx, job.ID, 5, stepStartReinstall, model.StepStatusRunning, "启动新实例")
	startBody, _ := json.Marshal(map[string]any{"action": "start", "timeout": 60})
	startResp, err := client.APIPut(ctx, fmt.Sprintf("/1.0/instances/%s/state?project=%s", job.TargetName, params.Project), bytes.NewReader(startBody))
	if err != nil {
		rt.finishStep(ctx, job.ID, 5, stepStartReinstall, model.StepStatusFailed, err.Error())
		return fmt.Errorf("start after reinstall: %w", err)
	}
	if startResp.Type == "async" {
		var op struct{ ID string }
		// Session-2 F-37
		if uerr := json.Unmarshal(startResp.Metadata, &op); uerr != nil {
			rt.finishStep(ctx, job.ID, 5, stepStartReinstall, model.StepStatusFailed, uerr.Error())
			return fmt.Errorf("parse start async metadata: %w", uerr)
		}
		if op.ID == "" {
			rt.finishStep(ctx, job.ID, 5, stepStartReinstall, model.StepStatusFailed, "incus async response missing operation id")
			return fmt.Errorf("start: incus async response missing operation id")
		}
		if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
			rt.finishStep(ctx, job.ID, 5, stepStartReinstall, model.StepStatusFailed, werr.Error())
			return fmt.Errorf("wait start after reinstall: %w", werr)
		}
	}
	rt.finishStep(ctx, job.ID, 5, stepStartReinstall, model.StepStatusSucceeded, "")

	// OPS-051 / PLAN-052 step 6 wait_cloud_init + step 7 verify_ready
	// （仅 Linux；Windows reinstall 路径暂未启用，与 vm_create 对齐）
	rt.step(ctx, job.ID, 6, stepWaitCloudInit, model.StepStatusRunning, "等待 cloud-init 完成（重新安装 SSH 服务）")
	ciCtx, ciCancel := context.WithTimeout(ctx, 5*time.Minute)
	ciRet, ciErr := client.ExecNonInteractive(ciCtx, params.Project, job.TargetName,
		[]string{"cloud-init", "status", "--wait"})
	ciCancel()
	switch {
	case ciErr != nil:
		rt.finishStep(ctx, job.ID, 6, stepWaitCloudInit, model.StepStatusWarning,
			fmt.Sprintf("cloud-init exec 失败 (err=%v)；重装已完成，请稍后重试 SSH", ciErr))
	case ciRet != 0:
		rt.finishStep(ctx, job.ID, 6, stepWaitCloudInit, model.StepStatusWarning,
			fmt.Sprintf("cloud-init 退出码 %d；重装已完成，请等 1-2 分钟后重试 SSH", ciRet))
	default:
		rt.finishStep(ctx, job.ID, 6, stepWaitCloudInit, model.StepStatusSucceeded, "")
	}

	rt.step(ctx, job.ID, 7, stepVerifyReady, model.StepStatusRunning, "验证 SSH/22 端口监听")
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	verifyRet, verifyErr := client.ExecNonInteractive(verifyCtx, params.Project, job.TargetName,
		[]string{"sh", "-c", "ss -ltn | grep -qE ':22[[:space:]]'"})
	verifyCancel()
	switch {
	case verifyErr != nil:
		rt.finishStep(ctx, job.ID, 7, stepVerifyReady, model.StepStatusWarning,
			fmt.Sprintf("SSH 探活 exec 失败 (err=%v)；重装已完成，请稍后试", verifyErr))
	case verifyRet != 0:
		rt.finishStep(ctx, job.ID, 7, stepVerifyReady, model.StepStatusWarning,
			fmt.Sprintf("SSH/22 端口尚未监听 (exit=%d)；重装已完成，请等 cloud-init 完成", verifyRet))
	default:
		rt.finishStep(ctx, job.ID, 7, stepVerifyReady, model.StepStatusSucceeded, "")
	}

	// 写新密码（reinstall 用户最关心的产物）
	if job.VMID != nil {
		_ = rt.deps.VMs.UpdatePassword(ctx, *job.VMID, password)
	}

	// pma-cr H-3 / Session-2 F-39：take 出来的 params 必须显式 Wipe，不依赖
	// runOne defer（runOne defer 的 takeParams 在这里返 nil，本地变量永不 Wipe）
	if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
	return nil
}

// Reinstall 失败的 rollback 比 vm.create 弱：原 VM 已经被删除，无法"恢复"。
// 我们能做的：vm row 标 error，让用户走客服 / admin 介入。不退款（reinstall 不走订单）。
//
// OPS-008/012/016 数据保护已经把"上游挂掉但没拉到镜像"挡在 handler 同步阶段了，
// 走到这里失败的概率应该很低；真发生时是 VM 状态破坏，DB 标 error 配合 audit。
func (e *vmReinstallExecutor) Rollback(ctx context.Context, rt *Runtime, job *model.ProvisioningJob, reason string) {
	if job.VMID != nil {
		_ = rt.deps.VMs.UpdateStatus(ctx, *job.VMID, model.VMStatusError)
	}
	// pma-cr H-3：rollback 路径同样显式 Wipe
	if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
}

// convertV2NetworkConfigToV1 把老 VM 存的 cloud-init network-config v2
// 转成 v1（OPS-051：RHEL 系 NM 后端 v2 兼容性差）。简单正则提取
// addresses / via，不解析 yaml.Node。提取失败返回 "" 让调用方保留原值。
func convertV2NetworkConfigToV1(v2 string) string {
	if !strings.Contains(v2, "version: 2") {
		return "" // 不是 v2 或已经 v1，原样保留
	}
	// 匹配 "- A.B.C.D/N" addresses 第一条 + "via A.B.C.D" gateway
	ipRe := regexp.MustCompile(`addresses:\s*\n\s*-\s*([0-9.]+/\d+)`)
	gwRe := regexp.MustCompile(`via:\s*([0-9.]+)`)
	ipMatch := ipRe.FindStringSubmatch(v2)
	gwMatch := gwRe.FindStringSubmatch(v2)
	if len(ipMatch) < 2 || len(gwMatch) < 2 {
		return ""
	}
	addr := ipMatch[1]
	gw := gwMatch[1]
	return fmt.Sprintf(`version: 1
config:
  - type: physical
    name: enp5s0
    subnets:
      - type: static
        address: %s
        gateway: %s
        dns_nameservers:
          - 1.1.1.1
          - 8.8.8.8`, addr, gw)
}
