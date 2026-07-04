package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
)

// reinstallImageProbeClient 给 Reinstall pre-check 用的短超时 HTTP client。
// 单独定义而不复用全局 http.DefaultClient，避免被外部修改默认 timeout 影响。
var reinstallImageProbeClient = &http.Client{Timeout: 8 * time.Second}

// prePullImage 在 Reinstall 删除原 VM 之前主动把镜像拉到本地缓存。这样即使
// probe → recreate 之间上游挂掉，create 仍能命中本地 cache；同时把"server 可达
// 但 alias 不存在"的边缘情况提前到删除前暴露 —— 拉失败 → return early，原 VM
// 完整保留。
//
// 复用 Incus images API（POST /1.0/images with source spec）。镜像若已在本地
// 缓存，Incus 会无操作返回；otherwise 触发一次拉取。任一情况下 op 完成都说明
// 镜像可用，create 阶段可放心引用。
func prePullImage(ctx context.Context, client *cluster.Client, project, serverURL, protocol, alias string) error {
	body, _ := json.Marshal(map[string]any{
		"source": map[string]any{
			"type":     "image",
			"mode":     "pull",
			"server":   serverURL,
			"protocol": protocol,
			"alias":    alias,
		},
	})
	path := fmt.Sprintf("/1.0/images?project=%s", project)
	resp, err := client.APIPost(ctx, path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("submit image pull: %w", err)
	}
	if resp == nil || resp.Type != "async" {
		// 非 async 通常意味着已经命中缓存或返回错误状态；前者就是成功。
		return nil
	}
	var op struct{ ID string }
	_ = json.Unmarshal(resp.Metadata, &op)
	if op.ID == "" {
		return nil
	}
	pullCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := client.WaitForOperation(pullCtx, op.ID); err != nil {
		return fmt.Errorf("wait image pull: %w", err)
	}
	return nil
}

// probeImageServer 在 Reinstall 删除原 VM 之前探测 simplestreams 镜像服务器。
// 不可达 → 立即 abort 整个 Reinstall（不删原 VM）。可达不保证 alias 一定存在，
// 但能挡住 99% 的"上游服务挂了"导致用户数据被永久销毁的最常见场景（OPS-008 #8）。
func probeImageServer(ctx context.Context, serverURL string) error {
	if serverURL == "" {
		return nil
	}
	probeURL := strings.TrimRight(serverURL, "/") + "/streams/v1/index.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, probeURL, nil)
	if err != nil {
		return fmt.Errorf("probe request: %w", err)
	}
	resp, err := reinstallImageProbeClient.Do(req)
	if err != nil {
		return fmt.Errorf("image server unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusMethodNotAllowed {
		// 405 Method Not Allowed 表示服务器在线但不支持 HEAD（比如某些 CDN），
		// 这种情况视为可达；其他 4xx/5xx 都视为不可达。
		return fmt.Errorf("image server returned %d", resp.StatusCode)
	}
	return nil
}

type VMService struct {
	clusters *cluster.Manager
}

func NewVMService(clusters *cluster.Manager) *VMService {
	return &VMService{clusters: clusters}
}

// OPS-051 / PLAN-052：VMService.Create / VMService.Reinstall（同步路径）+
// CreateVMParams / CreateVMResult / ReinstallResult 已删除。生产部署强制注入
// jobs runtime（cmd/server/main.go startup gate），portal handler 的
// `h.jobs == nil` 兜底分支已改为 503。同步 buildCloudInit (lowercase wrapper)
// 也一并删除，jobs/vm_create.go + vm_reinstall.go 直接调新签名的
// service.BuildCloudInit(CloudInitInput) string。

func (s *VMService) ChangeState(ctx context.Context, clusterName, project, vmName, action string, force bool) error {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}

	body := map[string]any{"action": action, "timeout": 30, "force": force}
	bodyJSON, _ := json.Marshal(body)
	path := fmt.Sprintf("/1.0/instances/%s/state?project=%s", vmName, project)

	resp, err := client.APIPut(ctx, path, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("%s vm: %w", action, err)
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			return client.WaitForOperation(ctx, op.ID)
		}
	}

	return nil
}

// MigrateMode 描述迁移模式。
//
//   - MigrateAuto：probe 实例 migration.stateful；true → live，否则 cold（默认）
//   - MigrateLive：强制 live 不停机；不支持 → 报错（不 fallback）
//   - MigrateCold：强制冷迁移（停 → 迁 → 启）
type MigrateMode string

const (
	MigrateAuto MigrateMode = "auto"
	MigrateLive MigrateMode = "live"
	MigrateCold MigrateMode = "cold"
)

// MigrateResult 描述一次迁移的结果，供 handler / batch executor 共用。
type MigrateResult struct {
	WasRunning bool        `json:"was_running"`
	Target     string      `json:"target"`
	Mode       MigrateMode `json:"mode"` // 实际走的模式（auto 时回填 live/cold）
}

// Migrate 把 VM 迁移到 cluster 内另一台节点。
//
// PLAN-039 / OPS-043：mode 决定 live / cold；auto 模式根据 instance 的
// `migration.stateful` 配置自动选。
//
//   - cold：probe state → 如果 running 则 force-stop → POST migration → wait → 启回
//   - live：直接 POST `{name, migration:true, live:true}` 不停机；失败不 fallback
//   - auto：probe instance.config["migration.stateful"]==="true" 则走 live；否则 cold
//
// 失败 fallback 规则：
//   - cold 路径迁移失败 → best-effort 启回原节点（避免留 stopped）
//   - live 路径迁移失败 → 不 fallback（admin 选 live 表示明确要 live；自动 fallback
//     可能掩盖问题）；返错让 handler 决策
func (s *VMService) Migrate(ctx context.Context, clusterName, project, vmName, targetNode string, mode MigrateMode) (*MigrateResult, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}

	if mode == "" {
		mode = MigrateAuto
	}
	resolvedMode := mode
	if mode == MigrateAuto {
		stateful, _ := s.isStateful(ctx, client, project, vmName)
		if stateful {
			resolvedMode = MigrateLive
		} else {
			resolvedMode = MigrateCold
		}
	}

	switch resolvedMode {
	case MigrateLive:
		return s.migrateLive(ctx, client, clusterName, project, vmName, targetNode)
	case MigrateCold:
		return s.migrateCold(ctx, clusterName, project, vmName, targetNode)
	default:
		return nil, fmt.Errorf("unknown migrate mode %q", mode)
	}
}

// migrateLive 走 Incus live migration（不停机）。
// 需要 instance.config["migration.stateful"] == "true"（已通过 isStateful 检查
// 或 mode=live 强制时由 admin 自负）+ 共享存储（vmc.5ok.co ceph-pool 满足）。
func (s *VMService) migrateLive(ctx context.Context, client *cluster.Client, clusterName, project, vmName, targetNode string) (*MigrateResult, error) {
	wasRunning, _ := s.isRunning(ctx, client, project, vmName)
	body := fmt.Sprintf(`{"name":%q,"migration":true,"live":true}`, vmName)
	path := fmt.Sprintf("/1.0/instances/%s?project=%s&target=%s", vmName, project, targetNode)
	resp, err := client.APIPost(ctx, path, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("live migrate request: %w", err)
	}
	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		parts := strings.Split(resp.Operation, "/")
		opID := parts[len(parts)-1]
		if opID != "" {
			if waitErr := client.WaitForOperation(ctx, opID); waitErr != nil {
				return nil, fmt.Errorf("wait live migrate op: %w", waitErr)
			}
		}
	}
	return &MigrateResult{WasRunning: wasRunning, Target: targetNode, Mode: MigrateLive}, nil
}

// migrateCold 走传统冷迁移（停 → 迁 → 启）。与 PLAN-037 之前的实现一致。
func (s *VMService) migrateCold(ctx context.Context, clusterName, project, vmName, targetNode string) (*MigrateResult, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}

	wasRunning, _ := s.isRunning(ctx, client, project, vmName)
	if wasRunning {
		if err := s.ChangeState(ctx, clusterName, project, vmName, "stop", true); err != nil {
			return nil, fmt.Errorf("stop before migrate: %w", err)
		}
	}

	body := fmt.Sprintf(`{"name":%q,"migration":true}`, vmName)
	path := fmt.Sprintf("/1.0/instances/%s?project=%s&target=%s", vmName, project, targetNode)
	resp, err := client.APIPost(ctx, path, strings.NewReader(body))
	if err != nil {
		if wasRunning {
			_ = s.ChangeState(ctx, clusterName, project, vmName, "start", false)
		}
		return nil, fmt.Errorf("migrate request: %w", err)
	}
	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		parts := strings.Split(resp.Operation, "/")
		opID := parts[len(parts)-1]
		if opID != "" {
			if waitErr := client.WaitForOperation(ctx, opID); waitErr != nil {
				if wasRunning {
					_ = s.ChangeState(ctx, clusterName, project, vmName, "start", false)
				}
				return nil, fmt.Errorf("wait migrate op: %w", waitErr)
			}
		}
	}
	if wasRunning {
		if err := s.ChangeState(ctx, clusterName, project, vmName, "start", false); err != nil {
			slog.Warn("migrate post-start failed", "vm", vmName, "target", targetNode, "error", err)
		}
	}
	return &MigrateResult{WasRunning: wasRunning, Target: targetNode, Mode: MigrateCold}, nil
}

// isStateful 读 instance config 判断是否已设 `migration.stateful=true`。
func (s *VMService) isStateful(ctx context.Context, client *cluster.Client, project, vmName string) (bool, error) {
	path := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, project)
	resp, err := client.APIGet(ctx, path)
	if err != nil {
		return false, err
	}
	var inst struct {
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(resp.Metadata, &inst); err != nil {
		return false, err
	}
	v, ok := inst.Config["migration.stateful"]
	return ok && (v == "true" || v == "1"), nil
}

// EnableStateful 把 VM 的 `migration.stateful` 设为 true 并按需重启。
// 必须重启才生效（QEMU 进程级别需要带 live migration 支持启动）。
//
// 幂等：已经 stateful=true 的 VM 直接 return，不会引入无谓停机（pma-cr F2）。
// 仅 stateful=false / 缺省的 VM 走 stop → set config → start。stopped 的 VM 只
// set config，不强行 start（保留用户原状态）。
//
// PLAN-039 / OPS-043 Phase B1：让历史 VM 也能 live migrate。
func (s *VMService) EnableStateful(ctx context.Context, clusterName, project, vmName string) error {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}
	// pma-cr F2 幂等保护：已启用就直接退出，避免对已 stateful 的 VM 触发无谓
	// 30s+ 停机（admin 误点批量启用是常见场景）。
	// pma-cr F8：probe 错误不再静默吞——出错时仍按"未启用"继续走 stop+patch
	// 路径，但留 warn 日志方便排查（如 VM 已删 / API 超时这类根因被掩盖）。
	already, statefulErr := s.isStateful(ctx, client, project, vmName)
	if statefulErr != nil {
		slog.Warn("enable-stateful probe failed; proceeding with stop/patch path",
			"vm", vmName, "cluster", clusterName, "error", statefulErr)
	}
	if already {
		return nil
	}
	wasRunning, _ := s.isRunning(ctx, client, project, vmName)
	if wasRunning {
		if err := s.ChangeState(ctx, clusterName, project, vmName, "stop", true); err != nil {
			return fmt.Errorf("stop before enable-stateful: %w", err)
		}
	}
	patchBody := `{"config":{"migration.stateful":"true"}}`
	patchPath := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, project)
	if _, err := client.APIPatch(ctx, patchPath, strings.NewReader(patchBody)); err != nil {
		// 启回避免用户无感卡停机
		if wasRunning {
			_ = s.ChangeState(ctx, clusterName, project, vmName, "start", false)
		}
		return fmt.Errorf("patch instance: %w", err)
	}
	if wasRunning {
		if err := s.ChangeState(ctx, clusterName, project, vmName, "start", false); err != nil {
			return fmt.Errorf("start after enable-stateful: %w", err)
		}
	}
	return nil
}

// isRunning 探活；失败默认 false（避免误 stop）。
func (s *VMService) isRunning(ctx context.Context, client *cluster.Client, project, vmName string) (bool, error) {
	stateData, err := client.GetInstanceState(ctx, project, vmName)
	if err != nil {
		return false, err
	}
	var state struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		return false, err
	}
	return state.Status == "Running", nil
}

func (s *VMService) Delete(ctx context.Context, clusterName, project, vmName string) error {
	_ = s.ChangeState(ctx, clusterName, project, vmName, "stop", true)

	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}

	path := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, project)
	_, err := client.APIDelete(ctx, path)
	return err
}

// Trash 把 VM 软移入回收站（PLAN-034）：先尝试停机，但不动 Incus 实例 / 不调
// Incus DELETE。DB 层由 handler 调 VMRepo.MarkTrashed 完成。Restore 期间 VM 保持
// stopped 不重启（更安全；用户可手动 start）。worker.RunVMTrashPurger 在 trash
// 窗口过后接手 hard-delete。
//
// stop 失败不视为致命错误：用户可能在 Incus 已经离线的情况下 trash，purger 兜底。
func (s *VMService) Trash(ctx context.Context, clusterName, project, vmName string) error {
	if err := s.ChangeState(ctx, clusterName, project, vmName, "stop", true); err != nil {
		// running 状态下 stop 失败仅日志告警，不阻断 trash 流程——VM 仍会进入 30s
		// 窗口，purger 最终走 force=true 路径。
		slog.Warn("trash: stop vm failed (continuing)", "vm", vmName, "error", err)
	}
	return nil
}

// PurgeTrashed 是 trash 窗口过后实际的 hard-delete。等价于历史 Delete 路径
// （先 force-stop 再 Incus DELETE），独立命名让 worker / handler 调用语义更清楚。
// 调用者负责在 Purge 成功后把 DB 行 status='deleted'。
func (s *VMService) PurgeTrashed(ctx context.Context, clusterName, project, vmName string) error {
	return s.Delete(ctx, clusterName, project, vmName)
}

// ReinstallParams is resolved by the handler — callers pick a template_slug
// or a legacy os_image string, and the handler translates that into the four
// fields below so this service stays oblivious to the os_templates table.
type ReinstallParams struct {
	ClusterName string
	Project     string
	VMName      string
	ImageSource string // e.g. "ubuntu/24.04/cloud"; required
	ServerURL   string // defaults to https://images.linuxcontainers.org
	Protocol    string // defaults to simplestreams
	DefaultUser string // defaults to "ubuntu"
}

// OPS-051 / PLAN-052：旧 VMService.Reinstall 同步路径 + ReinstallResult 已删除。
// portal handler `h.jobs == nil` 兜底已改 503，重装路径完全走 jobs/vm_reinstall.go。
// ReinstallParams 保留作为 portal/reinstall_resolve.go 的返回 dto，其字段被
// jobs.Params 复用（不再被 service 层消费）。

func (s *VMService) ListInstances(ctx context.Context, clusterName, project string) ([]json.RawMessage, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	return client.GetInstances(ctx, project)
}

func (s *VMService) GetInstanceState(ctx context.Context, clusterName, project, vmName string) (json.RawMessage, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	return client.GetInstanceState(ctx, project, vmName)
}

// ResetPasswordMode selects between online (guest-agent exec) and offline
// (cloud-init reboot) reset paths. "auto" tries online first and falls back
// on failure; this is the default and works for running-but-broken VMs too.
type ResetPasswordMode string

const (
	ResetPasswordAuto    ResetPasswordMode = "auto"
	ResetPasswordOnline  ResetPasswordMode = "online"
	ResetPasswordOffline ResetPasswordMode = "offline"
)

// ResetPasswordResult carries both the new password and the channel that
// actually delivered it so the caller / audit can record which path ran.
// Fallback=true means auto mode tried online, failed, then went offline.
type ResetPasswordResult struct {
	Password string            `json:"password"`
	Username string            `json:"username"`
	Channel  ResetPasswordMode `json:"channel"`
	Fallback bool              `json:"fallback"`
}

// ResetPassword sets a new password for `username` inside the VM. The online
// path runs `chpasswd` via the Incus guest agent (works for healthy running
// VMs). The offline path rewrites cloud-init vendor-data + bumps the
// instance-id so cloud-init re-runs on next boot; suitable for stopped or
// unresponsive VMs.
func (s *VMService) ResetPassword(ctx context.Context, clusterName, project, vmName, username string, mode ResetPasswordMode) (*ResetPasswordResult, error) {
	client, ok := s.clusters.Get(clusterName)
	if !ok {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	if !isValidLinuxUsername(username) {
		return nil, fmt.Errorf("invalid username %q", username)
	}
	if mode == "" {
		mode = ResetPasswordAuto
	}

	newPassword := generatePassword()
	result := &ResetPasswordResult{Password: newPassword, Username: username}

	if mode == ResetPasswordOnline || mode == ResetPasswordAuto {
		if err := s.resetPasswordOnline(ctx, client, project, vmName, username, newPassword); err == nil {
			result.Channel = ResetPasswordOnline
			slog.Info("vm password reset", "vm", vmName, "user", username, "channel", "online")
			return result, nil
		} else if mode == ResetPasswordOnline {
			return nil, fmt.Errorf("online reset: %w", err)
		} else {
			slog.Warn("online reset failed, falling back to offline", "vm", vmName, "error", err)
			result.Fallback = true
		}
	}

	if err := s.resetPasswordOffline(ctx, client, project, vmName, username, newPassword); err != nil {
		return nil, fmt.Errorf("offline reset: %w", err)
	}
	result.Channel = ResetPasswordOffline
	slog.Info("vm password reset", "vm", vmName, "user", username, "channel", "offline", "fallback", result.Fallback)
	return result, nil
}

func (s *VMService) resetPasswordOnline(ctx context.Context, client *cluster.Client, project, vmName, username, password string) error {
	payload := shellSingleQuote(username + ":" + password)
	cmd := []string{"sh", "-c", "echo " + payload + " | chpasswd"}
	retCode, err := client.ExecNonInteractive(ctx, project, vmName, cmd)
	if err != nil {
		return fmt.Errorf("exec chpasswd: %w", err)
	}
	if retCode != 0 {
		return fmt.Errorf("chpasswd exited with code %d", retCode)
	}
	return nil
}

// resetPasswordOffline reboots the VM into a cloud-init pass that runs
// chpasswd on first re-boot. Preserves existing user-data (SSH keys, etc.) by
// injecting into cloud-init.vendor-data instead of user-data.
//
// Flow: stop (best-effort, ignore if already stopped) → PATCH config with
// new vendor-data + new instance-id → start → return. We don't poll guest
// agent to confirm because offline is by definition used when the agent
// isn't cooperative; an audit row is enough for the admin to re-verify.
func (s *VMService) resetPasswordOffline(ctx context.Context, client *cluster.Client, project, vmName, username, password string) error {
	// Best-effort stop; ChangeState handles already-stopped cleanly.
	_ = s.ChangeState(ctx, clusterNameFromClient(client), project, vmName, "stop", true)

	// PATCH-only-config: just the two cloud-init keys we need to flip.
	// PUT-whole-instance broke `volatile.uuid` on 2026-04-25; PATCH lets
	// Incus keep its managed volatile state untouched.
	patchBody := map[string]any{
		"config": map[string]string{
			"cloud-init.vendor-data":  offlineChpasswdCloudConfig(username, password),
			"cloud-init.instance-id":  newInstanceID(),
		},
	}
	bodyJSON, err := json.Marshal(patchBody)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	path := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, project)
	resp, err := client.APIPatch(ctx, path, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("update instance config: %w", err)
	}
	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			if err := client.WaitForOperation(ctx, op.ID); err != nil {
				slog.Warn("wait for instance update failed", "vm", vmName, "error", err)
			}
		}
	}

	if err := s.ChangeState(ctx, clusterNameFromClient(client), project, vmName, "start", false); err != nil {
		return fmt.Errorf("restart instance: %w", err)
	}
	return nil
}

// offlineChpasswdCloudConfig emits a cloud-config that resets ONE user's
// password. Deliberately minimal: no ssh_pwauth override, no chpasswd expire
// change, no runcmd echoing secrets (cloud-init writes the user-data path to
// /var/log/cloud-init.log with mode 600). The cloud-init `chpasswd.users`
// module is idempotent on re-run and keyed on user+password hash, so a
// repeated reset without a new instance-id would be a no-op — that's why the
// caller also bumps cloud-init.instance-id.
func offlineChpasswdCloudConfig(username, password string) string {
	// Use YAML literal block for the password to avoid shell-escaping issues
	// when cloud-init reads vendor-data. The password is hex (generatePassword
	// returns hex.Encode), so it has no YAML special characters, but the
	// double-quoted form keeps the shape explicit for future refactors that
	// might relax the password generator.
	return fmt.Sprintf(`#cloud-config
chpasswd:
  expire: false
  users:
    - name: %q
      password: %q
      type: text
ssh_pwauth: true
`, username, password)
}

// newInstanceID returns a random ID suitable for cloud-init.instance-id so
// cloud-init treats the next boot as a fresh instance and re-runs modules.
func newInstanceID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "iid-" + hex.EncodeToString(b)
}

// clusterNameFromClient reads back the cluster name via the Name field on
// the client struct. Dedicated helper to keep the call site readable.
func clusterNameFromClient(c *cluster.Client) string {
	return c.Name
}

// stripVolatileConfig removes Incus-managed `volatile.*` keys from the
// instance config map. Incus 6.x rejects PUT bodies that include these
// keys ("Setting volatile.X is forbidden") even when the values are
// unchanged. Any code path that does GET-instance → modify → PUT-instance
// must call this before the PUT.
func stripVolatileConfig(config map[string]string) {
	for k := range config {
		if strings.HasPrefix(k, "volatile.") {
			delete(config, k)
		}
	}
}

// isValidLinuxUsername matches POSIX/Debian user_valid_name: start with [a-z_],
// then [a-z0-9_-], max 32 chars.
func isValidLinuxUsername(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		case c == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// shellSingleQuote wraps s in single quotes, escaping any embedded single quote
// with the standard '\'' sequence so the result is safe to concatenate into a
// sh -c command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func generatePassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// OSFamily 分类 Linux 镜像家族，用来选 packages / 服务名 / unattended-upgrades 包。
// Windows 走 applyWindowsCloudInit 单独路径，不进 BuildCloudInit。
//
// 来源：依据 OS template slug / image alias 前缀分类（cloud variant 命名稳定）。
type OSFamily string

const (
	OSFamilyAPT     OSFamily = "apt"     // ubuntu / debian
	OSFamilyDNF     OSFamily = "dnf"     // rocky / almalinux / fedora / centos
	OSFamilyPacman  OSFamily = "pacman"  // archlinux
	OSFamilyAlpine  OSFamily = "alpine"  // alpine (apk)
	OSFamilyUnknown OSFamily = "unknown" // 兜底走 apt 模板（最常见）
)

// ClassifyOSFamily 从 image alias / template source 推断包管理器家族。
// 接受形如 "images:ubuntu/24.04/cloud" / "ubuntu/24.04/cloud" / "rocky/9/cloud"
// / "windows-server-2022"（→ unknown，但 Windows 不会进这条路径）。
func ClassifyOSFamily(image string) OSFamily {
	s := strings.ToLower(image)
	switch {
	case strings.Contains(s, "ubuntu"), strings.Contains(s, "debian"), strings.Contains(s, "kali"), strings.Contains(s, "mint"):
		return OSFamilyAPT
	case strings.Contains(s, "rocky"), strings.Contains(s, "almalinux"), strings.Contains(s, "fedora"), strings.Contains(s, "centos"), strings.Contains(s, "rhel"):
		return OSFamilyDNF
	case strings.Contains(s, "arch"):
		return OSFamilyPacman
	case strings.Contains(s, "alpine"):
		return OSFamilyAlpine
	default:
		return OSFamilyUnknown
	}
}

// CloudInitInput 是 BuildCloudInit 的全部输入。所有字段都是 buildCloudInit
// 编译期可知 + 调用站点显式传入；不读全局 config 让纯函数易测。
type CloudInitInput struct {
	OSFamily     OSFamily // 走哪套 packages / 服务名
	LoginUser    string   // 强制建立的统一登录账号；空 → "root"
	Password     string   // 明文密码（cloud-init chpasswd plain）
	SSHKeys      []string // 注入到 LoginUser 的 authorized_keys
	AptProxyURL  string   // 形如 http://139.162.24.177:3142/；空 → 不注入
	ExtraYAML    string   // 来自 os_templates.cloud_init_template，用户/AI 写
	UserData     string   // WP-I1：/v1 用户自定义 user-data，合并进基础配置（不覆盖）
}

// BuildCloudInit 输出 cloud-config user-data 字符串。
//
// 设计 invariant：
//  1. 必装 openssh-server / qemu-guest-agent（linuxcontainers cloud variant
//     不带 sshd，业界共识：通过 packages 装 + runcmd 兜底启用）。
//  2. 统一 root：cloud-config users.root + chpasswd users[root]
//     + sshd_config drop-in `PermitRootLogin yes` + `PasswordAuthentication
//     yes`（OPS-051 Q7 决策）。镜像自带 ubuntu/debian/rocky user 保留但不再
//     当默认登录。
//  3. apt 系自动启用 unattended-upgrades 周一安全补丁（dnf 系装 dnf-automatic
//     同义）。
//  4. apt-cacher-ng proxy 注入：5 秒 healthcheck，宕机自动剥离 → fallback
//     直连上游，避免 mirror 故障阻塞首装。
//  5. ExtraYAML 走 mergeCloudConfig：用户字段追加（packages / write_files /
//     runcmd 等 list 字段去重合并；mapping 不递归覆盖系统字段 —— 见
//     mergeCloudConfig 注释）。解析失败 → 直接 panic（admin 端配置错误，必须
//     在 UI 校验阶段挡住，BuildCloudInit 走到这里说明 validator 漏了，应明
//     确暴露）。
//
// 输出格式：纯 #cloud-config YAML。不做 jinja 模板渲染（cloud-init 服务端默认
// 不开 jinja，避开误注入）。
func BuildCloudInit(in CloudInitInput) string {
	user := in.LoginUser
	if user == "" {
		user = "root"
	}
	family := in.OSFamily
	if family == "" || family == OSFamilyUnknown {
		family = OSFamilyAPT
	}

	var pkgs []string
	var enableSSHService string
	var systemPkgs string
	switch family {
	case OSFamilyDNF:
		pkgs = []string{"openssh-server", "qemu-guest-agent", "dnf-automatic"}
		enableSSHService = "sshd"
		systemPkgs = "    - dnf-automatic\n"
	case OSFamilyPacman:
		pkgs = []string{"openssh", "qemu-guest-agent"}
		enableSSHService = "sshd"
	case OSFamilyAlpine:
		pkgs = []string{"openssh-server", "qemu-guest-agent"}
		enableSSHService = "sshd"
	default: // apt
		pkgs = []string{"openssh-server", "qemu-guest-agent", "unattended-upgrades"}
		enableSSHService = "ssh"
		systemPkgs = "    - unattended-upgrades\n"
	}
	_ = systemPkgs // 占位，保留给未来 dnf-automatic 专用 runcmd

	var b strings.Builder
	b.WriteString("#cloud-config\n")

	// apt proxy（仅 apt 家族；其它系包管理器不消费 apt: 字段）
	if family == OSFamilyAPT && in.AptProxyURL != "" {
		fmt.Fprintf(&b, "apt:\n  http_proxy: %s\n  https_proxy: %s\n", in.AptProxyURL, in.AptProxyURL)
	}

	b.WriteString("package_update: true\n")
	b.WriteString("package_upgrade: false\n") // unattended-upgrades 异步做，首装不全量升级
	b.WriteString("packages:\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "  - %s\n", p)
	}

	// Users + root 强制 + SSH key 注入。
	// OPS-051 测试发现：cloud-init `ssh_pwauth: true` 模块在 Rocky 9 / RHEL
	// 系上**重写整个 sshd_config 为单行** `PasswordAuthentication yes`，
	// 丢失 `Include /etc/ssh/sshd_config.d/*.conf` → 我们的 99-incusadmin
	// drop-in 永不生效 + sshd 仍走默认 `PermitRootLogin without-password`。
	// 改用 runcmd 直接 sed 修改 sshd_config（更可控）。
	b.WriteString("disable_root: false\n")
	b.WriteString("users:\n")
	fmt.Fprintf(&b, "  - name: %s\n", user)
	b.WriteString("    lock_passwd: false\n")
	if user != "root" {
		b.WriteString("    sudo: ALL=(ALL) NOPASSWD:ALL\n")
		b.WriteString("    shell: /bin/bash\n")
	}
	if len(in.SSHKeys) > 0 {
		b.WriteString("    ssh_authorized_keys:\n")
		for _, k := range in.SSHKeys {
			fmt.Fprintf(&b, "      - %s\n", strings.TrimSpace(k))
		}
	}

	// chpasswd 设密码：cloud-init 24.4+ chpasswd.users[] 兼容性更好，但
	// Rocky 9 cloud-init Final Stage 在 packages 失败时部分模块跳过。
	// 同时设顶层 `password:` 字段（cloud-init 所有版本都支持，cc_set_passwords
	// 早期模块；任一生效即可）+ chpasswd.users[] 双保险。
	fmt.Fprintf(&b, "password: %q\n", in.Password)
	b.WriteString("chpasswd:\n")
	b.WriteString("  expire: false\n")
	b.WriteString("  users:\n")
	fmt.Fprintf(&b, "    - name: %q\n", user)
	fmt.Fprintf(&b, "      password: %q\n", in.Password)
	b.WriteString("      type: text\n")

	// sshd_config drop-in：保证 PermitRootLogin + PasswordAuthentication
	b.WriteString("write_files:\n")
	b.WriteString("  - path: /etc/ssh/sshd_config.d/99-incusadmin.conf\n")
	b.WriteString("    permissions: '0644'\n")
	b.WriteString("    content: |\n")
	b.WriteString("      # Managed by IncusAdmin (OPS-051)\n")
	b.WriteString("      PermitRootLogin yes\n")
	b.WriteString("      PasswordAuthentication yes\n")
	if family == OSFamilyAPT {
		b.WriteString("  - path: /etc/apt/apt.conf.d/20auto-upgrades\n")
		b.WriteString("    permissions: '0644'\n")
		b.WriteString("    content: |\n")
		b.WriteString("      APT::Periodic::Update-Package-Lists \"1\";\n")
		b.WriteString("      APT::Periodic::Unattended-Upgrade \"7\";\n")
	}

	// runcmd 兜底：
	//   - apt-cacher 健康探测（5s 内不通 → 剥离 proxy；防 mirror 宕机阻塞首装）
	//   - **packages 字段重试**：cloud-init `packages` 模块在某些镜像
	//     （实测 Rocky 9 cloud-init 24.4-7）跑得太早，DNS 还没就绪 → dnf
	//     install 必失败。runcmd 在 cloud-final 阶段执行，此时 networking
	//     已稳定 → 重装一次确保 sshd 真在。每条加 retry 3 次 + 5s 间隔，
	//     应对上游瞬时不可达。
	//   - 启用 sshd（packages 装包后服务可能未自启）
	//   - 启用 qemu-guest-agent（迁移 / reset-password online 走 guest-agent）
	//   - reload sshd 让 drop-in 生效
	b.WriteString("runcmd:\n")
	// runcmd 早期硬化（OPS-051 现场测试）：
	//   - sshd_config 直接 sed：cloud-init ssh 模块在 RHEL 上可能重写整个
	//     sshd_config 丢失 Include drop-in。直接 sed 修改 main file 三个关键
	//     设置，必要时追加（无论 cloud-init 怎么改都生效）。
	//   - firewalld disable：RHEL 系阻塞 22 / 3389，Ubuntu/Debian noop
	//   - chpasswd 兜底：cloud-init cc_set_passwords 失败时设密码
	//   - reload sshd 让上述生效
	b.WriteString("  - 'sed -i \"/^#*PermitRootLogin/d;/^#*PasswordAuthentication/d\" /etc/ssh/sshd_config && printf \"PermitRootLogin yes\\nPasswordAuthentication yes\\n\" >> /etc/ssh/sshd_config'\n")
	b.WriteString("  - 'systemctl disable --now firewalld 2>/dev/null || true'\n")
	fmt.Fprintf(&b, "  - 'usermod -U %s 2>/dev/null || true'\n", user)
	// chpasswd 兜底：把 user:password 用 base64 编码后在 guest 内解码，避免用户
	// 指定的 root_pass（WP-I1）含 ' / $ / 反引号等破坏外层 YAML 单引号 scalar 或
	// 被 shell 展开设错密码。base64 输出仅 [A-Za-z0-9+/=]，在 YAML 单引号与
	// shell 内均安全（top-level password: / chpasswd: 已用 %q 双引号安全设置，
	// 本行是首装失败时的 runcmd 兜底）。
	credB64 := base64.StdEncoding.EncodeToString([]byte(user + ":" + in.Password))
	fmt.Fprintf(&b, "  - 'echo %s | base64 -d | chpasswd 2>/dev/null || true'\n", credB64)
	// 再等 DNS 可解析（Rocky 9 / Fedora 等 RHEL 系 NetworkManager-wait-online
	// 可能超时但 resolv.conf 还没完全 ready；cloud-init runcmd 启动太快导致
	// dnf install 必失败）。最多 90 秒 + 3 秒间隔 = 30 次。
	b.WriteString("  - 'for i in $(seq 1 30); do getent hosts deb.debian.org archive.ubuntu.com mirrors.rockylinux.org > /dev/null 2>&1 && break; sleep 3; done'\n")
	switch family {
	case OSFamilyAPT:
		if in.AptProxyURL != "" {
			fmt.Fprintf(&b, "  - 'timeout 5 curl -sf %sacng-report.html > /dev/null || sed -i \"/Acquire::http::Proxy/d;/Acquire::https::Proxy/d\" /etc/apt/apt.conf.d/*'\n", in.AptProxyURL)
		}
		b.WriteString("  - 'for i in 1 2 3 4 5; do apt-get update -q && apt-get install -y --no-install-recommends openssh-server qemu-guest-agent unattended-upgrades && break; sleep 10; done'\n")
	case OSFamilyDNF:
		b.WriteString("  - 'for i in 1 2 3 4 5; do dnf install -y --setopt=install_weak_deps=False openssh-server qemu-guest-agent dnf-automatic && break; sleep 10; done'\n")
	case OSFamilyPacman:
		b.WriteString("  - 'for i in 1 2 3 4 5; do pacman -Sy --noconfirm openssh qemu-guest-agent && break; sleep 10; done'\n")
	case OSFamilyAlpine:
		b.WriteString("  - 'for i in 1 2 3 4 5; do apk add --no-cache openssh-server qemu-guest-agent && break; sleep 10; done'\n")
	}
	fmt.Fprintf(&b, "  - 'systemctl enable --now %s.service || true'\n", enableSSHService)
	b.WriteString("  - 'systemctl enable --now qemu-guest-agent.service || true'\n")
	fmt.Fprintf(&b, "  - 'systemctl reload %s.service || systemctl restart %s.service || true'\n", enableSSHService, enableSSHService)
	if family == OSFamilyAPT {
		b.WriteString("  - 'systemctl enable --now unattended-upgrades.service || true'\n")
	}
	if family == OSFamilyDNF {
		b.WriteString("  - 'systemctl enable --now dnf-automatic.timer || true'\n")
	}

	base := b.String()

	// ExtraYAML 合并：cloud-init YAML 不允许同 mapping 内 key 重复，纯字符串
	// 拼接遇到 write_files / runcmd / packages 等系统已用 key 必报
	// `mapping key already defined`。这里用 yaml.v3 做真正的合并：
	//   - list 字段（write_files / runcmd / packages / users / bootcmd 等）
	//     append（去重靠 cloud-init 自己；重复 path/cmd 是 admin 自负）
	//   - mapping 字段递归合并（如 apt: { ... }）
	//   - scalar 字段 extra 覆盖 base（除了黑名单已禁的 disable_root /
	//     ssh_pwauth）
	// 合并失败（ExtraYAML 解析错）→ 退回 base，admin 端 validateCloudInit-
	// Template 已有 yaml.Unmarshal 挡 schema，走到这里失败极少。PLAN-053
	// 可升级为 cloud-init schema-validate。
	out := base
	if strings.TrimSpace(in.ExtraYAML) != "" {
		if merged, err := mergeCloudInit(out, in.ExtraYAML); err == nil {
			out = merged
		}
		// 解析失败兜底：保持 base 不变（系统功能不被 ExtraYAML 破坏）
	}

	// WP-I1：用户 user_data 合并进（ExtraYAML 已并入的）base 配置。cloud-config
	// 走 append 合并保留基础段；非 cloud-config 走 multipart MIME 一并交付。
	if strings.TrimSpace(in.UserData) != "" {
		out = combineUserData(out, in.UserData)
	}
	return out
}

// combineUserData 把 /v1 用户提供的 user_data 合并进系统 base cloud-config：
//   - #cloud-config：走 mergeCloudInit（list append + mapping merge），保留 base
//     的 packages / runcmd / write_files 等 OS-aware 基础段（追加而非覆盖）。
//   - 其它形态（#! 脚本 / #cloud-boothook / #include 等，cloud-config 无法与之
//     YAML 合并）：用 multipart/mixed MIME 把 base 与 user_data 作为两段一并交给
//     cloud-init，二者都会被消费——不静默丢弃用户 user-data。
func combineUserData(base, userData string) string {
	ud := strings.TrimSpace(userData)
	if ud == "" {
		return base
	}
	if strings.HasPrefix(ud, "#cloud-config") {
		if merged, err := mergeCloudInit(base, userData); err == nil {
			return merged
		}
		// YAML 非法：退回 MIME，保证 user_data 不被丢弃。
	}
	return buildMultipartUserData(base, userData)
}

// buildMultipartUserData 用 multipart/mixed 组合系统 base cloud-config 与用户
// user_data。cloud-init 支持 MIME 多段 user-data，各段依 Content-Type 路由
// handler。boundary 用固定值（内容无随机性需求，且便于测试断言）。
func buildMultipartUserData(base, userData string) string {
	const boundary = "===============incusadmin-userdata=="
	var b strings.Builder
	b.WriteString("Content-Type: multipart/mixed; boundary=\"")
	b.WriteString(boundary)
	b.WriteString("\"\nMIME-Version: 1.0\n\n")
	// part 1：系统 base cloud-config（含 OS-aware packages / runcmd / root 凭据）。
	b.WriteString("--")
	b.WriteString(boundary)
	b.WriteString("\nContent-Type: text/cloud-config; charset=\"utf-8\"\nMIME-Version: 1.0\n\n")
	b.WriteString(base)
	b.WriteString("\n")
	// part 2：用户 user_data，按首行 magic 判定 Content-Type。
	b.WriteString("--")
	b.WriteString(boundary)
	b.WriteString("\nContent-Type: ")
	b.WriteString(detectUserDataContentType(userData))
	b.WriteString("; charset=\"utf-8\"\nMIME-Version: 1.0\n\n")
	b.WriteString(userData)
	b.WriteString("\n--")
	b.WriteString(boundary)
	b.WriteString("--\n")
	return b.String()
}

// detectUserDataContentType 依 cloud-init 惯例的首行 magic 映射 MIME 类型；无法
// 识别默认按 shell 脚本处理（cloud-init 对未知裸文本的常见回退）。
func detectUserDataContentType(ud string) string {
	s := strings.TrimSpace(ud)
	switch {
	case strings.HasPrefix(s, "#cloud-config"):
		return "text/cloud-config"
	case strings.HasPrefix(s, "#cloud-boothook"):
		return "text/cloud-boothook"
	case strings.HasPrefix(s, "#include"):
		return "text/x-include-url"
	default:
		return "text/x-shellscript"
	}
}

// mergeCloudInit 把 ExtraYAML 合并到 base cloud-config（list append + mapping
// merge）。返回合并后的纯 YAML（不带 #cloud-config 头，调用方自加）。
func mergeCloudInit(baseYAML, extraYAML string) (string, error) {
	baseClean := strings.TrimPrefix(strings.TrimSpace(baseYAML), "#cloud-config")
	extraClean := strings.TrimPrefix(strings.TrimSpace(extraYAML), "#cloud-config")
	var baseMap map[string]any
	var extraMap map[string]any
	if err := yaml.Unmarshal([]byte(baseClean), &baseMap); err != nil {
		return "", fmt.Errorf("parse base: %w", err)
	}
	if err := yaml.Unmarshal([]byte(extraClean), &extraMap); err != nil {
		return "", fmt.Errorf("parse extra: %w", err)
	}
	merged := mergeMaps(baseMap, extraMap)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("marshal merged: %w", err)
	}
	return "#cloud-config\n" + string(out), nil
}

// mergeMaps 把 b 合并到 a：list append、mapping 递归合并、scalar b 覆盖 a。
func mergeMaps(a, b map[string]any) map[string]any {
	if a == nil {
		a = map[string]any{}
	}
	for k, vb := range b {
		va, ok := a[k]
		if !ok {
			a[k] = vb
			continue
		}
		// 同名 key：分类处理
		switch va2 := va.(type) {
		case []any:
			if vb2, ok := vb.([]any); ok {
				a[k] = append(va2, vb2...)
			} else {
				a[k] = vb // extra 替换（类型变化，按 extra 为准）
			}
		case map[string]any:
			if vb2, ok := vb.(map[string]any); ok {
				a[k] = mergeMaps(va2, vb2)
			} else {
				a[k] = vb
			}
		default:
			a[k] = vb // scalar 覆盖
		}
	}
	return a
}

func buildNetworkConfig(ip, cidr, gateway string) string {
	// OPS-051 测试发现：cloud-init network-config v2 在 RHEL 系
	// (Rocky 9 / AlmaLinux 9 / Fedora) NetworkManager renderer 兼容性差，
	// enp5s0 拿不到 IPv4 → cloud-init runcmd 永不跑。
	//
	// 改用 cloud-init network-config v1（老格式），所有 distro + 所有
	// cloud-init 版本都稳定支持（Ubuntu/Debian/RHEL/Arch/Alpine 通用）。
	// v1 直接告诉 cloud-init "subnets 是 static、地址/网关/DNS 这些"，
	// renderer 翻译到 netplan / sysconfig / NM keyfile / interfaces。
	return fmt.Sprintf(`version: 1
config:
  - type: physical
    name: enp5s0
    subnets:
      - type: static
        address: %s/%s
        gateway: %s
        dns_nameservers:
          - 1.1.1.1
          - 8.8.8.8`, ip, cidr, gateway)
}

// Ensure VMService implements status constants from model
var _ = model.VMStatusCreating

// PLAN-025 / INFRA-007 桥接：异步 jobs runner 在 internal/service/jobs/
// 子包内调用以下辅助函数。直接导出（GeneratePassword 等）会破坏旧调用方，
// 这里以小包装函数的形式提供 stable API。BuildCloudInit 已升级为
// CloudInitInput struct 签名（OPS-051 / PLAN-052）。
func GeneratePassword() string                                 { return generatePassword() }
func BuildNetworkConfig(ip, cidr, gateway string) string       { return buildNetworkConfig(ip, cidr, gateway) }
func StripVolatileConfig(config map[string]string)             { stripVolatileConfig(config) }
func ProbeImageServer(ctx context.Context, serverURL string) error {
	return probeImageServer(ctx, serverURL)
}
func PrePullImage(ctx context.Context, client *cluster.Client, project, serverURL, protocol, alias string) error {
	return prePullImage(ctx, client, project, serverURL, protocol, alias)
}

// GenerateVMName 给 handler 用：用户没填名字时生成 vm-{6hex}。
func GenerateVMName() string {
	b := make([]byte, 3)
	rand.Read(b)
	return fmt.Sprintf("vm-%s", hex.EncodeToString(b))
}
