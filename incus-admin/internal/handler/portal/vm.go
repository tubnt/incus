package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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
	r.Post("/clusters/{name}/vms", h.CreateVM)
	r.Get("/vms", h.ListAllVMs)
	r.Put("/vms/{name}/state", h.ChangeVMState)
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

func (h *AdminVMHandler) CreateVM(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	var req struct {
		CPU      int      `json:"cpu"`
		MemoryMB int      `json:"memory_mb"`
		DiskGB   int      `json:"disk_gb"`
		OSImage  string   `json:"os_image"`
		Project  string   `json:"project"`
		SSHKeys  []string `json:"ssh_keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	if req.CPU == 0 { req.CPU = 2 }
	if req.MemoryMB == 0 { req.MemoryMB = 2048 }
	if req.DiskGB == 0 { req.DiskGB = 50 }
	if req.OSImage == "" { req.OSImage = "images:ubuntu/24.04/cloud" }
	if req.Project == "" { req.Project = "customers" }

	ip, gateway, cidr, pool, network := "", "", "", "ceph-pool", "br-pub"
	if len(cc.IPPools) > 0 {
		p := cc.IPPools[0]
		gateway = p.Gateway
		cidr = extractCIDR(p.CIDR)
		ip = pickNextIP(r.Context(), h.vmSvc, clusterName, req.Project, p.Range)
	}
	if ip == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no available IPs"})
		return
	}

	result, err := h.vmSvc.Create(r.Context(), service.CreateVMParams{
		ClusterName: clusterName,
		Project:     req.Project,
		UserID:      0,
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

	slog.Info("VM created via admin", "vm", result.VMName, "ip", result.IP)
	writeJSON(w, http.StatusCreated, result)
}

func (h *AdminVMHandler) ListAllVMs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"vms": []any{}})
}

func (h *AdminVMHandler) ChangeVMState(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
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
	if req.Cluster == "" { req.Cluster = h.clusters.List()[0].Name }
	if req.Project == "" { req.Project = "customers" }

	err := h.vmSvc.ChangeState(r.Context(), req.Cluster, req.Project, vmName, req.Action, req.Force)
	if err != nil {
		slog.Error("vm state change failed", "vm", vmName, "action", req.Action, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	slog.Info("vm state changed", "vm", vmName, "action", req.Action)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": req.Action})
}

func (h *AdminVMHandler) DeleteVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	var req struct {
		Cluster string `json:"cluster"`
		Project string `json:"project"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Cluster == "" { req.Cluster = h.clusters.List()[0].Name }
	if req.Project == "" { req.Project = "customers" }

	err := h.vmSvc.Delete(r.Context(), req.Cluster, req.Project, vmName)
	if err != nil {
		slog.Error("delete VM failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	slog.Info("vm deleted", "vm", vmName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func extractCIDR(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return "27"
}

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

	instances, _ := vmSvc.ListInstances(ctx, clusterName, project)
	usedIPs := make(map[string]bool)
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
	json.NewEncoder(w).Encode(v)
}
