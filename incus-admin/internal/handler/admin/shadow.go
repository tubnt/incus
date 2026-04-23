// Package admin hosts HTTP handlers that don't fit the user-facing portal
// package. Currently only the shadow-login round-trip lives here.
package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/auth"
	"github.com/incuscloud/incus-admin/internal/httpx"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

type ShadowHandler struct {
	userRepo *repository.UserRepo
	secret   []byte
	auditor  func(actorID int64, action, targetType string, targetID int64, details map[string]any, ip string)
}

// ShadowAuditor signature mirrors the portal `audit()` helper so main.go can
// inject whatever logger writes into audit_logs.
type ShadowAuditor func(actorID int64, action, targetType string, targetID int64, details map[string]any, ip string)

func NewShadowHandler(userRepo *repository.UserRepo, secret string, auditor ShadowAuditor) *ShadowHandler {
	return &ShadowHandler{
		userRepo: userRepo,
		secret:   []byte(secret),
		auditor:  auditor,
	}
}

// LoginAdmin is the admin-initiated endpoint: POST /api/admin/users/{id}/shadow-login
// with body {reason}. Returns {redirect_url: "/shadow/enter?token=<signed>"}.
// The response doesn't set the cookie directly so the browser always takes
// one full round-trip through Enter, where the cookie is scoped correctly.
func (h *ShadowHandler) LoginAdmin(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	targetID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || targetID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid user id"})
		return
	}

	var req struct {
		Reason string `json:"reason" validate:"required,min=1,max=500"`
	}
	if !httpx.DecodeAndValidate(w, r, &req) {
		return
	}

	// Self-shadow guard: admin can't shadow themselves (no purpose, clutters audit).
	actorID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	actorEmail, _ := r.Context().Value(middleware.CtxUserEmail).(string)
	if actorID == targetID {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cannot shadow yourself"})
		return
	}

	target, err := h.userRepo.GetByID(r.Context(), targetID)
	if err != nil || target == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "target user not found"})
		return
	}

	now := time.Now()
	claims := auth.ShadowClaims{
		ActorID:     actorID,
		ActorEmail:  actorEmail,
		TargetID:    target.ID,
		TargetEmail: target.Email,
		Reason:      req.Reason,
		ExpiresAt:   now.Add(auth.ShadowTTL).Unix(),
		IssuedAt:    now.Unix(),
	}
	token, err := auth.SignShadow(h.secret, claims)
	if err != nil {
		slog.Error("shadow sign", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
		return
	}

	if h.auditor != nil {
		h.auditor(actorID, "shadow.enter", "user", target.ID, map[string]any{
			"actor_email":  actorEmail,
			"target_email": target.Email,
			"reason":       req.Reason,
			"ttl_seconds":  int(auth.ShadowTTL.Seconds()),
		}, clientIP(r))
	}

	slog.Info("shadow login issued",
		"actor_id", actorID, "actor_email", actorEmail,
		"target_id", target.ID, "target_email", target.Email,
		"reason", req.Reason,
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"redirect_url": "/shadow/enter?token=" + url.QueryEscape(token),
	})
}

// Enter consumes the signed token and sets the shadow_session cookie before
// bouncing the browser to /. oauth2-proxy validates the admin's own session
// before this handler runs, so we never create a shadow cookie for someone
// who can't already log in.
func (h *ShadowHandler) Enter(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	claims, err := auth.VerifyShadow(h.secret, token)
	if err != nil {
		slog.Warn("shadow enter verify failed", "error", err)
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}

	maxAge := int(time.Until(time.Unix(claims.ExpiresAt, 0)).Seconds())
	if maxAge <= 0 {
		http.Error(w, "token expired", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.ShadowSessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		// SameSite=Strict + HttpOnly makes the cookie unreadable to JS and
		// unusable across origins. The banner reads shadow state via
		// /api/auth/me instead.
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   maxAge,
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

// Exit clears the shadow cookie and bounces back to /. Safe to call even
// when no shadow session is active.
func (h *ShadowHandler) Exit(w http.ResponseWriter, r *http.Request) {
	actorID, _ := r.Context().Value(middleware.CtxActorID).(int64)
	targetID, _ := r.Context().Value(middleware.CtxUserID).(int64)

	http.SetCookie(w, &http.Cookie{
		Name:     auth.ShadowSessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   -1,
	})

	if actorID > 0 && h.auditor != nil {
		h.auditor(actorID, "shadow.exit", "user", targetID, map[string]any{
			"target_user_id": targetID,
		}, clientIP(r))
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clientIP(r *http.Request) string {
	if ra := r.RemoteAddr; ra != "" {
		// Typically "ip:port" — strip port if present.
		for i := len(ra) - 1; i >= 0; i-- {
			if ra[i] == ':' {
				return ra[:i]
			}
		}
		return ra
	}
	return ""
}
