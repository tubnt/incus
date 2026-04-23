package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

)

type ctxKey string

const (
	CtxUserEmail  ctxKey = "user_email"
	CtxUserRole   ctxKey = "user_role"
	CtxUserID     ctxKey = "user_id"
	CtxAuthMethod ctxKey = "auth_method"
	// CtxActorID is set only when the request runs under a shadow-login
	// session. When present, CtxUserID is the *target* user (so handler
	// business logic sees the target's resources) while CtxActorID records
	// the admin who initiated shadowing. Audit code reads both to distinguish
	// who actually performed the action from whose resources were touched.
	CtxActorID    ctxKey = "actor_id"
	CtxActorEmail ctxKey = "actor_email"
)

type TokenValidator func(ctx context.Context, token string) (userID int64, err error)

// ShadowVerifier verifies a shadow_session cookie and returns the actor and
// target identities. main.go wires this to auth.VerifyShadow; left nil in
// test/dev envs disables shadow cookie handling without breaking ProxyAuth.
type ShadowVerifier func(cookieValue string) (actorID int64, actorEmail string, targetID int64, targetEmail string, err error)

var (
	tokenValidator  TokenValidator
	emergencySecret string
	shadowVerifier  ShadowVerifier
)

func SetTokenValidator(v TokenValidator) {
	tokenValidator = v
}

func SetEmergencySecret(secret string) {
	emergencySecret = secret
}

func SetShadowVerifier(v ShadowVerifier) {
	shadowVerifier = v
}

func verifyEmergencyCookie(email, sig string) bool {
	if emergencySecret == "" || email == "" {
		return false
	}
	h := hmac.New(sha256.New, []byte(emergencySecret))
	h.Write([]byte(email))
	expected := hex.EncodeToString(h.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) == 1
}

func ProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Shadow session cookie takes precedence over every other auth path.
		// When present and valid, we treat the request as originating from
		// the *target* user (so handler business logic is scoped correctly)
		// but record the admin's identity for audit via CtxActorID.
		if shadowVerifier != nil {
			if c, err := r.Cookie("shadow_session"); err == nil && c.Value != "" {
				actorID, actorEmail, targetID, targetEmail, verifyErr := shadowVerifier(c.Value)
				if verifyErr == nil && actorID > 0 && targetID > 0 {
					ctx := r.Context()
					ctx = context.WithValue(ctx, CtxUserID, targetID)
					ctx = context.WithValue(ctx, CtxUserEmail, strings.ToLower(strings.TrimSpace(targetEmail)))
					ctx = context.WithValue(ctx, CtxActorID, actorID)
					ctx = context.WithValue(ctx, CtxActorEmail, strings.ToLower(strings.TrimSpace(actorEmail)))
					ctx = context.WithValue(ctx, CtxAuthMethod, "shadow")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Invalid cookie: don't silently fall through with target
				// identity; clear it and keep walking the other auth paths
				// so the admin can still work from their own session.
				slog.Warn("invalid shadow session", "error", verifyErr)
				http.SetCookie(w, &http.Cookie{Name: "shadow_session", Value: "", Path: "/", MaxAge: -1})
			}
		}

		// Bearer token 认证
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if tokenValidator != nil && strings.HasPrefix(token, "ica_") {
				userID, err := tokenValidator(r.Context(), token)
				if err == nil && userID > 0 {
					ctx := r.Context()
					ctx = context.WithValue(ctx, CtxUserID, userID)
					ctx = context.WithValue(ctx, CtxAuthMethod, "api_token")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				slog.Warn("invalid api token", "error", err)
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}
		}

		// emergency cookie 认证（HMAC 签名验证）
		if cookie, err := r.Cookie("emergency_auth"); err == nil {
			parts := strings.SplitN(cookie.Value, "|", 2)
			if len(parts) == 2 && verifyEmergencyCookie(parts[0], parts[1]) {
				ctx := r.Context()
				ctx = context.WithValue(ctx, CtxUserEmail, parts[0])
				ctx = context.WithValue(ctx, CtxAuthMethod, "emergency")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// oauth2-proxy header 认证
		email := r.Header.Get("X-Auth-Request-Email")
		if email == "" {
			email = r.Header.Get("X-Forwarded-Email")
		}
		if email == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), CtxUserEmail, strings.ToLower(strings.TrimSpace(email)))
		ctx = context.WithValue(ctx, CtxAuthMethod, "proxy")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userRole, _ := r.Context().Value(CtxUserRole).(string)
			if userRole != role {
				slog.Warn("access denied", "required", role, "actual", userRole, "path", r.URL.Path)
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}
}

func UserFromEmail(userLookup func(ctx context.Context, email string) (int64, string, error), roleLookup func(ctx context.Context, userID int64) (string, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// API Token 认证路径已有 userID，只需查 role
			if method, _ := r.Context().Value(CtxAuthMethod).(string); method == "api_token" {
				userID, _ := r.Context().Value(CtxUserID).(int64)
				if userID > 0 && roleLookup != nil {
					role, err := roleLookup(r.Context(), userID)
					if err != nil {
						http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
						return
					}
					ctx := context.WithValue(r.Context(), CtxUserRole, role)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Shadow session: CtxUserID is the target but role must come from
			// the actor (admin). Without this override, /api/admin routes
			// would 403 because the target might be a plain customer —
			// defeating the whole purpose of shadowing.
			if method, _ := r.Context().Value(CtxAuthMethod).(string); method == "shadow" {
				actorID, _ := r.Context().Value(CtxActorID).(int64)
				if actorID > 0 && roleLookup != nil {
					role, err := roleLookup(r.Context(), actorID)
					if err != nil {
						http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
						return
					}
					ctx := context.WithValue(r.Context(), CtxUserRole, role)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			email, _ := r.Context().Value(CtxUserEmail).(string)
			if email == "" {
				next.ServeHTTP(w, r)
				return
			}

			userID, role, err := userLookup(r.Context(), email)
			if err != nil {
				slog.Error("user lookup failed", "email", email, "error", err)
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, CtxUserID, userID)
			ctx = context.WithValue(ctx, CtxUserRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
