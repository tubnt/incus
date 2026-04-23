// Package auth handles the step-up authentication OIDC round-trip.
//
// Two endpoints:
//   - GET /api/auth/stepup/start?rd=<return URL>
//     Signs the return URL into the OIDC state parameter and 302 redirects to
//     Logto with prompt=login + max_age=0 so the user goes through full
//     re-authentication (including MFA if Logto enforces it).
//
//   - GET /api/auth/stepup-callback?code=...&state=...
//     Lives on oauth2-proxy's skip_auth_routes allowlist so Logto can reach it
//     without a session cookie. Verifies the state, exchanges the code for
//     tokens, verifies id_token signature, matches the user by email, and
//     updates users.stepup_auth_at before 302'ing back to rd.
package auth

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/auth"
	"github.com/incuscloud/incus-admin/internal/repository"
)

const (
	// State param TTL: Logto auth flow should finish well within this.
	stateTTL = 10 * time.Minute
)

type Handler struct {
	oidc        *auth.OIDCClient
	userRepo    *repository.UserRepo
	stateSecret []byte
}

func NewHandler(oidcClient *auth.OIDCClient, userRepo *repository.UserRepo, stateSecret string) *Handler {
	return &Handler{
		oidc:        oidcClient,
		userRepo:    userRepo,
		stateSecret: []byte(stateSecret),
	}
}

// Start redirects the user to Logto with prompt=login, carrying the target
// path in a signed state parameter so the callback can resume the flow.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	rd := r.URL.Query().Get("rd")
	if rd == "" || !strings.HasPrefix(rd, "/") {
		// Only allow relative paths so the state can't be turned into an open redirect.
		rd = "/"
	}
	state, err := auth.SignState(h.stateSecret, rd, stateTTL)
	if err != nil {
		slog.Error("stepup: sign state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.oidc.StepUpAuthURL(state), http.StatusFound)
}

// Callback is Logto's redirect target. It completes the step-up by marking
// users.stepup_auth_at and bouncing the browser back to the original rd.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	if errCode := q.Get("error"); errCode != "" {
		slog.Warn("stepup: OIDC error", "error", errCode, "description", q.Get("error_description"))
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	rd, err := auth.VerifyState(h.stateSecret, state)
	if err != nil {
		slog.Warn("stepup: verify state", "error", err)
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	claims, err := h.oidc.VerifyCode(ctx, code)
	if err != nil {
		slog.Warn("stepup: verify code", "error", err)
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}

	if claims.Email == "" {
		slog.Warn("stepup: id_token missing email claim", "sub", claims.Sub)
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}

	// Find the application user by email. If the user logged in for the first
	// time through step-up (unlikely — step-up only fires for sensitive ops
	// accessible to already-known users), we don't auto-create here.
	user, err := h.userRepo.GetByEmail(ctx, strings.ToLower(claims.Email))
	if err != nil {
		slog.Error("stepup: lookup user", "email", claims.Email, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		slog.Warn("stepup: no matching user", "email", claims.Email)
		http.Error(w, "unknown user", http.StatusForbidden)
		return
	}

	authTime := time.Unix(claims.AuthTime, 0)
	if err := h.userRepo.SetStepUpAuthAt(ctx, user.ID, authTime); err != nil {
		slog.Error("stepup: persist auth time", "user_id", user.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	slog.Info("stepup: completed", "user_id", user.ID, "email", user.Email, "auth_time", authTime)

	// Only allow relative-path redirects to block open-redirect abuse.
	if !strings.HasPrefix(rd, "/") {
		rd = "/"
	}
	http.Redirect(w, r, rd, http.StatusFound)
}

