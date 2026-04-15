package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	Snaps    AdminRouteRegistrar
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
	Audit     AdminRouteRegistrar
	APITokens RouteRegistrar
	Invoices  interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
}

func New(cfg *config.Config, userLookup func(ctx context.Context, email string) (int64, string, error), roleLookup func(ctx context.Context, userID int64) (string, error), h Handlers) *Server {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(slogMiddleware)

	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.ProxyAuth)
		r.Use(middleware.UserFromEmail(userLookup, roleLookup))

		r.Route("/api/portal", func(r chi.Router) {
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
			if h.APITokens != nil {
				h.APITokens.Routes(r)
			}
			if h.Invoices != nil {
				h.Invoices.PortalRoutes(r)
			}
		})

		r.Route("/api/admin", func(r chi.Router) {
			r.Use(middleware.RequireRole("admin"))
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
		})

		if h.Console != nil {
			r.Get("/api/console", h.Console.HandleConsole)
		}

		r.Get("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
			email, _ := r.Context().Value(middleware.CtxUserEmail).(string)
			userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
			role, _ := r.Context().Value(middleware.CtxUserRole).(string)
			writeJSON(w, http.StatusOK, map[string]any{
				"id":    userID,
				"email": email,
				"name":  email,
				"role":  role,
				"balance": 0,
			})
		})
	})

	// SPA static files (catch-all, must be last)
	r.NotFound(staticHandler().ServeHTTP)

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
		Addr:    s.cfg.Server.EmergencyListen,
		Handler: s.emergencyRouter(),
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
		// TODO: set session cookie for the first admin email
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
