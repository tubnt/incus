package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/service"
)

type VMHandler struct {
	vmSvc    *service.VMService
	vmRepo   *repository.VMRepo
	sshKeys  *repository.SSHKeyRepo
	clusters *cluster.Manager
}

func NewVMHandler(vmSvc *service.VMService, vmRepo *repository.VMRepo, sshKeys *repository.SSHKeyRepo, clusters *cluster.Manager) *VMHandler {
	return &VMHandler{vmSvc: vmSvc, vmRepo: vmRepo, sshKeys: sshKeys, clusters: clusters}
}

func (h *VMHandler) Routes(r chi.Router) {
	r.Get("/services", h.ListServices)
	r.Get("/services/{id}", h.GetService)
	r.Post("/services/{id}/actions/{action}", h.VMAction)
	r.Post("/services/{id}/reinstall", h.Reinstall)
	r.Post("/services/{id}/reset-password", h.ResetPassword)
}

func (h *VMHandler) Reinstall(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
		return
	}

	var req struct {
		OSImage string `json:"os_image"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.OSImage == "" { req.OSImage = "images:ubuntu/24.04/cloud" }

	cc, _ := h.clusters.ConfigByName(findClusterName(h.clusters, vm.ClusterID))
	project := cc.DefaultProject
	if project == "" { project = "customers" }

	result, err := h.vmSvc.Reinstall(r.Context(), service.ReinstallParams{
		ClusterName: findClusterName(h.clusters, vm.ClusterID),
		Project:     project,
		VMName:      vm.Name,
		NewOSImage:  req.OSImage,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "vm.reinstall", "vm", vmID, map[string]any{"name": vm.Name, "os": req.OSImage})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "reinstalled",
		"password": result.Password,
		"username": result.Username,
	})
}

func (h *VMHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
		return
	}

	clusterName := findClusterName(h.clusters, vm.ClusterID)
	cc, _ := h.clusters.ConfigByName(clusterName)
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}

	newPassword, err := h.vmSvc.ResetPassword(r.Context(), clusterName, project, vm.Name, "ubuntu")
	if err != nil {
		slog.Error("reset password failed", "vm", vm.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "password reset failed: " + err.Error()})
		return
	}

	// 更新 DB 中的密码
	h.vmRepo.UpdatePassword(r.Context(), vmID, newPassword)

	audit(r.Context(), r, "vm.reset_password", "vm", vmID, map[string]any{"name": vm.Name})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "password_reset",
		"password": newPassword,
		"username": "ubuntu",
	})
}

func (h *VMHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if userID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	vms, err := h.vmRepo.ListByUser(r.Context(), userID)
	if err != nil {
		slog.Error("list user vms failed", "user_id", userID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list VMs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": NewVMServiceDTOList(vms, h.clusters, defaultProjectForMgr(h.clusters))})
}

func (h *VMHandler) GetService(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vm": NewVMServiceDTO(*vm, h.clusters, defaultProjectForMgr(h.clusters))})
}

func (h *VMHandler) VMAction(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	action := chi.URLParam(r, "action")

	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	if h.vmSvc == nil || h.clusters == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster not connected"})
		return
	}

	cc, _ := h.clusters.ConfigByName(findClusterName(h.clusters, vm.ClusterID))
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}

	switch action {
	case "start", "stop", "restart":
		err := h.vmSvc.ChangeState(r.Context(), cc.Name, project, vm.Name, action, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": action})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action"})
	}
}

func (h *VMHandler) CreateService(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if userID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	var req struct {
		Name     string `json:"name"`
		CPU      int    `json:"cpu"`
		MemoryMB int    `json:"memory_mb"`
		DiskGB   int    `json:"disk_gb"`
		OSImage  string `json:"os_image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.CPU <= 0 { req.CPU = 2 }
	if req.MemoryMB <= 0 { req.MemoryMB = 2048 }
	if req.DiskGB <= 0 { req.DiskGB = 50 }
	if req.CPU > 32 || req.MemoryMB > 65536 || req.DiskGB > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "resource limits exceeded"})
		return
	}
	if req.OSImage == "" { req.OSImage = "images:ubuntu/24.04/cloud" }

	sshKeys, _ := h.sshKeys.GetByUser(r.Context(), userID)

	if h.clusters == nil || len(h.clusters.List()) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters available"})
		return
	}

	client := h.clusters.List()[0]
	cc, _ := h.clusters.ConfigByName(client.Name)

	ip, gateway, cidr := "", "", ""
	if len(cc.IPPools) > 0 {
		p := cc.IPPools[0]
		gateway = p.Gateway
		cidr = extractCIDR(p.CIDR)
		ip = pickNextIP(r.Context(), h.vmSvc, client.Name, cc.DefaultProject, p.Range)
	}
	if ip == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no available IPs"})
		return
	}

	defPool := cc.StoragePool
	if defPool == "" { defPool = "ceph-pool" }
	defNet := cc.Network
	if defNet == "" { defNet = "br-pub" }

	result, err := h.vmSvc.Create(r.Context(), service.CreateVMParams{
		ClusterName: client.Name,
		Project:     cc.DefaultProject,
		UserID:      userID,
		VMName:      req.Name,
		CPU:         req.CPU,
		MemoryMB:    req.MemoryMB,
		DiskGB:      req.DiskGB,
		OSImage:     req.OSImage,
		SSHKeys:     sshKeys,
		IP:          ip,
		Gateway:     gateway,
		SubnetCIDR:  cidr,
		StoragePool: defPool,
		Network:     defNet,
	})
	if err != nil {
		slog.Error("user create VM failed", "user_id", userID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	vm := &model.VM{
		Name:      result.VMName,
		ClusterID: h.clusters.IDByName(client.Name),
		UserID:    userID,
		Status:    model.VMStatusRunning,
		CPU:       req.CPU,
		MemoryMB:  req.MemoryMB,
		DiskGB:    req.DiskGB,
		OSImage:   req.OSImage,
		Node:      result.Node,
		Password:  &result.Password,
	}
	if result.IP != "" {
		vm.IP = &result.IP
	}
	if err := h.vmRepo.Create(r.Context(), vm); err != nil {
		slog.Error("vm row insert failed", "name", result.VMName, "error", err)
	} else {
		attachIPToVM(r.Context(), result.IP, vm.ID)
	}
	audit(r.Context(), r, "vm.create", "vm", vm.ID, map[string]any{"name": result.VMName, "ip": result.IP})

	writeJSON(w, http.StatusCreated, result)
}

// findClusterName resolves the DB cluster_id to its config name via the manager map.
// Falls back to the first available cluster for legacy rows where the id predates seeding.
func findClusterName(mgr *cluster.Manager, clusterID int64) string {
	if mgr == nil {
		return ""
	}
	if name := mgr.NameByID(clusterID); name != "" {
		return name
	}
	if clients := mgr.List(); len(clients) > 0 {
		return clients[0].Name
	}
	return ""
}

// defaultProjectForMgr returns the default project of the first configured cluster.
// Used by DTO builders so single-cluster deployments keep their project hint.
func defaultProjectForMgr(mgr *cluster.Manager) string {
	if mgr == nil {
		return ""
	}
	clients := mgr.List()
	if len(clients) == 0 {
		return ""
	}
	if cc, ok := mgr.ConfigByName(clients[0].Name); ok {
		return cc.DefaultProject
	}
	return ""
}

type AdminVMHandler struct {
	vmSvc     *service.VMService
	vmRepo    *repository.VMRepo
	sshKeys   *repository.SSHKeyRepo
	clusters  *cluster.Manager
	scheduler *cluster.Scheduler
}

func NewAdminVMHandler(vmSvc *service.VMService, vmRepo *repository.VMRepo, sshKeys *repository.SSHKeyRepo, clusters *cluster.Manager, scheduler *cluster.Scheduler) *AdminVMHandler {
	return &AdminVMHandler{vmSvc: vmSvc, vmRepo: vmRepo, sshKeys: sshKeys, clusters: clusters, scheduler: scheduler}
}

func (h *AdminVMHandler) Routes(r chi.Router) {
	r.Get("/clusters", h.ListClusters)
	r.Get("/clusters/{name}/nodes", h.ListNodes)
	r.Get("/clusters/{name}/projects", h.ListProjects)
	r.Get("/clusters/{name}/vms", h.ListClusterVMs)
	r.Get("/clusters/{name}/vms/{vmName}", h.GetClusterVMDetail)
	r.Post("/clusters/{name}/vms", h.CreateVM)
	r.Get("/clusters/{name}/ha", h.GetHAStatus)
	r.Post("/clusters/{name}/nodes/{node}/evacuate", h.EvacuateNode)
	r.Post("/clusters/{name}/nodes/{node}/restore", h.RestoreNode)
	r.Get("/vms", h.ListAllVMs)
	r.Put("/vms/{name}/state", h.ChangeVMState)
	r.Post("/vms/{name}/reinstall", h.ReinstallVM)
	r.Post("/vms/{name}/migrate", h.MigrateVM)
	r.Post("/vms/{name}/reset-password", h.ResetPasswordAdmin)
	r.Delete("/vms/{name}", h.DeleteVM)
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

func (h *AdminVMHandler) GetHAStatus(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	cc, _ := h.clusters.ConfigByName(clusterName)

	members, err := client.GetClusterMembers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	type memberStatus struct {
		Name    string `json:"server_name"`
		URL     string `json:"url"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Roles   string `json:"roles"`
	}

	var nodes []memberStatus
	for _, raw := range members {
		var m memberStatus
		json.Unmarshal(raw, &m)
		nodes = append(nodes, m)
	}
	if nodes == nil {
		nodes = []memberStatus{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":            clusterName,
		"healing_threshold":  300,
		"storage":            cc.StoragePool,
		"nodes":              nodes,
		"ha_enabled":         true,
	})
}

func (h *AdminVMHandler) EvacuateNode(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodeName := chi.URLParam(r, "node")
	if !isValidName(nodeName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid node name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"action": "evacuate"})
	path := fmt.Sprintf("/1.0/cluster/members/%s/state", nodeName)
	resp, err := client.APIPost(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		slog.Error("evacuate node failed", "node", nodeName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			client.WaitForOperation(r.Context(), op.ID)
		}
	}

	audit(r.Context(), r, "node.evacuate", "node", 0, map[string]any{"cluster": clusterName, "node": nodeName})
	slog.Info("node evacuated", "cluster", clusterName, "node", nodeName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "evacuated", "node": nodeName})
}

func (h *AdminVMHandler) RestoreNode(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodeName := chi.URLParam(r, "node")
	if !isValidName(nodeName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid node name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"action": "restore"})
	path := fmt.Sprintf("/1.0/cluster/members/%s/state", nodeName)
	resp, err := client.APIPost(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		slog.Error("restore node failed", "node", nodeName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if resp.Type == "async" {
		var op struct{ ID string }
		json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			client.WaitForOperation(r.Context(), op.ID)
		}
	}

	audit(r.Context(), r, "node.restore", "node", 0, map[string]any{"cluster": clusterName, "node": nodeName})
	slog.Info("node restored", "cluster", clusterName, "node", nodeName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored", "node": nodeName})
}

var (
	vmListCacheMu sync.RWMutex
	vmListCache   map[string]vmListCacheEntry
)

type vmListCacheEntry struct {
	vms     []json.RawMessage
	updated time.Time
}

func (h *AdminVMHandler) ListClusterVMs(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	projects := make([]string, 0, len(cc.Projects))
	for _, proj := range cc.Projects {
		projects = append(projects, proj.Name)
	}
	if len(projects) == 0 {
		projects = []string{"default"}
	}

	var allInstances []json.RawMessage
	var fetchErr error
	for _, proj := range projects {
		instances, err := h.vmSvc.ListInstances(r.Context(), clusterName, proj)
		if err != nil {
			slog.Warn("list instances failed", "cluster", clusterName, "project", proj, "error", err)
			fetchErr = err
			continue
		}
		allInstances = append(allInstances, instances...)
	}

	// Inject DB-recorded IPs into each instance payload so the UI shows an IP
	// even before Incus reports the `state.network` block.
	allInstances = h.injectDBIPs(r.Context(), allInstances)

	// 如果成功获取到数据，更新缓存
	if len(allInstances) > 0 || fetchErr == nil {
		vmListCacheMu.Lock()
		if vmListCache == nil {
			vmListCache = make(map[string]vmListCacheEntry)
		}
		vmListCache[clusterName] = vmListCacheEntry{vms: allInstances, updated: time.Now()}
		vmListCacheMu.Unlock()
	}

	// 如果完全失败且有缓存，使用缓存
	if len(allInstances) == 0 && fetchErr != nil {
		vmListCacheMu.RLock()
		cached, hasCached := vmListCache[clusterName]
		vmListCacheMu.RUnlock()
		if hasCached {
			writeJSON(w, http.StatusOK, map[string]any{
				"vms": cached.vms, "count": len(cached.vms),
				"stale": true, "cached_at": cached.updated.Format(time.RFC3339),
				"error": "cluster unreachable, showing cached data",
			})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "cluster unreachable: " + fetchErr.Error(),
			"vms": []any{}, "count": 0,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"vms": allInstances, "count": len(allInstances)})
}

// injectDBIPs decodes each instance payload, looks up the DB-recorded IP by VM name,
// and re-encodes with an `ip` field when a match is found. On any decode / lookup
// failure we silently keep the original payload — the UI already falls back to the
// Incus state.network block.
func (h *AdminVMHandler) injectDBIPs(ctx context.Context, instances []json.RawMessage) []json.RawMessage {
	if len(instances) == 0 || h.vmRepo == nil {
		return instances
	}
	decoded := make([]map[string]any, 0, len(instances))
	names := make([]string, 0, len(instances))
	for _, raw := range instances {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return instances
		}
		decoded = append(decoded, m)
		if n, ok := m["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}
	ipByName, err := h.vmRepo.IPsByNames(ctx, names)
	if err != nil {
		slog.Warn("lookup VM IPs from DB failed", "error", err)
		return instances
	}
	out := make([]json.RawMessage, len(decoded))
	for i, m := range decoded {
		if n, ok := m["name"].(string); ok {
			if ip, hit := ipByName[n]; hit {
				m["ip"] = ip
			}
		}
		buf, err := json.Marshal(m)
		if err != nil {
			return instances
		}
		out[i] = buf
	}
	return out
}

func (h *AdminVMHandler) CreateVM(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	var req struct {
		CPU          int      `json:"cpu"`
		MemoryMB     int      `json:"memory_mb"`
		DiskGB       int      `json:"disk_gb"`
		OSImage      string   `json:"os_image"`
		Project      string   `json:"project"`
		SSHKeys      []string `json:"ssh_keys"`
		TargetUserID int64    `json:"target_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	if req.CPU == 0 { req.CPU = 2 }
	if req.MemoryMB == 0 { req.MemoryMB = 2048 }
	if req.DiskGB == 0 { req.DiskGB = 50 }
	if req.OSImage == "" { req.OSImage = "images:ubuntu/24.04/cloud" }
	if req.Project == "" { req.Project = "default" }

	pool := cc.StoragePool
	if pool == "" { pool = "ceph-pool" }
	network := cc.Network
	if network == "" { network = "br-pub" }

	ip, gateway, cidr, err := allocateIP(r.Context(), cc, 0)
	if err != nil {
		slog.Error("allocate IP failed", "cluster", clusterName, "error", err)
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no available IPs: " + err.Error()})
		return
	}

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if req.TargetUserID > 0 {
		userID = req.TargetUserID
	}

	sshKeys, _ := h.sshKeys.GetByUser(r.Context(), userID)
	if len(req.SSHKeys) == 0 && len(sshKeys) > 0 {
		req.SSHKeys = sshKeys
	}

	result, err := h.vmSvc.Create(r.Context(), service.CreateVMParams{
		ClusterName: clusterName,
		Project:     req.Project,
		UserID:      userID,
		CPU:         req.CPU,
		MemoryMB:    req.MemoryMB,
		DiskGB:      req.DiskGB,
		OSImage:     req.OSImage,
		SSHKeys:     req.SSHKeys,
		IP:          ip,
		Gateway:     gateway,
		SubnetCIDR:  cidr,
		StoragePool: pool,
		Network:     network,
	})
	if err != nil {
		slog.Error("create VM failed", "cluster", clusterName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	vm := &model.VM{
		Name:      result.VMName,
		ClusterID: h.clusters.IDByName(clusterName),
		UserID:    userID,
		Status:    model.VMStatusRunning,
		CPU:       req.CPU,
		MemoryMB:  req.MemoryMB,
		DiskGB:    req.DiskGB,
		OSImage:   req.OSImage,
		Node:      result.Node,
		Password:  &result.Password,
	}
	if result.IP != "" {
		vm.IP = &result.IP
	}
	if err := h.vmRepo.Create(r.Context(), vm); err != nil {
		slog.Error("vm row insert failed", "name", result.VMName, "error", err)
	} else {
		attachIPToVM(r.Context(), result.IP, vm.ID)
	}
	audit(r.Context(), r, "vm.create", "vm", vm.ID, map[string]any{"name": result.VMName, "ip": result.IP, "admin": true})

	slog.Info("VM created via admin", "vm", result.VMName, "ip", result.IP)
	writeJSON(w, http.StatusCreated, result)
}

func (h *AdminVMHandler) ListAllVMs(w http.ResponseWriter, r *http.Request) {
	var allInstances []json.RawMessage
	for _, client := range h.clusters.List() {
		cc, ok := h.clusters.ConfigByName(client.Name)
		if !ok {
			continue
		}
		for _, proj := range cc.Projects {
			instances, err := h.vmSvc.ListInstances(r.Context(), client.Name, proj.Name)
			if err != nil {
				continue
			}
			allInstances = append(allInstances, instances...)
		}
		if len(cc.Projects) == 0 {
			instances, _ := h.vmSvc.ListInstances(r.Context(), client.Name, "default")
			allInstances = append(allInstances, instances...)
		}
	}
	if allInstances == nil {
		allInstances = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": allInstances, "count": len(allInstances)})
}

func (h *AdminVMHandler) ChangeVMState(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Action  string `json:"action"`
		Force   bool   `json:"force"`
		Cluster string `json:"cluster"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Cluster == "" {
		clients := h.clusters.List()
		if len(clients) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
			return
		}
		req.Cluster = clients[0].Name
	}
	if req.Project == "" { req.Project = "default" }

	err := h.vmSvc.ChangeState(r.Context(), req.Cluster, req.Project, vmName, req.Action, req.Force)
	if err != nil {
		slog.Error("vm state change failed", "vm", vmName, "action", req.Action, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	slog.Info("vm state changed", "vm", vmName, "action", req.Action)
	audit(r.Context(), r, "vm."+req.Action, "vm", h.vmIDByName(r.Context(), vmName), map[string]any{"name": vmName})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": req.Action})
}

// vmIDByName returns the DB id for a VM name, or 0 if the VM is not in our DB
// (e.g. legacy VMs created outside the portal). Used to populate audit.target_id
// so the audit log can link back to the VM instead of always recording 0.
func (h *AdminVMHandler) vmIDByName(ctx context.Context, name string) int64 {
	if h.vmRepo == nil {
		return 0
	}
	v, err := h.vmRepo.GetByName(ctx, name)
	if err != nil || v == nil {
		return 0
	}
	return v.ID
}

func (h *AdminVMHandler) DeleteVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	clusterParam := r.URL.Query().Get("cluster")
	projectParam := r.URL.Query().Get("project")
	if clusterParam == "" {
		clients := h.clusters.List()
		if len(clients) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
			return
		}
		clusterParam = clients[0].Name
	}
	if projectParam == "" { projectParam = "default" }

	req := struct{ Cluster, Project string }{clusterParam, projectParam}

	err := h.vmSvc.Delete(r.Context(), req.Cluster, req.Project, vmName)
	if err != nil {
		slog.Error("delete VM failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	var deletedID int64
	if dbVM, _ := h.vmRepo.GetByName(r.Context(), vmName); dbVM != nil {
		deletedID = dbVM.ID
		h.vmRepo.Delete(r.Context(), dbVM.ID)
	}

	slog.Info("vm deleted", "vm", vmName)
	audit(r.Context(), r, "vm.delete", "vm", deletedID, map[string]any{"name": vmName})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (h *AdminVMHandler) ReinstallVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Cluster string `json:"cluster"`
		Project string `json:"project"`
		OSImage string `json:"os_image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Cluster == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cluster required"})
		return
	}
	if req.Project == "" {
		req.Project = "default"
	}
	if req.OSImage == "" {
		req.OSImage = "images:ubuntu/24.04/cloud"
	}

	result, err := h.vmSvc.Reinstall(r.Context(), service.ReinstallParams{
		ClusterName: req.Cluster,
		Project:     req.Project,
		VMName:      vmName,
		NewOSImage:  req.OSImage,
	})
	if err != nil {
		slog.Error("reinstall VM failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	slog.Info("vm reinstalled", "vm", vmName, "os", req.OSImage)
	audit(r.Context(), r, "vm.reinstall", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{"name": vmName, "os": req.OSImage})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "reinstalled",
		"password": result.Password,
		"username": result.Username,
	})
}

// MigrateVM 将单个 VM 迁移到指定目标节点
func (h *AdminVMHandler) ResetPasswordAdmin(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	var req struct {
		Cluster  string `json:"cluster"`
		Project  string `json:"project"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Cluster == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cluster required"})
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}
	if req.Username == "" {
		req.Username = "ubuntu"
	}

	newPassword, err := h.vmSvc.ResetPassword(r.Context(), req.Cluster, req.Project, vmName, req.Username)
	if err != nil {
		slog.Error("admin reset password failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "password reset failed: " + err.Error()})
		return
	}

	audit(r.Context(), r, "vm.reset_password", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{"name": vmName, "username": req.Username})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "password_reset",
		"password": newPassword,
		"username": req.Username,
	})
}

func (h *AdminVMHandler) MigrateVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Cluster    string `json:"cluster"`
		Project    string `json:"project"`
		TargetNode string `json:"target_node"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Cluster == "" || req.TargetNode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cluster and target_node required"})
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}

	client, ok := h.clusters.Get(req.Cluster)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	// Incus 迁移：POST /1.0/instances/{name}?project={project}&target={node}
	// 请求体需包含实例当前配置（空 body 触发迁移）
	migrateBody := fmt.Sprintf(`{"name":"%s","migration":true}`, vmName)
	path := fmt.Sprintf("/1.0/instances/%s?project=%s&target=%s", vmName, req.Project, req.TargetNode)
	resp, err := client.APIPost(r.Context(), path, strings.NewReader(migrateBody))
	if err != nil {
		slog.Error("migrate VM failed", "vm", vmName, "target", req.TargetNode, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "migration failed: " + err.Error()})
		return
	}

	// 等待异步操作完成
	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		parts := strings.Split(resp.Operation, "/")
		opID := parts[len(parts)-1]
		if opID != "" {
			_ = client.WaitForOperation(r.Context(), opID)
		}
	}

	audit(r.Context(), r, "vm.migrate", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{
		"name": vmName, "target": req.TargetNode, "cluster": req.Cluster,
	})
	slog.Info("vm migrated", "vm", vmName, "target", req.TargetNode)
	writeJSON(w, http.StatusOK, map[string]any{"status": "migrated", "target": req.TargetNode})
}

// ListProjects 返回集群实际存在的 project 列表（来自 Incus `/1.0/projects?recursion=1`）。
// 批次 8 B-1：前端 ProjectPicker 依赖此端点，不再硬编码 `customers/default`。
func (h *AdminVMHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	resp, err := client.APIGet(r.Context(), "/1.0/projects?recursion=1")
	if err != nil {
		slog.Error("list projects failed", "cluster", clusterName, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "list projects failed: " + err.Error()})
		return
	}
	var raw []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Config      map[string]string `json:"config"`
	}
	if err := json.Unmarshal(resp.Metadata, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "parse projects: " + err.Error()})
		return
	}
	projects := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		projects = append(projects, map[string]any{
			"name":        p.Name,
			"description": p.Description,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// GetClusterVMDetail 返回单台 VM 的合并详情：incus instance + state + snapshots + DB 行。
// 批次 8 B-2：前端 AdminVMDetail 从 O(N) 列表扫描改为单端点。
// 查询参数 `?project=<name>` 可选；若省略则在该集群的已配置 projects 中逐一查找。
func (h *AdminVMHandler) GetClusterVMDetail(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	vmName := chi.URLParam(r, "vmName")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	cc, _ := h.clusters.ConfigByName(clusterName)

	candidates := make([]string, 0, len(cc.Projects)+2)
	if p := r.URL.Query().Get("project"); p != "" {
		candidates = append(candidates, p)
	} else {
		for _, pc := range cc.Projects {
			candidates = append(candidates, pc.Name)
		}
		if len(candidates) == 0 {
			candidates = []string{"customers", "default"}
		}
	}

	var (
		instance json.RawMessage
		state    json.RawMessage
		project  string
		lastErr  error
	)
	for _, p := range candidates {
		raw, err := client.GetInstance(r.Context(), p, vmName)
		if err != nil {
			lastErr = err
			continue
		}
		instance = raw
		project = p
		if s, serr := client.GetInstanceState(r.Context(), p, vmName); serr == nil {
			state = s
		}
		break
	}
	if instance == nil {
		status := http.StatusNotFound
		msg := "vm not found"
		if lastErr != nil {
			msg = lastErr.Error()
		}
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}

	snapshotsPath := fmt.Sprintf("/1.0/instances/%s/snapshots?project=%s&recursion=1", vmName, project)
	snapResp, snapErr := client.APIGet(r.Context(), snapshotsPath)
	var snapshots json.RawMessage
	if snapErr == nil {
		snapshots = snapResp.Metadata
	}

	var dbRow *model.VM
	if h.vmRepo != nil {
		dbRow, _ = h.vmRepo.GetByName(r.Context(), vmName)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vm":        instance,
		"state":     state,
		"snapshots": snapshots,
		"project":   project,
		"db":        dbRow,
	})
}

func extractCIDR(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return "27"
}

var (
	ipCacheMu      sync.Mutex
	ipCacheData    map[string]bool
	ipCacheUpdated time.Time
)

func pickNextIP(ctx context.Context, vmSvc *service.VMService, clusterName, project, ipRange string) string {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return ""
	}
	startParts := strings.Split(strings.TrimSpace(parts[0]), ".")
	endParts := strings.Split(strings.TrimSpace(parts[1]), ".")
	if len(startParts) != 4 || len(endParts) != 4 {
		return ""
	}

	ipCacheMu.Lock()
	usedIPs := ipCacheData
	if usedIPs == nil || time.Since(ipCacheUpdated) > 60*time.Second {
		instances, _ := vmSvc.ListInstances(ctx, clusterName, project)
		usedIPs = make(map[string]bool)
		for _, raw := range instances {
			var inst struct {
				State struct {
					Network map[string]struct {
						Addresses []struct {
							Address string `json:"address"`
							Family  string `json:"family"`
							Scope   string `json:"scope"`
						} `json:"addresses"`
					} `json:"network"`
				} `json:"state"`
			}
			json.Unmarshal(raw, &inst)
			for nic, data := range inst.State.Network {
				if nic == "lo" { continue }
				for _, addr := range data.Addresses {
					if addr.Family == "inet" && addr.Scope == "global" {
						usedIPs[addr.Address] = true
					}
				}
			}
		}
		ipCacheData = usedIPs
		ipCacheUpdated = time.Now()
	}
	ipCacheMu.Unlock()

	prefix := strings.Join(startParts[:3], ".")
	start := atoi(startParts[3])
	end := atoi(endParts[3])

	for i := start; i <= end; i++ {
		ip := fmt.Sprintf("%s.%d", prefix, i)
		if !usedIPs[ip] {
			return ip
		}
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			if isNilSlice(val) {
				m[k] = []any{}
			}
		}
	}
	json.NewEncoder(w).Encode(v)
}

func isNilSlice(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.IsNil()
}
