package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// noopNext 是一个最小 handler，用于穿透 RateLimitV1。
func noopNext(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func withUserID(req *http.Request, uid int64) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), CtxUserID, uid))
}

// TestRateLimitV1_BurstAllowed：burst 30 默认下，前 30 个请求全部 200。
func TestRateLimitV1_BurstAllowed(t *testing.T) {
	mw := RateLimitV1(100, 30)
	h := mw(http.HandlerFunc(noopNext))

	for i := 0; i < 30; i++ {
		req := withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 42)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d (body=%s)", i+1, rr.Code, rr.Body.String())
		}
	}
}

// TestRateLimitV1_ExceedBurst：burst 用完后第 31 个 429 + IETF headers + Retry-After。
func TestRateLimitV1_ExceedBurst(t *testing.T) {
	mw := RateLimitV1(100, 30)
	h := mw(http.HandlerFunc(noopNext))

	// 把桶用完
	for i := 0; i < 30; i++ {
		req := withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 100)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("warmup request %d expected 200, got %d", i+1, rr.Code)
		}
	}

	// 第 31 个：429
	req := withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 100)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("RateLimit-Limit"); got != "100" {
		t.Errorf("RateLimit-Limit = %q, want 100", got)
	}
	if got := rr.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("RateLimit-Remaining = %q, want 0", got)
	}
	if got := rr.Header().Get("RateLimit-Reset"); got == "" {
		t.Errorf("RateLimit-Reset missing")
	} else if n, err := strconv.Atoi(got); err != nil || n < 1 {
		t.Errorf("RateLimit-Reset = %q (parsed=%d), expect >=1", got, n)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Errorf("Retry-After missing")
	} else if n, err := strconv.Atoi(got); err != nil || n < 1 {
		t.Errorf("Retry-After = %q (parsed=%d), expect >=1", got, n)
	}

	var body struct {
		Errors []struct {
			Field  string `json:"field"`
			Reason string `json:"reason"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body decode: %v (raw=%s)", err, rr.Body.String())
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "rate_limited" {
		t.Fatalf("body = %+v, want errors[0].reason=rate_limited", body.Errors)
	}
}

// TestRateLimitV1_IsolatedPerUser：用户 A 用尽不影响用户 B。
func TestRateLimitV1_IsolatedPerUser(t *testing.T) {
	mw := RateLimitV1(100, 5)
	h := mw(http.HandlerFunc(noopNext))

	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 1))
		if rr.Code != http.StatusOK {
			t.Fatalf("user-1 req %d got %d", i+1, rr.Code)
		}
	}
	rrA := httptest.NewRecorder()
	h.ServeHTTP(rrA, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 1))
	if rrA.Code != http.StatusTooManyRequests {
		t.Fatalf("user-1 6th req: want 429, got %d", rrA.Code)
	}

	// 用户 2 独立桶，照常 200
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 2))
		if rr.Code != http.StatusOK {
			t.Fatalf("user-2 req %d: want 200, got %d", i+1, rr.Code)
		}
	}
}

// TestRateLimitV1_WriteHasOwnBucket：read 用尽不阻塞下一次第一次写，
// 但 write 桶用尽后写操作 429（同时 read 桶 read-bound）。
func TestRateLimitV1_WriteHasOwnBucket(t *testing.T) {
	mw := RateLimitV1(100, 5)
	h := mw(http.HandlerFunc(noopNext))

	// 5 次写：扣 main + write 桶各 5
	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodPost, "/v1/instances", nil), 7))
		if rr.Code != http.StatusOK {
			t.Fatalf("write req %d: got %d", i+1, rr.Code)
		}
	}

	// 第 6 次写：main 桶空 + write 桶也空 → 429
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodPost, "/v1/instances", nil), 7))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("6th write: want 429, got %d", rr.Code)
	}
}

// TestRateLimitV1_HeadersOnSuccess：成功响应也带 RateLimit-* headers，
// 方便客户端 proactive backoff。
func TestRateLimitV1_HeadersOnSuccess(t *testing.T) {
	mw := RateLimitV1(120, 10)
	h := mw(http.HandlerFunc(noopNext))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 11))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("RateLimit-Limit"); got != "120" {
		t.Errorf("RateLimit-Limit = %q, want 120", got)
	}
	if got := rr.Header().Get("RateLimit-Remaining"); got == "" {
		t.Errorf("RateLimit-Remaining missing")
	}
	if got := rr.Header().Get("X-RateLimit-Burst"); got != "10" {
		t.Errorf("X-RateLimit-Burst = %q, want 10", got)
	}
}

// TestRateLimitV1FromEnv_DefaultsWhenUnset：未设 env 时仍要能返回非 nil mw。
func TestRateLimitV1FromEnv_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("INCUS_ADMIN_RATELIMIT_V1_RPM", "")
	t.Setenv("INCUS_ADMIN_RATELIMIT_V1_BURST", "")
	mw := RateLimitV1FromEnv()
	if mw == nil {
		t.Fatal("RateLimitV1FromEnv returned nil")
	}
	h := mw(http.HandlerFunc(noopNext))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 99))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (under default limit), got %d", rr.Code)
	}
	if got := rr.Header().Get("RateLimit-Limit"); got != "100" {
		t.Errorf("default RateLimit-Limit = %q, want 100", got)
	}
}

// TestRateLimitV1FromEnv_ReadsValues：env 配的值要生效。
func TestRateLimitV1FromEnv_ReadsValues(t *testing.T) {
	t.Setenv("INCUS_ADMIN_RATELIMIT_V1_RPM", "300")
	t.Setenv("INCUS_ADMIN_RATELIMIT_V1_BURST", "7")
	mw := RateLimitV1FromEnv()
	h := mw(http.HandlerFunc(noopNext))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, withUserID(httptest.NewRequest(http.MethodGet, "/v1/account", nil), 100))
	if got := rr.Header().Get("RateLimit-Limit"); got != "300" {
		t.Errorf("RateLimit-Limit = %q, want 300", got)
	}
	if got := rr.Header().Get("X-RateLimit-Burst"); got != "7" {
		t.Errorf("X-RateLimit-Burst = %q, want 7", got)
	}
}

// TestRateLimitV1_FallsBackToIPWhenUserMissing：CtxUserID 缺失时退回 IP key，
// 不能 nil-panic。
func TestRateLimitV1_FallsBackToIPWhenUserMissing(t *testing.T) {
	mw := RateLimitV1(100, 30)
	h := mw(http.HandlerFunc(noopNext))

	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("no-ctx request should still allow first hit, got %d", rr.Code)
	}
}

// TestRequireBearer_NoTokenValidator：tokenValidator nil 时应 503。
func TestRequireBearer_NoTokenValidator(t *testing.T) {
	// 保存并恢复全局 tokenValidator
	saved := tokenValidator
	tokenValidator = nil
	defer func() { tokenValidator = saved }()

	h := RequireBearer(http.HandlerFunc(noopNext))
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer ica_abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when validator unset, got %d", rr.Code)
	}
}

// TestRequireBearer_MissingHeader：无 Authorization 返 401 StructuredError。
func TestRequireBearer_MissingHeader(t *testing.T) {
	saved := tokenValidator
	tokenValidator = func(_ context.Context, _ string) (int64, error) { return 1, nil }
	defer func() { tokenValidator = saved }()

	h := RequireBearer(http.HandlerFunc(noopNext))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/account", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	var body struct {
		Errors []struct {
			Reason string `json:"reason"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "missing_bearer" {
		t.Fatalf("got %+v, want missing_bearer", body.Errors)
	}
}

// TestRequireBearer_NonIcaPrefix：非 ica_ token 拒绝。
func TestRequireBearer_NonIcaPrefix(t *testing.T) {
	saved := tokenValidator
	tokenValidator = func(_ context.Context, _ string) (int64, error) { return 1, nil }
	defer func() { tokenValidator = saved }()

	h := RequireBearer(http.HandlerFunc(noopNext))
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer xyz_notours")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// TestRequireBearer_Valid：合法 token 写 CtxUserID + CtxAuthMethod。
func TestRequireBearer_Valid(t *testing.T) {
	saved := tokenValidator
	tokenValidator = func(_ context.Context, token string) (int64, error) {
		if token == "ica_good" {
			return 42, nil
		}
		return 0, http.ErrAbortHandler
	}
	defer func() { tokenValidator = saved }()

	var gotUID int64
	var gotMethod string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID, _ = r.Context().Value(CtxUserID).(int64)
		gotMethod, _ = r.Context().Value(CtxAuthMethod).(string)
		w.WriteHeader(http.StatusOK)
	})
	h := RequireBearer(inner)

	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer ica_good")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if gotUID != 42 {
		t.Errorf("CtxUserID = %d, want 42", gotUID)
	}
	if gotMethod != "api_token" {
		t.Errorf("CtxAuthMethod = %q, want api_token", gotMethod)
	}
}
