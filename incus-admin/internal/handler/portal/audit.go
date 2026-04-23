package portal

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type AuditHandler struct {
	repo *repository.AuditRepo
}

func NewAuditHandler(repo *repository.AuditRepo) *AuditHandler {
	return &AuditHandler{repo: repo}
}

func (h *AuditHandler) AdminRoutes(r chi.Router) {
	r.Get("/audit-logs", h.List)
	r.Get("/audit-logs/export", h.ExportCSV)
}

func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	p := ParsePageParams(r)
	if p.Limit > 100 {
		p.Limit = 100
	}

	logs, total, err := h.repo.List(r.Context(), p.Limit, p.Offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list logs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":   logs,
		"total":  total,
		"limit":  p.Limit,
		"offset": p.Offset,
	})
}

// ExportCSV streams audit rows as text/csv. Accepts ?from=YYYY-MM-DD,
// ?to=YYYY-MM-DD, ?action=<prefix> (e.g. "vm." matches vm.* business events,
// "http." matches the middleware's route-level rows). Defaults to last 30
// days, all actions. Hard-capped at 100k rows by the repository layer.
func (h *AuditHandler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	from := parseDateOr(q.Get("from"), now.AddDate(0, 0, -30))
	to := parseDateOr(q.Get("to"), now)
	actionPrefix := q.Get("action")

	filename := fmt.Sprintf("audit-%s-to-%s.csv", from.Format("20060102"), to.Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "user_id", "action", "target_type", "target_id", "details", "ip_address", "created_at"})

	var rowCount int
	err := h.repo.ExportRange(r.Context(), from, to, actionPrefix, func(l model.AuditLog) error {
		uid := ""
		if l.UserID != nil {
			uid = strconv.FormatInt(*l.UserID, 10)
		}
		if err := cw.Write([]string{
			strconv.FormatInt(l.ID, 10),
			uid,
			l.Action,
			l.TargetType,
			strconv.FormatInt(l.TargetID, 10),
			l.Details,
			l.IPAddress,
			l.CreatedAt.Format(time.RFC3339),
		}); err != nil {
			return err
		}
		rowCount++
		return nil
	})
	cw.Flush()

	if err != nil {
		// Headers already flushed; best we can do is log server-side.
		slog.Error("audit export failed mid-stream", "error", err, "rows_written", rowCount)
	}

	// Meta-audit: record who exported what. Row count and filter parameters
	// are captured so unusual exports (broad ranges, targeted actions) are
	// visible in the audit trail itself.
	audit(r.Context(), r, "audit.export", "audit_logs", 0, map[string]any{
		"from":          from.Format(time.RFC3339),
		"to":            to.Format(time.RFC3339),
		"action_filter": actionPrefix,
		"rows":          rowCount,
	})

	slog.Info("audit export", "from", from, "to", to, "action", actionPrefix, "rows", rowCount)
}

// parseDateOr accepts YYYY-MM-DD or RFC3339; falls back to def on error.
func parseDateOr(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return def
}
