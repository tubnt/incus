package portal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

// resolveVM 按 VM 名反解其 cluster 名与 project，忽略客户端传入的 cluster/project
// （WP-E 跨集群同名越权修复）。portal 路径已在 portalWrap 做过 owner 校验；admin
// 路径无 owner 校验但仍按 VM 行定位，杜绝"传 cluster=B 操作别人集群同名快照"。
// 定位不到 VM 或 cluster 时写好响应并返回 ok=false。
func (h *SnapshotHandler) resolveVM(w http.ResponseWriter, r *http.Request, vmName string) (clusterName, project string, ok bool) {
	if h.vmRepo == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "vm repository unavailable"})
		return "", "", false
	}
	vm, err := h.vmRepo.GetByName(r.Context(), vmName)
	if err != nil || vm == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not found"})
		return "", "", false
	}
	clusterName, project = resolveClusterProjectForVM(h.clusters, vm)
	if clusterName == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return "", "", false
	}
	return clusterName, project, true
}

func (h *SnapshotHandler) List(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	clusterName, project, ok := h.resolveVM(w, r, vmName)
	if !ok {
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	path := fmt.Sprintf("/1.0/instances/%s/snapshots?recursion=1&project=%s", url.PathEscape(vmName), url.QueryEscape(project))
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
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
		return
	}
	// WP-E：cluster/project 不再从请求体取，改为按 VM 行反解；仅快照名可由客户端指定。
	var req struct {
		Name string `json:"name" validate:"omitempty,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprintf("snap-%s", time.Now().Format("20060102-150405"))
	}

	clusterName, project, ok := h.resolveVM(w, r, vmName)
	if !ok {
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"name": req.Name})
	path := fmt.Sprintf("/1.0/instances/%s/snapshots?project=%s", url.PathEscape(vmName), url.QueryEscape(project))
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
		"cluster": clusterName,
		"project": project,
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
	clusterName, project, ok := h.resolveVM(w, r, vmName)
	if !ok {
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	path := fmt.Sprintf("/1.0/instances/%s/snapshots/%s?project=%s", url.PathEscape(vmName), url.PathEscape(snapName), url.QueryEscape(project))
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
	// WP-E：cluster/project 改为按 VM 行反解，忽略请求体传值。
	clusterName, project, ok := h.resolveVM(w, r, vmName)
	if !ok {
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"restore": snapName})
	path := fmt.Sprintf("/1.0/instances/%s?project=%s", url.PathEscape(vmName), url.QueryEscape(project))
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
		"cluster": clusterName,
		"project": project,
		"name":    snapName,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored"})
}
