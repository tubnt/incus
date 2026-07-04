package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
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
	tokenValidator    TokenValidator
	emergencySecret   string
	shadowVerifier    ShadowVerifier
	proxySharedSecret string
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

// SetProxySharedSecret 接线 PLAN-055 决策#6 的可选前置代理信任加固开关。
// 空字符串（默认）= 关闭：ProxyAuth 完全跳过签名校验，行为与现网一致。
func SetProxySharedSecret(secret string) {
	proxySharedSecret = secret
}

// untrustedProxyHeaders 列出仅应由受信前置代理注入、客户端绝不可伪造的头。
// 当 PROXY_SHARED_SECRET 已开启且请求未通过签名校验时，这些头会被剥离，
// 使下游只信任直连 RemoteAddr（IP 场景回退直连），且伪造的身份头无法冒充登录。
//
// 注意：故意不含 Authorization / Cookie —— Bearer token 与 emergency/shadow
// cookie 都是自带 HMAC/token 的自证明凭据，不依赖"代理是否可信"，剥离它们
// 反而会误伤合法直连的 API/应急通道。
var untrustedProxyHeaders = []string{
	"X-Forwarded-For",      // 客户端真实 IP（realClientIP / 限流 key 依赖）
	"X-Real-Ip",            // chi RealIP 的备选来源
	"X-Auth-Request-Email", // oauth2-proxy 注入的登录身份
	"X-Forwarded-Email",    // oauth2-proxy 身份的兼容别名
}

// proxyHeadersTrusted 判定本请求携带的代理头是否可信。
//
//   - proxySharedSecret 为空（默认）：始终返回 true —— 加固关闭，零行为变化。
//   - 已开启：要求 X-Proxy-Signature 头存在且与共享密钥 constant-time 相等。
//
// 采用"静态共享密钥直接比对"而非动态 HMAC，是因为签名由反代（nginx/Caddy 等）
// 用一行静态 proxy_set_header 注入即可，运维成本最低，符合决策#6 的 opt-in 定位。
func proxyHeadersTrusted(r *http.Request) bool {
	if proxySharedSecret == "" {
		return true
	}
	sig := r.Header.Get("X-Proxy-Signature")
	if sig == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sig), []byte(proxySharedSecret)) == 1
}

// stripUntrustedProxyHeaders 在签名校验失败时剥离可伪造的代理头（保守方案：
// 不直接拒绝请求，避免运维刚开启开关、反代尚未配好签名时把现网打挂；但绝不
// 采信伪造头——IP 回退直连 RemoteAddr，冒充的身份头被清空后 oauth2-proxy
// header 认证自然回落 401）。
func stripUntrustedProxyHeaders(r *http.Request) {
	for _, h := range untrustedProxyHeaders {
		r.Header.Del(h)
	}
}

// verifyEmergencyCookie 校验 emergency cookie。两种格式兼容：
//   - 新格式 (Session-1 W7 / PLAN-051 §2-B 决策 D-07)：email|expires_unix|hmac
//     带 10 分钟 TTL，过期自动失效；email 与 expires 一同进 HMAC，防止单独篡改
//   - 旧格式：email|hmac（仅签 email，永久有效，仅在 EMERGENCY_TOKEN 轮换前生效）
//     OPS 升级期间 cookie 同存才不至于把现网应急通道一刀切死。下次 token 轮换后
//     旧格式自然失效；本函数读到旧格式打 warn 但仍接受，便于运维一次切换。
//
// 调用方传 cookie.Value（含完整 a|b|c），自身 SplitN 不再适用。
func verifyEmergencyCookie(cookieValue string) (email string, ok bool) {
	if emergencySecret == "" || cookieValue == "" {
		return "", false
	}
	parts := strings.Split(cookieValue, "|")
	switch len(parts) {
	case 3:
		// 新格式 email|expires_unix|hmac
		emailV, expS, sig := parts[0], parts[1], parts[2]
		if emailV == "" || expS == "" || sig == "" {
			return "", false
		}
		// 解析过期时间
		var exp int64
		if _, err := fmt.Sscanf(expS, "%d", &exp); err != nil {
			return "", false
		}
		if time.Now().Unix() > exp {
			slog.Warn("emergency cookie expired", "email", emailV, "exp", exp)
			return "", false
		}
		// HMAC over email|expires
		h := hmac.New(sha256.New, []byte(emergencySecret))
		h.Write([]byte(emailV + "|" + expS))
		expected := hex.EncodeToString(h.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
			return "", false
		}
		return emailV, true
	case 2:
		// pma-cr M-1 / PLAN-051 §2-B：旧格式 grace deadline。EMERGENCY_LEGACY_DEADLINE
		// 为 RFC3339 时间戳；过该时间后旧格式（无 TTL）一律拒绝。空值表示当前
		// 还在 grace 期（向后兼容）。建议运维一次性配 +30 天，到期后删除该 env，
		// 自然进入"仅新格式"模式。
		if deadline := getLegacyDeadline(); !deadline.IsZero() && time.Now().After(deadline) {
			slog.Warn("emergency cookie legacy format rejected after deadline", "deadline", deadline)
			return "", false
		}
		emailV, sig := parts[0], parts[1]
		if emailV == "" {
			return "", false
		}
		h := hmac.New(sha256.New, []byte(emergencySecret))
		h.Write([]byte(emailV))
		expected := hex.EncodeToString(h.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
			return "", false
		}
		slog.Warn("emergency cookie using legacy format (no TTL); rotate EMERGENCY_TOKEN to invalidate", "email", emailV)
		return emailV, true
	}
	return "", false
}

var (
	legacyDeadlineOnce sync.Once
	legacyDeadlineVal  time.Time
)

// getLegacyDeadline 解析 EMERGENCY_LEGACY_DEADLINE env（RFC3339）；空值或解析
// 失败返 zero time（grace 阶段，旧格式仍有效）。
func getLegacyDeadline() time.Time {
	legacyDeadlineOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("EMERGENCY_LEGACY_DEADLINE"))
		if raw == "" {
			return
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			slog.Warn("EMERGENCY_LEGACY_DEADLINE parse failed; treating as no deadline (legacy still accepted)", "raw", raw, "error", err)
			return
		}
		legacyDeadlineVal = t
	})
	return legacyDeadlineVal
}

func ProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// PLAN-055 决策#6：可选的前置代理信任加固（opt-in）。默认关闭时
		// proxyHeadersTrusted 恒为 true，整段短路，行为与现网完全一致。
		// 开启后：签名校验不通过 → 剥离伪造的 X-Forwarded-For / 身份头，
		// 与 WP-A 的 TRUSTED_PROXIES 语义叠加（本层只做签名闸，不改其网段判定）。
		if !proxyHeadersTrusted(r) {
			slog.Warn("proxy signature check failed; stripping untrusted proxy headers", "remote", r.RemoteAddr, "path", r.URL.Path)
			stripUntrustedProxyHeaders(r)
		}

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

		// emergency cookie 认证（HMAC 签名 + TTL 校验）
		if cookie, err := r.Cookie("emergency_auth"); err == nil {
			if email, ok := verifyEmergencyCookie(cookie.Value); ok {
				ctx := r.Context()
				ctx = context.WithValue(ctx, CtxUserEmail, email)
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

// RequireBearer 为 /v1/* 提供 Bearer-only 鉴权。与 ProxyAuth 的区别：
//
//   - 不接受 oauth2-proxy header / shadow cookie / emergency cookie
//   - 只接受 Authorization: Bearer ica_xxx
//   - 失败时返 401 + StructuredError (`{"errors":[{"field":"","reason":"unauthorized"}]}`)
//
// 通过后写入 CtxUserID + CtxAuthMethod="api_token"，与 ProxyAuth 的 Bearer 分支
// 保持一致，下游 handler 可继续用 CtxUserID 取用户。
func RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenValidator == nil {
			slog.Error("RequireBearer used but tokenValidator unset — refusing all v1 requests")
			writeBearerErr(w, http.StatusServiceUnavailable, "token_validator_unset")
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeBearerErr(w, http.StatusUnauthorized, "missing_bearer")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if !strings.HasPrefix(token, "ica_") {
			writeBearerErr(w, http.StatusUnauthorized, "invalid_token")
			return
		}
		userID, err := tokenValidator(r.Context(), token)
		if err != nil || userID <= 0 {
			slog.Warn("v1 bearer auth failed", "error", err, "path", r.URL.Path)
			writeBearerErr(w, http.StatusUnauthorized, "invalid_token")
			return
		}
		ctx := context.WithValue(r.Context(), CtxUserID, userID)
		ctx = context.WithValue(ctx, CtxAuthMethod, "api_token")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeBearerErr 写入与 v1 包一致的 StructuredError 响应体。
// 不引用 v1 包以避免 middleware → v1 的反向依赖；用 encoding/json 而非
// 字符串拼接，避免后续新增 reason 含特殊字符时被 JSON 注入。
func writeBearerErr(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{
			{"field": "", "reason": reason},
		},
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
				// 客户端取消（关闭浏览器/超时/导航离开）会让 DB query 返回 context canceled。
				// 这不是真错误，记 DEBUG 即可，不要打 ERROR 噪音；此时 client 已断开，
				// 写不写 response 不重要，但保持原 500 兜底以防代码路径被 unit test 触发。
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					slog.Debug("user lookup aborted by client", "email", email, "error", err)
				} else {
					slog.Error("user lookup failed", "email", email, "error", err)
				}
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
