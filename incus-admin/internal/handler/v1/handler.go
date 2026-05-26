// Package v1 实现 cloud-gateway 标准 /v1/* 适配层（PLAN-053 Phase A 骨架）。
//
// 本包只暴露路由、错误响应、分页 helper 与限流接入；不实现业务逻辑。
// 11 个端点全部返 501 + StructuredError，由后续 Phase B/D/E 覆盖。
//
// 与现有 /api/portal /api/admin 的差异：
//   - 认证：只接受 Bearer ica_ token（不接受 oauth2-proxy header / shadow cookie）
//   - 错误格式：`{"errors":[{"field":"...","reason":"..."}]}`（非 `{"error":"..."}`）
//   - 分页格式：`?page=&page_size=` + `{"data":[...],"page","page_size","pages","total"}`
//   - 限流：独立 token-bucket（默认 100 rpm + burst 30），不与 portal 共桶
package v1

// Handler 持有 /v1/* 端点所需的依赖（Phase A 骨架阶段全空；
// 后续 Phase B/D/E 按需注入 repos / services）。
type Handler struct{}

// New 构造 v1 Handler。Phase A 不需要依赖；保留构造函数形态以便后续阶段
// 注入 db / svc / authz 时不破坏 main.go 调用点。
func New() *Handler {
	return &Handler{}
}
