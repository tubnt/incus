package portal

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/sshexec"
)

// shellMetaChars 任一出现即拒绝命令（防止命令链 / 管道 / 重定向 / 反引号等注入）。
const shellMetaChars = ";&|`$<>\n\r\\\"'"

// programAllowlist 节点远程执行的程序白名单（程序名必须精确匹配第一个 token）。
var programAllowlist = map[string]bool{
	"hostname":   true,
	"uname":      true,
	"uptime":     true,
	"free":       true,
	"df":         true,
	"lsblk":      true,
	"incus":      true,
	"ceph":       true,
	"ip":         true,
	"cat":        true,
	"journalctl": true,
	"systemctl":  true,
}

// systemctlActions 限制 systemctl 仅可执行只读查询动作。
var systemctlActions = map[string]bool{
	"status": true, "is-active": true, "is-enabled": true,
	"list-units": true, "list-unit-files": true,
}

type NodeOpsHandler struct {
	defaultUser    string
	defaultKeyFile string
	knownHostsFile string
}

func NewNodeOpsHandler(defaultUser, defaultKeyFile, knownHostsFile string) *NodeOpsHandler {
	return &NodeOpsHandler{defaultUser: defaultUser, defaultKeyFile: defaultKeyFile, knownHostsFile: knownHostsFile}
}

func (h *NodeOpsHandler) AdminRoutes(r chi.Router) {
	r.Post("/nodes/test-ssh", h.TestSSH)
	r.Post("/nodes/exec", h.ExecCommand)
}

func (h *NodeOpsHandler) TestSSH(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host    string `json:"host"     validate:"required,hostname_rfc1123|ip"`
		User    string `json:"user"     validate:"omitempty,max=64"`
		KeyFile string `json:"key_file" validate:"omitempty,max=512"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.User == "" { req.User = h.defaultUser }
	if req.KeyFile == "" { req.KeyFile = h.defaultKeyFile }

	runner := sshexec.New(req.Host, req.User, req.KeyFile).WithKnownHosts(h.knownHostsFile)
	out, err := runner.Run(r.Context(), "hostname && uname -r && uptime")
	if err != nil {
		slog.Error("SSH test failed", "host", req.Host, "error", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "failed",
			"error":  err.Error(),
			"output": out,
		})
		return
	}

	audit(r.Context(), r, "node.ssh_test", "node", 0, map[string]any{"host": req.Host})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"output": out,
	})
}

func (h *NodeOpsHandler) ExecCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host    string `json:"host"     validate:"required,hostname_rfc1123|ip"`
		User    string `json:"user"     validate:"omitempty,max=64"`
		KeyFile string `json:"key_file" validate:"omitempty,max=512"`
		Command string `json:"command"  validate:"required,min=1,max=4096"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.User == "" { req.User = h.defaultUser }
	if req.KeyFile == "" { req.KeyFile = h.defaultKeyFile }

	program, args, ok := validateCommand(req.Command)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command not allowed"})
		return
	}

	runner := sshexec.New(req.Host, req.User, req.KeyFile).WithKnownHosts(h.knownHostsFile)
	out, err := runner.RunArgs(r.Context(), program, args...)

	audit(r.Context(), r, "node.exec", "node", 0, map[string]any{
		"host": req.Host, "command": req.Command, "error": fmt.Sprint(err),
	})

	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "error", "output": out, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "output": out})
}

// validateCommand 拆分并校验远程执行命令；返回 program + args。
// 规则：
//  1. 拒绝任何包含 shell 元字符的字符串（阻止命令链 / 管道 / 重定向）。
//  2. 第一个 token 必须精确匹配 programAllowlist。
//  3. systemctl / cat 进一步收紧子动作与参数路径。
func validateCommand(cmd string) (string, []string, bool) {
	if strings.ContainsAny(cmd, shellMetaChars) {
		return "", nil, false
	}
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return "", nil, false
	}
	program, args := tokens[0], tokens[1:]
	if !programAllowlist[program] {
		return "", nil, false
	}
	switch program {
	case "systemctl":
		if len(args) < 1 || !systemctlActions[args[0]] {
			return "", nil, false
		}
	case "cat":
		if len(args) == 0 {
			return "", nil, false
		}
		for _, a := range args {
			if !strings.HasPrefix(a, "/etc/") && !strings.HasPrefix(a, "/proc/") {
				return "", nil, false
			}
		}
	}
	return program, args, true
}
