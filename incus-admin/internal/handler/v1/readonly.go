package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
)

// userIDFromCtx 取 RequireBearer middleware 写入的 user_id。
// 缺失或非 int64 视为 internal —— bearer middleware 保证一旦放行就一定有；
// 没有就是程序错误（路由挂漏了 middleware 之类），统一 500 而不是 401。
func userIDFromCtx(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(middleware.CtxUserID).(int64)
	if !ok || v <= 0 {
		return 0, false
	}
	return v, true
}

// internalIfMissingDeps 把缺依赖的 5xx 路径统一记日志 + 写响应。
// dep 在 main.go 装配时不会为 nil；触发只可能是测试 mis-wire 或代码改动遗漏。
func internalIfMissingDeps(w http.ResponseWriter, path string) {
	slog.Error("v1 handler dependency missing", "path", path)
	writeErr(w, http.StatusInternalServerError, "", "internal")
}

// Account GET /v1/account：返当前 token 持有人账户信息。
func (h *Handler) Account(w http.ResponseWriter, r *http.Request) {
	if h.deps.Users == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	user, err := h.deps.Users.GetByID(r.Context(), uid)
	if err != nil {
		slog.Error("v1 Account: get user", "user_id", uid, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	if user == nil {
		writeErr(w, http.StatusNotFound, "", "not_found")
		return
	}
	writeJSON(w, http.StatusOK, toAccountDTO(*user))
}

// Instances GET /v1/instances：返当前用户可见的 VM 列表（分页）。
// 解析 clusterID → cluster.name 与 VM.OrderID → product_id 时做一次批量预查，
// 避免 N+1。
func (h *Handler) Instances(w http.ResponseWriter, r *http.Request) {
	if h.deps.VMs == nil || h.deps.Clusters == nil || h.deps.Orders == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	page, pageSize, perr := parsePagination(r)
	if perr != nil {
		writePaginationErr(w, perr)
		return
	}
	vms, total, err := h.deps.VMs.ListByUserPaged(r.Context(), uid, pageSize, (page-1)*pageSize)
	if err != nil {
		slog.Error("v1 Instances: list vms", "user_id", uid, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}

	// cluster_id → name：clusters 表 N 通常 < 20，一次 List 比逐行 GetByID 便宜。
	regionByCluster, err := h.regionNameMap(r.Context())
	if err != nil {
		slog.Error("v1 Instances: list clusters", "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}

	// VM.OrderID → product_id：对每条 VM 各查一次 order；同一 page_size<=100 内可接受。
	// 后续若性能不够再加 OrderRepo.GetByIDs 批量接口。
	out := make([]InstanceDTO, 0, len(vms))
	for _, vm := range vms {
		productID := resolveProductID(r.Context(), h.deps.Orders, vm.OrderID)
		out = append(out, toInstanceDTO(vm, regionByCluster[vm.ClusterID], productID))
	}
	writePage(w, out, page, pageSize, int(total))
}

// InstanceByID GET /v1/instances/{id}：返单条 VM。owner 校验：非本人 VM 一律 404
// （不区分 403 / 404 防资源存在性泄露）。
func (h *Handler) InstanceByID(w http.ResponseWriter, r *http.Request) {
	if h.deps.VMs == nil || h.deps.Clusters == nil || h.deps.Orders == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, perr := strconv.ParseInt(idStr, 10, 64)
	if perr != nil || id <= 0 {
		// 非整数 / 非正 → 当作 not_found（标准 REST 行为：不暴露 id 解析细节）
		writeErr(w, http.StatusNotFound, "", "not_found")
		return
	}
	vm, err := h.deps.VMs.GetByID(r.Context(), id)
	if err != nil {
		slog.Error("v1 InstanceByID: get vm", "vm_id", id, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	if vm == nil || vm.UserID != uid || vm.TrashedAt != nil || vm.Status == model.VMStatusDeleted || vm.Status == "gone" {
		// trashed / deleted / gone 一律 not_found，与 ListByUserPaged 过滤口径一致
		writeErr(w, http.StatusNotFound, "", "not_found")
		return
	}

	cluster, err := h.deps.Clusters.GetByID(r.Context(), vm.ClusterID)
	if err != nil {
		slog.Error("v1 InstanceByID: get cluster", "cluster_id", vm.ClusterID, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	regionName := ""
	if cluster != nil {
		regionName = cluster.Name
	}
	productID := resolveProductID(r.Context(), h.deps.Orders, vm.OrderID)
	writeJSON(w, http.StatusOK, toInstanceDTO(*vm, regionName, productID))
}

// Types GET /v1/types：返激活态产品分页列表。products 一般 < 50 行，
// 走 ListActive + 内存分页足够；后续真大了再做 SQL paging。
func (h *Handler) Types(w http.ResponseWriter, r *http.Request) {
	if h.deps.Products == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	page, pageSize, perr := parsePagination(r)
	if perr != nil {
		writePaginationErr(w, perr)
		return
	}
	products, err := h.deps.Products.ListActive(r.Context())
	if err != nil {
		slog.Error("v1 Types: list products", "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	dtos := make([]TypeDTO, 0, len(products))
	for _, p := range products {
		dtos = append(dtos, toTypeDTO(p))
	}
	paged, total := paginateInMemory(dtos, page, pageSize)
	writePage(w, paged, page, pageSize, total)
}

// Regions GET /v1/regions：返 clusters 列表。一期固定 ["instances"] capability。
func (h *Handler) Regions(w http.ResponseWriter, r *http.Request) {
	if h.deps.Clusters == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	page, pageSize, perr := parsePagination(r)
	if perr != nil {
		writePaginationErr(w, perr)
		return
	}
	clusters, err := h.deps.Clusters.List(r.Context())
	if err != nil {
		slog.Error("v1 Regions: list clusters", "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	dtos := make([]RegionDTO, 0, len(clusters))
	for _, c := range clusters {
		dtos = append(dtos, toRegionDTO(c))
	}
	paged, total := paginateInMemory(dtos, page, pageSize)
	writePage(w, paged, page, pageSize, total)
}

// Images GET /v1/images：返激活态 OS 模板分页列表。
func (h *Handler) Images(w http.ResponseWriter, r *http.Request) {
	if h.deps.OSTemplates == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	page, pageSize, perr := parsePagination(r)
	if perr != nil {
		writePaginationErr(w, perr)
		return
	}
	templates, err := h.deps.OSTemplates.ListEnabled(r.Context())
	if err != nil {
		slog.Error("v1 Images: list templates", "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	dtos := make([]ImageDTO, 0, len(templates))
	for _, t := range templates {
		dtos = append(dtos, toImageDTO(t))
	}
	paged, total := paginateInMemory(dtos, page, pageSize)
	writePage(w, paged, page, pageSize, total)
}

// SSHKeys GET /v1/ssh-keys：返当前用户的 SSH key 分页列表。
func (h *Handler) SSHKeys(w http.ResponseWriter, r *http.Request) {
	if h.deps.SSHKeys == nil {
		internalIfMissingDeps(w, r.URL.Path)
		return
	}
	uid, ok := userIDFromCtx(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "", "unauthorized")
		return
	}
	page, pageSize, perr := parsePagination(r)
	if perr != nil {
		writePaginationErr(w, perr)
		return
	}
	keys, total, err := h.deps.SSHKeys.ListByUserPaged(r.Context(), uid, pageSize, (page-1)*pageSize)
	if err != nil {
		slog.Error("v1 SSHKeys: list keys", "user_id", uid, "error", err)
		writeErr(w, http.StatusInternalServerError, "", "internal")
		return
	}
	dtos := make([]SSHKeyDTO, 0, len(keys))
	for _, k := range keys {
		dtos = append(dtos, toSSHKeyDTO(k))
	}
	writePage(w, dtos, page, pageSize, int(total))
}

// regionNameMap 一次性把所有 cluster_id 拉到 map，给 Instances 列表 DTO 解析用。
// 空集时返回非 nil map（外层 m[k] 取 "" 即可，无需额外判空）。
func (h *Handler) regionNameMap(ctx context.Context) (map[int64]string, error) {
	clusters, err := h.deps.Clusters.List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(clusters))
	for _, c := range clusters {
		m[c.ID] = c.Name
	}
	return m, nil
}

// resolveProductID 单条 VM 的 order → product_id 解析。
// 任何错误 / 缺单 → 返 0（log warn 不阻断响应），InstanceDTO.Type 字段最坏退化为 0。
func resolveProductID(ctx context.Context, repo orderReader, orderID *int64) int64 {
	if orderID == nil {
		return 0
	}
	o, err := repo.GetByID(ctx, *orderID)
	if err != nil {
		// 单条 order 失败不应导致整张 instance list 500；记 warn 继续。
		slog.Warn("v1 resolveProductID: get order", "order_id", *orderID, "error", err)
		return 0
	}
	if o == nil {
		return 0
	}
	return o.ProductID
}

// paginateInMemory 把整切片按 1-indexed page + pageSize 取子片。
// page 超出尾页时返回 (空切片, 原 total)；caller 仍照常 writePage。
// 泛型避免对每个 DTO 类型重复同样的 slicing 代码。
func paginateInMemory[T any](items []T, page, pageSize int) ([]T, int) {
	total := len(items)
	if pageSize <= 0 || page <= 0 {
		return []T{}, total
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []T{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := make([]T, end-start)
	copy(out, items[start:end])
	return out, total
}

// writeJSON 单对象响应（非分页）。错误格式仍走 writeErr / writeErrs，
// 这里只负责 200 类成功响应。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("v1 writeJSON encode failed", "error", err, "status", status)
	}
}
