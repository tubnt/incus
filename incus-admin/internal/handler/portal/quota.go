package portal

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type QuotaHandler struct {
	quotas *repository.QuotaRepo
	vms    *repository.VMRepo
}

func NewQuotaHandler(quotas *repository.QuotaRepo, vms *repository.VMRepo) *QuotaHandler {
	return &QuotaHandler{quotas: quotas, vms: vms}
}

func (h *QuotaHandler) PortalRoutes(r chi.Router) {
	r.Get("/quota", h.MyQuota)
}

func (h *QuotaHandler) AdminRoutes(r chi.Router) {
	r.Get("/users/{id}/quota", h.GetUserQuota)
	r.Put("/users/{id}/quota", h.UpdateUserQuota)
}

// MyQuota 返回当前用户的配额及使用量
func (h *QuotaHandler) MyQuota(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	q, err := h.quotas.GetByUserID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"quota": nil})
		return
	}

	used := h.getUsage(r, userID)
	writeJSON(w, http.StatusOK, map[string]any{"quota": q, "usage": used})
}

// GetUserQuota 管理员获取指定用户配额
func (h *QuotaHandler) GetUserQuota(w http.ResponseWriter, r *http.Request) {
	userID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	q, err := h.quotas.GetByUserID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"quota": nil})
		return
	}

	used := h.getUsage(r, userID)
	writeJSON(w, http.StatusOK, map[string]any{"quota": q, "usage": used})
}

// UpdateUserQuota 管理员更新指定用户配额
func (h *QuotaHandler) UpdateUserQuota(w http.ResponseWriter, r *http.Request) {
	userID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if userID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid user id"})
		return
	}

	var req struct {
		MaxVMs       int `json:"max_vms"`
		MaxVCPUs     int `json:"max_vcpus"`
		MaxRAMMB     int `json:"max_ram_mb"`
		MaxDiskGB    int `json:"max_disk_gb"`
		MaxIPs       int `json:"max_ips"`
		MaxSnapshots int `json:"max_snapshots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	q, err := h.quotas.GetByUserID(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "quota not found for user"})
		return
	}

	q.MaxVMs = req.MaxVMs
	q.MaxVCPUs = req.MaxVCPUs
	q.MaxRAMMB = req.MaxRAMMB
	q.MaxDiskGB = req.MaxDiskGB
	q.MaxIPs = req.MaxIPs
	q.MaxSnapshots = req.MaxSnapshots

	if err := h.quotas.Update(r.Context(), q); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "quota.update", "user", userID, map[string]any{
		"max_vms": q.MaxVMs, "max_vcpus": q.MaxVCPUs, "max_ram_mb": q.MaxRAMMB,
	})
	writeJSON(w, http.StatusOK, map[string]any{"quota": q})
}

type quotaUsage struct {
	VMs    int `json:"vms"`
	VCPUs  int `json:"vcpus"`
	RAMMB  int `json:"ram_mb"`
	DiskGB int `json:"disk_gb"`
}

func (h *QuotaHandler) getUsage(r *http.Request, userID int64) *quotaUsage {
	if h.vms == nil {
		return &quotaUsage{}
	}
	vms, err := h.vms.ListByUser(r.Context(), userID)
	if err != nil {
		return &quotaUsage{}
	}
	u := &quotaUsage{VMs: len(vms)}
	for _, vm := range vms {
		u.VCPUs += vm.CPU
		u.RAMMB += vm.MemoryMB
		u.DiskGB += vm.DiskGB
	}
	return u
}
