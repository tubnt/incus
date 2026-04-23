package portal

import (
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
	r.Get("/products/{id}", h.AdminGetByID)
	r.Post("/products", h.Create)
	r.Put("/products/{id}", h.Update)
}

func (h *ProductHandler) AdminGetByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid product id"})
		return
	}
	product, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if product == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "product not found"})
		return
	}
	writeJSON(w, http.StatusOK, product)
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
	// 创建接口不直接绑定 model.Product 是为了让校验 tag 跟 handler 层走，
	// model 包保持纯粹的持久化形状。字段集与 UpdateProductReq 对齐，只是这里
	// 没有指针（创建时需要给字段提供默认/显式值）。
	var req struct {
		Name         string  `json:"name"          validate:"required,min=1,max=200"`
		Slug         string  `json:"slug"          validate:"omitempty,safename"`
		CPU          int     `json:"cpu"           validate:"gte=0,lte=128"`
		MemoryMB     int     `json:"memory_mb"     validate:"gte=0,lte=1048576"`
		DiskGB       int     `json:"disk_gb"       validate:"gte=0,lte=10240"`
		BandwidthTB  int     `json:"bandwidth_tb"  validate:"gte=0,lte=1024"`
		PriceMonthly float64 `json:"price_monthly" validate:"gte=0"`
		Currency     string  `json:"currency"      validate:"omitempty,len=3,alpha"`
		Access       string  `json:"access"        validate:"omitempty,max=64"`
		SortOrder    int     `json:"sort_order"    validate:"gte=0,lte=100000"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	p := model.Product{
		Name:         req.Name,
		Slug:         req.Slug,
		CPU:          req.CPU,
		MemoryMB:     req.MemoryMB,
		DiskGB:       req.DiskGB,
		BandwidthTB:  req.BandwidthTB,
		PriceMonthly: req.PriceMonthly,
		Currency:     req.Currency,
		Access:       req.Access,
		SortOrder:    req.SortOrder,
		Active:       true,
	}

	created, err := h.repo.Create(r.Context(), &p)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	audit(r.Context(), r, "product.create", "product", created.ID, map[string]any{
		"name":          created.Name,
		"slug":          created.Slug,
		"price_monthly": created.PriceMonthly,
		"active":        created.Active,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"product": created})
}

// UpdateProductReq uses pointer fields so the handler can distinguish
// "field absent from request" from "field explicitly set to zero / empty".
// Only non-nil fields are merged into the existing record.
type UpdateProductReq struct {
	Name         *string  `json:"name"          validate:"omitempty,min=1,max=200"`
	Slug         *string  `json:"slug"          validate:"omitempty,safename"`
	CPU          *int     `json:"cpu"           validate:"omitempty,gte=0,lte=128"`
	MemoryMB     *int     `json:"memory_mb"     validate:"omitempty,gte=0,lte=1048576"`
	DiskGB       *int     `json:"disk_gb"       validate:"omitempty,gte=0,lte=10240"`
	BandwidthTB  *int     `json:"bandwidth_tb"  validate:"omitempty,gte=0,lte=1024"`
	PriceMonthly *float64 `json:"price_monthly" validate:"omitempty,gte=0"`
	Currency     *string  `json:"currency"      validate:"omitempty,len=3,alpha"`
	Access       *string  `json:"access"        validate:"omitempty,max=64"`
	Active       *bool    `json:"active"`
	SortOrder    *int     `json:"sort_order"    validate:"omitempty,gte=0,lte=100000"`
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
	if !decodeAndValidate(w, r, &req) {
		return
	}
	applyUpdateProductReq(existing, req)
	existing.ID = id

	if err := h.repo.Update(r.Context(), existing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "product.update", "product", id, map[string]any{
		"name":          existing.Name,
		"price_monthly": existing.PriceMonthly,
		"active":        existing.Active,
	})
	writeJSON(w, http.StatusOK, map[string]any{"product": existing})
}
