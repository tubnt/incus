package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/config"
)

type IPPoolHandler struct {
	clusters *cluster.Manager
}

func NewIPPoolHandler(clusters *cluster.Manager) *IPPoolHandler {
	return &IPPoolHandler{clusters: clusters}
}

func (h *IPPoolHandler) AdminRoutes(r chi.Router) {
	r.Get("/ip-pools", h.ListPools)
	r.Post("/ip-pools", h.AddPool)
	r.Delete("/ip-pools/{cluster}", h.RemovePool)
}

func (h *IPPoolHandler) AddPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string `json:"cluster"`
		CIDR    string `json:"cidr"`
		Gateway string `json:"gateway"`
		Range   string `json:"range"`
		VLAN    int    `json:"vlan"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Cluster == "" || req.CIDR == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cluster, cidr required"})
		return
	}

	cc, ok := h.clusters.ConfigByName(req.Cluster)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	newPool := config.IPPoolConfig{
		CIDR:    req.CIDR,
		Gateway: req.Gateway,
		Range:   req.Range,
		VLAN:    req.VLAN,
	}
	cc.IPPools = append(cc.IPPools, newPool)
	h.clusters.UpdateConfig(req.Cluster, cc)

	audit(r.Context(), r, "ippool.add", "ippool", 0, map[string]any{"cluster": req.Cluster, "cidr": req.CIDR})
	writeJSON(w, http.StatusCreated, map[string]any{"status": "added"})
}

func (h *IPPoolHandler) RemovePool(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "cluster")
	cidr := r.URL.Query().Get("cidr")
	if cidr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cidr required"})
		return
	}

	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	var updated []config.IPPoolConfig
	for _, p := range cc.IPPools {
		if p.CIDR != cidr {
			updated = append(updated, p)
		}
	}
	cc.IPPools = updated
	h.clusters.UpdateConfig(clusterName, cc)

	audit(r.Context(), r, "ippool.remove", "ippool", 0, map[string]any{"cluster": clusterName, "cidr": cidr})
	writeJSON(w, http.StatusOK, map[string]any{"status": "removed"})
}

func (h *IPPoolHandler) ListPools(w http.ResponseWriter, r *http.Request) {
	if h.clusters == nil {
		writeJSON(w, http.StatusOK, map[string]any{"pools": []any{}})
		return
	}

	type poolInfo struct {
		ClusterName string `json:"cluster_name"`
		CIDR        string `json:"cidr"`
		Gateway     string `json:"gateway"`
		VLAN        int    `json:"vlan"`
		Range       string `json:"range"`
		Total       int    `json:"total"`
		Used        int    `json:"used"`
		Available   int    `json:"available"`
	}

	var pools []poolInfo
	for _, client := range h.clusters.List() {
		cc, ok := h.clusters.ConfigByName(client.Name)
		if !ok {
			continue
		}
		for _, p := range cc.IPPools {
			total, used := countIPs(r.Context(), h.clusters, client.Name, p.Range)
			pools = append(pools, poolInfo{
				ClusterName: client.DisplayName,
				CIDR:        p.CIDR,
				Gateway:     p.Gateway,
				VLAN:        p.VLAN,
				Range:       p.Range,
				Total:       total,
				Used:        used,
				Available:   total - used,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"pools": pools})
}

func countIPs(ctx context.Context, mgr *cluster.Manager, clusterName, ipRange string) (total, used int) {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return 0, 0
	}
	startParts := strings.Split(strings.TrimSpace(parts[0]), ".")
	endParts := strings.Split(strings.TrimSpace(parts[1]), ".")
	if len(startParts) != 4 || len(endParts) != 4 {
		return 0, 0
	}
	start := atoi(startParts[3])
	end := atoi(endParts[3])
	total = end - start + 1

	client, ok := mgr.Get(clusterName)
	if !ok {
		return total, 0
	}

	cc, _ := mgr.ConfigByName(clusterName)
	usedIPs := make(map[string]bool)
	for _, proj := range cc.Projects {
		instances, err := client.GetInstances(ctx, proj.Name)
		if err != nil {
			continue
		}
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
				if nic == "lo" {
					continue
				}
				for _, addr := range data.Addresses {
					if addr.Family == "inet" && addr.Scope == "global" {
						usedIPs[addr.Address] = true
					}
				}
			}
		}
	}

	prefix := strings.Join(startParts[:3], ".")
	for i := start; i <= end; i++ {
		ip := fmt.Sprintf("%s.%d", prefix, i)
		if usedIPs[ip] {
			used++
		}
	}
	return total, used
}
