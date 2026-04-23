package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// AuditWriter writes a single row to audit_logs. Satisfied by
// repository.AuditRepo.Log — kept as a function type to decouple middleware
// from the repository package.
type AuditWriter func(ctx context.Context, userID *int64, action, targetType string, targetID int64, details any, ip string)

// Max request body bytes captured for audit. Bigger bodies are truncated to
// keep audit_logs.details bounded; the handler still sees the full body.
const auditMaxBodyBytes = 64 * 1024

var sensitiveKeyFragments = []string{
	"password",
	"token",
	"secret",
	"api_key",
	"apikey",
	"ssh_key",
	"sshkey",
	"private_key",
	"privatekey",
	"access_token",
	"accesstoken",
	"refresh_token",
}

// AuditAdminWrites records every write request (POST/PUT/PATCH/DELETE) under
// the router group it is mounted on into audit_logs. It complements the
// handler-layer `audit()` helper: middleware guarantees a coarse-grained row
// per write (action = `http.<METHOD>`, target_type = "http") so coverage
// can't regress when a handler forgets to call audit(), while handler calls
// retain business-level semantics (`vm.delete`, `node.evacuate`, etc).
//
// If writer is nil the middleware becomes a no-op so servers without an
// AuditRepo wired still boot.
//
// Mount inside the ProxyAuth group (needs CtxUserID) and after RequireRole so
// rows only reflect authorized attempts; route-level rejections from earlier
// middleware won't be recorded (auth failures are already logged separately).
func AuditAdminWrites(writer AuditWriter) func(http.Handler) http.Handler {
	if writer == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isAuditedMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// Capture body for audit while leaving the full stream available
			// to the handler. We always read the whole body (so handlers can
			// still parse arbitrary-size payloads) but only the first
			// auditMaxBodyBytes are written into audit_logs.
			var fullBody []byte
			if r.Body != nil {
				fullBody, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(fullBody))
			}
			auditBody := fullBody
			truncated := false
			if len(auditBody) > auditMaxBodyBytes {
				auditBody = auditBody[:auditMaxBodyBytes]
				truncated = true
			}

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r)

			// Collect context/response fields after handler completion so
			// status / duration reflect actual outcome. Under a shadow
			// session the audit row's user_id is the admin (actor) with
			// acting_as_user_id recording who the admin was acting as —
			// same semantics as the handler-layer audit() helper.
			userID, _ := r.Context().Value(CtxUserID).(int64)
			actorID, _ := r.Context().Value(CtxActorID).(int64)
			effectiveUserID := userID
			if actorID > 0 {
				effectiveUserID = actorID
			}
			var uid *int64
			if effectiveUserID > 0 {
				uid = &effectiveUserID
			}
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			if ip == "" {
				ip = r.RemoteAddr
			}

			details := map[string]any{
				"path":        r.URL.Path,
				"status":      ww.Status(),
				"duration_ms": time.Since(start).Milliseconds(),
				"body":        redactJSONBody(auditBody),
			}
			if r.URL.RawQuery != "" {
				details["query"] = r.URL.RawQuery
			}
			if truncated {
				details["body_truncated"] = true
				details["body_full_bytes"] = len(fullBody)
			}
			if method, _ := r.Context().Value(CtxAuthMethod).(string); method != "" {
				details["auth_method"] = method
			}
			if actorID > 0 {
				details["acting_as_user_id"] = userID
			}

			action := "http." + r.Method

			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			go func() {
				defer cancel()
				writer(bgCtx, uid, action, "http", 0, details, ip)
			}()
		})
	}
}

func isAuditedMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// redactJSONBody parses body as JSON and recursively masks values whose keys
// contain any substring in sensitiveKeyFragments. Non-JSON or unparsable
// bodies are represented by a short marker so raw binary never leaks into
// audit_logs.details. Empty body returns nil.
func redactJSONBody(body []byte) any {
	if len(body) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "<non-json>"
	}
	return redactValue(v)
}

func redactValue(v any) any {
	switch vv := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(vv))
		for k, val := range vv {
			if isSensitiveKey(k) {
				out[k] = "***redacted***"
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(vv))
		for i, x := range vv {
			out[i] = redactValue(x)
		}
		return out
	default:
		return vv
	}
}

func isSensitiveKey(k string) bool {
	kl := strings.ToLower(k)
	for _, s := range sensitiveKeyFragments {
		if strings.Contains(kl, s) {
			return true
		}
	}
	return false
}
