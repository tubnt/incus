package portal

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

type EventsHandler struct {
	clusters *cluster.Manager
}

func NewEventsHandler(clusters *cluster.Manager) *EventsHandler {
	return &EventsHandler{clusters: clusters}
}

func (h *EventsHandler) AdminRoutes(r chi.Router) {
	r.Get("/events/ws", h.StreamEvents)
}

// StreamEvents 代理 Incus 事件流到浏览器 WebSocket
func (h *EventsHandler) StreamEvents(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" && h.clusters != nil && len(h.clusters.List()) > 0 {
		clusterName = h.clusters.List()[0].Name
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	cc, _ := h.clusters.ConfigByName(clusterName)

	// 构建 Incus events WebSocket URL
	eventTypes := r.URL.Query().Get("type")
	if eventTypes == "" {
		eventTypes = "lifecycle,operation"
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		project = "customers"
	}

	incusWSURL := buildEventsWSURL(client.APIURL, eventTypes, project)

	// 配置 mTLS dialer
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	if cc.CertFile != "" && cc.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cc.CertFile, cc.KeyFile)
		if err == nil {
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 10 * time.Second,
	}

	// 连接 Incus events WebSocket
	incusConn, _, err := dialer.Dial(incusWSURL, nil)
	if err != nil {
		slog.Error("incus events ws dial failed", "url", incusWSURL, "error", err)
		http.Error(w, "incus events connection failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer incusConn.Close()

	// Upgrade 浏览器连接
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("client ws upgrade failed for events", "error", err)
		return
	}
	defer clientConn.Close()

	slog.Info("events stream started", "cluster", clusterName, "types", eventTypes)

	done := make(chan struct{}, 2)

	// Incus → 浏览器（主要方向，事件只从 Incus 流向客户端）
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, msg, err := incusConn.ReadMessage()
			if err != nil {
				slog.Debug("incus events read done", "error", err)
				return
			}
			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				slog.Debug("client events write done", "error", err)
				return
			}
		}
	}()

	// 浏览器 → Incus（处理客户端关闭）
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, _, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()

	<-done
	slog.Info("events stream ended", "cluster", clusterName)
}

func buildEventsWSURL(apiURL, eventTypes, project string) string {
	u, _ := url.Parse(apiURL)
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return fmt.Sprintf("%s://%s/1.0/events?type=%s&project=%s", scheme, u.Host, eventTypes, project)
}
