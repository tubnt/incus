package portal

import (
	"net/http"
	"strconv"
)

// Page 是统一的分页响应外壳。Items 为当前页数据，Total 为过滤后总数。
// 仅 admin 列表接口使用；portal 侧小规模接口保持现状。
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// PageParams 表示从请求查询串解析出的分页参数。
// 未显式传入时：Limit=50、Offset=0；上限 200。
type PageParams struct {
	Limit  int
	Offset int
}

// ParsePageParams 从 `?limit=&offset=` 解析分页参数，做必要的范围钳制。
func ParsePageParams(r *http.Request) PageParams {
	q := r.URL.Query()
	p := PageParams{Limit: 50, Offset: 0}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.Offset = n
		}
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}
