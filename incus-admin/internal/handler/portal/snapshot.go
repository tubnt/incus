package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type SnapshotHandler struct {
	clusters *cluster.Manager
	vmRepo   *repository.VMRepo
}

func NewSnapshotHandler(clusters *cluster.Manager, vmRepo *repository.VMRepo) *SnapshotHandler {
	return &SnapshotHandler{clusters: clusters, vmRepo: vmRepo}
}

func (h *SnapshotHandler) AdminRoutes(r chi.Router) {
	r.Get("/vms/{name}/snapshots", h.List)
	r.Post("/vms/{name}/snapshots", h.Create)
	r.Delete("/vms/{name}/snapshots/{snap}", h.Delete)
	r.Post("/vms/{name}/snapshots/{snap}/restore", h.Restore)
}

func (h *SnapshotHandler) PortalRoutes(r chi.Router) {
	r.Get("/vms/{name}/snapshots", h.portalWrap(h.List))
	r.Post("/vms/{name}/snapshots", h.portalWrap(h.Create))
	r.Delete("/vms/{name}/snapshots/{snap}", h.portalWrap(h.Delete))
	r.Post("/vms/{name}/snapshots/{snap}/restore", h.portalWrap(h.Restore))
}

func (h *SnapshotHandler) portalWrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vmName := chi.URLParam(r, "name")
		userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
		if h.vmRepo != nil {
			vm, err := h.vmRepo.GetByName(r.Context(), vmName)
			if err != nil || vm == nil || vm.UserID != userID {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
				return
			}
		}
		next(w, r)
	}
}

func (h *SnapshotHandler) List(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	clusterName := r.URL.Query().Get("cluster")
	project := r.URL.Query().Get("project")
	if clusterName == "" || project == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing cluster or project"})
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	path := fmt.Sprintf("/1.0/instances/%s/snapshots?recursion=1&project=%s", vmName, project)
	resp, err := client.APIGet(r.Context(), path)
	if err != nil {
		slog.Error("list snapshots failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"snapshots": resp.Metadata})
}

func (h *SnapshotHandler) Create(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	var req struct {
		Cluster string `json:"cluster" validate:"required,safename"`
		Project string `json:"project" validate:"omitempty,safename"`
		Name    string `json:"name"    validate:"omitempty,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("snap-%s", time.Now().Format("20060102-150405"))
	}

	client, ok := h.clusters.Get(req.Cluster)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"name": req.Name})
	path := fmt.Sprintf("/1.0/instances/%s/snapshots?project=%s", vmName, req.Project)
	resp, err := client.APIPost(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		slog.Error("create snapshot failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			_ = client.WaitForOperation(r.Context(), op.ID)
		}
	}

	slog.Info("snapshot created", "vm", vmName, "name", req.Name)
	audit(r.Context(), r, "snapshot.create", "vm", 0, map[string]any{
		"vm":      vmName,
		"cluster": req.Cluster,
		"project": req.Project,
		"name":    req.Name,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok", "name": req.Name})
}

func (h *SnapshotHandler) Delete(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	snapName := chi.URLParam(r, "snap")
	if !isValidName(vmName) || !isValidName(snapName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	clusterName := r.URL.Query().Get("cluster")
	project := r.URL.Query().Get("project")

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	path := fmt.Sprintf("/1.0/instances/%s/snapshots/%s?project=%s", vmName, snapName, project)
	resp, err := client.APIDelete(r.Context(), path)
	if err != nil {
		slog.Error("delete snapshot failed", "vm", vmName, "snap", snapName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp != nil && resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			_ = client.WaitForOperation(r.Context(), op.ID)
		}
	}

	slog.Info("snapshot deleted", "vm", vmName, "snap", snapName)
	audit(r.Context(), r, "snapshot.delete", "vm", 0, map[string]any{
		"vm":      vmName,
		"cluster": clusterName,
		"project": project,
		"name":    snapName,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (h *SnapshotHandler) Restore(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	snapName := chi.URLParam(r, "snap")
	if !isValidName(vmName) || !isValidName(snapName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	var req struct {
		Cluster string `json:"cluster" validate:"required,safename"`
		Project string `json:"project" validate:"omitempty,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	client, ok := h.clusters.Get(req.Cluster)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"restore": snapName})
	path := fmt.Sprintf("/1.0/instances/%s?project=%s", vmName, req.Project)
	resp, err := client.APIPut(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		slog.Error("restore snapshot failed", "vm", vmName, "snap", snapName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			_ = client.WaitForOperation(r.Context(), op.ID)
		}
	}

	slog.Info("snapshot restored", "vm", vmName, "snap", snapName)
	audit(r.Context(), r, "snapshot.restore", "vm", 0, map[string]any{
		"vm":      vmName,
		"cluster": req.Cluster,
		"project": req.Project,
		"name":    snapName,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored"})
}
