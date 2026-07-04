package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// downstream 回显 ProxyAuth 处理后下游能看到的登录 email 与 X-Forwarded-For，
// 便于断言"代理头是否被采信 / 是否被剥离"。
func proxyProbeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, _ := r.Context().Value(CtxUserEmail).(string)
		w.Header().Set("X-Probe-Email", email)
		w.Header().Set("X-Probe-XFF", r.Header.Get("X-Forwarded-For"))
		w.WriteHeader(http.StatusOK)
	})
}

// TestProxyAuth_SecretUnset_DefaultBehaviorUnchanged 回归断言：PROXY_SHARED_SECRET
// 为空（默认）时行为与现网完全一致——oauth2-proxy 身份头照常被采信，
// X-Forwarded-For 原样透传给下游。这是决策#6"不改现网默认行为"的守门测试。
func TestProxyAuth_SecretUnset_DefaultBehaviorUnchanged(t *testing.T) {
	SetProxySharedSecret("")
	defer SetProxySharedSecret("")

	req := httptest.NewRequest(http.MethodGet, "/api/portal/me", nil)
	req.Header.Set("X-Auth-Request-Email", "User@Example.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()

	ProxyAuth(proxyProbeHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("默认关闭时应放行，got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Probe-Email"); got != "user@example.com" {
		t.Errorf("默认关闭时身份头应被采信并规范化，want user@example.com got %q", got)
	}
	if got := rec.Header().Get("X-Probe-XFF"); got != "203.0.113.9" {
		t.Errorf("默认关闭时 X-Forwarded-For 应原样透传，got %q", got)
	}
}

// TestProxyAuth_SecretSet_MissingSignatureNotTrusted：开启后，缺签名头的请求
// 其代理身份头/转发头一律不被采信——身份头被剥离后 oauth2-proxy 认证回落 401，
// X-Forwarded-For 也被清空（回退直连 IP）。
func TestProxyAuth_SecretSet_MissingSignatureNotTrusted(t *testing.T) {
	SetProxySharedSecret("s3cr3t-value")
	defer SetProxySharedSecret("")

	req := httptest.NewRequest(http.MethodGet, "/api/portal/me", nil)
	req.Header.Set("X-Auth-Request-Email", "attacker@evil.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()

	ProxyAuth(proxyProbeHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无签名时伪造身份头不应被采信，want 401 got %d", rec.Code)
	}
}

// TestProxyAuth_SecretSet_WrongSignatureNotTrusted：签名不匹配等同缺签名。
func TestProxyAuth_SecretSet_WrongSignatureNotTrusted(t *testing.T) {
	SetProxySharedSecret("s3cr3t-value")
	defer SetProxySharedSecret("")

	req := httptest.NewRequest(http.MethodGet, "/api/portal/me", nil)
	req.Header.Set("X-Proxy-Signature", "wrong")
	req.Header.Set("X-Auth-Request-Email", "attacker@evil.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()

	ProxyAuth(proxyProbeHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误签名时伪造头不应被采信，want 401 got %d", rec.Code)
	}
}

// TestProxyAuth_SecretSet_ValidSignatureTrusted：签名正确时受信代理头照常被采信，
// X-Forwarded-For 保留，供下游 realClientIP 取真实客户端 IP。
func TestProxyAuth_SecretSet_ValidSignatureTrusted(t *testing.T) {
	SetProxySharedSecret("s3cr3t-value")
	defer SetProxySharedSecret("")

	req := httptest.NewRequest(http.MethodGet, "/api/portal/me", nil)
	req.Header.Set("X-Proxy-Signature", "s3cr3t-value")
	req.Header.Set("X-Auth-Request-Email", "user@example.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()

	ProxyAuth(proxyProbeHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("有效签名时应放行，got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Probe-Email"); got != "user@example.com" {
		t.Errorf("有效签名时身份头应被采信，got %q", got)
	}
	if got := rec.Header().Get("X-Probe-XFF"); got != "203.0.113.9" {
		t.Errorf("有效签名时 X-Forwarded-For 应保留，got %q", got)
	}
}

// TestProxyHeadersTrusted_DefaultOpen 单元验证 opt-in 语义：空 secret 恒信任。
func TestProxyHeadersTrusted_DefaultOpen(t *testing.T) {
	SetProxySharedSecret("")
	defer SetProxySharedSecret("")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if !proxyHeadersTrusted(req) {
		t.Error("空 PROXY_SHARED_SECRET 时应恒返回 trusted=true")
	}
}
