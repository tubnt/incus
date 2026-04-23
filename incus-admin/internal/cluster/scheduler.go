package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type NodeInfo struct {
	Name      string  `json:"server_name"`
	Status    string  `json:"status"`
	Message   string  `json:"message"`
	CPUTotal  int     `json:"cpu_total"`
	MemTotal  int64   `json:"mem_total"`
	MemUsed   int64   `json:"mem_used"`
	MemFree   int64   `json:"mem_free"`
	FreeRatio float64 `json:"free_ratio"`
}

type Scheduler struct {
	mu    sync.RWMutex
	cache map[string][]NodeInfo // cluster name -> nodes
	mgr   *Manager
}

func NewScheduler(mgr *Manager) *Scheduler {
	s := &Scheduler{
		cache: make(map[string][]NodeInfo),
		mgr:   mgr,
	}
	go s.refreshLoop()
	return s
}

func (s *Scheduler) PickNode(clusterName string) (string, error) {
	s.mu.RLock()
	nodes := s.cache[clusterName]
	s.mu.RUnlock()

	if len(nodes) == 0 {
		if err := s.refreshCluster(clusterName); err != nil {
			return "", fmt.Errorf("refresh nodes: %w", err)
		}
		s.mu.RLock()
		nodes = s.cache[clusterName]
		s.mu.RUnlock()
	}

	var candidates []NodeInfo
	for _, n := range nodes {
		if n.Status == "Online" && n.Message == "Fully operational" {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no available nodes in cluster %s", clusterName)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].FreeRatio > candidates[j].FreeRatio
	})

	return candidates[0].Name, nil
}

func (s *Scheduler) GetNodes(clusterName string) []NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[clusterName]
}

func (s *Scheduler) refreshLoop() {
	s.refreshAll()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.refreshAll()
	}
}

func (s *Scheduler) refreshAll() {
	for _, client := range s.mgr.List() {
		if err := s.refreshCluster(client.Name); err != nil {
			slog.Warn("refresh cluster nodes failed", "cluster", client.Name, "error", err)
		}
	}
}

func (s *Scheduler) refreshCluster(clusterName string) error {
	client, ok := s.mgr.Get(clusterName)
	if !ok {
		return fmt.Errorf("cluster %q not found", clusterName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	members, err := client.GetClusterMembers(ctx)
	if err != nil {
		return err
	}

	var nodes []NodeInfo
	for _, raw := range members {
		var member struct {
			ServerName string `json:"server_name"`
			Status     string `json:"status"`
			Message    string `json:"message"`
		}
		_ = json.Unmarshal(raw, &member)

		info := NodeInfo{
			Name:    member.ServerName,
			Status:  member.Status,
			Message: member.Message,
		}

		resPath := fmt.Sprintf("/1.0/resources?target=%s", member.ServerName)
		if resp, err := client.APIGet(ctx, resPath); err == nil {
			var res struct {
				CPU struct {
					Total int `json:"total"`
				} `json:"cpu"`
				Memory struct {
					Total int64 `json:"total"`
					Used  int64 `json:"used"`
				} `json:"memory"`
			}
			_ = json.Unmarshal(resp.Metadata, &res)
			info.CPUTotal = res.CPU.Total
			info.MemTotal = res.Memory.Total
			info.MemUsed = res.Memory.Used
			info.MemFree = res.Memory.Total - res.Memory.Used
			if res.Memory.Total > 0 {
				info.FreeRatio = float64(info.MemFree) / float64(res.Memory.Total)
			}
		}

		nodes = append(nodes, info)
	}

	s.mu.Lock()
	s.cache[clusterName] = nodes
	s.mu.Unlock()

	return nil
}
