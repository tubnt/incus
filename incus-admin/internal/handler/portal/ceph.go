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
	r.Post("/ceph/osd/{id}/out", h.OSDOut)
	r.Post("/ceph/osd/{id}/in", h.OSDIn)
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

// OSDOut 将 OSD 标记为 out（准备维护）
func (h *CephHandler) OSDOut(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ceph SSH not configured"})
		return
	}
	osdID := chi.URLParam(r, "id")
	cmd := "ceph osd out osd." + osdID
	out, err := h.runner.Run(r.Context(), cmd)
	if err != nil {
		slog.Error("ceph osd out failed", "osd", osdID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "output": out})
		return
	}
	audit(r.Context(), r, "ceph.osd_out", "osd", 0, map[string]any{"osd_id": osdID})
	h.invalidateCache()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "output": out})
}

// OSDIn 将 OSD 标记为 in（恢复）
func (h *CephHandler) OSDIn(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ceph SSH not configured"})
		return
	}
	osdID := chi.URLParam(r, "id")
	cmd := "ceph osd in osd." + osdID
	out, err := h.runner.Run(r.Context(), cmd)
	if err != nil {
		slog.Error("ceph osd in failed", "osd", osdID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "output": out})
		return
	}
	audit(r.Context(), r, "ceph.osd_in", "osd", 0, map[string]any{"osd_id": osdID})
	h.invalidateCache()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "output": out})
}

func (h *CephHandler) invalidateCache() {
	h.cache.mu.Lock()
	h.cache.updated = time.Time{}
	h.cache.mu.Unlock()
}
