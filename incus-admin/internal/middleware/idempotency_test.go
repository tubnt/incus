package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// fakeIdempStore 是 in-memory IdempotencyStore，supports
// 注入错误 / 模拟 race（Put 永远 ON CONFLICT DO NOTHING 等价）。
type fakeIdempStore struct {
	mu      sync.Mutex
	rows    map[string]model.IdempotencyKey
	getErr  error
	putErr  error
	putCalls int
}

func newFakeStore() *fakeIdempStore {
	return &fakeIdempStore{rows: make(map[string]model.IdempotencyKey)}
}

func (f *fakeIdempStore) Get(_ context.Context, key string, userID int64) (*model.IdempotencyKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	row, ok := f.rows[key]
	if !ok || row.UserID != userID {
		return nil, nil
	}
	dup := row
	return &dup, nil
}

func (f *fakeIdempStore) Put(_ context.Context, k model.IdempotencyKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++
	if f.putErr != nil {
		return f.putErr
	}
	// ON CONFLICT DO NOTHING semantics: first writer wins
	if _, exists := f.rows[k.Key]; exists {
		return nil
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now()
	}
	f.rows[k.Key] = k
	return nil
}

func withUser(req *http.Request, uid int64) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), CtxUserID, uid))
}

// echoHandler 返指定 status + body；用 [closure] 控制每次调用的行为。
func echoHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// TestIdempotency_NoKeyPassthrough：缺 Idempotency-Key header 中间件直通。
func TestIdempotency_NoKeyPassthrough(t *testing.T) {
	store := newFakeStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"a":1}`)), 42)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if calls != 1 {
		t.Fatalf("downstream calls=%d want 1", calls)
	}
	if store.putCalls != 0 {
		t.Fatalf("no key → must not write cache, got %d puts", store.putCalls)
	}
}

// TestIdempotency_NonWriteMethodsPassthrough：GET/PATCH/PUT/HEAD/OPTIONS 直通。
func TestIdempotency_NonWriteMethodsPassthrough(t *testing.T) {
	store := newFakeStore()
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodHead, http.MethodOptions} {
		h := Idempotency(store)(echoHandler(http.StatusOK, `{}`))
		req := withUser(httptest.NewRequest(m, "/v1/instances", nil), 7)
		req.Header.Set("Idempotency-Key", "k0123456789abcdef")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("method %s: status %d, want 200", m, rr.Code)
		}
		if store.putCalls != 0 {
			t.Errorf("method %s: must not cache, got %d puts", m, store.putCalls)
		}
	}
}

// TestIdempotency_MissThenCachedReplay：首次请求走业务并写缓存；二次同 key + 同 body
// 不再调下游，返回缓存的 status + body + Idempotent-Replay: true 头。
func TestIdempotency_MissThenCachedReplay(t *testing.T) {
	store := newFakeStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))

	key := "abcdef0123456789"
	body := `{"product":"p-mini","ssh_key_id":3}`

	// 首次：业务跑、缓存写
	rr1 := httptest.NewRecorder()
	req1 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(body)), 42)
	req1.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first call: status %d want 201", rr1.Code)
	}
	if got := rr1.Header().Get("Idempotent-Replay"); got != "" {
		t.Errorf("first call: must not have Idempotent-Replay header, got %q", got)
	}
	// 等待 detached Put goroutine 落盘（同步实现，无需 sleep）
	if store.putCalls != 1 {
		t.Fatalf("first call must Put once, got %d", store.putCalls)
	}

	// 二次：缓存命中，重放
	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(body)), 42)
	req2.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusCreated {
		t.Fatalf("replay status %d want 201", rr2.Code)
	}
	if rr2.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("replay missing Idempotent-Replay: true header (got %q)", rr2.Header().Get("Idempotent-Replay"))
	}
	if rr2.Body.String() != `{"id":1}` {
		t.Errorf("replay body %q want %q", rr2.Body.String(), `{"id":1}`)
	}
	if calls != 1 {
		t.Errorf("replay must not invoke downstream; calls=%d want 1", calls)
	}
}

// TestIdempotency_MismatchPayload：同 key 异 body 必须返 422 mismatch。
func TestIdempotency_MismatchPayload(t *testing.T) {
	store := newFakeStore()
	h := Idempotency(store)(echoHandler(http.StatusCreated, `{"id":2}`))
	key := "mismatch-test-key-001"

	// seed
	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"a":1}`)), 9)
	req.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status %d want 201", rr.Code)
	}

	// 异 payload
	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"a":2}`)), 9)
	req2.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatch status %d want 422", rr2.Code)
	}
	var body struct {
		Errors []struct {
			Field, Reason, Message string
		} `json:"errors"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Reason != "mismatch" || body.Errors[0].Field != "Idempotency-Key" {
		t.Fatalf("unexpected error body: %+v", body)
	}
	if body.Errors[0].Message == "" {
		t.Errorf("mismatch should carry message hint")
	}
}

// TestIdempotency_InvalidKeyShape：长度太短 / charset 不合 → 422 invalid，不查 DB。
func TestIdempotency_InvalidKeyShape(t *testing.T) {
	store := newFakeStore()
	h := Idempotency(store)(echoHandler(http.StatusCreated, `{}`))

	bad := []string{
		"",                  // 已被 no-key 短路（这一行不会走到 validator）
		"short",             // 长度 <16
		strings.Repeat("a", 256), // 长度 >255
		"hasSpace andStuff_x", // 含空格
		"slash/notallowed_x", // 含 /
		"semicolons;notok_x", // 含 ;
	}
	for _, k := range bad {
		if k == "" {
			continue // 跳过，no-key 不走 validator
		}
		rr := httptest.NewRecorder()
		req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 7)
		req.Header.Set("Idempotency-Key", k)
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("key %q: status %d want 422", k, rr.Code)
			continue
		}
		var body struct {
			Errors []struct{ Field, Reason string } `json:"errors"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Errorf("key %q parse body: %v", k, err)
			continue
		}
		if len(body.Errors) != 1 || body.Errors[0].Reason != "invalid" {
			t.Errorf("key %q unexpected body: %+v", k, body)
		}
	}
	if store.putCalls != 0 {
		t.Errorf("invalid key must short-circuit before Put; got %d puts", store.putCalls)
	}
}

// TestIdempotency_5xxNotCached：业务返 5xx 不进缓存，client 可重试。
func TestIdempotency_5xxNotCached(t *testing.T) {
	store := newFakeStore()
	h := Idempotency(store)(echoHandler(http.StatusBadGateway, `{"err":"upstream down"}`))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "five-xx-test-key-0000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d want 502", rr.Code)
	}
	if store.putCalls != 0 {
		t.Errorf("5xx must skip cache; got %d puts", store.putCalls)
	}
}

// TestIdempotency_4xxCached：业务返 4xx 进缓存（避免 client 反复打错请求）。
func TestIdempotency_4xxCached(t *testing.T) {
	store := newFakeStore()
	h := Idempotency(store)(echoHandler(http.StatusUnprocessableEntity, `{"errors":[{"field":"product","reason":"required"}]}`))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "four-xx-test-key-0000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d want 422", rr.Code)
	}
	if store.putCalls != 1 {
		t.Errorf("4xx must cache; got %d puts", store.putCalls)
	}

	// 二次同 key 同 body 应回放 422 + body
	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req2.Header.Set("Idempotency-Key", "four-xx-test-key-0000")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("replay status %d want 422", rr2.Code)
	}
	if rr2.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("replay missing Idempotent-Replay: true")
	}
}

// TestIdempotency_MissingUserID：上游 RequireBearer 没跑（ctx.UserID 缺）→ 401。
func TestIdempotency_MissingUserID(t *testing.T) {
	store := newFakeStore()
	h := Idempotency(store)(echoHandler(http.StatusCreated, `{}`))

	req := httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", "no-userid-test-key000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d want 401", rr.Code)
	}
}

// TestIdempotency_StoreGetError：Get 抛 DB 错 → 500 + 不写缓存。
func TestIdempotency_StoreGetError(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("simulated db error")
	h := Idempotency(store)(echoHandler(http.StatusCreated, `{}`))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "get-err-test-key-0000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500", rr.Code)
	}
}

// TestIdempotency_PutErrorSwallowed：Put 失败时 slog.Warn 但响应继续——
// 客户端拿到完整业务响应，下次同 key 重新走业务（最差等同于 client 不带 key）。
func TestIdempotency_PutErrorSwallowed(t *testing.T) {
	store := newFakeStore()
	store.putErr = errors.New("simulated put error")
	h := Idempotency(store)(echoHandler(http.StatusCreated, `{"id":99}`))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "put-err-test-key-0000")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Put error must not affect response; got %d", rr.Code)
	}
	if rr.Body.String() != `{"id":99}` {
		t.Errorf("body lost: got %q", rr.Body.String())
	}
}

// TestIdempotency_CrossUserScoping：A 用户 seed 的 key，B 用户用同 key 不命中，
// 业务继续跑（fake store 行为：Get 按 user_id 过滤）。
func TestIdempotency_CrossUserScoping(t *testing.T) {
	store := newFakeStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	key := "cross-user-test-key-1"

	// 用户 A seed
	rr := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 100)
	req.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr, req)
	if calls != 1 {
		t.Fatalf("seed expected 1 call, got %d", calls)
	}

	// 用户 B 用同 key — 必须 miss 并继续走业务
	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 200)
	req2.Header.Set("Idempotency-Key", key)
	h.ServeHTTP(rr2, req2)
	if calls != 2 {
		t.Errorf("cross-user must miss + invoke downstream; calls=%d want 2", calls)
	}
	if rr2.Header().Get("Idempotent-Replay") == "true" {
		t.Errorf("cross-user must not set Idempotent-Replay: true")
	}
}

// TestIdempotency_BodyRestoredForDownstream：下游必须能完整读到原 body
// （middleware buffer 后 io.NopCloser 还原）。
func TestIdempotency_BodyRestoredForDownstream(t *testing.T) {
	store := newFakeStore()
	want := `{"product":"p-mini","ssh_key_id":42}`
	var seenBody []byte
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = b
		w.WriteHeader(http.StatusCreated)
	}))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(want)), 1)
	req.Header.Set("Idempotency-Key", "body-restore-test-001")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d want 201", rr.Code)
	}
	if string(seenBody) != want {
		t.Fatalf("downstream body mismatch: got %q want %q", seenBody, want)
	}
}

// TestIdempotency_NormalisedHash：同语义不同 JSON key 顺序 → 同 hash → 命中重放。
func TestIdempotency_NormalisedHash(t *testing.T) {
	store := newFakeStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))

	// 第一次：keys in order a,b,c
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"a":1,"b":2,"c":3}`)), 5)
	req.Header.Set("Idempotency-Key", "normalised-hash-key01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	// 第二次：keys c,b,a 同语义，应命中重放
	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"c":3,"b":2,"a":1}`)), 5)
	req2.Header.Set("Idempotency-Key", "normalised-hash-key01")
	h.ServeHTTP(rr2, req2)

	if calls != 1 {
		t.Errorf("normalised hash should cache hit; downstream calls=%d want 1", calls)
	}
	if rr2.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("expected replay header")
	}
}

// TestIdempotency_QueryStringSortedIntoHash：query 参数顺序不应影响 hash。
func TestIdempotency_QueryStringSortedIntoHash(t *testing.T) {
	store := newFakeStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))

	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances?b=2&a=1", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "query-sort-test-key01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	rr2 := httptest.NewRecorder()
	req2 := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances?a=1&b=2", strings.NewReader(`{}`)), 1)
	req2.Header.Set("Idempotency-Key", "query-sort-test-key01")
	h.ServeHTTP(rr2, req2)

	if calls != 1 {
		t.Errorf("query-sorted hash should hit; calls=%d", calls)
	}
}

// TestIdempotency_NilStorePassthroughCleanly：传 nil store 时中间件直通，不 panic。
func TestIdempotency_NilStorePassthroughCleanly(t *testing.T) {
	h := Idempotency(nil)(echoHandler(http.StatusCreated, `{}`))
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{}`)), 1)
	req.Header.Set("Idempotency-Key", "ignored-nil-store-001")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("nil store should be no-op + passthrough; got %d", rr.Code)
	}
}

// TestRequestFingerprint_ConsistentRehash：同输入两次 hash 结果一致；
// 输入差异（method/path/body/query）→ hash 必变。
func TestRequestFingerprint_ConsistentRehash(t *testing.T) {
	body := []byte(`{"a":1}`)
	h1 := requestFingerprint("POST", "/v1/instances", "", body)
	h2 := requestFingerprint("POST", "/v1/instances", "", body)
	if h1 != h2 {
		t.Fatalf("fingerprint not deterministic: %s vs %s", h1, h2)
	}
	// 改 method
	if hh := requestFingerprint("DELETE", "/v1/instances", "", body); hh == h1 {
		t.Errorf("method change must shift hash")
	}
	// 改 path
	if hh := requestFingerprint("POST", "/v1/other", "", body); hh == h1 {
		t.Errorf("path change must shift hash")
	}
	// 改 body
	if hh := requestFingerprint("POST", "/v1/instances", "", []byte(`{"a":2}`)); hh == h1 {
		t.Errorf("body change must shift hash")
	}
	// 加 query
	if hh := requestFingerprint("POST", "/v1/instances", "x=1", body); hh == h1 {
		t.Errorf("query change must shift hash")
	}
}

// TestCanonicalBody_NonJSONRawHash：非 JSON body 退回原 byte，相同 raw 相同 hash。
func TestCanonicalBody_NonJSONRawHash(t *testing.T) {
	a := canonicalBody([]byte("not a json"))
	b := canonicalBody([]byte("not a json"))
	if !bytes.Equal(a, b) {
		t.Errorf("raw passthrough should be stable: %q vs %q", a, b)
	}
	if !bytes.Equal(a, []byte("not a json")) {
		t.Errorf("non-JSON should be raw bytes; got %q", a)
	}
}

// TestCanonicalBody_EmptyBody：空 body 返 nil（hash 仍稳定）。
func TestCanonicalBody_EmptyBody(t *testing.T) {
	if c := canonicalBody(nil); c != nil {
		t.Errorf("empty body should canonicalise to nil; got %q", c)
	}
	if c := canonicalBody([]byte{}); c != nil {
		t.Errorf("empty body should canonicalise to nil; got %q", c)
	}
}
