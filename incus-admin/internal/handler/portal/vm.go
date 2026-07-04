package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
	"github.com/incuscloud/incus-admin/internal/service"
	"github.com/incuscloud/incus-admin/internal/service/jobs"
)

type VMHandler struct {
	vmSvc    *service.VMService
	vmRepo   *repository.VMRepo
	sshKeys  *repository.SSHKeyRepo
	clusters *cluster.Manager
	// PLAN-025：异步 reinstall 入口
	jobs    *jobs.Runtime
	jobRepo *repository.ProvisioningJobRepo
	// PLAN-054 / INFRA-013：vm_subscriptions repo。trash/restore 时联动切状态。
	// nil 时跳过（兼容尚未注入的部署），与生产 main 注入保持一致。
	subs *repository.SubscriptionRepo
}

func NewVMHandler(vmSvc *service.VMService, vmRepo *repository.VMRepo, sshKeys *repository.SSHKeyRepo, clusters *cluster.Manager) *VMHandler {
	return &VMHandler{vmSvc: vmSvc, vmRepo: vmRepo, sshKeys: sshKeys, clusters: clusters}
}

// WithJobs 注入 PLAN-025 异步运行时；handler 在两个写入站点（Reinstall + AdminVMHandler.CreateVM）
// 都判 nil 走兼容兜底。
func (h *VMHandler) WithJobs(rt *jobs.Runtime, jobRepo *repository.ProvisioningJobRepo) *VMHandler {
	h.jobs = rt
	h.jobRepo = jobRepo
	return h
}

// WithSubscriptions 注入 vm_subscriptions repo，启用 PLAN-054 trash/restore 联动。
func (h *VMHandler) WithSubscriptions(s *repository.SubscriptionRepo) *VMHandler {
	h.subs = s
	return h
}

func (h *VMHandler) Routes(r chi.Router) {
	r.Get("/services", h.ListServices)
	r.Get("/services/{id}", h.GetService)
	r.Post("/services/{id}/actions/{action}", h.VMAction)
	r.Post("/services/{id}/reinstall", h.Reinstall)
	r.Post("/services/{id}/reset-password", h.ResetPassword)
	// UX-007: 重看初始密码（创建瞬间的 root 密码）。step-up gated，audit 全记。
	r.Post("/services/{id}/initial-credentials", h.GetInitialCredentials)
	// PLAN-034: 用户自助 trash-with-undo。删除 = 软移入回收站（30s 窗口可撤销），
	// purge 后 status = 'deleted'。配合 admin 端 worker.RunVMTrashPurger。
	r.Delete("/services/{id}", h.TrashService)
	r.Post("/services/{id}/restore", h.RestoreService)
	r.Get("/services/trashed", h.ListMyTrashed)
}

// TrashService 是用户自助 VM 删除的入口（PLAN-034）。校验 owner = current user，
// stop Incus 实例后 DB 标 trashed_at = NOW()。30s 内可调 POST /services/{id}/restore。
func (h *VMHandler) TrashService(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if vmID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not found"})
		return
	}
	clusterName := h.clusters.NameByID(vm.ClusterID)
	if clusterName == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster unavailable"})
		return
	}
	project := "customers"
	if err := h.vmSvc.Trash(r.Context(), clusterName, project, vm.Name); err != nil {
		slog.Warn("portal trash: stop failed", "vm", vm.Name, "error", err)
	}
	ok, err := h.vmRepo.MarkTrashed(r.Context(), vm.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "vm already trashed or deleted"})
		return
	}
	cancelSubscriptionOnTrash(r.Context(), r, h.subs, vm.ID)
	audit(r.Context(), r, "vm.trash", "vm", vm.ID, map[string]any{"name": vm.Name, "self": true})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "trashed",
		"vm_id":      vm.ID,
		"name":       vm.Name,
		"trashed_at": time.Now().UTC(),
		"window_s":   model.VMTrashWindowSeconds,
	})
}

// RestoreService 撤销 30s 窗口内的删除（PLAN-034）。owner check 同 TrashService。
func (h *VMHandler) RestoreService(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if vmID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not found"})
		return
	}
	if vm.TrashedAt == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "vm not in trash"})
		return
	}
	ok, err := h.vmRepo.UnmarkTrashed(r.Context(), vm.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "vm not in trash (race)"})
		return
	}
	reactivateSubscriptionOnRestore(r.Context(), r, h.subs, vm.ID)
	prev := ""
	if vm.TrashedPrevStatus != nil {
		prev = *vm.TrashedPrevStatus
	}
	audit(r.Context(), r, "vm.restore", "vm", vm.ID, map[string]any{"name": vm.Name, "prev_status": prev, "self": true})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "restored",
		"vm_id":       vm.ID,
		"name":        vm.Name,
		"prev_status": prev,
	})
}

// ListMyTrashed 返回当前用户在 30s 窗口内的 trashed VM。即便用户刷新页面，前端也能
// 据此恢复 undo 提示（之前的 toast 不指望存活，DB 是真理之源）。
func (h *VMHandler) ListMyTrashed(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	rows, err := h.vmRepo.ListMyTrashed(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, vm := range rows {
		out = append(out, map[string]any{
			"id":         vm.ID,
			"name":       vm.Name,
			"trashed_at": vm.TrashedAt,
			"window_s":   model.VMTrashWindowSeconds,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": out, "count": len(out)})
}

func (h *VMHandler) Reinstall(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
		return
	}

	var req struct {
		TemplateSlug string `json:"template_slug" validate:"omitempty,safename,max=64"`
		OSImage      string `json:"os_image"      validate:"omitempty,max=200"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.TemplateSlug == "" && req.OSImage == "" {
		req.TemplateSlug = "ubuntu-24-04"
	}

	resolved, err := resolveReinstallTemplate(r.Context(), req.TemplateSlug, req.OSImage)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	clusterName := findClusterName(h.clusters, vm.ClusterID)
	cc, _ := h.clusters.ConfigByName(clusterName)
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}
	resolved.ClusterName = clusterName
	resolved.Project = project
	resolved.VMName = vm.Name

	if h.jobs == nil || h.jobRepo == nil {
		// OPS-051 / PLAN-052 Q3=A：删除同步兜底路径。生产强制注入 jobs runtime。
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "reinstall runtime unavailable"})
		return
	}

	// 异步路径：保留 probe + prePullImage 同步前置（OPS-008/012/016 数据保护防线）。
	// 拉失败 → 立即 4xx 返回；原 VM 完整保留，未删除。
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cluster not registered"})
		return
	}
	if err := service.ProbeImageServer(r.Context(), resolved.ServerURL); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": fmt.Sprintf("镜像服务器 %s 不可达，已取消重装以保护原 VM 数据: %v", resolved.ServerURL, err),
		})
		return
	}
	if err := service.PrePullImage(r.Context(), client, resolved.Project, resolved.ServerURL, resolved.Protocol, resolved.ImageSource); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error": fmt.Sprintf("镜像 %s 预拉取失败，已取消重装以保护原 VM 数据: %v", resolved.ImageSource, err),
		})
		return
	}

	clusterID := h.clusters.IDByName(clusterName)
	job, err := h.jobRepo.Create(r.Context(), model.JobKindVMReinstall, userID, clusterID, nil, &vm.ID, vm.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create job: " + err.Error()})
		return
	}

	if err := h.jobs.Enqueue(r.Context(), job.ID, jobs.Params{
		Project:     resolved.Project,
		ImageSource: resolved.ImageSource,
		ServerURL:   resolved.ServerURL,
		Protocol:    resolved.Protocol,
		DefaultUser: resolved.DefaultUser,
	}); err != nil {
		_ = h.jobRepo.Finish(r.Context(), job.ID, model.JobStatusFailed, "enqueue: "+err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "enqueue job: " + err.Error()})
		return
	}

	auditDetails := map[string]any{"name": vm.Name, "source": resolved.ImageSource, "user": resolved.DefaultUser, "job_id": job.ID}
	if req.TemplateSlug != "" {
		auditDetails["template_slug"] = req.TemplateSlug
	}
	audit(r.Context(), r, "vm.reinstall", "vm", vmID, auditDetails)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "provisioning",
		"job_id": job.ID,
		"vm_id":  vm.ID,
	})
}

func (h *VMHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	vmID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	vm, err := h.vmRepo.GetByID(r.Context(), vmID)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "access denied"})
		return
	}

	// PLAN-021 Phase C: optional mode selects online / offline / auto.
	// Default "auto" tries online first (works on healthy VMs via guest
	// agent) and falls back to offline (cloud-init re-run on reboot).
	var req struct {
		Mode string `json:"mode" validate:"omitempty,oneof=auto online offline"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	mode := service.ResetPasswordMode(req.Mode)

	clusterName := findClusterName(h.clusters, vm.ClusterID)
	cc, _ := h.clusters.ConfigByName(clusterName)
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}

	// OPS-051 / PLAN-052 Q7：portal reset 默认目标用户 = root（与新建 VM 一致）
	result, err := h.vmSvc.ResetPassword(r.Context(), clusterName, project, vm.Name, "root", mode)
	if err != nil {
		slog.Error("reset password failed", "vm", vm.Name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "password reset failed: " + err.Error()})
		return
	}

	_ = h.vmRepo.UpdatePassword(r.Context(), vmID, result.Password)

	audit(r.Context(), r, "vm.reset_password", "vm", vmID, map[string]any{
		"name":     vm.Name,
		"channel":  string(result.Channel),
		"fallback": result.Fallback,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "password_reset",
		"password": result.Password,
		"username": result.Username,
		"channel":  result.Channel,
		"fallback": result.Fallback,
	})
}

func (h *VMHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if userID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	vms, err := h.vmRepo.ListByUser(r.Context(), userID)
	if err != nil {
		slog.Error("list user vms failed", "user_id", userID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list VMs"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": NewVMServiceDTOList(vms, h.clusters, defaultProjectForMgr(h.clusters))})
}

// GetInitialCredentials 让 owner 在 VM 创建后任何时刻重看初始 root 密码。
//
// UX-007 / PLAN-051 follow-up：DonePanel 仅创建瞬间显示一次，关闭页面后用户
// 没有路径找回。后端 vms.password 一直存（AES-256-GCM 加密，OPS-022），
// 这里只是把已有数据通过 step-up gate 重新返给 owner。
//
// 与 ResetPassword 的区别：
//   - GetInitialCredentials：返历史密码，不变更 VM 状态（只读）
//   - ResetPassword：生成新密码 + 写入 VM（agent-exec 或 cloud-init），破坏旧密码
//
// 安全：
//   - step-up gated（middleware/stepup.go 白名单）
//   - owner_id 校验（与 GetService 同语义）
//   - audit user.view_initial_credentials
//   - 解密失败返 422 + 文案提示走 reset-password
func (h *VMHandler) GetInitialCredentials(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if vm.Password == nil || *vm.Password == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "no_initial_password",
			"hint":  "use reset-password instead",
		})
		return
	}
	// pma-cr Session-2 F-45 sentinel：解密失败返特定标记
	const decryptFailed = "<DECRYPT_FAILED>"
	if *vm.Password == decryptFailed {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "decrypt_failed",
			"hint":  "use reset-password instead",
		})
		return
	}
	audit(r.Context(), r, "user.view_initial_credentials", "vm", vm.ID, map[string]any{
		"vm_name": vm.Name,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"vm_name":    vm.Name,
		"ip":         vm.IP,
		"username":   defaultUserForVM(vm),
		"password":   *vm.Password,
		"created_at": vm.CreatedAt,
	})
}

// defaultUserForVM 推断 VM 默认登录用户。OPS-051 / PLAN-052 Q7：所有 Linux
// 统一 root；Windows 仍 Administrator。
func defaultUserForVM(vm *model.VM) string {
	img := strings.ToLower(vm.OSImage)
	if strings.Contains(img, "windows") {
		return "Administrator"
	}
	return "root"
}

func (h *VMHandler) GetService(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vm": NewVMServiceDTO(*vm, h.clusters, defaultProjectForMgr(h.clusters))})
}

func (h *VMHandler) VMAction(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	action := chi.URLParam(r, "action")

	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil || vm == nil || vm.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	if h.vmSvc == nil || h.clusters == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cluster not connected"})
		return
	}

	cc, _ := h.clusters.ConfigByName(findClusterName(h.clusters, vm.ClusterID))
	project := cc.DefaultProject
	if project == "" {
		project = "customers"
	}

	switch action {
	case "start", "stop", "restart":
		err := h.vmSvc.ChangeState(r.Context(), cc.Name, project, vm.Name, action, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Sync DB status so the portal detail view reflects the new state
		// immediately; otherwise GetService returns the stale row.
		newStatus := ""
		switch action {
		case "start", "restart":
			newStatus = model.VMStatusRunning
		case "stop":
			newStatus = model.VMStatusStopped
		}
		if newStatus != "" {
			if uerr := h.vmRepo.UpdateStatus(r.Context(), vm.ID, newStatus); uerr != nil {
				slog.Warn("vm status db sync failed", "vm", vm.Name, "action", action, "error", uerr)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": action})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown action"})
	}
}

// OPS-021：原 VMHandler.CreateService（POST /portal/services）已删除。
// 该 handler 函数从 PLAN-005 立项以来虽存在于代码中，但 Routes() 方法从
// 未注册其路由；前端 useCreateVMMutation 也仅有定义无引用。purchase-sheet 走
// OrderHandler.Pay（订单驱动 VM 创建）才是真正的用户购买入口。删除避免
// 将来误读为"用户可绕开订单直接创建 VM"。

// findClusterName resolves the DB cluster_id to its config name via the manager map.
// Falls back to the first available cluster for legacy rows where the id predates seeding.
func findClusterName(mgr *cluster.Manager, clusterID int64) string {
	if mgr == nil {
		return ""
	}
	if name := mgr.NameByID(clusterID); name != "" {
		return name
	}
	if clients := mgr.List(); len(clients) > 0 {
		return clients[0].Name
	}
	return ""
}

// defaultProjectForMgr returns the default project of the first configured cluster.
// Used by DTO builders so single-cluster deployments keep their project hint.
func defaultProjectForMgr(mgr *cluster.Manager) string {
	if mgr == nil {
		return ""
	}
	clients := mgr.List()
	if len(clients) == 0 {
		return ""
	}
	if cc, ok := mgr.ConfigByName(clients[0].Name); ok {
		return cc.DefaultProject
	}
	return ""
}

type AdminVMHandler struct {
	vmSvc     *service.VMService
	vmRepo    *repository.VMRepo
	sshKeys   *repository.SSHKeyRepo
	clusters  *cluster.Manager
	scheduler *cluster.Scheduler
	jobs      *jobs.Runtime
	jobRepo   *repository.ProvisioningJobRepo
	// PLAN-054 / INFRA-013：admin trash/restore 也联动 vm_subscriptions。
	subs *repository.SubscriptionRepo
}

func NewAdminVMHandler(vmSvc *service.VMService, vmRepo *repository.VMRepo, sshKeys *repository.SSHKeyRepo, clusters *cluster.Manager, scheduler *cluster.Scheduler) *AdminVMHandler {
	return &AdminVMHandler{vmSvc: vmSvc, vmRepo: vmRepo, sshKeys: sshKeys, clusters: clusters, scheduler: scheduler}
}

// WithJobs 注入 PLAN-025 异步运行时；admin direct create 也走相同 jobs runner。
func (h *AdminVMHandler) WithJobs(rt *jobs.Runtime, jobRepo *repository.ProvisioningJobRepo) *AdminVMHandler {
	h.jobs = rt
	h.jobRepo = jobRepo
	return h
}

// WithSubscriptions 注入 vm_subscriptions repo，启用 PLAN-054 trash/restore 联动。
func (h *AdminVMHandler) WithSubscriptions(s *repository.SubscriptionRepo) *AdminVMHandler {
	h.subs = s
	return h
}

func (h *AdminVMHandler) Routes(r chi.Router) {
	r.Get("/clusters", h.ListClusters)
	r.Get("/clusters/{name}/nodes", h.ListNodes)
	r.Get("/clusters/{name}/projects", h.ListProjects)
	r.Get("/clusters/{name}/vms", h.ListClusterVMs)
	r.Get("/clusters/{name}/vms/{vmName}", h.GetClusterVMDetail)
	r.Post("/clusters/{name}/vms", h.CreateVM)
	r.Get("/clusters/{name}/ha", h.GetHAStatus)
	r.Post("/clusters/{name}/nodes/{node}/evacuate", h.EvacuateNode)
	r.Post("/clusters/{name}/nodes/{node}/restore", h.RestoreNode)
	// PLAN-020 Phase E: chaos drill — locked behind appEnv != "production"
	// at the handler level so a misclick in prod can never run it.
	r.Post("/clusters/{name}/ha/chaos/simulate-node-down", h.ChaosSimulateNodeDown)
	r.Get("/vms", h.ListAllVMs)
	// PLAN-023: batch operations 必须排在 /{name} 通配前，避免 chi 把 ":batch" 当 vmName。
	r.Post("/vms:batch", h.BatchVMs)
	// PLAN-037: 批量冷迁移；同样必须先于 /{name} 通配。
	r.Post("/vms:migrate-batch", h.MigrateVMBatch)
	// PLAN-039 / OPS-043: 批量启用 stateful（live migration 前置）；先于 /{name} 通配
	r.Post("/vms:enable-stateful-batch", h.EnableStatefulBatch)
	r.Put("/vms/{name}/state", h.ChangeVMState)
	r.Post("/vms/{name}/reinstall", h.ReinstallVM)
	r.Post("/vms/{name}/migrate", h.MigrateVM)
	// PLAN-039 / OPS-043: 单台启用 stateful
	r.Post("/vms/{name}/enable-stateful", h.EnableStateful)
	r.Post("/vms/{name}/reset-password", h.ResetPasswordAdmin)
	r.Delete("/vms/{name}", h.DeleteVM)
	// PLAN-034: trash-with-undo 三联——回收站列表、按名 restore、按名 purge（强删，
	// 跳过 30s 等待，运维 escape hatch）。restore/purge 路径必须先于 force-delete
	// 的 {id} 通配，避免 chi 误匹配。
	r.Get("/vms/trashed", h.ListTrashedVMs)
	r.Post("/vms/{name}/restore", h.RestoreVM)
	r.Post("/vms/{name}/purge", h.PurgeVM)
	// PLAN-020 Phase B: gone-VM inventory + force-delete must sit on static
	// path segments before the name-wildcard routes so chi doesn't match
	// /vms/gone as name="gone".
	r.Get("/vms/gone", h.ListGoneVMs)
	r.Post("/vms/{id}/force-delete", h.ForceDeleteGone)
}

// ListGoneVMs returns rows marked `gone` by the reconciler. Frontend's
// admin VM list renders a "Drift" panel fed by this endpoint so the admin
// can investigate + clean up out-of-band deletions.
func (h *AdminVMHandler) ListGoneVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := h.vmRepo.ListGone(r.Context())
	if err != nil {
		slog.Error("list gone vms", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list failed"})
		return
	}
	if vms == nil {
		vms = []model.VM{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": vms, "count": len(vms)})
}

// ForceDeleteGone soft-deletes a VM row whose status is 'gone' (marked by
// PLAN-020 reconciler when the Incus instance vanished out-of-band) and
// releases any still-assigned IP. Non-gone rows are rejected with 409 to
// prevent accidental destruction of active VMs through this admin-only
// escape hatch. Identified by numeric id because gone rows are decoupled
// from any live Incus state, so the typical name-based routes no longer
// apply.
func (h *AdminVMHandler) ForceDeleteGone(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}

	vm, err := h.vmRepo.GetByID(r.Context(), id)
	if err != nil {
		slog.Error("force-delete lookup", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "lookup failed"})
		return
	}
	if vm == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not found"})
		return
	}
	if vm.Status != "gone" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "force delete only allowed for gone VMs",
			"status":  vm.Status,
			"message": "只能强删 status='gone' 的记录；其他状态请走常规删除",
		})
		return
	}

	if err := h.vmRepo.Delete(r.Context(), id); err != nil {
		slog.Error("force-delete row update", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "delete failed"})
		return
	}

	// Release the IP if still marked assigned. Non-fatal: next reconciler
	// pass + IP cooldown recovery would clean it up anyway.
	if vm.IP != nil && *vm.IP != "" && ipAddrRepo != nil {
		if err := ipAddrRepo.Release(r.Context(), *vm.IP); err != nil {
			slog.Warn("force-delete ip release", "id", id, "ip", *vm.IP, "error", err)
		}
	}

	audit(r.Context(), r, "vm.force_delete", "vm", id, map[string]any{
		"name":   vm.Name,
		"ip":     vm.IP,
		"reason": "gone-cleanup",
	})

	writeJSON(w, http.StatusOK, map[string]any{"status": "force_deleted", "id": id})
}

func (h *AdminVMHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	clients := h.clusters.List()
	result := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		nodes := h.scheduler.GetNodes(c.Name)
		result = append(result, map[string]any{
			"name":         c.Name,
			"display_name": c.DisplayName,
			"api_url":      c.APIURL,
			"nodes":        len(nodes),
			"status":       "active",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": result})
}

func (h *AdminVMHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodes := h.scheduler.GetNodes(clusterName)
	if nodes == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// ChaosSimulateNodeDown is the PLAN-020 Phase E drill entry point. It
// evacuates a node, waits the requested duration, then restores it —
// recording the whole cycle as a healing_events row with trigger='chaos'
// and the actor pinned. Gated at two layers:
//
//   1. Hard-reject when appEnv == "production" regardless of RBAC.
//   2. Caller must already be an admin (middleware.RequireRole) and have
//      completed a recent step-up (sensitive-route allowlist).
//
// The evacuate → wait → restore cycle runs in a background goroutine so
// the HTTP handler returns immediately with the healing_event_id for the
// admin UI to poll / render in the history page.
func (h *AdminVMHandler) ChaosSimulateNodeDown(w http.ResponseWriter, r *http.Request) {
	if appEnv == "production" {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error":   "chaos_disabled_in_production",
			"message": "Chaos drill is disabled in production environments.",
		})
		return
	}

	clusterName := chi.URLParam(r, "name")
	var req struct {
		Node            string `json:"node"             validate:"required,safename"`
		DurationSeconds int    `json:"duration_seconds" validate:"required,gte=10,lte=600"`
		Reason          string `json:"reason"           validate:"required,min=1,max=500"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}

	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	clusterID := h.clusters.IDByName(clusterName)
	if clusterID == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster id unknown"})
		return
	}

	// Pin the actor (admin) from the current request ctx — shadow session
	// preserves CtxActorID, plain admin puts identity in CtxUserID.
	actorID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if a, _ := r.Context().Value(middleware.CtxActorID).(int64); a > 0 {
		actorID = a
	}
	var actorPtr *int64
	if actorID > 0 {
		actorPtr = &actorID
	}

	var healingID int64
	if healingRepo != nil {
		if id, hErr := healingRepo.Create(r.Context(), clusterID, req.Node, "chaos", actorPtr); hErr == nil {
			healingID = id
		} else {
			slog.Warn("chaos healing event create failed", "node", req.Node, "error", hErr)
		}
	}

	audit(r.Context(), r, "node.chaos_drill.start", "node", 0, map[string]any{
		"cluster":          clusterName,
		"node":             req.Node,
		"duration_seconds": req.DurationSeconds,
		"reason":           req.Reason,
		"healing_event_id": healingID,
	})

	// Run the evac/wait/restore asynchronously. Using context.Background()
	// so the handler returning doesn't cancel the operation mid-flight. The
	// healing_events row is the durable record of what happened.
	duration := time.Duration(req.DurationSeconds) * time.Second
	//nolint:gosec // G118: 意图为之 —— handler 返回 202 后 drill 继续；runChaosCycle 内部 WithTimeout(duration+5min) 封底
	go runChaosCycle(clusterName, req.Node, duration, healingID, client)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":           "started",
		"healing_event_id": healingID,
		"duration_seconds": req.DurationSeconds,
		"node":             req.Node,
	})
}

// runChaosCycle performs the evacuate → wait → restore cycle in its own
// goroutine. Each step updates healing_events so the history UI reflects
// progress. Uses context.Background() with a generous timeout so the whole
// drill fits inside one supervised goroutine lifetime.
func runChaosCycle(clusterName, nodeName string, duration time.Duration, healingID int64, client *cluster.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), duration+5*time.Minute)
	defer cancel()

	failHealing := func(reason string) {
		if healingID > 0 && healingRepo != nil {
			_ = healingRepo.Fail(ctx, healingID, reason)
		}
		slog.Warn("chaos drill failed", "node", nodeName, "reason", reason)
	}

	// Evacuate
	evacBody := strings.NewReader(`{"action":"evacuate"}`)
	resp, err := client.APIPost(ctx, fmt.Sprintf("/1.0/cluster/members/%s/state", url.PathEscape(nodeName)), evacBody)
	if err != nil {
		failHealing("evacuate: " + err.Error())
		return
	}
	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		opID := extractOperationID(resp.Operation)
		if opID != "" {
			if werr := client.WaitForOperation(ctx, opID); werr != nil {
				failHealing("evacuate wait: " + werr.Error())
				return
			}
		}
	}
	slog.Info("chaos: node evacuated", "node", nodeName, "wait", duration)

	// Wait the drill duration
	select {
	case <-time.After(duration):
	case <-ctx.Done():
		failHealing("ctx cancelled during wait")
		return
	}

	// Restore
	restoreBody := strings.NewReader(`{"action":"restore"}`)
	resp, err = client.APIPost(ctx, fmt.Sprintf("/1.0/cluster/members/%s/state", url.PathEscape(nodeName)), restoreBody)
	if err != nil {
		failHealing("restore: " + err.Error())
		return
	}
	if resp != nil && resp.Type == "async" && resp.Operation != "" {
		opID := extractOperationID(resp.Operation)
		if opID != "" {
			if werr := client.WaitForOperation(ctx, opID); werr != nil {
				failHealing("restore wait: " + werr.Error())
				return
			}
		}
	}

	if healingID > 0 && healingRepo != nil {
		_ = healingRepo.Complete(ctx, healingID)
	}
	slog.Info("chaos: drill completed", "node", nodeName)
}

func (h *AdminVMHandler) GetHAStatus(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	cc, _ := h.clusters.ConfigByName(clusterName)

	members, err := client.GetClusterMembers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	type memberStatus struct {
		Name    string `json:"server_name"`
		URL     string `json:"url"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Roles   string `json:"roles"`
	}

	var nodes []memberStatus
	for _, raw := range members {
		var m memberStatus
		_ = json.Unmarshal(raw, &m)
		nodes = append(nodes, m)
	}
	if nodes == nil {
		nodes = []memberStatus{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"cluster":            clusterName,
		"healing_threshold":  300,
		"storage":            cc.StoragePool,
		"nodes":              nodes,
		"ha_enabled":         true,
	})
}

func (h *AdminVMHandler) EvacuateNode(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodeName := chi.URLParam(r, "node")
	if !isValidName(nodeName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid node name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	// PLAN-020 Phase D: healing_events double-write around the Incus call.
	// Row is created first so a crash between insert and completion still
	// surfaces as in_progress → ExpireStale → partial. Failed/completed
	// terminate the row deterministically below.
	var healingID int64
	if healingRepo != nil {
		clusterID := h.clusters.IDByName(clusterName)
		if clusterID > 0 {
			actorID, _ := r.Context().Value(middleware.CtxUserID).(int64)
			if a, _ := r.Context().Value(middleware.CtxActorID).(int64); a > 0 {
				actorID = a // shadow session: credit the acting admin
			}
			var actorPtr *int64
			if actorID > 0 {
				actorPtr = &actorID
			}
			if id, hErr := healingRepo.Create(r.Context(), clusterID, nodeName, "manual", actorPtr); hErr == nil {
				healingID = id
			} else {
				slog.Warn("healing event create failed", "node", nodeName, "error", hErr)
			}
		}
	}

	body, _ := json.Marshal(map[string]any{"action": "evacuate"})
	path := fmt.Sprintf("/1.0/cluster/members/%s/state", url.PathEscape(nodeName))
	resp, err := client.APIPost(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		if healingID > 0 && healingRepo != nil {
			_ = healingRepo.Fail(r.Context(), healingID, err.Error())
		}
		slog.Error("evacuate node failed", "node", nodeName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			if waitErr := client.WaitForOperation(r.Context(), op.ID); waitErr != nil {
				if healingID > 0 && healingRepo != nil {
					_ = healingRepo.Fail(r.Context(), healingID, waitErr.Error())
				}
				slog.Warn("evacuate operation wait failed", "node", nodeName, "error", waitErr)
			}
		}
	}

	if healingID > 0 && healingRepo != nil {
		_ = healingRepo.Complete(r.Context(), healingID)
	}

	audit(r.Context(), r, "node.evacuate", "node", 0, map[string]any{"cluster": clusterName, "node": nodeName, "healing_event_id": healingID})
	slog.Info("node evacuated", "cluster", clusterName, "node", nodeName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "evacuated", "node": nodeName})
}

func (h *AdminVMHandler) RestoreNode(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	nodeName := chi.URLParam(r, "node")
	if !isValidName(nodeName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid node name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	body, _ := json.Marshal(map[string]any{"action": "restore"})
	path := fmt.Sprintf("/1.0/cluster/members/%s/state", url.PathEscape(nodeName))
	resp, err := client.APIPost(r.Context(), path, bytes.NewReader(body))
	if err != nil {
		slog.Error("restore node failed", "node", nodeName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if resp.Type == "async" {
		var op struct{ ID string }
		_ = json.Unmarshal(resp.Metadata, &op)
		if op.ID != "" {
			_ = client.WaitForOperation(r.Context(), op.ID)
		}
	}

	audit(r.Context(), r, "node.restore", "node", 0, map[string]any{"cluster": clusterName, "node": nodeName})
	slog.Info("node restored", "cluster", clusterName, "node", nodeName)
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored", "node": nodeName})
}

var (
	vmListCacheMu sync.RWMutex
	vmListCache   map[string]vmListCacheEntry
)

type vmListCacheEntry struct {
	vms     []json.RawMessage
	updated time.Time
}

func (h *AdminVMHandler) ListClusterVMs(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	projects := make([]string, 0, len(cc.Projects))
	for _, proj := range cc.Projects {
		projects = append(projects, proj.Name)
	}
	if len(projects) == 0 {
		projects = []string{"default"}
	}

	var allInstances []json.RawMessage
	var fetchErr error
	for _, proj := range projects {
		instances, err := h.vmSvc.ListInstances(r.Context(), clusterName, proj)
		if err != nil {
			slog.Warn("list instances failed", "cluster", clusterName, "project", proj, "error", err)
			fetchErr = err
			continue
		}
		allInstances = append(allInstances, instances...)
	}

	// Inject DB-recorded IPs into each instance payload so the UI shows an IP
	// even before Incus reports the `state.network` block.
	allInstances = h.injectDBIPs(r.Context(), allInstances)

	// 如果成功获取到数据，更新缓存
	if len(allInstances) > 0 || fetchErr == nil {
		vmListCacheMu.Lock()
		if vmListCache == nil {
			vmListCache = make(map[string]vmListCacheEntry)
		}
		vmListCache[clusterName] = vmListCacheEntry{vms: allInstances, updated: time.Now()}
		vmListCacheMu.Unlock()
	}

	// 如果完全失败且有缓存，使用缓存（Session-2 F-7 / PLAN-051 §2-K：加 5min TTL，
	// 防止过期 entry 永久驻留 + cluster 卸载后无人清理）。
	const vmListCacheTTL = 5 * time.Minute
	if len(allInstances) == 0 && fetchErr != nil {
		vmListCacheMu.RLock()
		cached, hasCached := vmListCache[clusterName]
		vmListCacheMu.RUnlock()
		if hasCached && time.Since(cached.updated) < vmListCacheTTL {
			writeJSON(w, http.StatusOK, map[string]any{
				"vms": cached.vms, "count": len(cached.vms),
				"stale": true, "cached_at": cached.updated.Format(time.RFC3339),
				"error": "cluster unreachable, showing cached data",
			})
			return
		}
		if hasCached {
			// 过期则主动清，避免内存堆积；同时不再返给用户（要么显示 503）。
			vmListCacheMu.Lock()
			delete(vmListCache, clusterName)
			vmListCacheMu.Unlock()
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "cluster unreachable: " + fetchErr.Error(),
			"vms": []any{}, "count": 0,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"vms": allInstances, "count": len(allInstances)})
}

// injectDBIPs decodes each instance payload, looks up the DB-recorded IP by VM name,
// re-encodes with an `ip` field when a match is found, and redacts sensitive
// cloud-init / volatile fields in one pass. On any decode / lookup failure we
// silently keep the original payload — the UI already falls back to the Incus
// state.network block. Redaction is best-effort: a decode failure means the
// original (still containing cloud-init) is returned, but Incus JSON shape
// changes are essentially never silent.
func (h *AdminVMHandler) injectDBIPs(ctx context.Context, instances []json.RawMessage) []json.RawMessage {
	if len(instances) == 0 || h.vmRepo == nil {
		// 没有 DB 注入需求时仍要做敏感字段裁剪（SEC-002 / OPS-028）
		return redactInstanceList(instances)
	}
	decoded := make([]map[string]any, 0, len(instances))
	names := make([]string, 0, len(instances))
	for _, raw := range instances {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return redactInstanceList(instances)
		}
		decoded = append(decoded, m)
		if n, ok := m["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}
	ipByName, err := h.vmRepo.IPsByNames(ctx, names)
	if err != nil {
		slog.Warn("lookup VM IPs from DB failed", "error", err)
	}
	out := make([]json.RawMessage, len(decoded))
	for i, m := range decoded {
		if n, ok := m["name"].(string); ok {
			if ip, hit := ipByName[n]; hit {
				m["ip"] = ip
			}
		}
		redactInstanceMap(m)
		buf, err := json.Marshal(m)
		if err != nil {
			out[i] = instances[i]
			continue
		}
		out[i] = buf
	}
	return out
}

// redactInstanceMap 原地移除 Incus instance JSON 中的敏感字段。覆盖：
//   - config.user.cloud-init / expanded_config.user.cloud-init（legacy key）
//   - config.cloud-init.* / expanded_config.cloud-init.*（Incus 标准 key；
//     PLAN-025 之后 vm_create 与 reinstall 实际用 cloud-init.user-data /
//     cloud-init.network-config / cloud-init.vendor-data。Session-2 F-01：
//     原版只删 legacy key，新 key 里的明文 `password: <hex>` 完整外漏到
//     admin 的 /admin/vms、/admin/clusters/{name}/vms、vm 详情，从浏览器
//     缓存与 nginx access log 二次扩散）
//
// 用前缀匹配 + 显式 legacy key 兜底，避免后续新增 cloud-init.* 字段再漏。
// 这是 admin 视角双重防御 —— admin 本身有 reset-password 权限，但响应体
// 比按需走 reset-password 接口扩散面更大。
func redactInstanceMap(m map[string]any) {
	for _, key := range []string{"config", "expanded_config"} {
		cfg, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		for k := range cfg {
			if k == "user.cloud-init" || strings.HasPrefix(k, "cloud-init.") {
				delete(cfg, k)
			}
		}
	}
}

// redactInstanceJSON 是 redactInstanceMap 的 raw-JSON 版本，用于单实例
// pass-through 路径（GetClusterVMDetail / 节点详情）。Decode 失败时返回
// 原样（fail-open），因为 admin 已 gate 且 Incus JSON 形状稳定。
func redactInstanceJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		slog.Warn("redact instance: decode failed; passing through raw (cloud-init may leak)", "error", err)
		return raw
	}
	redactInstanceMap(m)
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// redactInstanceList 批量版本，用于 ListAllVMs / 节点详情这些不走 injectDBIPs
// 的路径。
func redactInstanceList(instances []json.RawMessage) []json.RawMessage {
	if len(instances) == 0 {
		return instances
	}
	out := make([]json.RawMessage, len(instances))
	for i, raw := range instances {
		out[i] = redactInstanceJSON(raw)
	}
	return out
}

func (h *AdminVMHandler) CreateVM(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	cc, ok := h.clusters.ConfigByName(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	// 默认值通过零值 + 后置填充实现，避免 required/min 与"缺省即默认"的冲突。
	// OSImage / Project 允许为空串（后置填默认）；CPU/Memory/Disk 上限仅做合理防御，
	// 真正的额度限制在 quota 层。
	var req struct {
		CPU          int      `json:"cpu"            validate:"gte=0,lte=128"`
		MemoryMB     int      `json:"memory_mb"      validate:"gte=0,lte=1048576"`
		DiskGB       int      `json:"disk_gb"        validate:"gte=0,lte=10240"`
		OSImage      string   `json:"os_image"       validate:"omitempty,max=200"`
		Project      string   `json:"project"        validate:"omitempty,safename"`
		SSHKeys      []string `json:"ssh_keys"       validate:"omitempty,dive,max=8192"`
		TargetUserID int64    `json:"target_user_id" validate:"gte=0"`
		// OPS-024 B2：count 为空 / 1 表示单 VM；2..16 一次性入队多个 jobs。
		// 上限 16 防止 admin 误操作 IP 池一次性耗尽。
		Count int `json:"count" validate:"omitempty,gte=1,lte=16"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Count == 0 {
		req.Count = 1
	}

	if req.CPU == 0 { req.CPU = 2 }
	if req.MemoryMB == 0 { req.MemoryMB = 2048 }
	if req.DiskGB == 0 { req.DiskGB = 50 }
	if req.OSImage == "" { req.OSImage = "images:ubuntu/24.04/cloud" }
	if req.Project == "" { req.Project = "default" }

	pool := cc.StoragePool
	if pool == "" { pool = "ceph-pool" }
	network := cc.Network
	if network == "" { network = "br-pub" }

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if req.TargetUserID > 0 {
		userID = req.TargetUserID
	}

	sshKeys, _ := h.sshKeys.GetByUser(r.Context(), userID)
	if len(req.SSHKeys) == 0 && len(sshKeys) > 0 {
		req.SSHKeys = sshKeys
	}

	// OPS-051 / PLAN-052 Q3=A：删除所有同步兜底路径，jobs runtime 必须注入。
	if h.jobs == nil || h.jobRepo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "provisioning runtime unavailable"})
		return
	}

	// 异步路径：循环 count 次。任一步失败 → 已成功的保留（每个 job 独立）+ 当前步报 error。
	clusterID := h.clusters.IDByName(clusterName)
	type batchItem struct {
		JobID  int64  `json:"job_id"`
		VMID   int64  `json:"vm_id"`
		VMName string `json:"vm_name"`
		IP     string `json:"ip"`
	}
	results := make([]batchItem, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		ip, gateway, cidr, ipErr := allocateIP(r.Context(), cc, 0)
		if ipErr != nil {
			slog.Error("allocate IP failed in batch", "cluster", clusterName, "i", i, "error", ipErr)
			if len(results) > 0 {
				writeJSON(w, http.StatusOK, map[string]any{
					"status": "partial", "items": results, "failed_at": i, "error": ipErr.Error(),
				})
				return
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no available IPs: " + ipErr.Error()})
			return
		}
		vmName := service.GenerateVMName()
		ipRef := ip
		vm := &model.VM{
			Name: vmName, ClusterID: clusterID, UserID: userID, Status: model.VMStatusCreating,
			CPU: req.CPU, MemoryMB: req.MemoryMB, DiskGB: req.DiskGB, OSImage: req.OSImage, IP: &ipRef,
		}
		if err := h.vmRepo.Create(r.Context(), vm); err != nil {
			_ = ipAddrRepo.Release(r.Context(), ip)
			slog.Error("vm row insert failed (admin batch)", "name", vmName, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status": "partial", "items": results, "failed_at": i, "error": "vm row: " + err.Error(),
			})
			return
		}
		attachIPToVM(r.Context(), ip, vm.ID)

		job, err := h.jobRepo.Create(r.Context(), model.JobKindVMCreate, userID, clusterID, nil, &vm.ID, vmName)
		if err != nil {
			_ = ipAddrRepo.Release(r.Context(), ip)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status": "partial", "items": results, "failed_at": i, "error": "create job: " + err.Error(),
			})
			return
		}
		if err := h.jobs.Enqueue(r.Context(), job.ID, jobs.Params{
			Project: req.Project, CPU: req.CPU, MemoryMB: req.MemoryMB, DiskGB: req.DiskGB,
			OSImage: req.OSImage, SSHKeys: req.SSHKeys,
			IP: ip, Gateway: gateway, SubnetCIDR: cidr,
			StoragePool: pool, Network: network,
		}); err != nil {
			_ = h.jobRepo.Finish(r.Context(), job.ID, model.JobStatusFailed, "enqueue: "+err.Error())
			_ = ipAddrRepo.Release(r.Context(), ip)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status": "partial", "items": results, "failed_at": i, "error": "enqueue: " + err.Error(),
			})
			return
		}
		results = append(results, batchItem{JobID: job.ID, VMID: vm.ID, VMName: vmName, IP: ip})
		audit(r.Context(), r, "vm.create", "vm", vm.ID, map[string]any{
			"name": vmName, "ip": ip, "admin": true, "job_id": job.ID, "batch": req.Count > 1, "batch_idx": i,
		})
	}

	// 单 VM 兼容：保留旧响应字段；多 VM 返 items 数组。
	if req.Count == 1 && len(results) == 1 {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":  "provisioning",
			"job_id":  results[0].JobID,
			"vm_id":   results[0].VMID,
			"vm_name": results[0].VMName,
			"ip":      results[0].IP,
		})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "provisioning",
		"count":  len(results),
		"items":  results,
	})
}

func (h *AdminVMHandler) ListAllVMs(w http.ResponseWriter, r *http.Request) {
	var allInstances []json.RawMessage
	for _, client := range h.clusters.List() {
		cc, ok := h.clusters.ConfigByName(client.Name)
		if !ok {
			continue
		}
		for _, proj := range cc.Projects {
			instances, err := h.vmSvc.ListInstances(r.Context(), client.Name, proj.Name)
			if err != nil {
				continue
			}
			allInstances = append(allInstances, instances...)
		}
		if len(cc.Projects) == 0 {
			instances, _ := h.vmSvc.ListInstances(r.Context(), client.Name, "default")
			allInstances = append(allInstances, instances...)
		}
	}
	if allInstances == nil {
		allInstances = []json.RawMessage{}
	}
	allInstances = redactInstanceList(allInstances)
	writeJSON(w, http.StatusOK, map[string]any{"vms": allInstances, "count": len(allInstances)})
}

func (h *AdminVMHandler) ChangeVMState(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Action  string `json:"action"  validate:"required,oneof=start stop restart freeze unfreeze"`
		Force   bool   `json:"force"`
		Cluster string `json:"cluster" validate:"omitempty,safename"`
		Project string `json:"project" validate:"omitempty,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Cluster == "" {
		clients := h.clusters.List()
		if len(clients) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
			return
		}
		req.Cluster = clients[0].Name
	}
	if req.Project == "" { req.Project = "default" }

	err := h.vmSvc.ChangeState(r.Context(), req.Cluster, req.Project, vmName, req.Action, req.Force)
	if err != nil {
		slog.Error("vm state change failed", "vm", vmName, "action", req.Action, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Sync DB status so portal list/detail reflects the action immediately.
	if dbVM, _ := h.vmRepo.GetByName(r.Context(), vmName); dbVM != nil {
		newStatus := ""
		switch req.Action {
		case "start", "restart":
			newStatus = model.VMStatusRunning
		case "stop":
			newStatus = model.VMStatusStopped
		}
		if newStatus != "" {
			if uerr := h.vmRepo.UpdateStatus(r.Context(), dbVM.ID, newStatus); uerr != nil {
				slog.Warn("vm status db sync failed", "vm", vmName, "action", req.Action, "error", uerr)
			}
		}
	}
	slog.Info("vm state changed", "vm", vmName, "action", req.Action)
	audit(r.Context(), r, "vm."+req.Action, "vm", h.vmIDByName(r.Context(), vmName), map[string]any{"name": vmName})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "action": req.Action})
}

// vmIDByName returns the DB id for a VM name, or 0 if the VM is not in our DB
// (e.g. legacy VMs created outside the portal). Used to populate audit.target_id
// so the audit log can link back to the VM instead of always recording 0.
func (h *AdminVMHandler) vmIDByName(ctx context.Context, name string) int64 {
	if h.vmRepo == nil {
		return 0
	}
	v, err := h.vmRepo.GetByName(ctx, name)
	if err != nil || v == nil {
		return 0
	}
	return v.ID
}

// DeleteVM 现在执行"软删除（回收站）"——PLAN-034。
//
// 流程：先 stop Incus 实例（best-effort），再 DB 标 trashed_at = NOW()。Incus
// 实例本身保留 30s（model.VMTrashWindowSeconds），window 过后由 worker.RunVMTrashPurger
// 走原 hard-delete 路径。期间用户可调 POST /vms/{name}/restore 撤销。
//
// 强制立即删（不等窗口）走 POST /vms/{name}/purge，保留运维逃生口。
func (h *AdminVMHandler) DeleteVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	clusterParam := r.URL.Query().Get("cluster")
	projectParam := r.URL.Query().Get("project")
	if clusterParam == "" {
		clients := h.clusters.List()
		if len(clients) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
			return
		}
		clusterParam = clients[0].Name
	}
	if projectParam == "" {
		projectParam = "default"
	}

	dbVM, _ := h.vmRepo.GetByName(r.Context(), vmName)
	if dbVM == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not found"})
		return
	}

	// best-effort stop Incus；purger 兜底走 force=true，所以这里失败不阻塞 trash
	if err := h.vmSvc.Trash(r.Context(), clusterParam, projectParam, vmName); err != nil {
		slog.Warn("trash vm: stop failed", "vm", vmName, "error", err)
	}

	ok, err := h.vmRepo.MarkTrashed(r.Context(), dbVM.ID)
	if err != nil {
		slog.Error("mark trashed failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "vm already trashed or deleted"})
		return
	}
	cancelSubscriptionOnTrash(r.Context(), r, h.subs, dbVM.ID)
	slog.Info("vm trashed", "vm", vmName, "window_s", model.VMTrashWindowSeconds)
	audit(r.Context(), r, "vm.trash", "vm", dbVM.ID, map[string]any{"name": vmName, "window_s": model.VMTrashWindowSeconds})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "trashed",
		"vm_id":      dbVM.ID,
		"name":       vmName,
		"trashed_at": time.Now().UTC(),
		"window_s":   model.VMTrashWindowSeconds,
	})
}

// RestoreVM 把 trashed VM 拉回主列表（PLAN-034）。窗口未过则成功；已被 purger
// 抢先 hard-delete 的 → 404。restore 后 VM 在 Incus 仍是 stopped（DeleteVM 阶段
// 主动 stop 过），故不自动启动；前端 toast 引导用户手动 start。
func (h *AdminVMHandler) RestoreVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	dbVM, _ := h.vmRepo.GetByNameIncludingTrashed(r.Context(), vmName)
	if dbVM == nil || dbVM.TrashedAt == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "vm not in trash"})
		return
	}
	ok, err := h.vmRepo.UnmarkTrashed(r.Context(), dbVM.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "vm not in trash (race)"})
		return
	}
	reactivateSubscriptionOnRestore(r.Context(), r, h.subs, dbVM.ID)
	prev := ""
	if dbVM.TrashedPrevStatus != nil {
		prev = *dbVM.TrashedPrevStatus
	}
	slog.Info("vm restored from trash", "vm", vmName, "prev_status", prev)
	audit(r.Context(), r, "vm.restore", "vm", dbVM.ID, map[string]any{"name": vmName, "prev_status": prev})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "restored",
		"vm_id":       dbVM.ID,
		"name":        vmName,
		"prev_status": prev,
	})
}

// PurgeVM 强制立即 hard-delete trashed VM（不等 30s 窗口）。运维 escape hatch；
// 不在 trash 中的 VM 不允许 purge——admin 走标准 DELETE 才行（强制 user-flow）。
func (h *AdminVMHandler) PurgeVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	clusterParam := r.URL.Query().Get("cluster")
	projectParam := r.URL.Query().Get("project")
	if clusterParam == "" {
		clients := h.clusters.List()
		if len(clients) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "no clusters"})
			return
		}
		clusterParam = clients[0].Name
	}
	if projectParam == "" {
		projectParam = "default"
	}

	dbVM, _ := h.vmRepo.GetByNameIncludingTrashed(r.Context(), vmName)
	if dbVM == nil || dbVM.TrashedAt == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "vm not in trash; call DELETE first"})
		return
	}
	if err := h.vmSvc.PurgeTrashed(r.Context(), clusterParam, projectParam, vmName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := h.vmRepo.Delete(r.Context(), dbVM.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	slog.Info("vm purged from trash", "vm", vmName)
	audit(r.Context(), r, "vm.purge", "vm", dbVM.ID, map[string]any{"name": vmName})
	writeJSON(w, http.StatusOK, map[string]any{"status": "purged", "name": vmName})
}

// ListTrashedVMs 返回所有处于 trash 窗口内的 VM。admin 视角：包含全部用户的
// trashed VM；窗口尚未过的行 worker 还未 purge。前端可显示倒计时 + restore 按钮。
func (h *AdminVMHandler) ListTrashedVMs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.vmRepo.ListTrashedBefore(r.Context(), time.Now().Add(24*time.Hour))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, vm := range rows {
		out = append(out, map[string]any{
			"id":                  vm.ID,
			"name":                vm.Name,
			"cluster_id":          vm.ClusterID,
			"user_id":             vm.UserID,
			"ip":                  vm.IP,
			"status":              vm.Status,
			"trashed_at":          vm.TrashedAt,
			"trashed_prev_status": vm.TrashedPrevStatus,
			"window_s":            model.VMTrashWindowSeconds,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": out, "count": len(out)})
}

func (h *AdminVMHandler) ReinstallVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Cluster      string `json:"cluster"       validate:"required,safename"`
		Project      string `json:"project"       validate:"omitempty,safename"`
		TemplateSlug string `json:"template_slug" validate:"omitempty,safename,max=64"`
		OSImage      string `json:"os_image"      validate:"omitempty,max=200"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Project == "" {
		req.Project = "default"
	}
	if req.TemplateSlug == "" && req.OSImage == "" {
		req.TemplateSlug = "ubuntu-24-04"
	}

	resolved, err := resolveReinstallTemplate(r.Context(), req.TemplateSlug, req.OSImage)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	resolved.ClusterName = req.Cluster
	resolved.Project = req.Project
	resolved.VMName = vmName

	// OPS-021：admin reinstall 与 portal reinstall 一致走 jobs runtime + SSE。
	// 兜底同步路径在 jobs runtime 缺失时保留（DB-only 测试 / 老配置）。
	if h.jobs != nil && h.jobRepo != nil {
		client, ok := h.clusters.Get(req.Cluster)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
			return
		}
		if perr := service.ProbeImageServer(r.Context(), resolved.ServerURL); perr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": fmt.Sprintf("镜像服务器 %s 不可达，已取消重装以保护原 VM 数据: %v", resolved.ServerURL, perr),
			})
			return
		}
		if perr := service.PrePullImage(r.Context(), client, resolved.Project, resolved.ServerURL, resolved.Protocol, resolved.ImageSource); perr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": fmt.Sprintf("镜像 %s 预拉取失败，已取消重装以保护原 VM 数据: %v", resolved.ImageSource, perr),
			})
			return
		}

		userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
		clusterID := h.clusters.IDByName(req.Cluster)
		vmID := h.vmIDByName(r.Context(), vmName)
		var vmIDPtr *int64
		if vmID > 0 {
			vmIDPtr = &vmID
		}

		job, jerr := h.jobRepo.Create(r.Context(), model.JobKindVMReinstall, userID, clusterID, nil, vmIDPtr, vmName)
		if jerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create job: " + jerr.Error()})
			return
		}
		if eerr := h.jobs.Enqueue(r.Context(), job.ID, jobs.Params{
			Project:     resolved.Project,
			ImageSource: resolved.ImageSource,
			ServerURL:   resolved.ServerURL,
			Protocol:    resolved.Protocol,
			DefaultUser: resolved.DefaultUser,
		}); eerr != nil {
			_ = h.jobRepo.Finish(r.Context(), job.ID, model.JobStatusFailed, "enqueue: "+eerr.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "enqueue: " + eerr.Error()})
			return
		}

		auditDetails := map[string]any{"name": vmName, "source": resolved.ImageSource, "user": resolved.DefaultUser, "job_id": job.ID, "admin": true}
		if req.TemplateSlug != "" {
			auditDetails["template_slug"] = req.TemplateSlug
		}
		audit(r.Context(), r, "vm.reinstall", "vm", vmID, auditDetails)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status": "provisioning",
			"job_id": job.ID,
			"vm_id":  vmID,
		})
		return
	}

	// OPS-051 / PLAN-052 Q3=A：删除同步兜底，jobs runtime 必须注入。
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "reinstall runtime unavailable"})
}

// ResetPasswordAdmin 给 VM 重置密码（admin 路径）。PLAN-021 Phase C 起支持
// online（guest-agent chpasswd）/ offline（cloud-init 重启重跑）/ auto（试
// online 失败再 offline）三种模式，默认 auto。
func (h *AdminVMHandler) ResetPasswordAdmin(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	var req struct {
		Cluster  string `json:"cluster"  validate:"required,safename"`
		Project  string `json:"project"  validate:"omitempty,safename"`
		Username string `json:"username" validate:"omitempty,max=64"`
		Mode     string `json:"mode"     validate:"omitempty,oneof=auto online offline"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}
	if req.Username == "" {
		// OPS-051 / PLAN-052 Q7：默认登录用户改 root
		req.Username = "root"
	}

	result, err := h.vmSvc.ResetPassword(r.Context(), req.Cluster, req.Project, vmName, req.Username, service.ResetPasswordMode(req.Mode))
	if err != nil {
		slog.Error("admin reset password failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "password reset failed: " + err.Error()})
		return
	}

	audit(r.Context(), r, "vm.reset_password", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{
		"name":     vmName,
		"username": req.Username,
		"channel":  string(result.Channel),
		"fallback": result.Fallback,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "password_reset",
		"password": result.Password,
		"username": result.Username,
		"channel":  result.Channel,
		"fallback": result.Fallback,
	})
}

func (h *AdminVMHandler) MigrateVM(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Cluster    string `json:"cluster"     validate:"required,safename"`
		Project    string `json:"project"     validate:"omitempty,safename"`
		TargetNode string `json:"target_node" validate:"required,safename"`
		// PLAN-039 / OPS-043: auto / live / cold；空 → auto
		Mode string `json:"mode" validate:"omitempty,oneof=auto live cold"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}

	if _, ok := h.clusters.Get(req.Cluster); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	mode := service.MigrateMode(req.Mode)
	if mode == "" {
		mode = service.MigrateAuto
	}
	res, err := h.vmSvc.Migrate(r.Context(), req.Cluster, req.Project, vmName, req.TargetNode, mode)
	if err != nil {
		slog.Error("migrate VM failed", "vm", vmName, "target", req.TargetNode, "mode", mode, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "migration failed: " + err.Error()})
		return
	}

	audit(r.Context(), r, "vm.migrate", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{
		"name": vmName, "target": req.TargetNode, "cluster": req.Cluster,
		"was_running": res.WasRunning, "requested_mode": string(mode), "actual_mode": string(res.Mode),
	})
	slog.Info("vm migrated", "vm", vmName, "target", req.TargetNode,
		"was_running", res.WasRunning, "mode", res.Mode)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "migrated",
		"target":      req.TargetNode,
		"was_running": res.WasRunning,
		"mode":        res.Mode,
	})
}

// EnableStateful 把 VM 的 migration.stateful 设为 true（live migration 前置）。
// 必须重启才生效，因此 running 的 VM 会停 → set → 启。
//
// 路由：POST /admin/vms/{name}/enable-stateful（step-up gated）
func (h *AdminVMHandler) EnableStateful(w http.ResponseWriter, r *http.Request) {
	vmName := chi.URLParam(r, "name")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	var req struct {
		Cluster string `json:"cluster" validate:"required,safename"`
		Project string `json:"project" validate:"omitempty,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}
	if err := h.vmSvc.EnableStateful(r.Context(), req.Cluster, req.Project, vmName); err != nil {
		slog.Error("enable-stateful failed", "vm", vmName, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	audit(r.Context(), r, "vm.enable_stateful", "vm", h.vmIDByName(r.Context(), vmName), map[string]any{
		"name": vmName, "cluster": req.Cluster, "project": req.Project,
	})
	slog.Info("vm enable-stateful done", "vm", vmName, "cluster", req.Cluster)
	writeJSON(w, http.StatusOK, map[string]any{"status": "stateful_enabled", "name": vmName})
}

// EnableStatefulBatch 批量启用 stateful。串行执行避免节点同时停多台 VM 影响业务。
//
// 路由：POST /admin/vms:enable-stateful-batch（step-up gated）
func (h *AdminVMHandler) EnableStatefulBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cluster string   `json:"cluster" validate:"required,safename"`
		Project string   `json:"project" validate:"omitempty,safename"`
		Names   []string `json:"names"   validate:"required,min=1,max=50,dive,safename"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if req.Project == "" {
		req.Project = "customers"
	}
	type result struct {
		Name  string `json:"name"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(req.Names))
	okCount := 0
	for _, name := range req.Names {
		err := h.vmSvc.EnableStateful(r.Context(), req.Cluster, req.Project, name)
		if err != nil {
			slog.Warn("batch enable-stateful failed", "vm", name, "error", err)
			results = append(results, result{Name: name, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, result{Name: name, OK: true})
		okCount++
	}
	audit(r.Context(), r, "vm.enable_stateful_batch", "vm", 0, map[string]any{
		"cluster": req.Cluster, "project": req.Project, "names": req.Names,
		"ok_count": okCount, "fail_count": len(req.Names) - okCount,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"total":     len(req.Names),
		"succeeded": okCount,
		"results":   results,
	})
}

// MigrateVMBatch 批量冷迁移 N 台 VM 到目标节点。
// PLAN-037 / OPS-040：异步 job 化执行（复用 jobs runtime + SSE）。
// 与单台 MigrateVM 共享 vmSvc.Migrate 逻辑，单台失败不阻塞批次。
//
// Request:
//
//	{
//	  "cluster": "ph-c0",
//	  "items": [
//	    {"vm_name":"vm-aaa","project":"customers","source_node":"node-1","target_node":"node-3"},
//	    ...
//	  ],
//	  "concurrency_per_source": 2,    // 可选；默认 2
//	  "global_concurrency":     4     // 可选；默认 4
//	}
//
// Response: {"job_id": <id>}（前端用 SSE /admin/jobs/{id}/stream 跟进度）
func (h *AdminVMHandler) MigrateVMBatch(w http.ResponseWriter, r *http.Request) {
	if h.jobs == nil || h.jobRepo == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "jobs runtime unavailable"})
		return
	}
	var req struct {
		Cluster string `json:"cluster"  validate:"required,safename"`
		Items   []struct {
			VMName     string `json:"vm_name"     validate:"required,safename"`
			Project    string `json:"project"     validate:"omitempty,safename"`
			SourceNode string `json:"source_node" validate:"omitempty,safename"`
			TargetNode string `json:"target_node" validate:"required,safename"`
			// PLAN-039 / OPS-043 mode：每条独立可指定，默认整批 mode 兜底
			Mode string `json:"mode" validate:"omitempty,oneof=auto live cold"`
		} `json:"items"   validate:"required,min=1,max=200"`
		ConcurrencyPerSource int    `json:"concurrency_per_source" validate:"gte=0,lte=8"`
		GlobalConcurrency    int    `json:"global_concurrency"     validate:"gte=0,lte=16"`
		Mode                 string `json:"mode"                   validate:"omitempty,oneof=auto live cold"` // 整批默认
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if _, ok := h.clusters.Get(req.Cluster); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}

	defaultMode := req.Mode
	if defaultMode == "" {
		defaultMode = "auto"
	}
	items := make([]jobs.MigrateBatchItem, 0, len(req.Items))
	names := make([]string, 0, len(req.Items))
	seen := make(map[string]struct{}, len(req.Items))
	for _, it := range req.Items {
		project := it.Project
		if project == "" {
			project = "customers"
		}
		key := project + "/" + it.VMName
		if _, dup := seen[key]; dup {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "duplicate item: " + key})
			return
		}
		seen[key] = struct{}{}
		mode := it.Mode
		if mode == "" {
			mode = defaultMode
		}
		items = append(items, jobs.MigrateBatchItem{
			VMName:     it.VMName,
			Project:    project,
			SourceNode: it.SourceNode,
			TargetNode: it.TargetNode,
			Mode:       mode,
		})
		names = append(names, it.VMName)
	}

	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	clusterID := h.clusters.IDByName(req.Cluster)
	targetName := fmt.Sprintf("batch-%d-vms", len(items))
	job, err := h.jobRepo.Create(r.Context(), model.JobKindClusterVMMigrateBatch, userID, clusterID, nil, nil, targetName)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create job: " + err.Error()})
		return
	}

	if err := h.jobs.Enqueue(r.Context(), job.ID, jobs.Params{
		ClusterName:       req.Cluster,
		BatchItems:        items,
		ConcurrencyPerSrc: req.ConcurrencyPerSource,
		GlobalConcurrency: req.GlobalConcurrency,
	}); err != nil {
		_ = h.jobRepo.Finish(r.Context(), job.ID, model.JobStatusFailed, "enqueue: "+err.Error())
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "enqueue job: " + err.Error()})
		return
	}

	audit(r.Context(), r, "vm.migrate-batch", "job", job.ID, map[string]any{
		"cluster":                req.Cluster,
		"item_count":             len(items),
		"names":                  names,
		"concurrency_per_source": req.ConcurrencyPerSource,
		"global_concurrency":     req.GlobalConcurrency,
	})
	slog.Info("vm migrate-batch enqueued", "job_id", job.ID, "cluster", req.Cluster, "count", len(items))
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID})
}

// ListProjects 返回集群实际存在的 project 列表（来自 Incus `/1.0/projects?recursion=1`）。
// 批次 8 B-1：前端 ProjectPicker 依赖此端点，不再硬编码 `customers/default`。
func (h *AdminVMHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	resp, err := client.APIGet(r.Context(), "/1.0/projects?recursion=1")
	if err != nil {
		slog.Error("list projects failed", "cluster", clusterName, "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "list projects failed: " + err.Error()})
		return
	}
	var raw []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Config      map[string]string `json:"config"`
	}
	if err := json.Unmarshal(resp.Metadata, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "parse projects: " + err.Error()})
		return
	}
	projects := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		projects = append(projects, map[string]any{
			"name":        p.Name,
			"description": p.Description,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// GetClusterVMDetail 返回单台 VM 的合并详情：incus instance + state + snapshots + DB 行。
// 批次 8 B-2：前端 AdminVMDetail 从 O(N) 列表扫描改为单端点。
// 查询参数 `?project=<name>` 可选；若省略则在该集群的已配置 projects 中逐一查找。
func (h *AdminVMHandler) GetClusterVMDetail(w http.ResponseWriter, r *http.Request) {
	clusterName := chi.URLParam(r, "name")
	vmName := chi.URLParam(r, "vmName")
	if !isValidName(vmName) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid vm name"})
		return
	}
	client, ok := h.clusters.Get(clusterName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "cluster not found"})
		return
	}
	cc, _ := h.clusters.ConfigByName(clusterName)

	candidates := make([]string, 0, len(cc.Projects)+2)
	if p := r.URL.Query().Get("project"); p != "" {
		candidates = append(candidates, p)
	} else {
		for _, pc := range cc.Projects {
			candidates = append(candidates, pc.Name)
		}
		if len(candidates) == 0 {
			candidates = []string{"customers", "default"}
		}
	}

	var (
		instance json.RawMessage
		state    json.RawMessage
		project  string
		lastErr  error
	)
	for _, p := range candidates {
		raw, err := client.GetInstance(r.Context(), p, vmName)
		if err != nil {
			lastErr = err
			continue
		}
		instance = raw
		project = p
		if s, serr := client.GetInstanceState(r.Context(), p, vmName); serr == nil {
			state = s
		}
		break
	}
	if instance == nil {
		status := http.StatusNotFound
		msg := "vm not found"
		if lastErr != nil {
			msg = lastErr.Error()
		}
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}

	snapshotsPath := fmt.Sprintf("/1.0/instances/%s/snapshots?project=%s&recursion=1", url.PathEscape(vmName), url.QueryEscape(project))
	snapResp, snapErr := client.APIGet(r.Context(), snapshotsPath)
	var snapshots json.RawMessage
	if snapErr == nil {
		snapshots = snapResp.Metadata
	}

	var dbRow *model.VM
	if h.vmRepo != nil {
		dbRow, _ = h.vmRepo.GetByName(r.Context(), vmName)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"vm":        redactInstanceJSON(instance),
		"state":     state,
		"snapshots": snapshots,
		"project":   project,
		"db":        dbRow,
	})
}

func extractCIDR(cidr string) string {
	parts := strings.Split(cidr, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return "27"
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			if isNilSlice(val) {
				m[k] = []any{}
			}
		}
	}
	json.NewEncoder(w).Encode(v)
}

func isNilSlice(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.IsNil()
}
