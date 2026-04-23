package middleware

import (
	"net/http"
	"regexp"
)

// moneyRoutes lists every endpoint that moves balances, touches invoices,
// or otherwise has financial blast radius. Shadow-login sessions are
// forbidden from reaching these handlers — the admin must exit shadow and
// act under their own identity, so money moves are never attributed to a
// user whose resources the admin is debugging.
//
// Keep this list in sync with the audit inventory. An entry here should
// correspond to a real route registered under /api/admin or /api/portal.
var moneyRoutes = []sensitiveRoute{
	// Admin-initiated balance adjustments and top-ups.
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/admin/users/\d+/balance$`)},

	// Payment / refund / invoice mutations — portal side.
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/orders/\d+/pay$`)},
	{method: http.MethodPost, path: regexp.MustCompile(`^/api/portal/invoices/\d+/refund$`)},
}

func isMoneyRoute(method, path string) bool {
	for _, s := range moneyRoutes {
		if s.method == method && s.path.MatchString(path) {
			return true
		}
	}
	return false
}

// RejectShadowSessionOnMoney blocks money-moving routes when the current
// request is running under a shadow session. Returns 403 with a clear JSON
// body the frontend can surface to the admin.
//
// Mount after ProxyAuth so CtxActorID reflects shadow state. Non-money
// routes pass through unchanged.
func RejectShadowSessionOnMoney(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actorID, _ := r.Context().Value(CtxActorID).(int64)
		if actorID == 0 {
			// Not a shadow session — nothing to reject.
			next.ServeHTTP(w, r)
			return
		}
		if !isMoneyRoute(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"shadow_session_forbidden","message":"Money-moving operations are not allowed while acting as another user. Exit shadow mode first."}`))
	})
}
