package v1

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

const (
	defaultPage     = 1
	defaultPageSize = 25
	maxPageSize     = 100
)

// errPagination 区分错误字段，便于 caller 把哪个字段错了透传到 StructuredError。
type paginationErr struct {
	Field  string
	Reason string
}

func (e *paginationErr) Error() string { return e.Field + ": " + e.Reason }

// parsePagination 解析 ?page=&page_size=。
// 缺省：page=1, page_size=25；page_size 上限 100。
// 任一字段非法（非整数 / 越界）返 *paginationErr，handler 直接 writeErr 即可。
func parsePagination(r *http.Request) (page, pageSize int, err error) {
	q := r.URL.Query()
	page = defaultPage
	pageSize = defaultPageSize

	if v := q.Get("page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			return 0, 0, &paginationErr{Field: "page", Reason: "invalid"}
		}
		page = n
	}
	if v := q.Get("page_size"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			return 0, 0, &paginationErr{Field: "page_size", Reason: "invalid"}
		}
		if n > maxPageSize {
			return 0, 0, &paginationErr{Field: "page_size", Reason: "exceeds_max"}
		}
		pageSize = n
	}
	return page, pageSize, nil
}

// writePaginationErr 是 handler 在 parsePagination 报错时的便捷出口：
// 统一返 422 + StructuredError，并把字段名 / 原因透传到响应体。
func writePaginationErr(w http.ResponseWriter, err error) {
	var pe *paginationErr
	if errors.As(err, &pe) {
		writeErr(w, http.StatusUnprocessableEntity, pe.Field, pe.Reason)
		return
	}
	writeErr(w, http.StatusUnprocessableEntity, "", "invalid_pagination")
}

// writePage 写入 cloud-gateway 标准分页响应：
//
//	{"data":[...],"page":N,"page_size":M,"pages":ceil(total/page_size),"total":T}
//
// total<0 视为 0；pageSize<=0 时 pages=0（避免除零）。data 为 nil 时序列化为
// `null`；caller 若想保证空数组形态，应自行传 `[]Foo{}`。
func writePage(w http.ResponseWriter, data any, page, pageSize, total int) {
	if total < 0 {
		total = 0
	}
	pages := 0
	if pageSize > 0 && total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"data":      data,
		"page":      page,
		"page_size": pageSize,
		"pages":     pages,
		"total":     total,
	}); err != nil {
		slog.Warn("v1 writePage encode failed", "error", err)
	}
}
