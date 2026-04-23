package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newStepUpHandler returns a chi-less handler wrapped with
// RequireRecentAuthOnSensitive for direct ServeHTTP testing.
func newStepUpHandler(lookup StepUpLookup, maxAge time.Duration) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return RequireRecentAuthOnSensitive(lookup, maxAge)(inner)
}

func TestStepUp_NoLookupIsNoOp(t *testing.T) {
	// When step-up lookup isn't wired, middleware must pass through every
	// request unchanged — otherwise deployments without OIDC envs can't boot.
	h := newStepUpHandler(nil, 5*time.Minute)
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/vms/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected pass-through 200, got %d", w.Code)
	}
}

func TestStepUp_NonSensitivePasses(t *testing.T) {
	lookup := func(_ context.Context, _ int64) (*time.Time, error) {
		t.Helper()
		t.Fatalf("lookup should not be called for non-sensitive route")
		return nil, nil
	}
	h := newStepUpHandler(lookup, 5*time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/vms", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStepUp_SensitiveWithoutUserIDIsUnauthorized(t *testing.T) {
	lookup := func(_ context.Context, _ int64) (*time.Time, error) { return nil, nil }
	h := newStepUpHandler(lookup, 5*time.Minute)
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/vms/vm-xyz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestStepUp_SensitiveWithFreshAuthPasses(t *testing.T) {
	recent := time.Now().Add(-30 * time.Second)
	lookup := func(_ context.Context, _ int64) (*time.Time, error) {
		return &recent, nil
	}
	h := newStepUpHandler(lookup, 5*time.Minute)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/vms/vm-xyz/migrate", nil)
	req = req.WithContext(context.WithValue(req.Context(), CtxUserID, int64(1)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestStepUp_SensitiveWithStaleAuthReturnsRedirect(t *testing.T) {
	stale := time.Now().Add(-2 * time.Hour)
	lookup := func(_ context.Context, _ int64) (*time.Time, error) {
		return &stale, nil
	}
	h := newStepUpHandler(lookup, 5*time.Minute)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/vms/vm-xyz", nil)
	req = req.WithContext(context.WithValue(req.Context(), CtxUserID, int64(1)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	var body struct {
		Error    string `json:"error"`
		Redirect string `json:"redirect"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body.Error != "step_up_required" {
		t.Fatalf("error=%q want step_up_required", body.Error)
	}
	if body.Redirect == "" || body.Redirect[:19] != "/api/auth/stepup/st" {
		t.Fatalf("redirect=%q should start with /api/auth/stepup/start", body.Redirect)
	}
}

func TestStepUp_ShadowUsesActorTimestamp(t *testing.T) {
	// Under a shadow session, the lookup should key off actor id, not target.
	// Simulated here by asserting the lookup is called with the actor id.
	const actor = int64(42)
	const target = int64(7)
	recent := time.Now()
	var gotID int64
	lookup := func(_ context.Context, id int64) (*time.Time, error) {
		gotID = id
		return &recent, nil
	}
	h := newStepUpHandler(lookup, 5*time.Minute)

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/vms/vm-xyz", nil)
	ctx := context.WithValue(req.Context(), CtxUserID, target)
	ctx = context.WithValue(ctx, CtxActorID, actor)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if gotID != actor {
		t.Fatalf("lookup id=%d want actor %d", gotID, actor)
	}
}

func TestStepUp_IsSensitiveMatchesAllEnumeratedRoutes(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{"DELETE", "/api/admin/vms/vm-abc", true},
		{"POST", "/api/admin/vms/vm-abc/migrate", true},
		{"POST", "/api/admin/clusters/cn-sz-01/nodes/node1/evacuate", true},
		{"POST", "/api/admin/clusters/cn-sz-01/nodes/node1/restore", true},
		{"POST", "/api/admin/nodes/node1/evacuate", true},
		{"POST", "/api/admin/nodes/node1/restore", true},
		{"POST", "/api/admin/users/42/balance", true},

		// Negatives — must not match.
		{"GET", "/api/admin/vms/vm-abc", false},
		{"DELETE", "/api/admin/vms", false},
		{"POST", "/api/admin/users/abc/balance", false},
		{"POST", "/api/admin/vms/vm-abc/start", false},
	}
	for _, c := range cases {
		if got := isSensitive(c.method, c.path); got != c.want {
			t.Errorf("isSensitive(%s %s)=%v want %v", c.method, c.path, got, c.want)
		}
	}
}
