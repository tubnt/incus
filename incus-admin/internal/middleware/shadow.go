package middleware

import (
	"net/http"
	"regexp"
)

// shadowForbiddenRoutes lists every endpoint a shadow-login session must never
// reach: routes that move balances / have financial blast radius, plus routes
// that expose a target user's secrets (e.g. VM 初始 root 密码). The admin must
// exit shadow and act under their own identity, so money moves are never
// attributed to a user whose resources the admin is debugging, and target
// credentials are never surfaced through an impersonated session.
//
// Keep this list in sync with the audit inventory. An entry here should
// correspond to a real route registered under /api/admin or /api/portal.
//
// PLAN-055 / OPS-052 §1：本中间件已上提到 portal+admin 公共 Group（server.go），
// 故 /api/portal 支付 / 看初始密码接口现在真正受此拒绝保护。
var shadowForbiddenRoutes = []sensitiveRoute{
	// Admin-initiated balance adjustments and top-ups.
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/users/\d+/balance$`)},

	// Payment mutation — portal side.
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/orders/\d+/pay$`)},

	// 看初始凭据：解密后明文 root 密码外发。shadow 会话（管理员冒名）绝不允许
	// 读取目标用户的初始凭据，必须退出 shadow 后以自身身份并经 step-up 才能查看。
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/services/\d+/initial-credentials$`)},
}

func isShadowForbidden(method, path string) bool {
	for _, s := range shadowForbiddenRoutes {
		if s.method == method && s.path.MatchString(path) {
			return true
		}
	}
	return false
}

// RejectShadowSessionOnMoney blocks money-moving / secret-exposing routes when
// the current request is running under a shadow session. Returns 403 with a
// clear JSON body the frontend can surface to the admin.
//
// Mount inside the portal+admin 公共 Group after ProxyAuth so CtxActorID
// reflects shadow state and portal routes are covered too. Non-forbidden
// routes pass through unchanged.
func RejectShadowSessionOnMoney(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorID, _ := r.Context().Value(CtxActorID).(int64)
		if actorID == 0 {
			// Not a shadow session — nothing to reject.
			next.ServeHTTP(w, r)
			return
		}
		if !isShadowForbidden(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"shadow_session_forbidden","message":"Money-moving operations are not allowed while acting as another user. Exit shadow mode first."}`))
	})
}
