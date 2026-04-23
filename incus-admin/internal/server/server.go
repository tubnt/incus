package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/incuscloud/incus-admin/internal/config"
	"github.com/incuscloud/incus-admin/internal/middleware"
)

type Server struct {
	cfg    *config.Config
	router *chi.Mux
}

type RouteRegistrar interface {
	Routes(r chi.Router)
}

type AdminRouteRegistrar interface {
	AdminRoutes(r chi.Router)
}

type ConsoleHandlerFunc interface {
	HandleConsole(w http.ResponseWriter, r *http.Request)
}

type PortalRouteRegistrar interface {
	PortalRoutes(r chi.Router)
}

type Handlers struct {
	Admin    RouteRegistrar
	Portal   RouteRegistrar
	Users    AdminRouteRegistrar
	IPPools  AdminRouteRegistrar
	Console  ConsoleHandlerFunc
	Snaps    interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Metrics  interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	SSHKeys  RouteRegistrar
	Tickets  interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Products interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Orders interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Audit      AdminRouteRegistrar
	APITokens  RouteRegistrar
	ClusterMgmt AdminRouteRegistrar
	Ceph        AdminRouteRegistrar
	NodeOps     AdminRouteRegistrar
	Invoices  interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Quotas interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	Events AdminRouteRegistrar
	// Healing registers PLAN-020 Phase F history endpoints
	// (GET /admin/ha/events, GET /admin/ha/events/{id}).
	Healing AdminRouteRegistrar
	// Auth exposes the step-up OIDC round-trip handlers. Start requires an
	// active session (mounted inside the ProxyAuth group); Callback is reached
	// directly from Logto (mounted outside, allowlisted by oauth2-proxy's
	// skip_auth_routes).
	Auth StepUpHandler
	// Shadow exposes LoginAdmin (admin-only), Enter (consumes the signed
	// token and sets the cookie) and Exit (clears the cookie). All three
	// require an existing oauth2-proxy session.
	Shadow ShadowHandler
}

// StepUpHandler is satisfied by *handler/auth.Handler.
type StepUpHandler interface {
	Start(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request)
}

// ShadowHandler is satisfied by *handler/admin.ShadowHandler.
type ShadowHandler interface {
	LoginAdmin(w http.ResponseWriter, r *http.Request)
	Enter(w http.ResponseWriter, r *http.Request)
	Exit(w http.ResponseWriter, r *http.Request)
}

// StepUpLookup is the function the admin middleware uses to check whether a
// user has a recent step-up completion. Wired to UserRepo.GetStepUpAuthAt.
type StepUpLookup = middleware.StepUpLookup

// AuditWriter is wired by main.go to repository.AuditRepo.Log; mounted as
// middleware on the /api/admin router group so every write request gets a
// coarse-grained audit_logs row automatically.
type AuditWriter = middleware.AuditWriter

type UserBalanceLookup func(ctx context.Context, userID int64) (float64, error)

func New(cfg *config.Config, userLookup func(ctx context.Context, email string) (int64, string, error), roleLookup func(ctx context.Context, userID int64) (string, error), balanceLookup UserBalanceLookup, stepUpLookup StepUpLookup, auditWriter AuditWriter, h Handlers) *Server {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(slogMiddleware)

	distHash := DistHash()
	if distHash == "" {
		slog.Warn("embedded dist/index.html missing; frontend was not built before go build — run `task web-build`")
	} else {
		slog.Info("embedded dist loaded", "index_sha256", distHash[:12])
	}
	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"dist_hash": distHash,
		})
	})

	// Step-up OIDC callback lives outside the ProxyAuth group because Logto
	// redirects the browser back here after the user completes re-authentication.
	// oauth2-proxy's skip_auth_routes must also include this path; otherwise
	// the callback would be intercepted before reaching incus-admin.
	if h.Auth != nil {
		r.Get("/api/auth/stepup-callback", h.Auth.Callback)
	}

	r.Group(func(r chi.Router) {
		r.Use(middleware.ProxyAuth)
		r.Use(middleware.UserFromEmail(userLookup, roleLookup))

		r.Route("/api/portal", func(r chi.Router) {
			r.Use(middleware.RateLimit)
			if h.Portal != nil {
				h.Portal.Routes(r)
			}
			if h.Metrics != nil {
				h.Metrics.PortalRoutes(r)
			}
			if h.SSHKeys != nil {
				h.SSHKeys.Routes(r)
			}
			if h.Tickets != nil {
				h.Tickets.PortalRoutes(r)
			}
			if h.Products != nil {
				h.Products.PortalRoutes(r)
			}
			if h.Orders != nil {
				h.Orders.PortalRoutes(r)
			}
			if h.Snaps != nil {
				h.Snaps.PortalRoutes(r)
			}
			if h.APITokens != nil {
				h.APITokens.Routes(r)
			}
			if h.Invoices != nil {
				h.Invoices.PortalRoutes(r)
			}
			if h.Quotas != nil {
				h.Quotas.PortalRoutes(r)
			}
		})

		r.Route("/api/admin", func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
			// Step-up gate: lookup == nil → no-op. See middleware/stepup.go
			// for the sensitive-route allowlist.
			r.Use(middleware.RequireRecentAuthOnSensitive(stepUpLookup, cfg.Auth.StepUpMaxAge))
			// Write-audit middleware: auditWriter == nil → no-op. Captures
			// every POST/PUT/PATCH/DELETE that survives step-up and reaches
			// the handler so coverage is guaranteed even if the handler
			// forgets to call audit() explicitly.
			r.Use(middleware.AuditAdminWrites(auditWriter))
			// Shadow-mode money gate: when a shadow session is active,
			// reject anything under moneyRoutes (balance adjustments,
			// refunds, etc) with 403. Non-shadow or non-money routes pass
			// through untouched.
			r.Use(middleware.RejectShadowSessionOnMoney)

			// Admin-only shadow-login entry point. Must live under /api/admin
			// so it inherits RequireRole("admin") — only admins can start a
			// shadow session against another user.
			if h.Shadow != nil {
				r.Post("/users/{id}/shadow-login", h.Shadow.LoginAdmin)
			}
			if h.Admin != nil {
				h.Admin.Routes(r)
			}
			if h.Users != nil {
				h.Users.AdminRoutes(r)
			}
			if h.IPPools != nil {
				h.IPPools.AdminRoutes(r)
			}
			if h.Snaps != nil {
				h.Snaps.AdminRoutes(r)
			}
			if h.Metrics != nil {
				h.Metrics.AdminRoutes(r)
			}
			if h.Tickets != nil {
				h.Tickets.AdminRoutes(r)
			}
			if h.Products != nil {
				h.Products.AdminRoutes(r)
			}
			if h.Orders != nil {
				h.Orders.AdminRoutes(r)
			}
			if h.Audit != nil {
				h.Audit.AdminRoutes(r)
			}
			if h.Invoices != nil {
				h.Invoices.AdminRoutes(r)
			}
			if h.Quotas != nil {
				h.Quotas.AdminRoutes(r)
			}
			if h.ClusterMgmt != nil {
				h.ClusterMgmt.AdminRoutes(r)
			}
			if h.Ceph != nil {
				h.Ceph.AdminRoutes(r)
			}
			if h.NodeOps != nil {
				h.NodeOps.AdminRoutes(r)
			}
			if h.Events != nil {
				h.Events.AdminRoutes(r)
			}
			if h.Healing != nil {
				h.Healing.AdminRoutes(r)
			}
		})

		if h.Console != nil {
			r.Get("/api/console", h.Console.HandleConsole)
		}

		// Step-up OIDC start requires an existing session so only the current
		// logged-in user can initiate a re-authentication for themselves.
		if h.Auth != nil {
			r.Get("/api/auth/stepup/start", h.Auth.Start)
		}

		// Shadow login enter/exit endpoints live inside the ProxyAuth group
		// so oauth2-proxy has already verified the admin's identity before
		// the cookie is minted/cleared.
		if h.Shadow != nil {
			r.Get("/shadow/enter", h.Shadow.Enter)
			r.Post("/shadow/exit", h.Shadow.Exit)
		}

		r.Get("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
			email, _ := r.Context().Value(middleware.CtxUserEmail).(string)
			userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
			role, _ := r.Context().Value(middleware.CtxUserRole).(string)
			actorID, _ := r.Context().Value(middleware.CtxActorID).(int64)
			actorEmail, _ := r.Context().Value(middleware.CtxActorEmail).(string)
			var balance float64
			if balanceLookup != nil && userID > 0 {
				balance, _ = balanceLookup(r.Context(), userID)
			}
			resp := map[string]any{
				"id":      userID,
				"email":   email,
				"name":    email,
				"role":    role,
				"balance": balance,
			}
			// When under shadow, expose actor info so the frontend banner
			// can display "you're acting as X · exit". HttpOnly cookie means
			// JS can't read the session directly; this endpoint is the
			// sanctioned way to check.
			if actorID > 0 {
				resp["acting_as"] = map[string]any{
					"target_user_id": userID,
					"target_email":   email,
					"actor_id":       actorID,
					"actor_email":    actorEmail,
				}
			}
			writeJSON(w, http.StatusOK, resp)
		})
	})

	// SPA static files (catch-all, must be last)
	r.NotFound(staticHandler().ServeHTTP)

	// Uniform JSON 405 for API paths; everything else delegates to the SPA
	// fallback. chi's default writes a plain-text body, which the browser
	// confuses with JSON.
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	})

	return &Server{cfg: cfg, router: r}
}

func (s *Server) Router() *chi.Mux {
	return s.router
}

func (s *Server) Run() error {
	mainSrv := &http.Server{
		Addr:         s.cfg.Server.Listen,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	emergencySrv := &http.Server{
		Addr:              s.cfg.Server.EmergencyListen,
		Handler:           s.emergencyRouter(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)

	go func() {
		slog.Info("main server starting", "addr", s.cfg.Server.Listen)
		errCh <- mainSrv.ListenAndServe()
	}()

	go func() {
		slog.Info("emergency server starting", "addr", s.cfg.Server.EmergencyListen)
		errCh <- emergencySrv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		slog.Info("shutdown signal received", "signal", sig)
	case err := <-errCh:
		slog.Error("server error", "error", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mainSrv.Shutdown(ctx)
	emergencySrv.Shutdown(ctx)

	slog.Info("server stopped")
	return nil
}

func (s *Server) emergencyRouter() http.Handler {
	r := chi.NewRouter()

	var (
		failCount int
		failMu    sync.Mutex
		lockUntil time.Time
	)
	const maxAttempts = 5
	const lockDuration = 15 * time.Minute

	r.Get("/auth/emergency", func(w http.ResponseWriter, _ *http.Request) {
		failMu.Lock()
		locked := time.Now().Before(lockUntil)
		failMu.Unlock()

		if locked {
			http.Error(w, "too many attempts, locked for 15 minutes", http.StatusTooManyRequests)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Emergency Login</title></head>
<body style="font-family:sans-serif;max-width:400px;margin:100px auto">
<h2>Emergency Admin Login</h2>
<p style="color:#666;font-size:14px">This port is localhost-only. Access via SSH tunnel.</p>
<form method="POST" action="/auth/emergency">
<input name="token" type="password" placeholder="Emergency Token" style="width:100%;padding:8px;margin:8px 0" autocomplete="off">
<button type="submit" style="width:100%;padding:8px;background:#dc2626;color:#fff;border:none;cursor:pointer">Login</button>
</form></body></html>`))
	})

	r.Post("/auth/emergency", func(w http.ResponseWriter, r *http.Request) {
		failMu.Lock()
		if time.Now().Before(lockUntil) {
			failMu.Unlock()
			slog.Warn("emergency login blocked (locked)", "ip", r.RemoteAddr)
			http.Error(w, "too many attempts, locked for 15 minutes", http.StatusTooManyRequests)
			return
		}
		failMu.Unlock()

		// 限制 emergency 登录 body 大小防止 memory exhaustion —— 这里只接受单个 token
		// 字段，8KB 完全够用。
		r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
		token := r.FormValue("token")
		if !constantTimeEqual(token, s.cfg.Auth.EmergencyToken) {
			failMu.Lock()
			failCount++
			if failCount >= maxAttempts {
				lockUntil = time.Now().Add(lockDuration)
				slog.Error("emergency login LOCKED after max attempts", "ip", r.RemoteAddr, "attempts", failCount)
				failCount = 0
			}
			failMu.Unlock()
			slog.Warn("emergency login failed", "ip", r.RemoteAddr)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		failMu.Lock()
		failCount = 0
		failMu.Unlock()

		slog.Warn("emergency login SUCCESS", "ip", r.RemoteAddr)
		adminEmail := ""
		if len(s.cfg.Auth.AdminEmails) > 0 {
			adminEmail = s.cfg.Auth.AdminEmails[0]
		}
		sig := hmacSign(adminEmail, s.cfg.Auth.EmergencyToken)
		http.SetCookie(w, &http.Cookie{
			Name:     "emergency_auth",
			Value:    adminEmail + "|" + sig,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   3600,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"ok","mode":"emergency"}`))
	})

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hmacSign(data, key string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func slogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", r.RemoteAddr,
		)
	})
}
