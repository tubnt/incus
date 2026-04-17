package portal

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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
