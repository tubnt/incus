package portal

import (
	"bufio"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type metricsCache struct {
	mu      sync.RWMutex
	data    map[string]map[string]*VMMetric
	updated time.Time
}

func (c *metricsCache) get(clusterName string) (map[string]*VMMetric, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.updated) > 30*time.Second {
		return nil, false
	}
	vms, ok := c.data[clusterName]
	return vms, ok
}

func (c *metricsCache) set(clusterName string, vms map[string]*VMMetric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data == nil {
		c.data = make(map[string]map[string]*VMMetric)
	}
	c.data[clusterName] = vms
	c.updated = time.Now()
}

type MetricsHandler struct {
	clusters *cluster.Manager
	vmRepo   *repository.VMRepo
	cache    metricsCache
}

func NewMetricsHandler(clusters *cluster.Manager, vmRepo *repository.VMRepo) *MetricsHandler {
	return &MetricsHandler{clusters: clusters, vmRepo: vmRepo}
}

func (h *MetricsHandler) AdminRoutes(r chi.Router) {
	r.Get("/metrics/overview", h.ClusterOverview)
	r.Get("/metrics/vm/{name}", h.VMMetrics)
}

func (h *MetricsHandler) PortalRoutes(r chi.Router) {
	r.Get("/metrics/vm/{name}", h.PortalVMMetrics)
}

func (h *MetricsHandler) PortalVMMetrics(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if h.vmRepo != nil {
		vm, err := h.vmRepo.GetByName(r.Context(), vmName)
		if err != nil || vm == nil || vm.UserID != userID {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
			return
		}
	}
	h.VMMetrics(w, r)
}

type VMMetric struct {
	Name          string  `json:"name"`
	CPUUserPct    float64 `json:"cpu_user_pct"`
	CPUSystemPct  float64 `json:"cpu_system_pct"`
	CPUIdlePct    float64 `json:"cpu_idle_pct"`
	MemTotalBytes int64   `json:"mem_total_bytes"`
	MemUsedBytes  int64   `json:"mem_used_bytes"`
	MemUsedPct    float64 `json:"mem_used_pct"`
	DiskTotalBytes int64  `json:"disk_total_bytes"`
	DiskUsedBytes  int64  `json:"disk_used_bytes"`
	DiskUsedPct    float64 `json:"disk_used_pct"`
	NetRxBytes    int64   `json:"net_rx_bytes"`
	NetTxBytes    int64   `json:"net_tx_bytes"`
}

func (h *MetricsHandler) fetchVMs(r *http.Request, clusterName string) (map[string]*VMMetric, error) {
	if vms, ok := h.cache.get(clusterName); ok {
		return vms, nil
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		return nil, nil
	}
	text, err := client.RawGet(r.Context(), "/1.0/metrics")
	if err != nil {
		return nil, err
	}
	vms := parseMetricsForAllVMs(text)
	h.cache.set(clusterName, vms)
	return vms, nil
}

func (h *MetricsHandler) VMMetrics(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" && len(h.clusters.List()) > 0 {
		clusterName = h.clusters.List()[0].Name
	}

	allVMs, err := h.fetchVMs(r, clusterName)
	if err != nil {
		slog.Error("fetch metrics failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "metrics unavailable"})
		return
	}

	if m, ok := allVMs[vmName]; ok {
		writeJSON(w, http.StatusOK, map[string]any{"metrics": m})
	} else {
		writeJSON(w, http.StatusOK, map[string]any{"metrics": nil, "note": "no metrics for this VM"})
	}
}

func (h *MetricsHandler) ClusterOverview(w http.ResponseWriter, r *http.Request) {
	if h.clusters == nil || len(h.clusters.List()) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"clusters": []any{}})
		return
	}

	type clusterMetrics struct {
		Name string      `json:"name"`
		VMs  []*VMMetric `json:"vms"`
	}

	var results []clusterMetrics
	for _, ci := range h.clusters.List() {
		vmMap, err := h.fetchVMs(r, ci.Name)
		if err != nil {
			slog.Error("fetch metrics failed", "cluster", ci.Name, "error", err)
			continue
		}
		vms := make([]*VMMetric, 0, len(vmMap))
		for name, m := range vmMap {
			m.Name = name
			vms = append(vms, m)
		}
		results = append(results, clusterMetrics{Name: ci.Name, VMs: vms})
	}

	writeJSON(w, http.StatusOK, map[string]any{"clusters": results})
}

type rawMetrics struct {
	cpuUser    float64
	cpuSystem  float64
	cpuIdle    float64
	memTotal   int64
	memFree    int64
	memAvail   int64
	diskTotal  int64
	diskAvail  int64
	netRx      int64
	netTx      int64
}

func parseMetricsForAllVMs(text string) map[string]*VMMetric {
	raw := make(map[string]*rawMetrics)

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		name := extractLabel(line, "name")
		if name == "" {
			continue
		}

		if _, ok := raw[name]; !ok {
			raw[name] = &rawMetrics{}
		}
		r := raw[name]

		metricName, value := parseMetricLine(line)

		switch {
		case metricName == "incus_cpu_seconds_total" && extractLabel(line, "mode") == "user":
			r.cpuUser += value
		case metricName == "incus_cpu_seconds_total" && extractLabel(line, "mode") == "system":
			r.cpuSystem += value
		case metricName == "incus_cpu_seconds_total" && extractLabel(line, "mode") == "idle":
			r.cpuIdle += value
		case metricName == "incus_memory_MemTotal_bytes":
			r.memTotal = int64(value)
		case metricName == "incus_memory_MemFree_bytes":
			r.memFree = int64(value)
		case metricName == "incus_memory_MemAvailable_bytes":
			r.memAvail = int64(value)
		case metricName == "incus_filesystem_size_bytes" && extractLabel(line, "mountpoint") == "/":
			r.diskTotal = int64(value)
		case metricName == "incus_filesystem_avail_bytes" && extractLabel(line, "mountpoint") == "/":
			r.diskAvail = int64(value)
		case metricName == "incus_network_receive_bytes_total" && extractLabel(line, "device") != "lo":
			r.netRx += int64(value)
		case metricName == "incus_network_transmit_bytes_total" && extractLabel(line, "device") != "lo":
			r.netTx += int64(value)
		}
	}

	vms := make(map[string]*VMMetric, len(raw))
	for name, r := range raw {
		m := &VMMetric{}

		cpuTotal := r.cpuUser + r.cpuSystem + r.cpuIdle
		if cpuTotal > 0 {
			m.CPUUserPct = r.cpuUser / cpuTotal * 100
			m.CPUSystemPct = r.cpuSystem / cpuTotal * 100
			m.CPUIdlePct = 100 - m.CPUUserPct - m.CPUSystemPct
		}

		m.MemTotalBytes = r.memTotal
		if r.memTotal > 0 {
			m.MemUsedBytes = r.memTotal - r.memFree
			m.MemUsedPct = float64(r.memTotal-r.memAvail) / float64(r.memTotal) * 100
		}

		m.DiskTotalBytes = r.diskTotal
		if r.diskTotal > 0 {
			m.DiskUsedBytes = r.diskTotal - r.diskAvail
			m.DiskUsedPct = float64(m.DiskUsedBytes) / float64(r.diskTotal) * 100
		}

		m.NetRxBytes = r.netRx
		m.NetTxBytes = r.netTx

		vms[name] = m
	}

	return vms
}

func extractLabel(line, key string) string {
	search := key + "=\""
	idx := strings.Index(line, search)
	if idx == -1 {
		return ""
	}
	start := idx + len(search)
	end := strings.Index(line[start:], "\"")
	if end == -1 {
		return ""
	}
	return line[start : start+end]
}

func parseMetricLine(line string) (string, float64) {
	braceIdx := strings.Index(line, "{")
	if braceIdx == -1 {
		return "", 0
	}
	metricName := line[:braceIdx]

	lastSpace := strings.LastIndex(line, " ")
	if lastSpace == -1 {
		return metricName, 0
	}
	valStr := strings.TrimSpace(line[lastSpace+1:])
	val, _ := strconv.ParseFloat(valStr, 64)
	return metricName, val
}
