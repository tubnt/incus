package portal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/httpx"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// Token TTL defaults. PLAN-019 Phase D tightens the default from 7d to 24h
// and introduces an hours-granularity field so the UI can offer 1h/6h slots
// for short-lived scripts. Range is 1h – 90d.
const (
	DefaultAPITokenTTL = 24 * time.Hour
	MaxAPITokenTTL     = 90 * 24 * time.Hour
	MinAPITokenTTL     = 1 * time.Hour
)

type APITokenHandler struct {
	repo *repository.APITokenRepo
}

func NewAPITokenHandler(repo *repository.APITokenRepo) *APITokenHandler {
	return &APITokenHandler{repo: repo}
}

func (h *APITokenHandler) Routes(r chi.Router) {
	r.Get("/api-tokens", h.List)
	r.Post("/api-tokens", h.Create)
	r.Post("/api-tokens/{id}/renew", h.Renew)
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
		Name           string `json:"name"                       validate:"omitempty,max=100"`
		ExpiresInDays  *int   `json:"expires_in_days,omitempty"  validate:"omitempty,gte=0,lte=90"`
		ExpiresInHours *int   `json:"expires_in_hours,omitempty" validate:"omitempty,gte=0,lte=2160"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "default"
	}

	ttl, ttlErr := resolveTokenTTL(req.ExpiresInHours, req.ExpiresInDays)
	if ttlErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ttlErr.Error()})
		return
	}
	expiresAt := time.Now().Add(ttl)

	token, err := h.repo.Create(r.Context(), userID, req.Name, &expiresAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "api_token.create", "api_token", token.ID, map[string]any{
		"name":       token.Name,
		"expires_at": token.ExpiresAt,
		"ttl_hours":  ttl.Hours(),
	})

	writeJSON(w, http.StatusCreated, map[string]any{"token": token})
}

// Renew mints a replacement token inheriting the original name; the old token
// becomes invalid immediately (its row is kept until the retention worker's
// grace period elapses so the audit trail survives).
func (h *APITokenHandler) Renew(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var req struct {
		ExpiresInDays  *int `json:"expires_in_days,omitempty"  validate:"omitempty,gte=0,lte=90"`
		ExpiresInHours *int `json:"expires_in_hours,omitempty" validate:"omitempty,gte=0,lte=2160"`
	}
	// Renew 允许空 body（表示用默认 TTL 续签）。EOF 不走 decodeAndValidate 是因为它会
	// 把 EOF 包成 400 —— 此处手动 decode 并只在有 body 时跑 validator。
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body: " + err.Error()})
		return
	}
	if err := httpx.Validator().Struct(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "validation_failed",
			"details": []string{err.Error()},
		})
		return
	}

	ttl, ttlErr := resolveTokenTTL(req.ExpiresInHours, req.ExpiresInDays)
	if ttlErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ttlErr.Error()})
		return
	}

	token, err := h.repo.Renew(r.Context(), id, userID, ttl)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "api_token.renew", "api_token", token.ID, map[string]any{
		"name":          token.Name,
		"old_id":        id,
		"new_expires_at": token.ExpiresAt,
		"ttl_hours":     ttl.Hours(),
	})

	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

// resolveTokenTTL picks a TTL from the hours field first, then days, then the
// package default. Values outside [MinAPITokenTTL, MaxAPITokenTTL] return an
// error so the client learns the allowed range.
func resolveTokenTTL(hours, days *int) (time.Duration, error) {
	var ttl time.Duration
	switch {
	case hours != nil && *hours > 0:
		ttl = time.Duration(*hours) * time.Hour
	case days != nil && *days > 0:
		ttl = time.Duration(*days) * 24 * time.Hour
	default:
		ttl = DefaultAPITokenTTL
	}
	if ttl < MinAPITokenTTL || ttl > MaxAPITokenTTL {
		return 0, fmt.Errorf("ttl must be between 1h and 90d; got %s", ttl)
	}
	return ttl, nil
}

func (h *APITokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	if err := h.repo.Delete(r.Context(), id, userID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}

	audit(r.Context(), r, "api_token.revoke", "api_token", id, nil)

	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}
