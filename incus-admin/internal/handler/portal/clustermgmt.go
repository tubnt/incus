package portal

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
	"github.com/incuscloud/incus-admin/internal/middleware"
)

type ClusterMgmtHandler struct {
	mgr *cluster.Manager
}

func NewClusterMgmtHandler(mgr *cluster.Manager) *ClusterMgmtHandler {
	return &ClusterMgmtHandler{mgr: mgr}
}

func (h *ClusterMgmtHandler) AdminRoutes(r chi.Router) {
	r.Post("/clusters/add", h.AddCluster)
	r.Delete("/clusters/{name}/remove", h.RemoveCluster)
	r.Get("/nodes", h.ListNodes)
	r.Get("/nodes/{name}", h.NodeDetail)
	r.Post("/nodes/{name}/evacuate", h.EvacuateNode)
	r.Post("/nodes/{name}/restore", h.RestoreNode)
}

func (h *ClusterMgmtHandler) AddCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"         validate:"required,safename"`
		DisplayName string `json:"display_name" validate:"omitempty,max=200"`
		APIURL      string `json:"api_url"      validate:"required,url"`
		CertFile    string `json:"cert_file"    validate:"omitempty,max=512"`
		KeyFile     string `json:"key_file"     validate:"omitempty,max=512"`
		CAFile      string `json:"ca_file"      validate:"omitempty,max=512"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	cc := config.ClusterConfig{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		APIURL:      req.APIURL,
		CertFile:    req.CertFile,
		KeyFile:     req.KeyFile,
		CAFile:      req.CAFile,
	}

	if err := h.mgr.AddCluster(cc); err != nil {
		slog.Error("add cluster failed", "name", req.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "cluster.add", "cluster", 0, map[string]any{"name": req.Name, "url": req.APIURL})
	slog.Info("cluster added", "name", req.Name, "url", req.APIURL)
	writeJSON(w, http.StatusCreated, map[string]any{"status": "added", "name": req.Name})
}

func (h *ClusterMgmtHandler) RemoveCluster(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	if err := h.mgr.RemoveCluster(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "cluster.remove", "cluster", 0, map[string]any{"name": name})
	slog.Info("cluster removed", "name", name)
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed", "name": name})
}

// ListNodes 返回所有集群的所有节点成员列表
func (h *ClusterMgmtHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	if h.mgr == nil || len(h.mgr.List()) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": []any{}})
		return
	}

	clusterFilter := r.URL.Query().Get("cluster")

	type nodeInfo struct {
		Cluster     string `json:"cluster"`
		ServerName  string `json:"server_name"`
		URL         string `json:"url"`
		Status      string `json:"status"`
		Message     string `json:"message"`
		Architecture string `json:"architecture"`
		Roles       []string `json:"roles"`
		Description string `json:"description"`
	}

	var nodes []nodeInfo
	for _, client := range h.mgr.List() {
		if clusterFilter != "" && client.Name != clusterFilter {
			continue
		}
		members, err := client.GetClusterMembers(r.Context())
		if err != nil {
			slog.Error("list cluster members failed", "cluster", client.Name, "error", err)
			continue
		}
		for _, raw := range members {
			var m struct {
				ServerName   string   `json:"server_name"`
				URL          string   `json:"url"`
				Status       string   `json:"status"`
				Message      string   `json:"message"`
				Architecture string   `json:"architecture"`
				Roles        []string `json:"roles"`
				Description  string   `json:"description"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			nodes = append(nodes, nodeInfo{
				Cluster:     client.Name,
				ServerName:  m.ServerName,
				URL:         m.URL,
				Status:      m.Status,
				Message:     m.Message,
				Architecture: m.Architecture,
				Roles:       m.Roles,
				Description: m.Description,
			})
		}
	}

	if nodes == nil {
		nodes = []nodeInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// NodeDetail 获取单个节点详情（含实例列表）
func (h *ClusterMgmtHandler) NodeDetail(w http.ResponseWriter, r *http.Request) {
	nodeName := chi.URLParam(r, "name")
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" && len(h.mgr.List()) > 0 {
		clusterName = h.mgr.List()[0].Name
	}

	client, ok := h.mgr.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	// 获取节点信息
	resp, err := client.APIGet(r.Context(), fmt.Sprintf("/1.0/cluster/members/%s", nodeName))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to get node: " + err.Error()})
		return
	}

	// 获取该节点上的实例列表
	project := r.URL.Query().Get("project")
	if project == "" {
		project = "customers"
	}
	instances, _ := client.GetInstances(r.Context(), project)
	var nodeInstances []json.RawMessage
	for _, inst := range instances {
		var brief struct {
			Name     string `json:"name"`
			Location string `json:"location"`
			Status   string `json:"status"`
			Type     string `json:"type"`
		}
		if err := json.Unmarshal(inst, &brief); err == nil && brief.Location == nodeName {
			nodeInstances = append(nodeInstances, inst)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node":      resp.Metadata,
		"instances": nodeInstances,
	})
}

// EvacuateNode 将节点设为维护模式（evacuate 迁移实例到其他节点）
func (h *ClusterMgmtHandler) EvacuateNode(w http.ResponseWriter, r *http.Request) {
	nodeName := chi.URLParam(r, "name")
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" && len(h.mgr.List()) > 0 {
		clusterName = h.mgr.List()[0].Name
	}

	client, ok := h.mgr.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	// PLAN-020 Phase D: double-write healing_events. The row is created
	// before we touch Incus so a crash between insert + API call still
	// shows up as in_progress → ExpireStale → partial. Completed once
	// the Incus evacuate operation returns (or Fail'd on error).
	var healingID int64
	if healingRepo != nil {
		clusterID := h.mgr.IDByName(clusterName)
		if clusterID > 0 {
			actorID, _ := r.Context().Value(middleware.CtxUserID).(int64)
			if a, _ := r.Context().Value(middleware.CtxActorID).(int64); a > 0 {
				// Under shadow session the "operator" is the admin behind it.
				actorID = a
			}
			var actorPtr *int64
			if actorID > 0 {
				actorPtr = &actorID
			}
			if id, hErr := healingRepo.Create(r.Context(), clusterID, nodeName, "manual", actorPtr); hErr == nil {
				healingID = id
			} else {
				slog.Warn("healing event create failed", "node", nodeName, "error", hErr)
			}
		}
	}

	evacuateBody := strings.NewReader(`{"action":"evacuate"}`)
	resp, err := client.APIPost(r.Context(), fmt.Sprintf("/1.0/cluster/members/%s/state", nodeName), evacuateBody)
	if err != nil {
		if healingID > 0 && healingRepo != nil {
			_ = healingRepo.Fail(r.Context(), healingID, err.Error())
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "evacuate failed: " + err.Error()})
		return
	}

	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		opID := extractOperationID(resp.Operation)
		if opID != "" {
			if waitErr := client.WaitForOperation(r.Context(), opID); waitErr != nil {
				if healingID > 0 && healingRepo != nil {
					_ = healingRepo.Fail(r.Context(), healingID, waitErr.Error())
				}
				slog.Warn("evacuate operation wait failed", "node", nodeName, "error", waitErr)
			}
		}
	}

	if healingID > 0 && healingRepo != nil {
		_ = healingRepo.Complete(r.Context(), healingID)
	}

	audit(r.Context(), r, "node.evacuate", "node", 0, map[string]any{
		"node": nodeName, "cluster": clusterName, "healing_event_id": healingID,
	})
	slog.Info("node evacuated", "node", nodeName, "cluster", clusterName, "healing_event_id", healingID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "evacuated", "node": nodeName})
}

// RestoreNode 恢复节点（evacuate 反向操作）
func (h *ClusterMgmtHandler) RestoreNode(w http.ResponseWriter, r *http.Request) {
	nodeName := chi.URLParam(r, "name")
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" && len(h.mgr.List()) > 0 {
		clusterName = h.mgr.List()[0].Name
	}

	client, ok := h.mgr.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	restoreBody := strings.NewReader(`{"action":"restore"}`)
	resp, err := client.APIPost(r.Context(), fmt.Sprintf("/1.0/cluster/members/%s/state", nodeName), restoreBody)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "restore failed: " + err.Error()})
		return
	}

	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		opID := extractOperationID(resp.Operation)
		if opID != "" {
			_ = client.WaitForOperation(r.Context(), opID)
		}
	}

	audit(r.Context(), r, "node.restore", "node", 0, map[string]any{
		"node": nodeName, "cluster": clusterName,
	})
	slog.Info("node restored", "node", nodeName, "cluster", clusterName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored", "node": nodeName})
}

// extractOperationID 从 "/1.0/operations/<uuid>" 中提取 uuid
func extractOperationID(opPath string) string {
	parts := strings.Split(opPath, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
