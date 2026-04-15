package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

type ctxKey string

const (
	CtxUserEmail    ctxKey = "user_email"
	CtxUserRole     ctxKey = "user_role"
	CtxUserID       ctxKey = "user_id"
	CtxAuthMethod   ctxKey = "auth_method"
)

type TokenValidator func(ctx context.Context, token string) (userID int64, err error)

var tokenValidator TokenValidator

func SetTokenValidator(v TokenValidator) {
	tokenValidator = v
}

func ProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
