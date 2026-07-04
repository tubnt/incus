package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// keyedMutex 是按字符串 key 维度的进程内互斥集合（advisory 占位锁）。
// 用于幂等中间件的 TOCTOU 防护：并发的相同 (user_id, key) 请求需要串行化，
// 否则两个请求可能同时 Get→miss→各自执行一遍业务（DB 层的 ON CONFLICT 只能
// 防重复插入缓存行，防不住业务被执行两次）。锁仅存活于本进程，多副本部署时
// 由 DB 复合主键 + 客户端重试兜底；单进程内它保证同 key 严格一次执行。
//
// 采用引用计数 + 用完即删，避免长期运行后 map 无限膨胀（key 空间是客户端可控的
// 任意 16..255 字符串）。
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu   sync.Mutex
	refs int
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*keyedMutexEntry)}
}

// lock 获取 key 对应的互斥锁并返回释放函数。释放时若无其它 waiter 引用，
// 顺带把 entry 从 map 删除，回收内存。
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	e, ok := k.locks[key]
	if !ok {
		e = &keyedMutexEntry{}
		k.locks[key] = e
	}
	e.refs++
	k.mu.Unlock()

	e.mu.Lock()

	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// lockKey 把 (userID, key) 拼成占位锁的复合键。用 NUL 分隔，避免 userID 与
// key 拼接歧义（key 字符集已由 idempotencyKeyRe 限定，不含 NUL）。
func lockKey(userID int64, key string) string {
	return strconv.FormatInt(userID, 10) + "\x00" + key
}

// IdempotencyStore 是 middleware 需要的最小 idempotency_keys 持久化接口。
// repository.IdempotencyRepo 直接实现；测试用 in-memory fake。
type IdempotencyStore interface {
	Get(ctx context.Context, key string, userID int64) (*model.IdempotencyKey, error)
	Put(ctx context.Context, k model.IdempotencyKey) error
}

// idempotencyKeyRe 限制 Idempotency-Key 形状：16..255 长度 + 安全字符集。
// 字符集对齐 Stripe/Cloudflare 行业惯例，避免 client 把整段 base64+pad 塞进来
// 时被打断；下划线 / 点 / 减号都是常见 UUIDv4-encoded 变体的合法字符。
var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9._-]{16,255}$`)

// putTimeout 是 cache write 的 detached ctx 上限。client 已经收到响应后，
// request ctx 通常已取消；用 background + 短超时确保 cache 写入不被中断，
// 同时不让 DB 慢 query 卡住整个 goroutine。
const putTimeout = 3 * time.Second

// Idempotency 是 cloud-gateway 标准 POST/DELETE 幂等中间件（PLAN-053 Phase E）。
//
// 工作机制：
//   - 仅对 POST/DELETE 生效；其它方法直通
//   - 缺 Idempotency-Key header 直通（client 选择性接入）
//   - key 形状校验：长度 16..255，charset [A-Za-z0-9._-]，违规 422
//   - request_hash = SHA-256(method + path + sorted query + canonical body)；
//     JSON body 解码后 re-marshal（json.Marshal 默认对 map 字段排序），
//     非 JSON 退回原样 byte hash；下游收到的 body 通过 io.NopCloser 还原
//   - 命中 + hash 一致 → 回放 status_code + response_body + `Idempotent-Replay: true`
//   - 命中 + hash 不一致 → 422 mismatch
//   - 未命中 → 包 captureWriter 跑下游 → 仅在 2xx / 4xx 时写缓存；5xx 让 client 重试
//
// 必须挂在 RequireBearer 之后（取 CtxUserID）；建议挂在 RateLimitV1 之后
// （限流先于业务，DB 读不必为被限流的请求消耗）。
func Idempotency(store IdempotencyStore) func(http.Handler) http.Handler {
	if store == nil {
		slog.Error("Idempotency middleware: nil store passed; refusing to wire")
		return func(next http.Handler) http.Handler { return next }
	}
	// 每个中间件实例持有一份 keyedMutex，跨请求共享——同 (user_id, key) 的
	// 并发请求靠它串行化，防 Get→miss 竞态导致业务重复执行。
	kmu := newKeyedMutex()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost && r.Method != http.MethodDelete {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			userID, _ := r.Context().Value(CtxUserID).(int64)
			if userID <= 0 {
				writeIdempErr(w, http.StatusUnauthorized, "", "unauthorized", "")
				return
			}

			if !idempotencyKeyRe.MatchString(key) {
				writeIdempErr(w, http.StatusUnprocessableEntity, "Idempotency-Key", "invalid", "")
				return
			}

			// Buffer body so we can hash it AND let downstream re-read it via
			// io.NopCloser. POST /v1 bodies are tiny JSON; large bodies should
			// be gated upstream by a MaxBytesReader (out of scope here).
			var bodyBytes []byte
			if r.Body != nil {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					slog.Warn("idempotency body read failed", "key", key, "error", err)
					writeIdempErr(w, http.StatusBadRequest, "", "body_read_failed", "")
					return
				}
				_ = r.Body.Close()
				bodyBytes = b
				r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}

			hash := requestFingerprint(r.Method, r.URL.Path, r.URL.RawQuery, bodyBytes)

			// TOCTOU 占位锁：串行化同 (user_id, key) 的并发请求。第一个请求跑
			// Get→业务→Put 的完整临界区，后续请求阻塞到它释放后再 Get，此时必然
			// 命中缓存直接回放，业务只执行一次。锁跨整个 handler，defer 覆盖回放
			// 提前返回与 miss 落盘两条路径。
			unlock := kmu.lock(lockKey(userID, key))
			defer unlock()

			existing, err := store.Get(r.Context(), key, userID)
			if err != nil {
				slog.Error("idempotency get failed", "key", key, "user_id", userID, "error", err)
				writeIdempErr(w, http.StatusInternalServerError, "", "idempotency_lookup_failed", "")
				return
			}
			if existing != nil {
				if existing.RequestHash != hash {
					writeIdempErr(w, http.StatusUnprocessableEntity, "Idempotency-Key", "mismatch", "same key, different payload")
					return
				}
				// Replay — byte-for-byte. Content-Type defaults to JSON; /v1
				// always returns JSON so this is safe. Any client peering on
				// content via response shape (Location header etc.) is not
				// covered — cloud-gateway spec says responses are JSON-only.
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Idempotent-Replay", "true")
				w.WriteHeader(existing.StatusCode)
				if _, werr := w.Write(existing.ResponseBody); werr != nil {
					slog.Warn("idempotency replay write failed", "key", key, "error", werr)
				}
				return
			}

			// Miss → capture downstream response then maybe persist.
			cw := newCaptureWriter(w)
			next.ServeHTTP(cw, r)

			// 决策：2xx / 4xx 缓存；5xx 不缓存（让 client retry hit 恢复的后端）。
			status := cw.status
			if cacheable(status) {
				row := model.IdempotencyKey{
					Key:          key,
					UserID:       userID,
					Method:       r.Method,
					Path:         r.URL.Path,
					StatusCode:   status,
					ResponseBody: cw.buf.Bytes(),
					RequestHash:  hash,
				}
				// Detached ctx：client 断开不应影响缓存落盘——下次相同 key 命中需要它。
				putCtx, cancel := context.WithTimeout(context.Background(), putTimeout)
				defer cancel()
				if perr := store.Put(putCtx, row); perr != nil {
					slog.Warn("idempotency cache write failed",
						"key", key, "user_id", userID, "status", status, "error", perr)
				}
			}
		})
	}
}

// cacheable 决定本次响应是否进缓存。2xx 成功路径必须缓存（重放保护核心场景），
// 4xx 客户端错误缓存（client 用同一 key 反复打错请求也只算一次），
// 5xx 服务端错误不缓存（DB 暂时挂、外部依赖 timeout 都属临时态，client 应重试）。
// 1xx/3xx 实际 /v1 不会产生（无重定向 / 无信息响应），按 default 不缓存即可。
func cacheable(status int) bool {
	return (status >= 200 && status < 300) || (status >= 400 && status < 500)
}

// captureWriter 包装 http.ResponseWriter 同时把响应体落到内存 buf，供 cache 落盘。
// 只在 miss 路径用；replay 路径不进 downstream，直接 w.Write 即可。
type captureWriter struct {
	http.ResponseWriter
	status      int
	buf         bytes.Buffer
	headerWrote bool
}

func newCaptureWriter(w http.ResponseWriter) *captureWriter {
	// 默认 200：handler 直接 w.Write 不显式 WriteHeader 时 net/http 也按 200。
	return &captureWriter{ResponseWriter: w, status: http.StatusOK}
}

func (c *captureWriter) WriteHeader(status int) {
	if c.headerWrote {
		return
	}
	c.status = status
	c.headerWrote = true
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.headerWrote {
		c.WriteHeader(http.StatusOK)
	}
	c.buf.Write(p)
	return c.ResponseWriter.Write(p)
}

// Flush 透传给下游 ResponseWriter；handler 用 chunked / SSE 时不丢 Flusher 接口。
// /v1 现在没有 SSE 端点，但保留 Flusher 让未来端点不被 captureWriter 隔离。
func (c *captureWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestFingerprint 算 method + path + sorted query + canonical body 的 SHA-256。
// "sorted query" 把同名参数保留顺序（a=1&a=2 ≠ a=2&a=1，符合 RFC）；不同 key 之间排序。
// "canonical body" 把 JSON object 字段排序（依赖 encoding/json 对 map 字段的稳定输出顺序），
// 非 JSON body 按原样 hash。这样同 key 异 payload 必然 hash 不一致。
func requestFingerprint(method, path, rawQuery string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write([]byte(canonicalQuery(rawQuery)))
	h.Write([]byte{'\n'})
	h.Write(canonicalBody(body))
	return hex.EncodeToString(h.Sum(nil))
}

func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	q, err := url.ParseQuery(raw)
	if err != nil || len(q) == 0 {
		return raw
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		for _, v := range q[k] {
			if buf.Len() > 0 {
				buf.WriteByte('&')
			}
			buf.WriteString(url.QueryEscape(k))
			buf.WriteByte('=')
			buf.WriteString(url.QueryEscape(v))
		}
	}
	return buf.String()
}

func canonicalBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		// encoding/json 对 map[string]any 输出按 key 字典序，递归生效。
		// 数组保留原顺序（语义不可换序）。
		if out, mErr := json.Marshal(v); mErr == nil {
			return out
		}
	}
	return body
}

// idempFieldErr 与 v1.FieldError 形态对齐，但加 message 字段（mismatch 场景给提示）。
type idempFieldErr struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

func writeIdempErr(w http.ResponseWriter, status int, field, reason, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []idempFieldErr{{Field: field, Reason: reason, Message: message}},
	})
}
