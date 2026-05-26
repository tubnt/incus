// Package v1 实现 cloud-gateway 标准 /v1/* 适配层（PLAN-053）。
//
// 与现有 /api/portal /api/admin 的差异：
//   - 认证：只接受 Bearer ica_ token（不接受 oauth2-proxy header / shadow cookie）
//   - 错误格式：`{"errors":[{"field":"...","reason":"..."}]}`（非 `{"error":"..."}`）
//   - 分页格式：`?page=&page_size=` + `{"data":[...],"page","page_size","pages","total"}`
//   - 限流：独立 token-bucket（默认 100 rpm + burst 30），不与 portal 共桶
//
// Phase A 把 12 个端点全部用 501 占位；Phase B（本文件）把 7 个 read-only 端点
// 接到真实 repo，POST/DELETE/actions 留给 Phase D/E。
package v1

import (
	"context"
	"net/http"

	"github.com/incuscloud/incus-admin/internal/handler/portal"
	"github.com/incuscloud/incus-admin/internal/model"
)

// userReader 抽象 UserRepo.GetByID，便于单测注入 fake。
// 返回 (nil, nil) 表示用户不存在（与 repo 现有约定一致）。
type userReader interface {
	GetByID(ctx context.Context, id int64) (*model.User, error)
}

// vmReader 抽象 VMRepo 的 read-only 方法集（Phase B 用）。
// ListByUserPaged limit<=0 表示不分页；caller 不会传 0/负数（pagination helper 已 clamp）。
type vmReader interface {
	GetByID(ctx context.Context, id int64) (*model.VM, error)
	ListByUserPaged(ctx context.Context, userID int64, limit, offset int) ([]model.VM, int64, error)
}

// productReader 抽象 ProductRepo 的 read 集（含 GetByID 给 POST 流校验 type）。
type productReader interface {
	ListActive(ctx context.Context) ([]model.Product, error)
	GetByID(ctx context.Context, id int64) (*model.Product, error)
}

// clusterReader 抽象 ClusterRepo 的 read-only 方法集。
// List 返回全部 cluster（含未填 country/city 的）；caller 自己分页 / 过滤。
type clusterReader interface {
	GetByID(ctx context.Context, id int64) (*model.Cluster, error)
	List(ctx context.Context) ([]model.Cluster, error)
}

// osTemplateReader 抽象 OSTemplateRepo.ListEnabled —— admin 关闭的模板不暴露给
// cloud-gateway client，与 portal 行为一致。
type osTemplateReader interface {
	ListEnabled(ctx context.Context) ([]model.OSTemplate, error)
}

// sshKeyReader 抽象 SSHKeyRepo 的 read-only 方法集。
type sshKeyReader interface {
	ListByUserPaged(ctx context.Context, userID int64, limit, offset int) ([]model.SSHKey, int64, error)
}

// sshKeyOwnerVerifier 用于 POST /v1/instances 校验请求里的 ssh_key id 都属于当前 user。
// 实现：repository.SSHKeyRepo.ListByUser → 拉所有 key 后由 handler 在内存里做集合比对。
type sshKeyOwnerVerifier interface {
	ListByUser(ctx context.Context, userID int64) ([]model.SSHKey, error)
}

// orderReader 抽象 OrderRepo.GetByID，用于把 VM.OrderID → product_id（InstanceDTO.Type）。
type orderReader interface {
	GetByID(ctx context.Context, id int64) (*model.Order, error)
}

// orderProvisioner 抽象 portal.OrderHandler 的一步购买入口；接口允许测试注入 fake。
// 实现见 portal/order_v1.go。
type orderProvisioner interface {
	CreatePayProvision(r *http.Request, req portal.V1ProvisionRequest) (*portal.V1ProvisionResult, *portal.V1ProvisionError)
}

// vmTrasher 抽象 portal.VMHandler 的 V1TrashByID 服务方法。
type vmTrasher interface {
	V1TrashByID(r *http.Request, userID, vmID int64) *portal.V1ProvisionError
}

// vmActioner 抽象 portal.VMHandler 的 V1ActionByID 服务方法。
type vmActioner interface {
	V1ActionByID(r *http.Request, userID, vmID int64, action string) *portal.V1ProvisionError
}

// Deps 把 Handler 依赖打包，main.go 把真实 *repository.*Repo / *portal.*Handler 喂进来；
// 单测注入 fake 实现。Phase D/E 在 Phase B 基础上加 OSTemplate.GetBySlug / SSHKey 越权
// 校验 / Cluster region 状态校验 / order 一步购买 + VM trash + action 服务方法。
type Deps struct {
	Users       userReader
	VMs         vmReader
	Products    productReader
	Clusters    clusterReader
	OSTemplates osTemplateReader
	SSHKeys     sshKeyReader
	Orders      orderReader

	// Phase D/E 新增依赖：
	// ClustersByName 用于校验 region 是 available；为空时 Phase D POST 拒绝（500）。
	ClustersByName clusterByNameReader
	// OSTemplatesBySlug 把请求里的 image slug 解析到完整 OSTemplate（拿 source / enabled）。
	OSTemplatesBySlug osTemplateBySlugReader
	// ProductsBySlug 解析 type 字段（接受 numeric id 或 slug）。
	ProductsBySlug productBySlugReader
	// SSHKeysOwner 越权校验 ssh_keys[]。
	SSHKeysOwner sshKeyOwnerVerifier

	// OrderProvision 一步购买入口（portal.OrderHandler.CreatePayProvision）。
	OrderProvision orderProvisioner
	// VMTrash / VMAction：DELETE + actions 服务方法（portal.VMHandler.V1TrashByID / V1ActionByID）。
	VMTrash  vmTrasher
	VMAction vmActioner
}

// clusterByNameReader 用于 POST /v1/instances 校验 region 字段。
type clusterByNameReader interface {
	GetByName(ctx context.Context, name string) (*model.Cluster, error)
}

// osTemplateBySlugReader 用于把 image=<slug> 解析到 OSTemplate（拿 source / enabled）。
type osTemplateBySlugReader interface {
	GetBySlug(ctx context.Context, slug string) (*model.OSTemplate, error)
}

// productBySlugReader 让 type 字段同时接受 numeric id 和 slug；
// 实现走 ListActive + 内存查找（products N < 50）。
type productBySlugReader interface {
	ListActive(ctx context.Context) ([]model.Product, error)
}

// Handler 持有 /v1/* 端点所需的依赖。Phase B 注入 read-only repos；
// Phase D/E 后续会按需扩。
type Handler struct {
	deps Deps
}

// New 构造 v1 Handler。Phase B 强烈推荐传齐 Deps —— 任一字段为 nil 时该 endpoint
// 会在调用时显式 500（writeErr reason=internal）+ slog.Error，避免 nil 解引用 panic。
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}
