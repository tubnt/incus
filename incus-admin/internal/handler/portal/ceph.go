package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/sshexec"
)

// cephPoolNameRe 限制 pool 名字符集 — 首字符字母数字，总长不超过 63。
var cephPoolNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// validCephPoolTypes 枚举 Ceph pool 类型，防止用户传入任意字符串。
var validCephPoolTypes = map[string]struct{}{
	"replicated": {},
	"erasure":    {},
}

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

func NewCephHandler(sshHost, sshUser, sshKeyFile, knownHostsFile string) *CephHandler {
	if sshHost == "" {
		return &CephHandler{}
	}
	return &CephHandler{
		runner: sshexec.New(sshHost, sshUser, sshKeyFile).WithKnownHosts(knownHostsFile),
	}
}

func (h *CephHandler) AdminRoutes(r chi.Router) {
	r.Get("/ceph/status", h.Status)
	r.Get("/ceph/osd-tree", h.OSDTree)
	r.Post("/ceph/osd/{id}/out", h.OSDOut)
	r.Post("/ceph/osd/{id}/in", h.OSDIn)
	r.Get("/ceph/pools", h.ListPools)
	r.Post("/ceph/pools", h.CreatePool)
	r.Delete("/ceph/pools/{name}", h.DeletePool)
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
	osdID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || osdID < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid osd id"})
		return
	}
	out, err := h.runner.RunArgs(r.Context(), "ceph", "osd", "out", "osd."+strconv.Itoa(osdID))
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
	osdID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || osdID < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid osd id"})
		return
	}
	out, err := h.runner.RunArgs(r.Context(), "ceph", "osd", "in", "osd."+strconv.Itoa(osdID))
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

// ListPools 列出所有 Ceph pool（含详情）
func (h *CephHandler) ListPools(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ceph SSH not configured"})
		return
	}
	out, err := h.runner.Run(r.Context(), "ceph osd pool ls detail -f json")
	if err != nil {
		slog.Error("ceph pool list failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(out))
}

// CreatePool 创建 Ceph pool
func (h *CephHandler) CreatePool(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ceph SSH not configured"})
		return
	}
	var req struct {
		Name  string `json:"name"`
		PGNum int    `json:"pg_num"`
		Type  string `json:"type"` // replicated or erasure
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if !cephPoolNameRe.MatchString(req.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid pool name"})
		return
	}
	if req.PGNum <= 0 {
		req.PGNum = 128
	}
	if req.PGNum > 65536 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "pg_num out of range"})
		return
	}
	if req.Type == "" {
		req.Type = "replicated"
	}
	if _, ok := validCephPoolTypes[req.Type]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid pool type"})
		return
	}

	out, err := h.runner.RunArgs(r.Context(), "ceph", "osd", "pool", "create", req.Name, strconv.Itoa(req.PGNum), req.Type)
	if err != nil {
		slog.Error("ceph pool create failed", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "output": out})
		return
	}

	// 启用 pool 应用
	_, _ = h.runner.RunArgs(r.Context(), "ceph", "osd", "pool", "application", "enable", req.Name, "rbd")

	audit(r.Context(), r, "ceph.pool_create", "pool", 0, map[string]any{"name": req.Name, "pg_num": req.PGNum})
	writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "name": req.Name, "output": out})
}

// DeletePool 删除 Ceph pool
func (h *CephHandler) DeletePool(w http.ResponseWriter, r *http.Request) {
	if h.runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ceph SSH not configured"})
		return
	}
	poolName := chi.URLParam(r, "name")
	if !cephPoolNameRe.MatchString(poolName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid pool name"})
		return
	}

	// 先允许删除
	_, _ = h.runner.RunArgs(r.Context(), "ceph", "osd", "pool", "set", poolName, "nodelete", "false")
	out, err := h.runner.RunArgs(r.Context(), "ceph", "osd", "pool", "delete", poolName, poolName, "--yes-i-really-really-mean-it")
	if err != nil {
		slog.Error("ceph pool delete failed", "name", poolName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "output": out})
		return
	}

	audit(r.Context(), r, "ceph.pool_delete", "pool", 0, map[string]any{"name": poolName})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "name": poolName, "output": out})
}
