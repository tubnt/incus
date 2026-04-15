package portal

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type ConsoleHandler struct {
	clusters *cluster.Manager
}

func NewConsoleHandler(clusters *cluster.Manager) *ConsoleHandler {
	return &ConsoleHandler{clusters: clusters}
}

func (h *ConsoleHandler) HandleConsole(w http.ResponseWriter, r *http.Request) {
	vmName := r.URL.Query().Get("vm")
	project := r.URL.Query().Get("project")
	clusterName := r.URL.Query().Get("cluster")

	if vmName == "" || project == "" || clusterName == "" {
		http.Error(w, "missing vm, project, or cluster param", http.StatusBadRequest)
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	cc, _ := h.clusters.ConfigByName(clusterName)

	execBody, _ := json.Marshal(map[string]any{
		"command":              []string{"/bin/bash"},
		"wait-for-websocket":  true,
		"interactive":         true,
		"environment":         map[string]string{"TERM": "xterm-256color"},
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
	if fd0Secret == "" {
		http.Error(w, "no fd secret in exec response", http.StatusInternalServerError)
		return
	}

	incusWSURL := buildIncusWSURL(client.APIURL, opMeta.ID, fd0Secret)

	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	if cc.CertFile != "" && cc.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cc.CertFile, cc.KeyFile)
		if err == nil {
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
	}

	dialer := websocket.Dialer{TLSClientConfig: tlsConfig}
	incusConn, _, err := dialer.Dial(incusWSURL, nil)
	if err != nil {
		slog.Error("incus ws connect failed", "url", incusWSURL, "error", err)
		http.Error(w, "incus websocket failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer incusConn.Close()

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("client ws upgrade failed", "error", err)
		return
	}
	defer clientConn.Close()

	slog.Info("console connected", "vm", vmName, "project", project)

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := incusConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := incusConn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		}
	}()

	<-done
	slog.Info("console disconnected", "vm", vmName)
}

func buildIncusWSURL(apiURL, operationID, secret string) string {
	u, _ := url.Parse(apiURL)
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/1.0/operations/%s/websocket?secret=%s", scheme, u.Host, operationID, secret)
}

