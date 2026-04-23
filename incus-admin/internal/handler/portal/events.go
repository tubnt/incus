package portal

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

const (
	wsPingInterval = 30 * time.Second
	wsReadTimeout  = 60 * time.Second
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

	// 构建 Incus events WebSocket URL
	eventTypes := r.URL.Query().Get("type")
	if eventTypes == "" {
		eventTypes = "lifecycle,operation"
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		project = "customers"
	}

	incusWSURL, err := buildEventsWSURL(client.APIURL, eventTypes, project)
	if err != nil {
		slog.Error("build incus events URL failed", "api_url", client.APIURL, "error", err)
		http.Error(w, "invalid cluster api url", http.StatusInternalServerError)
		return
	}

	// mTLS + SPKI pin 复用 cluster manager 的 fingerprint store
	tlsConfig, err := h.clusters.TLSConfigForCluster(clusterName)
	if err != nil {
		slog.Error("events tls config failed", "cluster", clusterName, "error", err)
		http.Error(w, "tls config failed", http.StatusInternalServerError)
		return
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 10 * time.Second,
	}

	// 连接 Incus events WebSocket
	incusConn, incusResp, err := dialer.Dial(incusWSURL, nil)
	if incusResp != nil {
		_ = incusResp.Body.Close()
	}
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

	// Install read deadlines + pong handlers so dead peers are detected.
	setReadDeadlineHandlers(incusConn)
	setReadDeadlineHandlers(clientConn)

	done := make(chan struct{}, 3)
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }

	// Incus → browser: forward frames.
	go func() {
		for {
			msgType, msg, err := incusConn.ReadMessage()
			if err != nil {
				slog.Debug("incus events read done", "error", err)
				finish()
				return
			}
			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				slog.Debug("client events write done", "error", err)
				finish()
				return
			}
		}
	}()

	// Browser → Incus: drain (we don't forward client input, just watch for close).
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				finish()
				return
			}
		}
	}()

	// Keep-alive: ping both legs so idle connections don't leak goroutines.
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				deadline := time.Now().Add(wsPingInterval / 2)
				if err := incusConn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					finish()
					return
				}
				if err := clientConn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					finish()
					return
				}
			}
		}
	}()

	<-done
	slog.Info("events stream ended", "cluster", clusterName)
}

func setReadDeadlineHandlers(c *websocket.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(wsReadTimeout))
	c.SetPongHandler(func(string) error {
		return c.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	// Browser clients also send ping frames periodically via gorilla default
	// handler; extending the deadline on ping keeps the connection healthy.
	prevPing := c.PingHandler()
	c.SetPingHandler(func(appData string) error {
		_ = c.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return prevPing(appData)
	})
}

func buildEventsWSURL(apiURL, eventTypes, project string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("parse api url %q: %w", apiURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("api url %q has no host", apiURL)
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	q := url.Values{}
	q.Set("type", eventTypes)
	q.Set("project", project)
	return fmt.Sprintf("%s://%s/1.0/events?%s", scheme, u.Host, q.Encode()), nil
}
