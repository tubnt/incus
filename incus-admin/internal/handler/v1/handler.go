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

// productReader 抽象 ProductRepo.ListActive。
type productReader interface {
	ListActive(ctx context.Context) ([]model.Product, error)
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

// orderReader 抽象 OrderRepo.GetByID，用于把 VM.OrderID → product_id（InstanceDTO.Type）。
type orderReader interface {
	GetByID(ctx context.Context, id int64) (*model.Order, error)
}

// Deps 把 Handler 依赖打包，main.go 把真实 *repository.*Repo 喂进来；
// 单测注入 fake 实现。Phase D/E 后续再加 OrderService / IdempotencyRepo。
type Deps struct {
	Users       userReader
	VMs         vmReader
	Products    productReader
	Clusters    clusterReader
	OSTemplates osTemplateReader
	SSHKeys     sshKeyReader
	Orders      orderReader
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
