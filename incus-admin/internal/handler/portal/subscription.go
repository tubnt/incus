package portal

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// SubscriptionHandler PLAN-054 L3-I：vm_subscriptions 列表 + admin 手动恢复。
//
// 端点：
//
//	GET  /portal/subscriptions             用户视角：列自己全部订阅
//	GET  /admin/subscriptions              admin 视角：列所有用户订阅（可按 user/status 过滤）
//	POST /admin/subscriptions/{id}/reactivate
//	                                       admin 手动把 suspended/cancelled 恢复为 active
//	                                       paid_until 重置为 NOW + 周期（与 restore 同语义）
type SubscriptionHandler struct {
	subs *repository.SubscriptionRepo
}

func NewSubscriptionHandler(subs *repository.SubscriptionRepo) *SubscriptionHandler {
	return &SubscriptionHandler{subs: subs}
}

func (h *SubscriptionHandler) PortalRoutes(r chi.Router) {
	r.Get("/subscriptions", h.ListMine)
}

func (h *SubscriptionHandler) AdminRoutes(r chi.Router) {
	r.Get("/subscriptions", h.ListAll)
	r.Post("/subscriptions/{id}/reactivate", h.Reactivate)
}

// ListMine GET /portal/subscriptions —— 当前登录用户的全部订阅，最新优先。
func (h *SubscriptionHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(middleware.CtxUserID).(int64)
	if userID <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	status := r.URL.Query().Get("status")
	list, err := h.subs.ListByUser(r.Context(), userID, status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list subscriptions"})
		return
	}
	if list == nil {
		list = []model.VMSubscription{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": list})
}

// ListAll GET /admin/subscriptions —— 列所有用户订阅。
//
// 可选 query：user=<id>  → 仅该用户；status=<active|suspended|cancelled> → 仅该状态。
// 没有 user 时为了简单走"全表 + ORDER BY id DESC"扫一遍——订阅数量级跟 VM 一致，
// 早期不需要分页；后期数据涨上去再加 ListPaged。
func (h *SubscriptionHandler) ListAll(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	var (
		list []model.VMSubscription
		err  error
	)
	if userStr := q.Get("user"); userStr != "" {
		uid, parseErr := strconv.ParseInt(userStr, 10, 64)
		if parseErr != nil || uid <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid user id"})
			return
		}
		list, err = h.subs.ListByUser(r.Context(), uid, status)
	} else {
		list, err = h.subs.ListAll(r.Context(), status)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list subscriptions"})
		return
	}
	if list == nil {
		list = []model.VMSubscription{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": list})
}

// Reactivate POST /admin/subscriptions/{id}/reactivate —— admin 手动把
// suspended / cancelled 的订阅恢复为 active，paid_until 重置为 NOW + 周期。
// 与 worker 自动恢复（topup 触发）语义对齐，纯运维兜底入口。
func (h *SubscriptionHandler) Reactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid subscription id"})
		return
	}
	sub, err := h.subs.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "lookup failed"})
		return
	}
	if sub == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "subscription not found"})
		return
	}
	if sub.Status == model.SubscriptionStatusActive {
		// 幂等：已经 active 就直接返回当前态。
		writeJSON(w, http.StatusOK, map[string]any{"subscription": sub, "noop": true})
		return
	}
	dur := model.BillingPeriodDuration(sub.Period)
	if dur == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "invalid period",
			"period": sub.Period,
		})
		return
	}
	paidUntil := time.Now().Add(dur)
	reactivated, err := h.subs.AdminReactivate(r.Context(), id, paidUntil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "reactivate failed"})
		return
	}
	updated, err := h.subs.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "lookup after reactivate failed"})
		return
	}
	if !reactivated {
		// VM 已 trashed / deleted：不恢复，订阅已被 cancel 让位（避免给已删 VM 续费）。
		audit(r.Context(), r, "subscription_admin_reactivate_denied", "subscription", id, map[string]any{
			"prev_status": sub.Status,
			"reason":      "vm_gone",
		})
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "vm no longer exists; subscription cancelled",
			"subscription": updated,
		})
		return
	}
	audit(r.Context(), r, "subscription_admin_reactivated", "subscription", id, map[string]any{
		"prev_status": sub.Status,
		"period":      sub.Period,
		"paid_until":  paidUntil,
	})
	writeJSON(w, http.StatusOK, map[string]any{"subscription": updated})
}
