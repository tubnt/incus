package portal

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// HealingHandler exposes PLAN-020 Phase F history endpoints. Read-only;
// write paths live in clustermgmt (manual evacuate) and vm (chaos drill).
type HealingHandler struct {
	repo     *repository.HealingEventRepo
	clusters *cluster.Manager
}

func NewHealingHandler(repo *repository.HealingEventRepo, clusters *cluster.Manager) *HealingHandler {
	return &HealingHandler{repo: repo, clusters: clusters}
}

func (h *HealingHandler) AdminRoutes(r chi.Router) {
	if h == nil || h.repo == nil {
		return
	}
	r.Get("/ha/events", h.List)
	r.Get("/ha/events/{id}", h.Get)
}

// List returns a filtered, paginated slice of healing_events. Query:
//
//	cluster: cluster name (resolved to id via Manager)
//	node:    node hostname
//	trigger: manual | auto | chaos
//	status:  in_progress | completed | failed | partial
//	from/to: YYYY-MM-DD or RFC3339
//	limit/offset: standard pagination
//
// Each item also carries `cluster_name` derived from id so the UI doesn't
// need a separate clusters fetch.
func (h *HealingHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := ParsePageParams(r)

	filter := repository.HealingListFilter{
		NodeName: strings.TrimSpace(q.Get("node")),
		Trigger:  strings.TrimSpace(q.Get("trigger")),
		Status:   strings.TrimSpace(q.Get("status")),
	}
	if cn := strings.TrimSpace(q.Get("cluster")); cn != "" && h.clusters != nil {
		if id := h.clusters.IDByName(cn); id > 0 {
			filter.ClusterID = id
		}
	}
	if s := q.Get("from"); s != "" {
		if t := parseHealingTime(s); !t.IsZero() {
			filter.FromTime = &t
		}
	}
	if s := q.Get("to"); s != "" {
		if t := parseHealingTime(s); !t.IsZero() {
			filter.ToTime = &t
		}
	}

	events, total, err := h.repo.ListFiltered(r.Context(), filter, p.Limit, p.Offset)
	if err != nil {
		slog.Error("list healing events", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}

	items := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		items = append(items, serialiseHealing(ev, h.clusterNameByID(ev.ClusterID)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  p.Limit,
		"offset": p.Offset,
	})
}

// Get returns one event by id. Used by the drawer UI to pull evacuated_vms
// detail after the user clicks a row.
func (h *HealingHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	ev, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		slog.Error("get healing event", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "get failed"})
		return
	}
	if ev == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "healing event not found"})
		return
	}
	writeJSON(w, http.StatusOK, serialiseHealing(*ev, h.clusterNameByID(ev.ClusterID)))
}

func (h *HealingHandler) clusterNameByID(id int64) string {
	if h.clusters == nil || id == 0 {
		return ""
	}
	for _, c := range h.clusters.List() {
		if h.clusters.IDByName(c.Name) == id {
			return c.Name
		}
	}
	return ""
}

func serialiseHealing(ev repository.HealingEvent, clusterName string) map[string]any {
	row := map[string]any{
		"id":            ev.ID,
		"cluster_id":    ev.ClusterID,
		"cluster_name":  clusterName,
		"node_name":     ev.NodeName,
		"trigger":       ev.Trigger,
		"actor_id":      ev.ActorID,
		"evacuated_vms": ev.EvacuatedVMs,
		"started_at":    ev.StartedAt.Format(time.RFC3339),
		"status":        ev.Status,
	}
	if ev.CompletedAt != nil {
		row["completed_at"] = ev.CompletedAt.Format(time.RFC3339)
		row["duration_seconds"] = int64(ev.CompletedAt.Sub(ev.StartedAt).Seconds())
	}
	if ev.Error != nil {
		row["error"] = *ev.Error
	}
	return row
}

func parseHealingTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
