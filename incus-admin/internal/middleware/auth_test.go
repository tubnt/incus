package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// selfRoleWrapped wraps RejectSelfRoleChange around a trivial 200 handler.
func selfRoleWrapped() http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return RejectSelfRoleChange(inner)
}

func TestRejectSelfRoleChange_BlocksSelf(t *testing.T) {
	h := selfRoleWrapped()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/42/role", nil)
	req = req.WithContext(context.WithValue(req.Context(), CtxUserID, int64(42)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on self role change, got %d", w.Code)
	}
	if !containsJSONKey(w.Body.String(), "self_role_change_forbidden") {
		t.Fatalf("body missing error key: %s", w.Body.String())
	}
}

func TestRejectSelfRoleChange_AllowsOther(t *testing.T) {
	h := selfRoleWrapped()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/99/role", nil)
	req = req.WithContext(context.WithValue(req.Context(), CtxUserID, int64(42)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 changing another user's role, got %d", w.Code)
	}
}

// 影子会话下真实操作者是 actorID；若 actor 改自己的角色也必须拒绝。
func TestRejectSelfRoleChange_ShadowActorIsSelf(t *testing.T) {
	h := selfRoleWrapped()
	req := httptest.NewRequest(http.MethodPut, "/api/admin/users/42/role", nil)
	ctx := context.WithValue(req.Context(), CtxUserID, int64(7)) // 被冒名目标
	ctx = context.WithValue(ctx, CtxActorID, int64(42))          // 真实操作者
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when shadow actor changes own role, got %d", w.Code)
	}
}

func TestRejectSelfRoleChange_NonRoleRoutePasses(t *testing.T) {
	h := selfRoleWrapped()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/users/42/role"},    // 非 PUT
		{http.MethodPut, "/api/admin/users/42/balance"}, // 非 role 路由
		{http.MethodPost, "/api/admin/users/42/role"},   // 非 PUT
		{http.MethodPut, "/api/admin/vms/vm-a"},         // 无关路由
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), CtxUserID, int64(42)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 pass-through for %s %s, got %d", c.method, c.path, w.Code)
		}
	}
}

// TestComputeLegacyDeadline 覆盖 PLAN-055 §6：未配 / 解析失败时旧格式 emergency
// cookie 不再永久有效，而是回退到 start + 24h 的代码级硬上限。
func TestComputeLegacyDeadline(t *testing.T) {
	start := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)

	// 空值 → start + 24h，绝不是 zero time。
	got := computeLegacyDeadline("", start)
	if got.IsZero() {
		t.Fatalf("empty env must not yield zero deadline (would be permanent)")
	}
	if want := start.Add(24 * time.Hour); !got.Equal(want) {
		t.Fatalf("empty env deadline = %v, want %v", got, want)
	}

	// 非法值 → 同样回退到 start + 24h。
	got = computeLegacyDeadline("not-a-timestamp", start)
	if want := start.Add(24 * time.Hour); !got.Equal(want) {
		t.Fatalf("invalid env deadline = %v, want %v", got, want)
	}

	// 合法 RFC3339 → 采用配置值。
	explicit := "2026-08-01T00:00:00Z"
	got = computeLegacyDeadline(explicit, start)
	want, _ := time.Parse(time.RFC3339, explicit)
	if !got.Equal(want) {
		t.Fatalf("explicit env deadline = %v, want %v", got, want)
	}
}
