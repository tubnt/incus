package portal

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/incuscloud/incus-admin/internal/handler/batchutil"
	"github.com/incuscloud/incus-admin/internal/model"
)

// PLAN-023 Phase A: VM 批量操作端点 POST /api/admin/vms:batch
//
// 请求：
//
//	{
//	  "names":   ["vm-aaa", "vm-bbb"],   // 必填
//	  "cluster": "ph-c0",                 // 必填
//	  "project": "default",               // 可选，默认 default
//	  "action":  "delete"                 // delete | start | stop | restart
//	}
//
// 响应（batchutil.Response[string]）：
//
//	{
//	  "total":     5,
//	  "succeeded": ["vm-aaa", "vm-bbb"],
//	  "failed":    [{"key": "vm-ccc", "error": "..."}, ...]
//	}
//
// step-up：路由整体走 step-up（middleware/stepup.go 中已注册 vms:batch）。
//
// 审计：父事件 vm.batch_<action>（含全部 names + 计数），子事件 vm.<action> 每条单子操作发出。

const (
	batchActionDelete  = "delete"
	batchActionStart   = "start"
	batchActionStop    = "stop"
	batchActionRestart = "restart"
)

var vmBatchAllowedActions = []string{
	batchActionDelete, batchActionStart, batchActionStop, batchActionRestart,
}

type batchVMRequest struct {
	Names   []string `json:"names"`
	Cluster string   `json:"cluster"`
	Project string   `json:"project,omitempty"`
	Action  string   `json:"action"`
}

// BatchVMs 批量执行 VM 动作。
func (h *AdminVMHandler) BatchVMs(w http.ResponseWriter, r *http.Request) {
	var req batchVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
		return
	}

	if verr := batchutil.Validate(len(req.Names), req.Action, vmBatchAllowedActions); verr != nil {
		writeJSON(w, verr.Status, verr.Body)
		return
	}
	if req.Cluster == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cluster is required"})
		return
	}
	for _, n := range req.Names {
		if !isValidName(n) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "invalid vm name",
				"name":  n,
			})
			return
		}
	}

	project := req.Project
	if project == "" {
		project = "default"
	}

	resp := batchutil.New[string]()
	resp.Total = len(req.Names)

	// 串行执行：避免 Incus 集群被并发请求打爆；50 个 name 最多耗时几秒。
	for _, name := range req.Names {
		if err := h.runVMBatchOp(r, req.Cluster, project, name, req.Action); err != nil {
			resp.Fail(name, err)
		} else {
			resp.OK(name)
		}
	}

	audit(r.Context(), r, "vm.batch_"+req.Action, "vm", 0, map[string]any{
		"requested_names": req.Names,
		"cluster":         req.Cluster,
		"succeeded_count": len(resp.Succeeded),
		"failed_count":    len(resp.Failed),
	})

	writeJSON(w, resp.Status(), resp)
}

// runVMBatchOp 单条 VM 动作；调 service → 子审计。
func (h *AdminVMHandler) runVMBatchOp(r *http.Request, cluster, project, name, action string) error {
	ctx := r.Context()

	switch action {
	case batchActionDelete:
		if err := h.vmSvc.Delete(ctx, cluster, project, name); err != nil {
			slog.Error("batch delete failed", "vm", name, "cluster", cluster, "error", err)
			return err
		}
		var deletedID int64
		if dbVM, _ := h.vmRepo.GetByName(ctx, name); dbVM != nil {
			deletedID = dbVM.ID
			_ = h.vmRepo.Delete(ctx, dbVM.ID)
		}
		audit(ctx, r, "vm.delete", "vm", deletedID, map[string]any{
			"name":   name,
			"source": "batch",
		})
		return nil

	case batchActionStart, batchActionStop, batchActionRestart:
		if err := h.vmSvc.ChangeState(ctx, cluster, project, name, action, false); err != nil {
			slog.Error("batch state change failed", "vm", name, "cluster", cluster, "action", action, "error", err)
			return err
		}
		newStatus := ""
		switch action {
		case batchActionStart, batchActionRestart:
			newStatus = model.VMStatusRunning
		case batchActionStop:
			newStatus = model.VMStatusStopped
		}
		var vmID int64
		if dbVM, _ := h.vmRepo.GetByName(ctx, name); dbVM != nil {
			vmID = dbVM.ID
			if newStatus != "" {
				_ = h.vmRepo.UpdateStatus(ctx, dbVM.ID, newStatus)
			}
		}
		audit(ctx, r, "vm."+action, "vm", vmID, map[string]any{
			"name":   name,
			"source": "batch",
		})
		return nil
	}

	return errInvalidBatchAction
}

var errInvalidBatchAction = batchError("invalid batch action")

type batchError string

func (e batchError) Error() string { return string(e) }
