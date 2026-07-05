package portal

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/service"
	"github.com/incuscloud/incus-admin/internal/service/jobs"
)

// V1ProvisionRequest 是 cloud-gateway /v1/instances 一步购买的标准化输入。
// 由 handler/v1 完成 region / product / image / ssh_keys 校验后填入：
//
//   - ClusterID / ClusterName：region 解析结果（v1 用 region_status='available' 校验）。
//   - ProductID / Period：v1 已校验 period 在 product.period_supported；本方法仍二次
//     校验 rate 兜底（admin 漏填 price_daily 等 corner case）。
//   - OSImage：os_template.source 形式（"ubuntu/24.04/cloud" 等），v1 已解析 slug。
//   - SSHKeys：完整 public_key 字符串数组（v1 已按 user_id 越权校验通过）。
//   - VMName 可空 —— 走 service.GenerateVMName。
type V1ProvisionRequest struct {
	UserID      int64
	ProductID   int64
	ClusterID   int64
	ClusterName string
	OSImage     string
	VMName      string
	Period      string
	SSHKeys     []string

	// WP-I1 /v1 三字段（已由 handler/v1 完成基本校验）：
	//   - RootPass：root 密码；空 → executor 随机生成。
	//   - UserData：cloud-init user-data，合并进 OS-aware 基础配置。
	//   - Tags：incus 实例标签（user.tags）。
	RootPass string
	UserData string
	Tags     []string
}

// V1ProvisionResult 是 CreatePayProvision 成功路径的输出。
type V1ProvisionResult struct {
	VMID    int64
	OrderID int64
	JobID   int64
	VMName  string
	IP      string
}

// V1ProvisionError 把订单/付款/provisioning 流程里的失败折成结构化错误。
// Status 是 cloud-gateway 标准的 HTTP 状态；Field/Reason 进 StructuredError；
// Msg 仅用于 log，不暴露给客户端（含内部错误明细 / 数据库错误）。
type V1ProvisionError struct {
	Status int
	Field  string
	Reason string
	Msg    string
}

func (e *V1ProvisionError) Error() string {
	if e.Msg != "" {
		return e.Reason + ": " + e.Msg
	}
	return e.Reason
}

// CreatePayProvision 实现 cloud-gateway /v1/instances 一步购买流程：
// 校验余额 → 创建订单 → 扣款 → 申请 IP → 写 vm 行 + 订阅 → enqueue provisioning。
//
// 复用现有 OrderHandler 的 helper（rollbackPayment / createSubscriptionForOrder
// / cancelSubscriptionForRollback / checkQuota）+ 包级 allocateIP / attachIPToVM，
// 避免与 portal Pay 路径产生第二份订单流逻辑。
//
// 与 portal /orders/{id}/pay 的差异：
//   - 入参由 v1 handler 预校验（region/product/image/ssh_keys），失败由 v1 自行返 422
//   - 失败返结构化错误，不写 HTTP 响应（v1 handler 走 StructuredError 包装）
//   - audit 标 source=api
//
// **不复制** Create + Pay 的两段式 HTTP handler 代码 —— 本方法是它们的二阶段融合。
func (h *OrderHandler) CreatePayProvision(r *http.Request, req V1ProvisionRequest) (*V1ProvisionResult, *V1ProvisionError) {
	ctx := r.Context()

	product, err := h.products.GetByID(ctx, req.ProductID)
	if err != nil {
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "products.GetByID: " + err.Error()}
	}
	if product == nil || !product.Active {
		return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "type", Reason: "type_not_found"}
	}

	// 二次校验 period（v1 已校 product.PeriodSupported；这里兜底 rate 缺失）。
	period := req.Period
	if period == "" {
		period = model.BillingPeriodMonthly
	}
	var amount float64
	switch period {
	case model.BillingPeriodDaily:
		if product.PriceDaily == nil {
			return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "period", Reason: "rate_missing"}
		}
		amount = *product.PriceDaily
	case model.BillingPeriodMonthly:
		if product.PriceMonthly <= 0 {
			return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "period", Reason: "rate_missing"}
		}
		amount = product.PriceMonthly
	default:
		return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "period", Reason: "unsupported_period"}
	}

	// 余额预检查 —— 直接读 users.balance 比 PayWithBalance 的中文错误字符串匹配
	// 更可靠（PayWithBalance 失败时也会撞这条；预检查只是为了显式 402 +
	// reason=insufficient_balance，避免给 cloud-gateway 客户端返不可解析的内部错误）。
	if userRepo != nil {
		user, uerr := userRepo.GetByID(ctx, req.UserID)
		if uerr != nil {
			return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "users.GetByID: " + uerr.Error()}
		}
		if user == nil {
			return nil, &V1ProvisionError{Status: http.StatusUnauthorized, Reason: "unauthorized"}
		}
		if user.Balance < amount {
			return nil, &V1ProvisionError{Status: http.StatusPaymentRequired, Field: "balance", Reason: "insufficient_balance"}
		}
	}

	// quota 预检查（与 portal Pay 等价）。fail-closed 时返 503，与现有行为一致。
	if h.quotas != nil {
		if qerr := h.checkQuota(ctx, req.UserID, product); qerr != nil {
			return nil, &V1ProvisionError{Status: http.StatusPaymentRequired, Field: "quota", Reason: "quota_exceeded", Msg: qerr.Error()}
		}
	}

	// jobs runtime 是硬依赖（同步 provisioning 已删，见 OPS-051 / PLAN-052 Q3=A）。
	if h.jobs == nil || h.jobRepo == nil {
		return nil, &V1ProvisionError{Status: http.StatusServiceUnavailable, Reason: "provisioning_unavailable"}
	}

	clusterName := req.ClusterName
	if clusterName == "" && req.ClusterID > 0 {
		clusterName = h.clusters.NameByID(req.ClusterID)
	}
	if clusterName == "" {
		return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "region", Reason: "region_not_found"}
	}
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		return nil, &V1ProvisionError{Status: http.StatusServiceUnavailable, Reason: "cluster_unavailable", Msg: "cluster " + clusterName + " not registered"}
	}
	clusterID := req.ClusterID
	if clusterID == 0 {
		clusterID = h.clusters.IDByName(clusterName)
	}
	if clusterID == 0 {
		return nil, &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "region", Reason: "region_not_found"}
	}

	order, err := h.orders.CreateWithPeriod(ctx, req.UserID, req.ProductID, clusterID, amount, product.Currency, period)
	if err != nil {
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "orders.Create: " + err.Error()}
	}

	if err := h.orders.PayWithBalance(ctx, order.ID); err != nil {
		// PayWithBalance 失败时订单仍 pending，best-effort 取消避免悬挂。
		if _, cerr := h.orders.CancelIfPending(ctx, order.ID, req.UserID); cerr != nil {
			slog.Warn("v1 provision: cancel after pay failure", "order_id", order.ID, "error", cerr)
		}
		// 余额预检查已挡掉绝大多数 insufficient；这里走到说明并发扣款 / 上面预检查 nil。
		return nil, &V1ProvisionError{Status: http.StatusPaymentRequired, Field: "balance", Reason: "insufficient_balance", Msg: err.Error()}
	}

	if err := h.orders.UpdateStatus(ctx, order.ID, model.OrderProvisioning); err != nil {
		h.rollbackPayment(ctx, order, "", "set provisioning failed: "+err.Error())
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "set provisioning failed"}
	}

	defProject := cc.DefaultProject
	if defProject == "" {
		defProject = "customers"
	}
	pool := cc.StoragePool
	if pool == "" {
		pool = "ceph-pool"
	}
	network := cc.Network
	if network == "" {
		network = "br-pub"
	}

	ip, gateway, cidr, ipErr := allocateIP(ctx, cc, 0)
	if ipErr != nil {
		h.rollbackPayment(ctx, order, "", "ip allocation failed: "+ipErr.Error())
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "ip_alloc_failed", Msg: ipErr.Error()}
	}

	vmName := req.VMName
	if vmName == "" {
		vmName = service.GenerateVMName()
	}
	osImage := req.OSImage
	if osImage == "" {
		// v1 已在 handler 层挡掉空 image；走到这里只能是 caller bug，兜底 ubuntu。
		osImage = "images:ubuntu/24.04/cloud"
	}

	ipRef := ip
	orderID := order.ID
	vm := &model.VM{
		Name:      vmName,
		ClusterID: clusterID,
		UserID:    req.UserID,
		OrderID:   &orderID,
		Status:    model.VMStatusCreating,
		CPU:       product.CPU,
		MemoryMB:  product.MemoryMB,
		DiskGB:    product.DiskGB,
		OSImage:   osImage,
		IP:        &ipRef,
	}
	if err := h.vmRepo.Create(ctx, vm); err != nil {
		h.rollbackPayment(ctx, order, ip, "vm row insert failed: "+err.Error())
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "vm row insert failed"}
	}
	attachIPToVM(ctx, ip, vm.ID)

	if err := h.createSubscriptionForOrder(ctx, r, order, product, vm.ID); err != nil {
		_ = h.vmRepo.Delete(ctx, vm.ID)
		h.rollbackPayment(ctx, order, ip, "subscription insert failed: "+err.Error())
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "sub insert failed"}
	}

	job, err := h.jobRepo.Create(ctx, model.JobKindVMCreate, req.UserID, clusterID, &orderID, &vm.ID, vmName)
	if err != nil {
		h.cancelSubscriptionForRollback(ctx, vm.ID)
		h.rollbackPayment(ctx, order, ip, "job create failed: "+err.Error())
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "job create failed"}
	}

	sshKeys := req.SSHKeys
	if sshKeys == nil {
		sshKeys = []string{}
	}
	if err := h.jobs.Enqueue(ctx, job.ID, jobs.Params{
		Project:     defProject,
		CPU:         product.CPU,
		MemoryMB:    product.MemoryMB,
		DiskGB:      product.DiskGB,
		OSImage:     osImage,
		SSHKeys:     sshKeys,
		IP:          ip,
		Gateway:     gateway,
		SubnetCIDR:  cidr,
		StoragePool: pool,
		Network:     network,
		OrderAmount: order.Amount,
		// WP-I1：/v1 root_pass / user_data / tags 透传到 provisioning executor。
		RootPass: req.RootPass,
		UserData: req.UserData,
		Tags:     req.Tags,
	}); err != nil {
		_ = h.jobRepo.Finish(ctx, job.ID, model.JobStatusFailed, "enqueue failed: "+err.Error())
		h.cancelSubscriptionForRollback(ctx, vm.ID)
		h.rollbackPayment(ctx, order, ip, "enqueue failed")
		return nil, &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "enqueue failed"}
	}

	audit(ctx, r, "order.pay", "order", order.ID, map[string]any{
		"vm_name": vmName,
		"ip":      ip,
		"amount":  order.Amount,
		"job_id":  job.ID,
		"source":  "api",
	})

	return &V1ProvisionResult{
		VMID:    vm.ID,
		OrderID: order.ID,
		JobID:   job.ID,
		VMName:  vmName,
		IP:      ip,
	}, nil
}

// V1TrashByID 是 cloud-gateway /v1/instances/{id} DELETE 的服务方法。
// owner 失败 / VM 不存在统一返 not_found（不区分 403/404，防资源存在性泄露）。
// 与 portal TrashService 等价但返结构化错误，不写 HTTP 响应。
func (h *VMHandler) V1TrashByID(r *http.Request, userID, vmID int64) *V1ProvisionError {
	ctx := r.Context()
	vm, err := h.vmRepo.GetByID(ctx, vmID)
	if err != nil {
		return &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "vmRepo.GetByID: " + err.Error()}
	}
	// 与 portal 一致：owner 不匹配 / 已 trash / 已 delete 一律 not_found（防泄露）。
	if vm == nil || vm.UserID != userID || vm.TrashedAt != nil || vm.Status == model.VMStatusDeleted || vm.Status == "gone" {
		return &V1ProvisionError{Status: http.StatusNotFound, Reason: "not_found"}
	}
	clusterName := h.clusters.NameByID(vm.ClusterID)
	if clusterName == "" {
		return &V1ProvisionError{Status: http.StatusServiceUnavailable, Reason: "cluster_unavailable"}
	}
	cc, _ := h.clusters.ConfigByName(clusterName)
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}
	if err := h.vmSvc.Trash(ctx, clusterName, project, vm.Name); err != nil {
		slog.Warn("v1 trash: stop failed", "vm", vm.Name, "error", err)
	}
	ok, err := h.vmRepo.MarkTrashed(ctx, vm.ID)
	if err != nil {
		return &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "MarkTrashed: " + err.Error()}
	}
	if !ok {
		// 并发竞态：另一个请求已 trash —— 当 not_found 处理。
		return &V1ProvisionError{Status: http.StatusNotFound, Reason: "not_found"}
	}
	cancelSubscriptionOnTrash(ctx, r, h.subs, vm.ID)
	audit(ctx, r, "vm.trash", "vm", vm.ID, map[string]any{
		"name":   vm.Name,
		"self":   true,
		"source": "api",
	})
	return nil
}

// V1ActionByID 是 cloud-gateway /v1/instances/{id}/{action} 的服务方法。
// action ∈ {boot, reboot, shutdown}（cloud-gateway 词汇）→ Incus
// {start, restart, stop} 映射。
func (h *VMHandler) V1ActionByID(r *http.Request, userID, vmID int64, action string) *V1ProvisionError {
	ctx := r.Context()
	vm, err := h.vmRepo.GetByID(ctx, vmID)
	if err != nil {
		return &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "internal", Msg: "vmRepo.GetByID: " + err.Error()}
	}
	if vm == nil || vm.UserID != userID || vm.TrashedAt != nil || vm.Status == model.VMStatusDeleted || vm.Status == "gone" {
		return &V1ProvisionError{Status: http.StatusNotFound, Reason: "not_found"}
	}
	if h.vmSvc == nil || h.clusters == nil {
		return &V1ProvisionError{Status: http.StatusServiceUnavailable, Reason: "cluster_unavailable"}
	}
	var (
		incusAction string
		newStatus   string
	)
	switch action {
	case "boot":
		incusAction = "start"
		newStatus = model.VMStatusRunning
	case "reboot":
		incusAction = "restart"
		newStatus = model.VMStatusRunning
	case "shutdown":
		incusAction = "stop"
		newStatus = model.VMStatusStopped
	default:
		return &V1ProvisionError{Status: http.StatusUnprocessableEntity, Field: "action", Reason: "invalid_action"}
	}

	clusterName := findClusterName(h.clusters, vm.ClusterID)
	if clusterName == "" {
		return &V1ProvisionError{Status: http.StatusServiceUnavailable, Reason: "cluster_unavailable"}
	}
	cc, _ := h.clusters.ConfigByName(clusterName)
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}
	if err := h.vmSvc.ChangeState(ctx, clusterName, project, vm.Name, incusAction, false); err != nil {
		return &V1ProvisionError{Status: http.StatusInternalServerError, Reason: "incus_action_failed", Msg: fmt.Sprintf("ChangeState %s: %v", incusAction, err)}
	}
	if uerr := h.vmRepo.UpdateStatus(ctx, vm.ID, newStatus); uerr != nil {
		// 与 portal VMAction 一致：DB 同步失败只 warn，不阻断 202 响应（reconciler 会兜底）。
		slog.Warn("v1 action: db status sync failed", "vm", vm.Name, "action", action, "error", uerr)
	}
	audit(ctx, r, "vm.action", "vm", vm.ID, map[string]any{
		"name":         vm.Name,
		"action":       action,
		"incus_action": incusAction,
		"source":       "api",
	})
	return nil
}

