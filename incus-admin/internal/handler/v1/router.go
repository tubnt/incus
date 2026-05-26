package v1

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes 把 11 个 /v1/* 端点挂到给定 chi.Router 上。Phase A 全部返 501 +
// StructuredError；Phase B/D/E 会逐个替换为业务实现。
//
// 端点清单（与 PLAN-053 Phase B/D/E 对齐）：
//
//	read-only (Phase B)：
//	  GET    /account
//	  GET    /instances
//	  GET    /instances/{id}
//	  GET    /types
//	  GET    /regions
//	  GET    /images
//	  GET    /ssh-keys
//
//	write (Phase D/E)：
//	  POST   /instances
//	  DELETE /instances/{id}
//	  POST   /instances/{id}/reboot
//	  POST   /instances/{id}/shutdown
//	  POST   /instances/{id}/boot
func (h *Handler) Routes(r chi.Router) {
	// /v1/* 子路由器必须自行托管 404 / 405，不能 fallback 到 server.go 的
	// SPA 静态兜底（会把 HTML 返给客户端），违反 cloud-gateway 标准。
	r.NotFound(h.notFound)
	r.MethodNotAllowed(h.methodNotAllowed)

	// Phase B：read-only（已接真实业务，见 readonly.go）
	r.Get("/account", h.Account)
	r.Get("/instances", h.Instances)
	r.Get("/instances/{id}", h.InstanceByID)
	r.Get("/types", h.Types)
	r.Get("/regions", h.Regions)
	r.Get("/images", h.Images)
	r.Get("/ssh-keys", h.SSHKeys)

	// Phase D/E：write
	r.Post("/instances", h.notImplemented)
	r.Delete("/instances/{id}", h.notImplemented)
	r.Post("/instances/{id}/reboot", h.notImplemented)
	r.Post("/instances/{id}/shutdown", h.notImplemented)
	r.Post("/instances/{id}/boot", h.notImplemented)
}

// EndpointCount 暴露给 server.go 用于启动日志（"v1 routes registered endpoints=12"）。
// 与上面 Routes 保持同步即可；不需要反射，硬编码可读性更好。
const EndpointCount = 12

func (h *Handler) notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusNotImplemented, "", "not_implemented")
}

func (h *Handler) notFound(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusNotFound, "", "not_found")
}

func (h *Handler) methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusMethodNotAllowed, "", "method_not_allowed")
}
