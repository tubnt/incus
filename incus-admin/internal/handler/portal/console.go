package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		// Session-1 W8 / PLAN-051 §2-B 决策 D-08：空 Origin + 非 cookie 路径放行；
		// 浏览器走 cookie 路径时一定带 Origin，空 Origin 仅 API token 场景合法。
		if origin == "" {
			// API token 走 Authorization: Bearer，浏览器 ws 不会带这个 header；
			// 老 NSURLSession / 个别移动 SDK 不送 Origin 但走 token，仍允许。
			// 浏览器 cookie 路径无 Origin 视为 CSRF 攻击拒绝。
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				return true
			}
			slog.Warn("ws upgrade: rejected blank Origin without bearer token", "path", r.URL.Path, "ua", r.Header.Get("User-Agent"))
			return false
		}
		host := r.Host
		return origin == "https://"+host || origin == "http://"+host
	},
}

type ConsoleHandler struct {
	clusters *cluster.Manager
	vmRepo   *repository.VMRepo
}

func NewConsoleHandler(clusters *cluster.Manager, vmRepo *repository.VMRepo) *ConsoleHandler {
	return &ConsoleHandler{clusters: clusters, vmRepo: vmRepo}
}

func (h *ConsoleHandler) HandleConsole(w http.ResponseWriter, r *http.Request) {
	vmName := r.URL.Query().Get("vm")
	if vmName == "" {
		http.Error(w, "missing vm param", http.StatusBadRequest)
		return
	}
	// WP-E 防注入：VM 名必须是合法标识符，杜绝把任意串拼进 Incus API path/query。
	if !isValidName(vmName) {
		http.Error(w, "invalid vm name", http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	role, _ := r.Context().Value(middleware.CtxUserRole).(string)

	// WP-E 越权修复：cluster/project 一律从 owner 的 VM 行反解，忽略客户端传入的
	// cluster/project。原实现信任 query 里的 cluster/project，配合 GetByName 只按名
	// 取行可被"跨集群同名 VM"利用：用户拥有集群 A 的 web，却传 cluster=B 去 exec 进
	// 别人集群 B 的同名 web。现在既然从 vm.ClusterID 反解，客户端传值不再有意义。
	if h.vmRepo == nil {
		http.Error(w, "vm repository unavailable", http.StatusInternalServerError)
		return
	}
	vm, err := h.vmRepo.GetByName(r.Context(), vmName)
	if err != nil || vm == nil {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	if role != "admin" && vm.UserID != userID {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	clusterName, project := resolveClusterProjectForVM(h.clusters, vm)
	if clusterName == "" {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	execBody, _ := json.Marshal(map[string]any{
		"command":             []string{"/bin/bash", "-l"},
		"wait-for-websocket": true,
		"interactive":        true,
		"width":              120,
		"height":             40,
		"environment": map[string]string{
			"TERM": "xterm-256color",
			"HOME": "/root",
		},
	})

	execPath := fmt.Sprintf("/1.0/instances/%s/exec?project=%s", url.PathEscape(vmName), url.QueryEscape(project))
	resp, err := client.APIPost(r.Context(), execPath, bytes.NewReader(execBody))
	if err != nil {
		slog.Error("exec request failed", "vm", vmName, "error", err)
		http.Error(w, "exec failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var opMeta struct {
		ID       string `json:"id"`
		Metadata struct {
			FDs map[string]string `json:"fds"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(resp.Metadata, &opMeta)

	fd0Secret := opMeta.Metadata.FDs["0"]
	controlSecret := opMeta.Metadata.FDs["control"]
	if fd0Secret == "" {
		slog.Error("no fd secret", "metadata", string(resp.Metadata))
		http.Error(w, "no fd secret in exec response", http.StatusInternalServerError)
		return
	}

	incusWSURL := buildIncusWSURL(client.APIURL, opMeta.ID, fd0Secret)
	controlWSURL := buildIncusWSURL(client.APIURL, opMeta.ID, controlSecret)

	tlsConfig, err := h.clusters.TLSConfigForCluster(clusterName)
	if err != nil {
		slog.Error("console tls config failed", "cluster", clusterName, "error", err)
		http.Error(w, "tls config failed", http.StatusInternalServerError)
		return
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 10 * time.Second,
		EnableCompression: false,
	}
	headers := http.Header{}
	incusConn, incusResp, err := dialer.Dial(incusWSURL, headers)
	if incusResp != nil {
		// gorilla 返回的 resp 即便成功也需要关闭 Body（底层 HTTP/1.1 upgrade 响应）
		_ = incusResp.Body.Close()
	}
	if err != nil {
		slog.Error("incus ws dial failed", "url", incusWSURL, "error", err)
		http.Error(w, "incus websocket failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer incusConn.Close()

	// Control WebSocket must be connected for Incus to start the shell
	if controlWSURL != "" {
		controlConn, controlResp, err := dialer.Dial(controlWSURL, headers)
		if controlResp != nil {
			_ = controlResp.Body.Close()
		}
		if err != nil {
			slog.Warn("control ws dial failed", "error", err)
		} else {
			defer controlConn.Close()
		}
	}

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("client ws upgrade failed", "error", err)
		return
	}
	defer clientConn.Close()

	sessionStart := time.Now()
	slog.Info("console session started", "vm", vmName, "project", project, "cluster", clusterName)
	audit(r.Context(), r, "console.session_open", "vm", 0, map[string]any{
		"vm": vmName, "project": project, "cluster": clusterName,
	})

	done := make(chan struct{}, 2)

	// Incus → Client
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, msg, err := incusConn.ReadMessage()
			if err != nil {
				slog.Debug("incus read done", "error", err)
				return
			}
			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				slog.Debug("client write done", "error", err)
				return
			}
		}
	}()

	// Client → Incus
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, msg, err := clientConn.ReadMessage()
			if err != nil {
				slog.Debug("client read done", "error", err)
				return
			}
			if err := incusConn.WriteMessage(msgType, msg); err != nil {
				slog.Debug("incus write done", "error", err)
				return
			}
		}
	}()

	<-done
	duration := time.Since(sessionStart)
	slog.Info("console session ended", "vm", vmName, "duration_ms", duration.Milliseconds())
	audit(r.Context(), r, "console.session_close", "vm", 0, map[string]any{
		"vm": vmName, "project": project, "cluster": clusterName,
		"duration_ms": duration.Milliseconds(),
	})
}

// resolveClusterProjectForVM 从 VM 行反解 cluster 名与 project，供 console/snapshot
// 等路径使用，替代信任客户端传入的 cluster/project（WP-E 跨集群同名越权修复）。
// cluster 名由 vm.ClusterID 经 manager 映射得到；project 取该 cluster 的 DefaultProject
// （即 VM 创建时的口径，见 order.Pay / firewall.resolveVMLocation），缺省回退 "customers"。
// clusterName 为空表示无法定位（manager 未注册该 cluster），调用方应据此拒绝。
func resolveClusterProjectForVM(mgr *cluster.Manager, vm *model.VM) (clusterName, project string) {
	clusterName = findClusterName(mgr, vm.ClusterID)
	if clusterName == "" {
		return "", ""
	}
	if cc, ok := mgr.ConfigByName(clusterName); ok {
		project = cc.DefaultProject
	}
	if project == "" {
		project = "customers"
	}
	return clusterName, project
}

func buildIncusWSURL(apiURL, operationID, secret string) string {
	u, _ := url.Parse(apiURL)
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/1.0/operations/%s/websocket?secret=%s", scheme, u.Host, operationID, secret)
}
