package middleware

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// /v1/* 用独立 token-bucket 限流，不与 portal 的固定窗口限流共桶：
//
//   - 每个 Bearer token（实际 key = userID）一桶
//   - 默认 100 rpm + burst 30
//   - 写操作（POST/DELETE/PATCH）再叠加 burst 30 的独立桶，防止 AI 把整集群打挂
//
// 命中限流：429 + IETF RateLimit-{Limit,Remaining,Reset} + Retry-After +
// StructuredError 体 (`{"errors":[{"field":"","reason":"rate_limited"}]}`)。
//
// 环境变量：
//
//	INCUS_ADMIN_RATELIMIT_V1_RPM   （默认 100）
//	INCUS_ADMIN_RATELIMIT_V1_BURST （默认 30）

const (
	defaultV1RatePerMinute = 100
	defaultV1Burst         = 30

	envV1RatePerMinute = "INCUS_ADMIN_RATELIMIT_V1_RPM"
	envV1Burst         = "INCUS_ADMIN_RATELIMIT_V1_BURST"
)

// tokenBucket 是一个最朴素的 leaky-bucket / token-bucket 实现：
// 容量 capacity，按 ratePerSec 持续注水。allow 取一桶水；
// 取不到时返回需要等多久（>=1s 时 Retry-After 用整秒）。
type tokenBucket struct {
	capacity   float64
	ratePerSec float64
	tokens     float64
	lastRefill time.Time
}

func newTokenBucket(capacity, ratePerSec float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		capacity:   capacity,
		ratePerSec: ratePerSec,
		tokens:     capacity,
		lastRefill: now,
	}
}

// refill 把上次访问到 now 之间应注入的水加进来，capacity 封顶。
func (b *tokenBucket) refill(now time.Time) {
	if now.Before(b.lastRefill) {
		// 时钟回拨：不退还 token，只把 lastRefill 拉回 now，避免下次大注入。
		b.lastRefill = now
		return
	}
	delta := now.Sub(b.lastRefill).Seconds()
	if delta <= 0 {
		return
	}
	b.tokens += delta * b.ratePerSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now
}

// take 尝试取一桶水。返回 (allowed, remainingTokensInt, retryAfter)。
// retryAfter 仅在 !allowed 时有意义；至少 1s（向上取整），避免 client 立刻重试。
func (b *tokenBucket) take(now time.Time) (bool, int, time.Duration) {
	b.refill(now)
	if b.tokens >= 1 {
		b.tokens--
		return true, int(math.Floor(b.tokens)), 0
	}
	// 需要 (1 - tokens) / rate 秒才能拿到下一个 token
	need := 1 - b.tokens
	wait := time.Duration(need / b.ratePerSec * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, 0, wait
}

// v1Limiter 持有两类桶：
//   - main：所有 /v1/* 请求过 main 桶（100 rpm + burst 30）
//   - write：仅写操作再过 write 桶（额外 burst 30，refill 沿用 rpm 节奏）
//
// 命中先后顺序：先 main 后 write。任一未通过即 429。
type v1Limiter struct {
	mu     sync.Mutex
	main   map[string]*tokenBucket
	write  map[string]*tokenBucket
	rps    float64 // 主桶 refill rate（rpm/60）
	burst  float64 // 主桶 + write 桶容量
	writes float64 // write 桶 refill rate（与 rps 一致，方便调参可独立）
}

func newV1Limiter(ratePerMinute, burst int) *v1Limiter {
	if ratePerMinute <= 0 {
		ratePerMinute = defaultV1RatePerMinute
	}
	if burst <= 0 {
		burst = defaultV1Burst
	}
	rps := float64(ratePerMinute) / 60.0
	l := &v1Limiter{
		main:   make(map[string]*tokenBucket),
		write:  make(map[string]*tokenBucket),
		rps:    rps,
		burst:  float64(burst),
		writes: rps,
	}
	go l.cleanup()
	return l
}

// allow 取一桶 key 的水；isWrite 控制是否同时扣 write 桶。
// 返回 (allowed, remainingMainTokensInt, retryAfter)。
func (l *v1Limiter) allow(key string, isWrite bool, now time.Time) (bool, int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	mb, ok := l.main[key]
	if !ok {
		mb = newTokenBucket(l.burst, l.rps, now)
		l.main[key] = mb
	}
	allowed, remaining, retry := mb.take(now)
	if !allowed {
		return false, remaining, retry
	}

	if isWrite {
		wb, ok := l.write[key]
		if !ok {
			wb = newTokenBucket(l.burst, l.writes, now)
			l.write[key] = wb
		}
		wAllowed, _, wRetry := wb.take(now)
		if !wAllowed {
			// 已经从 main 扣了 1 个 token；写桶顶住时退还，避免读写竞争互相饿死。
			mb.tokens++
			if mb.tokens > mb.capacity {
				mb.tokens = mb.capacity
			}
			return false, remaining, wRetry
		}
	}
	return true, remaining, 0
}

// cleanup 周期性回收满桶（lastRefill 距今超过 10 分钟），防止 token 数随时间膨胀。
func (l *v1Limiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for k, b := range l.main {
			if now.Sub(b.lastRefill) > 10*time.Minute {
				delete(l.main, k)
			}
		}
		for k, b := range l.write {
			if now.Sub(b.lastRefill) > 10*time.Minute {
				delete(l.write, k)
			}
		}
		l.mu.Unlock()
	}
}

// isWriteMethod 判定是否需要额外走 write 桶。HEAD/OPTIONS/GET 算读。
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// RateLimitV1 返回 /v1/* 专用的限流中间件。
//
// key 优先用 CtxUserID（数字形）；CtxUserID 缺失时退回 RemoteAddr，
// 避免 RequireBearer 已经放行但 ctx 缺值导致 nil-panic。
//
// IETF rate-limit headers 在每个响应上都会写（成功与失败一致），
// 这样客户端不必区分 200/429 即可读到当前桶状态。
func RateLimitV1(ratePerMinute, burst int) func(http.Handler) http.Handler {
	limiter := newV1Limiter(ratePerMinute, burst)
	if ratePerMinute <= 0 {
		ratePerMinute = defaultV1RatePerMinute
	}
	if burst <= 0 {
		burst = defaultV1Burst
	}
	rpmStr := strconv.Itoa(ratePerMinute)
	burstStr := strconv.Itoa(burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rateKey(r)
			now := time.Now()

			allowed, remaining, retry := limiter.allow(key, isWriteMethod(r.Method), now)

			// IETF draft RateLimit headers（成功 / 失败都写）
			w.Header().Set("RateLimit-Limit", rpmStr)
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
			// Reset 是从现在起到下一次有 token 可取的秒数；burst 状态下基本是 1s
			reset := 1
			if !allowed {
				reset = int(math.Ceil(retry.Seconds()))
			}
			w.Header().Set("RateLimit-Reset", strconv.Itoa(reset))
			// 帮助 ops 排查：哪个 burst 桶在用
			w.Header().Set("X-RateLimit-Burst", burstStr)

			if !allowed {
				retrySec := int(math.Ceil(retry.Seconds()))
				if retrySec < 1 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySec))
				slog.Info("v1 rate limit hit",
					"key", key,
					"method", r.Method,
					"path", r.URL.Path,
					"retry_after_s", retrySec,
				)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"errors": []map[string]string{
						{"field": "", "reason": "rate_limited"},
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitV1FromEnv 从环境变量构造限流中间件，方便 server.go 启动时一行串联：
//
//	r.Use(middleware.RateLimitV1FromEnv())
func RateLimitV1FromEnv() func(http.Handler) http.Handler {
	rpm := defaultV1RatePerMinute
	if v := strings.TrimSpace(os.Getenv(envV1RatePerMinute)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rpm = n
		} else {
			slog.Warn("invalid "+envV1RatePerMinute+" — using default",
				"value", v, "default", defaultV1RatePerMinute)
		}
	}
	burst := defaultV1Burst
	if v := strings.TrimSpace(os.Getenv(envV1Burst)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			burst = n
		} else {
			slog.Warn("invalid "+envV1Burst+" — using default",
				"value", v, "default", defaultV1Burst)
		}
	}
	return RateLimitV1(rpm, burst)
}

// rateKey 把请求映射到限流 key。优先 token 对应的 userID，缺失时退回 RemoteAddr。
func rateKey(r *http.Request) string {
	if uid, _ := r.Context().Value(CtxUserID).(int64); uid > 0 {
		return "u:" + strconv.FormatInt(uid, 10)
	}
	return "ip:" + r.RemoteAddr
}
