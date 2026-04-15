package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

type IPPoolHandler struct {
	clusters *cluster.Manager
}

func NewIPPoolHandler(clusters *cluster.Manager) *IPPoolHandler {
	return &IPPoolHandler{clusters: clusters}
}

func (h *IPPoolHandler) AdminRoutes(r chi.Router) {
	r.Get("/ip-pools", h.ListPools)
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
