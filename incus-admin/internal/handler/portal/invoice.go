package portal

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type InvoiceHandler struct {
	repo *repository.InvoiceRepo
}

func NewInvoiceHandler(repo *repository.InvoiceRepo) *InvoiceHandler {
	return &InvoiceHandler{repo: repo}
}

func (h *InvoiceHandler) PortalRoutes(r chi.Router) {
	r.Get("/invoices", h.ListMine)
}

func (h *InvoiceHandler) AdminRoutes(r chi.Router) {
	r.Get("/invoices", h.ListAll)
}

func (h *InvoiceHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	invoices, err := h.repo.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list invoices"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": invoices})
}

func (h *InvoiceHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	p := ParsePageParams(r)
	invoices, total, err := h.repo.ListPaged(r.Context(), p.Limit, p.Offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list invoices"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"invoices": invoices,
		"total":    total,
		"limit":    p.Limit,
		"offset":   p.Offset,
	})
}
