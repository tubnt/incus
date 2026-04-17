package portal

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type ProductHandler struct {
	repo *repository.ProductRepo
}

func NewProductHandler(repo *repository.ProductRepo) *ProductHandler {
	return &ProductHandler{repo: repo}
}

func (h *ProductHandler) PortalRoutes(r chi.Router) {
	r.Get("/products", h.ListActive)
}

func (h *ProductHandler) AdminRoutes(r chi.Router) {
	r.Get("/products", h.ListAll)
	r.Post("/products", h.Create)
	r.Put("/products/{id}", h.Update)
}

func (h *ProductHandler) ListActive(w http.ResponseWriter, r *http.Request) {
	products, err := h.repo.ListActive(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list products"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": products})
}

func (h *ProductHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	p := ParsePageParams(r)
	products, total, err := h.repo.ListPaged(r.Context(), p.Limit, p.Offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list products"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"products": products,
		"total":    total,
		"limit":    p.Limit,
		"offset":   p.Offset,
	})
}

func (h *ProductHandler) Create(w http.ResponseWriter, r *http.Request) {
	var p model.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if p.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	p.Active = true

	created, err := h.repo.Create(r.Context(), &p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"product": created})
}

// UpdateProductReq uses pointer fields so the handler can distinguish
// "field absent from request" from "field explicitly set to zero / empty".
// Only non-nil fields are merged into the existing record.
type UpdateProductReq struct {
	Name         *string  `json:"name"`
	Slug         *string  `json:"slug"`
	CPU          *int     `json:"cpu"`
	MemoryMB     *int     `json:"memory_mb"`
	DiskGB       *int     `json:"disk_gb"`
	BandwidthTB  *int     `json:"bandwidth_tb"`
	PriceMonthly *float64 `json:"price_monthly"`
	Currency     *string  `json:"currency"`
	Access       *string  `json:"access"`
	Active       *bool    `json:"active"`
	SortOrder    *int     `json:"sort_order"`
}

func applyUpdateProductReq(p *model.Product, req UpdateProductReq) {
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Slug != nil {
		p.Slug = *req.Slug
	}
	if req.CPU != nil {
		p.CPU = *req.CPU
	}
	if req.MemoryMB != nil {
		p.MemoryMB = *req.MemoryMB
	}
	if req.DiskGB != nil {
		p.DiskGB = *req.DiskGB
	}
	if req.BandwidthTB != nil {
		p.BandwidthTB = *req.BandwidthTB
	}
	if req.PriceMonthly != nil {
		p.PriceMonthly = *req.PriceMonthly
	}
	if req.Currency != nil {
		p.Currency = *req.Currency
	}
	if req.Access != nil {
		p.Access = *req.Access
	}
	if req.Active != nil {
		p.Active = *req.Active
	}
	if req.SortOrder != nil {
		p.SortOrder = *req.SortOrder
	}
}

func (h *ProductHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}

	existing, err := h.repo.GetByID(r.Context(), id)
	if err != nil || existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "product not found"})
		return
	}

	var req UpdateProductReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	applyUpdateProductReq(existing, req)
	existing.ID = id

	if err := h.repo.Update(r.Context(), existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"product": existing})
}
