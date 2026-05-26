package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRoutes_AllReturn501：把 11 个端点全部挂载，逐个验证返 501 + StructuredError，
// 模拟启动后 curl /v1/account 的验收路径（与 server.go 实际挂载一致）。
func TestRoutes_AllReturn501(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1", New().Routes)

	cases := []struct {
		method string
		path   string
	}{
		// read-only
		{http.MethodGet, "/v1/account"},
		{http.MethodGet, "/v1/instances"},
		{http.MethodGet, "/v1/instances/vm-aabb"},
		{http.MethodGet, "/v1/types"},
		{http.MethodGet, "/v1/regions"},
		{http.MethodGet, "/v1/images"},
		{http.MethodGet, "/v1/ssh-keys"},
		// write
		{http.MethodPost, "/v1/instances"},
		{http.MethodDelete, "/v1/instances/vm-aabb"},
		{http.MethodPost, "/v1/instances/vm-aabb/reboot"},
		{http.MethodPost, "/v1/instances/vm-aabb/shutdown"},
		{http.MethodPost, "/v1/instances/vm-aabb/boot"},
	}

	if got := len(cases); got != EndpointCount {
		t.Fatalf("test cases = %d, EndpointCount = %d (sync table)", got, EndpointCount)
	}

	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status = %d, want 501 (body=%s)",
				c.method, c.path, rr.Code, rr.Body.String())
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s %s: Content-Type = %q, want application/json; charset=utf-8", c.method, c.path, ct)
		}
		var body struct {
			Errors []FieldError `json:"errors"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s: body decode: %v (raw=%s)", c.method, c.path, err, rr.Body.String())
			continue
		}
		if len(body.Errors) != 1 || body.Errors[0].Reason != "not_implemented" {
			t.Errorf("%s %s: body errors = %+v, want [{reason:not_implemented}]",
				c.method, c.path, body.Errors)
		}
	}
}

// TestRoutes_UnknownPathStructuredError：/v1/foo 不能 fall through 到 SPA，
// 必须返 404 + StructuredError。
func TestRoutes_UnknownPathStructuredError(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1", New().Routes)

	req := httptest.NewRequest(http.MethodGet, "/v1/does-not-exist", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, rr.Body.String())
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "not_found" {
		t.Fatalf("body = %+v, want reason=not_found", body.Errors)
	}
}

// TestRoutes_MethodNotAllowed：已知路径用错方法 → 405 + StructuredError。
func TestRoutes_MethodNotAllowed(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1", New().Routes)

	// /v1/account 只有 GET；POST 应该 405
	req := httptest.NewRequest(http.MethodPost, "/v1/account", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body=%s)", rr.Code, rr.Body.String())
	}
	var body struct {
		Errors []FieldError `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "method_not_allowed" {
		t.Fatalf("body = %+v, want reason=method_not_allowed", body.Errors)
	}
}
