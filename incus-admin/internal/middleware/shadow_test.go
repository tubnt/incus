package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func shadowWrapped() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return RejectShadowSessionOnMoney(inner)
}

func TestShadowGate_NoActorPassesEverywhere(t *testing.T) {
	h := shadowWrapped()
	for _, path := range []string{
		"/api/admin/users/1/balance",
		"/api/portal/orders/1/pay",
		"/api/portal/invoices/1/refund",
		"/api/admin/vms/vm-a",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 on non-shadow %s, got %d", path, w.Code)
		}
	}
}

func TestShadowGate_BlocksMoneyRoutesUnderShadow(t *testing.T) {
	h := shadowWrapped()
	money := []string{
		"/api/admin/users/42/balance",
		"/api/portal/orders/7/pay",
		"/api/portal/invoices/9/refund",
	}
	for _, path := range money {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), CtxActorID, int64(99)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 under shadow for %s, got %d", path, w.Code)
		}
		if !containsJSONKey(w.Body.String(), "shadow_session_forbidden") {
			t.Errorf("body missing error key: %s", w.Body.String())
		}
	}
}

func TestShadowGate_AllowsNonMoneyUnderShadow(t *testing.T) {
	h := shadowWrapped()
	// Non-money under shadow should still pass — admin debugging a user's VM.
	cases := []string{
		"/api/admin/vms/vm-a",
		"/api/portal/ssh-keys",
		"/api/portal/tickets/1/messages",
	}
	for _, path := range cases {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req = req.WithContext(context.WithValue(req.Context(), CtxActorID, int64(99)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 for non-money %s under shadow, got %d", path, w.Code)
		}
	}
}

// containsJSONKey is a trivial substring check that avoids pulling in
// encoding/json for what's essentially an error-code assertion.
func containsJSONKey(body, key string) bool {
	return len(body) > 0 && len(key) > 0 && indexOf(body, key) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
