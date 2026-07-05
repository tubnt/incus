package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// FieldError 是 cloud-gateway 标准错误响应里 errors 数组的元素。
//
//   - Field 是出错字段名；为空字符串时表示与字段无关的通用错误
//     （例如 401 unauthorized / 429 rate limited / 501 not implemented）。
//   - Reason 是机器可读的错误原因 token，比如 "required"、"insufficient"、
//     "invalid"、"not_implemented"。
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// writeErr 写入单条结构化错误响应。reason 必填；field 为空时表示通用错误。
func writeErr(w http.ResponseWriter, status int, field, reason string) {
	writeErrs(w, status, []FieldError{{Field: field, Reason: reason}})
}

// writeErrs 写入多条结构化错误响应。
//
// 响应体形态：`{"errors":[{"field":"...","reason":"..."}]}`。
// errs 为空时退化为单条通用 "unknown" 错误，避免给客户端返空数组造成歧义。
func writeErrs(w http.ResponseWriter, status int, errs []FieldError) {
	if len(errs) == 0 {
		errs = []FieldError{{Reason: "unknown"}}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{"errors": errs}); err != nil {
		slog.Warn("v1 writeErrs encode failed", "error", err, "status", status)
	}
}
