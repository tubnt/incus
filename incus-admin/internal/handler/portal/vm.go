package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/service"
)

type VMHandler struct {
	vmSvc *service.VMService
}

func NewVMHandler(vmSvc *service.VMService) *VMHandler {
	return &VMHandler{vmSvc: vmSvc}
}

func (h *VMHandler) Routes(r chi.Router) {
	r.Get("/services", h.ListServices)
	r.Get("/services/{id}", h.GetService)
	r.Post("/services/{id}/actions/{action}", h.VMAction)
}

func (h *VMHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	_ = r.Context().Value(middleware.CtxUserID)
	writeJSON(w, http.StatusOK, map[string]any{"services": []any{}})
}

func (h *VMHandler) GetService(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{"service": nil})
}

func (h *VMHandler) VMAction(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")

	switch action {
	case "start", "stop", "restart":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": action})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action"})
	}
}

type AdminVMHandler struct {
	vmSvc     *service.VMService
	clusters  *cluster.Manager
	scheduler *cluster.Scheduler
}

func NewAdminVMHandler(vmSvc *service.VMService, clusters *cluster.Manager, scheduler *cluster.Scheduler) *AdminVMHandler {
	return &AdminVMHandler{vmSvc: vmSvc, clusters: clusters, scheduler: scheduler}
}

func (h *AdminVMHandler) Routes(r chi.Router) {
	r.Get("/clusters", h.ListClusters)
	r.Get("/clusters/{name}/nodes", h.ListNodes)
	r.Get("/clusters/{name}/vms", h.ListClusterVMs)
	r.Get("/vms", h.ListAllVMs)
	r.Put("/vms/{id}/state", h.ChangeVMState)
	r.Delete("/vms/{id}", h.DeleteVM)
}

func (h *AdminVMHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	clients := h.clusters.List()
	result := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		nodes := h.scheduler.GetNodes(c.Name)
		result = append(result, map[string]any{
			"name":         c.Name,
			"display_name": c.DisplayName,
			"api_url":      c.APIURL,
			"nodes":        len(nodes),
			"status":       "active",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": result})
}

func (h *AdminVMHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodes := h.scheduler.GetNodes(clusterName)
	if nodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

func (h *AdminVMHandler) ListClusterVMs(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	var allInstances []json.RawMessage
	for _, proj := range cc.Projects {
		instances, err := h.vmSvc.ListInstances(r.Context(), clusterName, proj.Name)
		if err != nil {
			slog.Warn("list instances failed", "cluster", clusterName, "project", proj.Name, "error", err)
			continue
		}
		allInstances = append(allInstances, instances...)
	}
	if len(cc.Projects) == 0 {
		instances, err := h.vmSvc.ListInstances(r.Context(), clusterName, "default")
		if err != nil {
			slog.Error("list instances failed", "cluster", clusterName, "error", err)
		} else {
			allInstances = append(allInstances, instances...)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"vms": allInstances, "count": len(allInstances)})
}

func (h *AdminVMHandler) ListAllVMs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"vms": []any{}})
}

func (h *AdminVMHandler) ChangeVMState(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	var req struct {
		Action string `json:"action"`
		Force  bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	slog.Info("admin vm state change", "action", req.Action)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *AdminVMHandler) DeleteVM(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
