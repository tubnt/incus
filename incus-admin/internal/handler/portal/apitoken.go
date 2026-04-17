package portal

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// DefaultAPITokenDays 创建 API Token 时若未指定过期则默认 7 天过期，
// 避免"永不过期"的 token 长期遗留造成凭据泄露扩大。
const DefaultAPITokenDays = 7

type APITokenHandler struct {
	repo *repository.APITokenRepo
}

func NewAPITokenHandler(repo *repository.APITokenRepo) *APITokenHandler {
	return &APITokenHandler{repo: repo}
}

func (h *APITokenHandler) Routes(r chi.Router) {
	r.Get("/api-tokens", h.List)
	r.Post("/api-tokens", h.Create)
	r.Delete("/api-tokens/{id}", h.Delete)
}

func (h *APITokenHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	tokens, err := h.repo.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list tokens"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (h *APITokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	var req struct {
		Name          string `json:"name"`
		ExpiresInDays *int   `json:"expires_in_days,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}
	days := DefaultAPITokenDays
	if req.ExpiresInDays != nil {
		days = *req.ExpiresInDays
	}
	if days < 0 || days > 365 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "expires_in_days must be between 0 and 365"})
		return
	}
	var expiresAt *time.Time
	if days > 0 {
		t := time.Now().AddDate(0, 0, days)
		expiresAt = &t
	}

	token, err := h.repo.Create(r.Context(), userID, req.Name, expiresAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"token": token})
}

func (h *APITokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	if err := h.repo.Delete(r.Context(), id, userID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}
