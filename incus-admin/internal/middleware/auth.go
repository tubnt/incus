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
	"regexp"
	"strconv"
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
		// pma-cr M-1 / PLAN-051 §2-B + PLAN-055 §6：旧格式 grace deadline。
		// EMERGENCY_LEGACY_DEADLINE 为 RFC3339 时间戳；过该时间后旧格式（无 TTL）
		// 一律拒绝。未配置时不再永久有效，而是回退到 processStart + 24h 的代码级
		// 硬上限（见 getLegacyDeadline）。建议运维一次性配 +30 天，到期后删除该
		// env，自然进入"仅新格式"模式。
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
	// processStart 记录进程启动时刻，用于给旧格式 emergency cookie 一个代码级
	// 默认硬上限（见 getLegacyDeadline）。
	processStart = time.Now()
)

// legacyDefaultTTL 是旧格式 emergency cookie（email|hmac，无自带 TTL）在未显式
// 配置 EMERGENCY_LEGACY_DEADLINE 时的代码级默认硬上限：进程启动后 24h 内旧格式
// 仍可接受，之后一律拒绝。旧格式 cookie 本身不含签发时间，无法做 per-cookie TTL，
// 因此以"进程启动 + 固定窗口"作为兜底上界，杜绝"未配 deadline → 旧格式永久有效"
// 的隐患（PLAN-055 / OPS-052 §6）。运维如需更长 grace，显式配置
// EMERGENCY_LEGACY_DEADLINE 覆盖此默认。
const legacyDefaultTTL = 24 * time.Hour

// getLegacyDeadline 解析 EMERGENCY_LEGACY_DEADLINE env（RFC3339），结果缓存。
// 空值或解析失败时不再返回 zero time（那会让旧格式永久有效），而是回退到
// processStart + legacyDefaultTTL 的代码级硬上限。纯计算逻辑抽到
// computeLegacyDeadline 便于单测。
func getLegacyDeadline() time.Time {
	legacyDeadlineOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("EMERGENCY_LEGACY_DEADLINE"))
		legacyDeadlineVal = computeLegacyDeadline(raw, processStart)
	})
	return legacyDeadlineVal
}

// computeLegacyDeadline 是 getLegacyDeadline 的纯函数核心：
//   - raw 为合法 RFC3339 → 返回该时间。
//   - raw 为空或解析失败 → 返回 start + legacyDefaultTTL（代码级硬上限），
//     绝不返回 zero time，杜绝"未配 deadline → 旧格式永久有效"。
func computeLegacyDeadline(raw string, start time.Time) time.Time {
	if raw == "" {
		return start.Add(legacyDefaultTTL)
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		slog.Warn("EMERGENCY_LEGACY_DEADLINE parse failed; falling back to code-level default cap (process start + 24h)", "raw", raw, "error", err)
		return start.Add(legacyDefaultTTL)
	}
	return t
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

// roleUpdateRoute 匹配 PUT /api/admin/users/{id}/role —— 单条改角色端点。
var roleUpdateRoute = regexp.MustCompile(`^/api/admin/users/(\d+)/role$`)

// RejectSelfRoleChange 阻止管理员修改自己的角色（自我提权 / 自我降权）。
// PLAN-055 / OPS-052 §2：仅拦截 PUT /api/admin/users/{id}/role；当路径 {id}
// 等于当前操作者（shadow 会话下取 CtxActorID，否则取 CtxUserID）时返回 403。
// 非该路由 / 非 PUT 一律透传。step-up 由 RequireRecentAuthOnSensitive 另行强制。
func RejectSelfRoleChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			next.ServeHTTP(w, r)
			return
		}
		m := roleUpdateRoute.FindStringSubmatch(r.URL.Path)
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}
		targetID, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			// 正则已保证是数字，理论不会到这；保守透传给 handler 出 400。
			next.ServeHTTP(w, r)
			return
		}
		// 操作者身份：shadow 会话下 CtxUserID 是被冒名的目标用户，真实操作者在
		// CtxActorID；非 shadow 时 CtxActorID 为空，回退 CtxUserID。
		selfID, _ := r.Context().Value(CtxActorID).(int64)
		if selfID == 0 {
			selfID, _ = r.Context().Value(CtxUserID).(int64)
		}
		if selfID > 0 && selfID == targetID {
			slog.Warn("self role change rejected", "user_id", selfID, "path", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"self_role_change_forbidden","message":"You cannot change your own role."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
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
