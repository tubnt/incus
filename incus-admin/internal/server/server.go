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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/incuscloud/incus-admin/internal/config"
	v1handler "github.com/incuscloud/incus-admin/internal/handler/v1"
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
	// NodeCredentials (PLAN-033 / OPS-039) exposes admin CRUD for SSH
	// credentials used to bootstrap new nodes. Step-up gated.
	NodeCredentials AdminRouteRegistrar
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
	// OSTemplates registers PLAN-021 Phase A endpoints
	// (portal GET /portal/os-templates; admin CRUD under /admin/os-templates).
	OSTemplates interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	// Firewall registers PLAN-021 Phase E endpoints (admin CRUD on groups
	// + portal bind/unbind on VMs).
	Firewall interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	// FloatingIPs registers PLAN-021 Phase G endpoints. Admin handles
	// allocate / release / attach-by-vm-id / detach. Portal lets the VM
	// owner attach/detach a Floating IP to one of their own VMs without
	// going through admin.
	FloatingIPs interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	// Rescue registers PLAN-021 Phase D safe-mode endpoints (enter / exit).
	// Admin routes use VM id or name; portal routes are id-only with owner
	// check inside the handler.
	Rescue interface {
		AdminRouteRegistrar
		PortalRouteRegistrar
	}
	// Jobs (PLAN-025) 暴露 portal-only 异步 provisioning 进度查询 + SSE：
	//   GET /portal/jobs/{id}        → 当前状态 + 全部 step + 完成态结果
	//   GET /portal/jobs/{id}/stream → SSE 流（Last-Event-ID 重连）
	Jobs interface {
		PortalRouteRegistrar
		AdminRouteRegistrar
	}
	// Auth exposes the step-up OIDC round-trip handlers. Start requires an
	// active session (mounted inside the ProxyAuth group); Callback is reached
	// directly from Logto (mounted outside, allowlisted by oauth2-proxy's
	// skip_auth_routes).
	Auth StepUpHandler
	// Shadow exposes LoginAdmin (admin-only), Enter (consumes the signed
	// token and sets the cookie) and Exit (clears the cookie). All three
	// require an existing oauth2-proxy session.
	Shadow ShadowHandler
	// PLAN-041 / INFRA-009 监控告警闭环。三个新 handler：
	//   - NotifyChannels: admin CRUD + 测试发送
	//   - AlertRules:     admin CRUD + 启停 + 历史送达
	//   - PromExport:     /api/metrics Prometheus 端点（默认匿名 + 可选 Bearer）
	NotifyChannels AdminRouteRegistrar
	AlertRules     AdminRouteRegistrar
	PromExport     http.Handler
	// PLAN-042 / INFRA-010 OpenAPI spec 暴露 + Swagger UI。
	// /api/openapi.yaml + /api/openapi.json + /api/docs。无鉴权（spec 是公开契约）。
	OpenAPI OpenAPIHandler
	// V1 (PLAN-053 / INFRA-012) registers the cloud-gateway-standard
	// /v1/* adapter routes. Mounted outside the ProxyAuth group with
	// Bearer-only auth + dedicated token-bucket rate limit.
	V1 RouteRegistrar
}

// OpenAPIHandler 把 openapi handler 的两个端点单独标出来，
// 让 server.go 注册时能直接挂多个 path。
type OpenAPIHandler interface {
	ServeYAML(w http.ResponseWriter, r *http.Request)
	ServeJSON(w http.ResponseWriter, r *http.Request)
	ServeUI(w http.ResponseWriter, r *http.Request)
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
	// Session-2 F-28 / PLAN-051 §2-H：原版 chimw.Timeout(60s) 是全局 middleware，
	// 把 /api/console (WebSocket) 与 /api/portal/jobs/{id}/stream (SSE) 都砍在
	// 60s——长重装、长加入节点等场景前端直接断流。改为路径白名单豁免：流式路径
	// 不进 timeout context，由 hijack/upgrade 自身控制超时。
	// pma-cr M-4：firewall batch bind/unbind 串行最多 64 VM × 30s/item = 320s
	// 全局 timeout 60s 不够。在 worker pool 全完成前 chi cancel 会砍剩余 items。
	// 加入白名单后由 portalRunBatch 自身的 worker pool + per-item 30s 控制。
	r.Use(timeoutExceptStreaming(60*time.Second, "/api/console", "/api/portal/jobs/", "/api/admin/jobs/", "/api/portal/firewall/groups/"))
	r.Use(slogMiddleware)
	// Session-3 §1🔴-1：HTTP 响应压缩。等级 5 在 CPU 与体积之间折中。entry JS
	// 307 KB → 96 KB gzip（3.2× 收益），CSS 175 KB → 58 KB；首屏 LCP 直接受益。
	// SSE / WS 路径走的是 hijack/upgrade，不会被压缩中间件触发。
	r.Use(chimw.Compress(5,
		"text/html",
		"text/css",
		"text/plain",
		"application/javascript",
		"application/json",
		"application/wasm",
		"image/svg+xml",
	))

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

	// PLAN-041 / INFRA-009：Prometheus exposition 端点。挂在 ProxyAuth 之外，
	// 让外部 Prom / Grafana 直接抓取（默认匿名；可选 Bearer 由 PromExport 自己处理）。
	if h.PromExport != nil {
		r.Method(http.MethodGet, "/api/metrics", h.PromExport)
	}

	// PLAN-042 / INFRA-010：OpenAPI spec + Swagger UI。挂在 ProxyAuth 之外，
	// 公开契约（spec 不含敏感信息，其访问反映"我是怎样的 API"）。
	if h.OpenAPI != nil {
		r.Get("/api/openapi.yaml", h.OpenAPI.ServeYAML)
		r.Get("/api/openapi.json", h.OpenAPI.ServeJSON)
		r.Get("/api/docs", h.OpenAPI.ServeUI)
		r.Get("/api/docs/", h.OpenAPI.ServeUI)
	}

	// Step-up OIDC callback lives outside the ProxyAuth group because Logto
	// redirects the browser back here after the user completes re-authentication.
	// oauth2-proxy's skip_auth_routes must also include this path; otherwise
	// the callback would be intercepted before reaching incus-admin.
	if h.Auth != nil {
		r.Get("/api/auth/stepup-callback", h.Auth.Callback)
	}

	// PLAN-053 Phase A: cloud-gateway 标准 /v1/* 适配层。Bearer-only 鉴权
	// 替代 ProxyAuth，所以挂在 ProxyAuth Group 之外；限流走独立 token-bucket
	// 桶，与 portal 共享桶隔离开。
	if h.V1 != nil {
		r.Route("/v1", func(r chi.Router) {
			r.Use(middleware.RequireBearer)
			r.Use(middleware.RateLimitV1FromEnv())
			h.V1.Routes(r)
		})
		slog.Info("v1 routes registered", "endpoints", v1handler.EndpointCount)
	}

	r.Group(func(r chi.Router) {
		r.Use(middleware.ProxyAuth)
		r.Use(middleware.UserFromEmail(userLookup, roleLookup))
		// pma-cr H-2 / Session-1 W4：高敏端点独立 5/min 限流。挂在 Group 顶层，
		// 覆盖 /api/auth/stepup/* + /api/admin/users:batch + /shadow/* 等子路径。
		// 非匹配路径透传，不影响 /api/portal 的 30/min（下面单独挂）。
		r.Use(middleware.RateLimitSensitive)

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
			if h.OSTemplates != nil {
				h.OSTemplates.PortalRoutes(r)
			}
			if h.Firewall != nil {
				h.Firewall.PortalRoutes(r)
			}
			if h.Rescue != nil {
				h.Rescue.PortalRoutes(r)
			}
			if h.FloatingIPs != nil {
				h.FloatingIPs.PortalRoutes(r)
			}
			if h.Jobs != nil {
				h.Jobs.PortalRoutes(r)
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
			// PLAN-038 / OPS-041 Tier 3：admin-only ai-diagnose
			if h.Jobs != nil {
				h.Jobs.AdminRoutes(r)
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
			if h.NodeCredentials != nil {
				h.NodeCredentials.AdminRoutes(r)
			}
			if h.Events != nil {
				h.Events.AdminRoutes(r)
			}
			if h.Healing != nil {
				h.Healing.AdminRoutes(r)
			}
			if h.OSTemplates != nil {
				h.OSTemplates.AdminRoutes(r)
			}
			if h.Firewall != nil {
				h.Firewall.AdminRoutes(r)
			}
			if h.FloatingIPs != nil {
				h.FloatingIPs.AdminRoutes(r)
			}
			if h.Rescue != nil {
				h.Rescue.AdminRoutes(r)
			}
			// PLAN-041 / INFRA-009 监控告警 admin handler
			if h.NotifyChannels != nil {
				h.NotifyChannels.AdminRoutes(r)
			}
			if h.AlertRules != nil {
				h.AlertRules.AdminRoutes(r)
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

	// Session-2 F-29 / PLAN-051 §2-A：原版 failCount 是全局变量，攻击者可
	// 把任意 IP 的 5 次失败"借给"另一个 IP，触发后整网卡 15min。改为 per-IP
	// LRU（容量 1024，新条目挤旧）+ 锁内部局部 mutex。同时把 token compare
	// 与 lock check 顺序保持不变（先 lock 再 compare），constantTimeEqual 已
	// 在使用 subtle.ConstantTimeCompare（W7 旧约束已落地，此处只补 per-IP）。
	type ipBucket struct {
		failCount int
		lockUntil time.Time
	}
	var (
		buckets = make(map[string]*ipBucket, 64)
		bMu     sync.Mutex
	)
	const maxAttempts = 5
	const lockDuration = 15 * time.Minute
	const maxBuckets = 1024
	getBucket := func(ip string) *ipBucket {
		bMu.Lock()
		defer bMu.Unlock()
		b, ok := buckets[ip]
		if ok {
			return b
		}
		// 简单容量控制：到上限随机踢一条已解锁条目；保持 in-flight 锁定不动
		if len(buckets) >= maxBuckets {
			now := time.Now()
			for k, v := range buckets {
				if now.After(v.lockUntil) {
					delete(buckets, k)
					break
				}
			}
		}
		nb := &ipBucket{}
		buckets[ip] = nb
		return nb
	}

	r.Get("/auth/emergency", func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPForRateLimit(r)
		b := getBucket(ip)
		bMu.Lock()
		locked := time.Now().Before(b.lockUntil)
		bMu.Unlock()

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
		ip := clientIPForRateLimit(r)
		b := getBucket(ip)

		bMu.Lock()
		if time.Now().Before(b.lockUntil) {
			bMu.Unlock()
			slog.Warn("emergency login blocked (locked)", "ip", ip)
			http.Error(w, "too many attempts, locked for 15 minutes", http.StatusTooManyRequests)
			return
		}
		bMu.Unlock()

		// 限制 emergency 登录 body 大小防止 memory exhaustion —— 这里只接受单个 token
		// 字段，8KB 完全够用。
		r.Body = http.MaxBytesReader(w, r.Body, 8*1024)
		token := r.FormValue("token")
		if !constantTimeEqual(token, s.cfg.Auth.EmergencyToken) {
			bMu.Lock()
			b.failCount++
			if b.failCount >= maxAttempts {
				b.lockUntil = time.Now().Add(lockDuration)
				slog.Error("emergency login LOCKED after max attempts", "ip", ip, "attempts", b.failCount)
				b.failCount = 0
			}
			bMu.Unlock()
			slog.Warn("emergency login failed", "ip", ip)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		bMu.Lock()
		b.failCount = 0
		bMu.Unlock()

		slog.Warn("emergency login SUCCESS", "ip", ip)
		adminEmail := ""
		if len(s.cfg.Auth.AdminEmails) > 0 {
			adminEmail = s.cfg.Auth.AdminEmails[0]
		}
		// Session-1 W7 / PLAN-051 §2-B 决策 D-07：emergency cookie 改 TTL 形态
		// (email|expires_unix|hmac)，10 min 后无论 cookie 是否被偷都自动失效。
		// MaxAge 同步收紧到 600s，cookie 在浏览器侧也立刻清除。
		exp := time.Now().Add(10 * time.Minute).Unix()
		expS := strconv.FormatInt(exp, 10)
		sig := hmacSign(adminEmail+"|"+expS, s.cfg.Auth.EmergencyToken)
		// pma-cr H-1：Secure 条件化。emergency 监听 127.0.0.1:8081 设计上是 HTTP；
		// 运维通过 SSH tunnel 转发到本机 → 浏览器看到 http://localhost:<port>。
		// 如果硬编码 Secure=true，浏览器会拒绝 set-cookie（非 https 上下文），
		// emergency 通道默默废掉。读 r.TLS 决定：仅 TLS 终结情况下加 Secure。
		// localhost 在主流浏览器是 secure context 例外，但 spec 上不保证；
		// 改 fail-safe：HTTP 时不加 Secure（emergency 走 SSH tunnel 已是私密通道）。
		secure := r.TLS != nil
		http.SetCookie(w, &http.Cookie{
			Name:     "emergency_auth",
			Value:    adminEmail + "|" + expS + "|" + sig,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   600,
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
	// Session-2 F-30 / PLAN-051 §2-K：encode 错误打 slog warn 不再静默吞咽。
	// 客户端已断开时 err = "broken pipe"，是常见情况，仍 warn 记录便于排查 backend bug。
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writeJSON encode failed", "error", err, "status", status)
	}
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// clientIPForRateLimit 从 r.RemoteAddr 摘 IP（去 :port），用于 emergency login
// per-IP 限流。emergency 监听 127.0.0.1:8081 通过 SSH tunnel 接入，不走前置代理；
// 因此不读 X-Forwarded-For，避免攻击者伪造头绕过单 IP 限制。
func clientIPForRateLimit(r *http.Request) string {
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		addr = addr[:idx]
	}
	if addr == "" {
		addr = "unknown"
	}
	return addr
}

func hmacSign(data, key string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// timeoutExceptStreaming 是 chimw.Timeout 的白名单变体。匹配 prefixes 之一时
// 完全跳过 timeout（SSE / WS 自己管 keep-alive），其它路径走标准 chimw.Timeout。
// stream prefix 还细分 jobs：/portal/jobs/{id}/stream + /admin/jobs/{id}/ai-diagnose
// 都属于长连接 / 长操作。匹配是 HasPrefix，调用方传精确前缀避免误伤 /jobs 列表 API。
func timeoutExceptStreaming(d time.Duration, prefixes ...string) func(http.Handler) http.Handler {
	standard := chimw.Timeout(d)
	return func(next http.Handler) http.Handler {
		wrapped := standard(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			for _, p := range prefixes {
				if strings.HasPrefix(path, p) {
					// 精确再判一次 stream 子路径，避免 /portal/jobs/{id} 详情接口（短查询）也被豁免
					if strings.HasPrefix(path, "/api/portal/jobs/") || strings.HasPrefix(path, "/api/admin/jobs/") {
						if strings.HasSuffix(path, "/stream") || strings.Contains(path, "/ai-diagnose") {
							next.ServeHTTP(w, r)
							return
						}
						break // 落到 wrapped 走 timeout
					}
					// pma-cr M-4：firewall 仅 :bind / :unbind 批量端点豁免；
					// GET / CRUD 等短查询仍走 60s timeout
					if strings.HasPrefix(path, "/api/portal/firewall/groups/") {
						if strings.HasSuffix(path, "/bind") || strings.HasSuffix(path, "/unbind") ||
							strings.Contains(path, ":bind") || strings.Contains(path, ":unbind") {
							next.ServeHTTP(w, r)
							return
						}
						break
					}
					next.ServeHTTP(w, r)
					return
				}
			}
			wrapped.ServeHTTP(w, r)
		})
	}
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
