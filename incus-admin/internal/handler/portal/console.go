package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
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
	project := r.URL.Query().Get("project")
	clusterName := r.URL.Query().Get("cluster")

	if vmName == "" || project == "" || clusterName == "" {
		http.Error(w, "missing vm, project, or cluster param", http.StatusBadRequest)
		return
	}

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	role, _ := r.Context().Value(middleware.CtxUserRole).(string)
	if role != "admin" && h.vmRepo != nil {
		vm, err := h.vmRepo.GetByName(r.Context(), vmName)
		if err != nil || vm == nil || vm.UserID != userID {
			http.Error(w, "access denied", http.StatusForbidden)
			return
		}
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

	execPath := fmt.Sprintf("/1.0/instances/%s/exec?project=%s", vmName, project)
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
	json.Unmarshal(resp.Metadata, &opMeta)

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
	incusConn, _, err := dialer.Dial(incusWSURL, headers)
	if err != nil {
		slog.Error("incus ws dial failed", "url", incusWSURL, "error", err)
		http.Error(w, "incus websocket failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer incusConn.Close()

	// Control WebSocket must be connected for Incus to start the shell
	if controlWSURL != "" {
		controlConn, _, err := dialer.Dial(controlWSURL, headers)
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

func buildIncusWSURL(apiURL, operationID, secret string) string {
	u, _ := url.Parse(apiURL)
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/1.0/operations/%s/websocket?secret=%s", scheme, u.Host, operationID, secret)
}
