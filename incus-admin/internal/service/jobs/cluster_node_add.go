package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/sshexec"
)

// clusterNodeAddExecutor 编排"添加节点到 Incus + Ceph 集群"的全流程：
//
//	step 0  leader_token       通过 Incus API 调 POST /1.0/cluster/members 拿 join token
//	step 1  upload_scripts     SSH 到新节点 mkdir + tee 上传 join-node.sh + 依赖
//	step 2  preflight          远程跑 join-node.sh 流式输出，按 "====== 步骤 N/7" marker 推进
//	step 3  install_packages
//	step 4  network_config
//	step 5  join_incus
//	step 6  join_ceph
//	step 7  firewall
//	step 8  verify
//
// 失败语义：任一 step 抛错 → step.failed + return；runtime 自动调 Rollback。
// Rollback 仅做 best-effort：调 Incus API 把可能 join 的 member 移除（force），
// 远端 OS 改动（apt 装包、netplan 应用）不回滚（数据保护考虑，留人工介入）。
type clusterNodeAddExecutor struct{}

const (
	stepLeaderToken     = "leader_token"
	stepUploadScripts   = "upload_scripts"
	stepRemotePreflight = "preflight"
	stepRemoteInstall   = "install_packages"
	stepRemoteNetwork   = "network_config"
	stepRemoteJoinIncus = "join_incus"
	stepRemoteJoinCeph  = "join_ceph"
	stepRemoteFirewall  = "firewall"
	stepRemoteVerify    = "verify"
)

// joinNodeStepRe 匹配 join-node.sh 输出里的 `====== 步骤 N/7: 描述 ======` 段落 marker。
// N=1..7 → 推进对应 step。任意 [ERROR] 行 → 当前 step 失败。
var joinNodeStepRe = regexp.MustCompile(`====== 步骤 (\d)/7: (.+?) ======`)

// addNodeStepBySection 把 join-node.sh 的 7 个段落映射到 jobs runner 的 seq 2..8。
func addNodeStepBySection(section int) (int, string) {
	switch section {
	case 1:
		return 2, stepRemotePreflight
	case 2:
		return 3, stepRemoteInstall
	case 3:
		return 4, stepRemoteNetwork
	case 4:
		return 5, stepRemoteJoinIncus
	case 5:
		return 6, stepRemoteJoinCeph
	case 6:
		return 7, stepRemoteFirewall
	case 7:
		return 8, stepRemoteVerify
	}
	return -1, ""
}

func (e *clusterNodeAddExecutor) Run(ctx context.Context, rt *Runtime, job *model.ProvisioningJob) error {
	params := rt.peekParams(job.ID)
	if params == nil {
		return fmt.Errorf("params missing for job %d", job.ID)
	}
	if params.NodeName == "" || params.NodePublicIP == "" {
		return fmt.Errorf("NodeName / NodePublicIP required")
	}
	sshUser := params.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}
	scriptDir := params.ScriptDir
	if scriptDir == "" {
		scriptDir = "/tmp/incus-admin-cluster-add"
	}

	clusterName := rt.clusterName(job.ClusterID)
	client, ok := rt.deps.Clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not registered", clusterName)
	}

	// step 0：leader_token
	rt.step(ctx, job.ID, 0, stepLeaderToken, model.StepStatusRunning, "通过 Incus API 生成集群 join token")
	token, err := requestJoinToken(ctx, client, params.NodeName)
	if err != nil {
		rt.finishStep(ctx, job.ID, 0, stepLeaderToken, model.StepStatusFailed, err.Error())
		return fmt.Errorf("generate join token: %w", err)
	}
	params.IncusToken = token
	rt.finishStep(ctx, job.ID, 0, stepLeaderToken, model.StepStatusSucceeded, "")

	// step 1：upload_scripts
	rt.step(ctx, job.ID, 1, stepUploadScripts, model.StepStatusRunning, fmt.Sprintf("上传脚本到 %s:%s", params.NodePublicIP, scriptDir))
	if err := uploadEmbeddedScripts(ctx, params.NodePublicIP, sshUser, params.Credential, params.SSHKeyFile, params.KnownHostsFile, scriptDir); err != nil {
		rt.finishStep(ctx, job.ID, 1, stepUploadScripts, model.StepStatusFailed, err.Error())
		return fmt.Errorf("upload scripts: %w", err)
	}
	rt.finishStep(ctx, job.ID, 1, stepUploadScripts, model.StepStatusSucceeded, "")

	// step 2..8：远程跑 join-node.sh，流式解析 marker 推进 step
	runner := newRunnerForParams(params.NodePublicIP, sshUser, params).WithKnownHosts(params.KnownHostsFile)
	cmdParts := []string{
		"bash", shellQuote(scriptDir + "/scripts/join-node.sh"),
		"--name", shellQuote(params.NodeName),
		"--pub-ip", shellQuote(params.NodePublicIP),
		"--incus-token", shellQuote(token),
	}
	// OPS-026 / PLAN-028：拓扑覆盖
	if params.NICPrimary != "" {
		cmdParts = append(cmdParts, "--nic-primary", shellQuote(params.NICPrimary))
	}
	if params.NICCluster != "" {
		cmdParts = append(cmdParts, "--nic-cluster", shellQuote(params.NICCluster))
	}
	if params.BridgeName != "" {
		cmdParts = append(cmdParts, "--bridge-name", shellQuote(params.BridgeName))
	}
	if params.MgmtIP != "" {
		cmdParts = append(cmdParts, "--mgmt-ip", shellQuote(params.MgmtIP))
	}
	if params.CephPubIP != "" {
		cmdParts = append(cmdParts, "--ceph-pub-ip", shellQuote(params.CephPubIP))
	}
	if params.CephClusterIP != "" {
		cmdParts = append(cmdParts, "--ceph-cluster-ip", shellQuote(params.CephClusterIP))
	}
	if params.SkipNetwork {
		cmdParts = append(cmdParts, "--skip-network")
	}
	cmd := strings.Join(cmdParts, " ")

	var (
		mu              sync.Mutex
		currentSeq      = -1
		currentName     = ""
		lastError      string
		stepFailedSent bool
		// Session-2 F-33 / PLAN-051 §2-G：原版每行 stdout 同步 BEGIN/UPDATE/COMMIT，
		// apt-install / image pull 输出可达数千行 → 5 并发节点加入即耗尽 PG 连接池。
		// 改：mu 内只更新 latestLine，500ms tick 把 latest 一次写到 DB；
		// 失败时立刻 flush（不依赖 ticker）。
		latestLine string
		dirty      bool
	)
	finishCurrent := func(status, detail string) {
		if currentSeq < 0 {
			return
		}
		rt.finishStep(ctx, job.ID, currentSeq, currentName, status, detail)
	}

	// P0-2：flush goroutine 用显式 stop chan + select 退出。
	// 原版 `for range flushTicker.C` 依赖 Stop() 关闭 channel —— 但 time.Ticker.Stop()
	// 只停止投递，不 close C，range 会永久阻塞等下一 tick，goroutine 卡死，主流程
	// `<-tickerDone` 随之死锁。改为 select{stopFlush | C}，close(stopFlush) 令其干净退出。
	flushTicker := time.NewTicker(500 * time.Millisecond)
	stopFlush := make(chan struct{})
	tickerDone := make(chan struct{})
	var stopFlushOnce sync.Once
	// stopFlushLoop 幂等：停 ticker、close(stopFlush) 通知 goroutine 退出并等其结束。
	// 显式在 RunStream 之后、写终态 step 之前调（避免 flush 与终态并发写同一 step）；
	// defer 兜底 panic 回卷路径，避免 goroutine 泄漏。close 用 Once 防重复 close panic，
	// <-tickerDone 对已关闭 chan 可重复接收，二次调用安全。
	stopFlushLoop := func() {
		flushTicker.Stop()
		stopFlushOnce.Do(func() { close(stopFlush) })
		<-tickerDone
	}
	defer stopFlushLoop()
	go func() {
		defer close(tickerDone)
		for {
			select {
			case <-stopFlush:
				return
			case <-flushTicker.C:
				mu.Lock()
				seq := currentSeq
				name := currentName
				line := latestLine
				d := dirty
				dirty = false
				mu.Unlock()
				if d && seq >= 0 {
					// safeRun 兜住 UpdateStep 的意外 panic，避免 flush goroutine 静默死掉。
					safeRun("cluster-node-add-flush", func() {
						_ = rt.deps.Jobs.UpdateStep(ctx, job.ID, seq, model.StepStatusRunning, line)
					})
					_ = name
				}
			}
		}
	}()

	onLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		// 段落 marker → 收前一段为 succeeded，开新段为 running
		if m := joinNodeStepRe.FindStringSubmatch(line); m != nil {
			finishCurrent(model.StepStatusSucceeded, "")
			seq, name := addNodeStepBySection(parseSection(m[1]))
			if seq < 0 {
				slog.Warn("unexpected join-node section", "section", m[1])
				return
			}
			currentSeq, currentName = seq, name
			latestLine = ""
			dirty = false
			rt.step(ctx, job.ID, seq, name, model.StepStatusRunning, strings.TrimSpace(m[2]))
			return
		}
		// [ERROR] 行：暂存最后一条；最终 exit code 非零时作为 detail
		if strings.Contains(line, "[ERROR]") {
			lastError = line
		}
		// 把最后一行 buffer 到 latestLine；500ms tick 后台 flush（避免高频 DB 写）
		if currentSeq >= 0 {
			truncated := line
			if len(truncated) > 160 {
				truncated = truncated[:160] + "…"
			}
			latestLine = truncated
			dirty = true
		}
	}

	streamErr := runner.RunStream(ctx, cmd, onLine)
	// 先干净停掉 flush goroutine，再写终态 step（defer 会再调一次，幂等无副作用）。
	stopFlushLoop()

	mu.Lock()
	defer mu.Unlock()
	if streamErr != nil {
		if !stepFailedSent {
			detail := lastError
			if detail == "" {
				detail = streamErr.Error()
			}
			finishCurrent(model.StepStatusFailed, detail)
		}
		return fmt.Errorf("remote join-node.sh: %w", streamErr)
	}
	finishCurrent(model.StepStatusSucceeded, "")

	taken := rt.takeParams(job.ID)
	if taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
	return nil
}

// Rollback：尝试把可能已 join 的 member 从 Incus 集群里 force-remove。
// 远端 OS 修改（装包/netplan）不动 —— admin 决定是否手工清盘后再加回来。
func (e *clusterNodeAddExecutor) Rollback(ctx context.Context, rt *Runtime, job *model.ProvisioningJob, reason string) {
	params := rt.peekParams(job.ID)
	if params == nil {
		return
	}
	clusterName := rt.clusterName(job.ClusterID)
	client, ok := rt.deps.Clusters.Get(clusterName)
	if !ok {
		return
	}

	// 仅当节点确实存在于 Incus 时尝试移除（避免 token 阶段就失败时也尝试 remove）
	resp, err := client.APIGet(ctx, fmt.Sprintf("/1.0/cluster/members/%s", params.NodeName))
	if err != nil || resp == nil {
		if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
			taken.Credential.Wipe()
		}
		return
	}

	if _, derr := client.APIDelete(ctx, fmt.Sprintf("/1.0/cluster/members/%s?force=1", params.NodeName)); derr != nil {
		slog.Warn("rollback remove cluster member failed", "job_id", job.ID, "node", params.NodeName, "error", derr)
	}
	if taken := rt.takeParams(job.ID); taken != nil && taken.Credential != nil {
		taken.Credential.Wipe()
	}
}

// requestJoinToken 调 Incus API 拿 cluster join token，等价于 `incus cluster add <name>`。
//
// 响应形态（Incus 6.x）：sync 包，metadata 含 `addresses`, `fingerprint`,
// `secret` 等；其中 `secret` 字段已是 base64 token；前端 incus CLI 把
// metadata 整体序列化成 token 字符串再 base64。我们直接重新构造 token：
//
//	{"server_name":"X","fingerprint":"...","addresses":["..."],"secret":"..."}
//	→ base64 编码即 join token
//
// 这与 incus CLI 的 GenerateJoinToken 行为一致。
func requestJoinToken(ctx context.Context, client *cluster.Client, nodeName string) (string, error) {
	body, _ := json.Marshal(map[string]any{"server_name": nodeName})
	resp, err := client.APIPost(ctx, "/1.0/cluster/members", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if resp.Type == "async" {
		// Session-2 F-34 / PLAN-051 §2-G：原版直接 return "not implemented"，
		// Incus 任何小版本变到 async 响应都会让 admin 看到 cryptic 错误。
		// 改：等 op 完成 → 拉 op metadata 提取 token；同样是 GenerateJoinToken
		// 的 sync 模式 metadata 形态。
		var op struct{ ID string }
		if uerr := json.Unmarshal(resp.Metadata, &op); uerr != nil {
			return "", fmt.Errorf("parse async metadata: %w", uerr)
		}
		if op.ID == "" {
			return "", fmt.Errorf("incus async response missing operation id")
		}
		if werr := client.WaitForOperation(ctx, op.ID); werr != nil {
			return "", fmt.Errorf("wait token operation: %w", werr)
		}
		// 拉完整 op 取 metadata（含 server_name/fingerprint/addresses/secret）
		opResp, gerr := client.APIGet(ctx, fmt.Sprintf("/1.0/operations/%s", url.PathEscape(op.ID)))
		if gerr != nil {
			return "", fmt.Errorf("get token operation metadata: %w", gerr)
		}
		// op metadata 嵌在 resources 与 metadata 字段；Incus 6.x 把 token 字段
		// 放 operation.metadata 里。这里直接复用下方解析逻辑（resp 重新指向 op）
		resp = opResp
	}

	var tok struct {
		ServerName  string   `json:"server_name"`
		Fingerprint string   `json:"fingerprint"`
		Addresses   []string `json:"addresses"`
		Secret      string   `json:"secret"`
		ExpiresAt   string   `json:"expires_at"`
	}
	if err := json.Unmarshal(resp.Metadata, &tok); err != nil {
		return "", fmt.Errorf("parse token metadata: %w", err)
	}
	if tok.Secret == "" {
		return "", fmt.Errorf("incus token response missing secret")
	}

	// Re-encode as the token string Incus admin init expects
	encoded, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	return base64Encode(encoded), nil
}

// uploadEmbeddedScripts 把 sshexec.embedded/ 全部 .sh + cluster-env.sh 上传到
// 远端 scriptDir 下 scripts/ 与 configs/ 子目录，保持 join-node.sh 内
// `${SCRIPT_DIR}/../configs/cluster-env.sh` 的相对路径假设。
//
// PLAN-033：cred 优先于 keyFile（兼容旧调用方传 nil cred + keyFile 路径）。
func uploadEmbeddedScripts(ctx context.Context, host, user string, cred *sshexec.Credential, keyFile, knownHosts, scriptDir string) error {
	runner := newRunnerForParts(host, user, cred, keyFile).WithKnownHosts(knownHosts)
	if err := runner.MkdirAll(ctx, scriptDir+"/scripts"); err != nil {
		return err
	}
	if err := runner.MkdirAll(ctx, scriptDir+"/configs"); err != nil {
		return err
	}

	root := sshexec.EmbeddedScripts()
	return fs.WalkDir(root, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		// p 形如 "scripts/join-node.sh" 或 "configs/cluster-env.sh"
		remotePath := scriptDir + "/" + p
		mode := uint32(0o755)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		} else {
			mode = 0o644
		}
		return runner.WriteFile(ctx, remotePath, data, mode2fmode(mode))
	})
}

// newRunnerForParams returns a Runner using Credential when supplied,
// falling back to the legacy SSHKeyFile path otherwise.
func newRunnerForParams(host, user string, p *Params) *sshexec.Runner {
	return newRunnerForParts(host, user, p.Credential, p.SSHKeyFile)
}

func newRunnerForParts(host, user string, cred *sshexec.Credential, keyFile string) *sshexec.Runner {
	if cred != nil {
		return sshexec.NewWithCredential(host, user, *cred)
	}
	return sshexec.New(host, user, keyFile)
}

// 以下是工具函数。

func parseSection(s string) int {
	switch s {
	case "1":
		return 1
	case "2":
		return 2
	case "3":
		return 3
	case "4":
		return 4
	case "5":
		return 5
	case "6":
		return 6
	case "7":
		return 7
	}
	return -1
}

// shellQuote / mode2fmode / base64Encode：保持 cluster_node_add 文件 self-contained，
// 避免引入更多 cross-package 桥接。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func mode2fmode(m uint32) os.FileMode { return os.FileMode(m) }

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
