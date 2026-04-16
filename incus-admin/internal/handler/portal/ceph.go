package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/sshexec"
)

type CephHandler struct {
	runner *sshexec.Runner
	cache  cephCache
}

type cephCache struct {
	mu      sync.RWMutex
	status  json.RawMessage
	osdTree json.RawMessage
	updated time.Time
}

func NewCephHandler(sshHost, sshUser, sshKeyFile string) *CephHandler {
	if sshHost == "" {
		return &CephHandler{}
	}
	return &CephHandler{
		runner: sshexec.New(sshHost, sshUser, sshKeyFile),
	}
}

func (h *CephHandler) AdminRoutes(r chi.Router) {
	r.Get("/ceph/status", h.Status)
	r.Get("/ceph/osd-tree", h.OSDTree)
}

func (h *CephHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "ceph SSH not configured"})
		return
	}

	h.cache.mu.RLock()
	if time.Since(h.cache.updated) < 30*time.Second && h.cache.status != nil {
		data := h.cache.status
		h.cache.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	}
	h.cache.mu.RUnlock()

	out, err := h.runner.Run(r.Context(), "ceph -s -f json")
	if err != nil {
		slog.Error("ceph status failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.cache.mu.Lock()
	h.cache.status = json.RawMessage(out)
	h.cache.updated = time.Now()
	h.cache.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(out))
}

func (h *CephHandler) OSDTree(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "ceph SSH not configured"})
		return
	}

	h.cache.mu.RLock()
	if time.Since(h.cache.updated) < 30*time.Second && h.cache.osdTree != nil {
		data := h.cache.osdTree
		h.cache.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	}
	h.cache.mu.RUnlock()

	out, err := h.runner.Run(r.Context(), "ceph osd tree -f json")
	if err != nil {
		slog.Error("ceph osd tree failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.cache.mu.Lock()
	h.cache.osdTree = json.RawMessage(out)
	h.cache.updated = time.Now()
	h.cache.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(out))
}
