package portal

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/incuscloud/incus-admin/internal/middleware"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// MaxTopUpPerRequest 单次充值上限（单位与 balance 相同，默认 10000）。
// 防止误操作或账户被盗后一次性转走巨额资金。
const MaxTopUpPerRequest = 10000.0

// MaxTopUpPerDay 单用户滚动 24h 累计充值上限。上限基于最近 24 小时，
// 而非自然日，避免 23:59/00:01 连刷的跨日绕过。
const MaxTopUpPerDay = 100000.0

// topUpWindow 日额度滚动窗口长度。
const topUpWindow = 24 * time.Hour

type UserHandler struct {
	repo *repository.UserRepo
}

func NewUserHandler(repo *repository.UserRepo) *UserHandler {
	return &UserHandler{repo: repo}
}

func (h *UserHandler) AdminRoutes(r chi.Router) {
	r.Get("/users", h.ListUsers)
	r.Get("/users/{id}", h.GetUser)
	r.Put("/users/{id}/role", h.UpdateRole)
	r.Post("/users/{id}/balance", h.TopUpBalance)
	r.Get("/users/{id}/topup-quota", h.GetTopUpQuota)
}

func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	p := ParsePageParams(r)
	users, total, err := h.repo.ListPaged(r.Context(), p.Limit, p.Offset)
	if err != nil {
		slog.Error("list users failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to list users"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":  users,
		"total":  total,
		"limit":  p.Limit,
		"offset": p.Offset,
	})
}

func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	user, err := h.repo.GetByID(r.Context(), id)
	if err != nil || user == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *UserHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		Role string `json:"role" validate:"required,oneof=admin customer"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if err := h.repo.UpdateRole(r.Context(), id, req.Role); err != nil {
		slog.Error("update role failed", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to update role"})
		return
	}
	slog.Info("user role updated", "user_id", id, "role", req.Role)
	audit(r.Context(), r, "user.update_role", "user", id, map[string]any{
		"role": req.Role,
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *UserHandler) TopUpBalance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	var req struct {
		// Amount 上下限通过 validator tag 约束；注意 gt=0 要求"严格大于 0"，
		// lte=10000 对应 MaxTopUpPerRequest —— 若后者调整需同步修改 tag。
		Amount      float64 `json:"amount"      validate:"required,gt=0,lte=10000"`
		Description string  `json:"description" validate:"omitempty,max=500"`
	}
	if !decodeAndValidate(w, r, &req) {
		return
	}
	// 禁止管理员给自己充值（防止特权账号自充）。
	if actorID, _ := r.Context().Value(middleware.CtxUserID).(int64); actorID > 0 && actorID == id {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "admin cannot top up own balance"})
		return
	}
	// 日额度守卫：滚动 24h 内 deposit 累加不得超过 MaxTopUpPerDay。
	// TopUpWithDailyCap 在同一事务内对 users 行加写锁 + 查询 + 扣款，
	// 保证并发场景下严格不越限（权威判定以 DB 为准；前端仅做 UX 提示）。
	used, _, ok, err := h.repo.TopUpWithDailyCap(
		r.Context(), id, req.Amount, req.Description, nil, topUpWindow, MaxTopUpPerDay,
	)
	if err != nil {
		slog.Error("top up balance failed", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "daily top-up quota exceeded",
			"limit": MaxTopUpPerDay,
			"used":  used,
		})
		return
	}
	slog.Info("balance topped up", "user_id", id, "amount", req.Amount)
	audit(r.Context(), r, "user.topup", "user", id, map[string]any{"amount": req.Amount})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// GetTopUpQuota 返回目标用户在滚动 24h 窗口内的充值额度使用情况，用于前端 UX 提示。
func (h *UserHandler) GetTopUpQuota(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}
	used, err := h.repo.SumDepositsSince(r.Context(), id, time.Now().Add(-topUpWindow))
	if err != nil {
		slog.Error("sum deposits failed", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to compute daily usage"})
		return
	}
	remaining := MaxTopUpPerDay - used
	if remaining < 0 {
		remaining = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"used":              used,
		"limit":             MaxTopUpPerDay,
		"remaining":         remaining,
		"per_request_limit": MaxTopUpPerRequest,
		"window_hours":      int(topUpWindow / time.Hour),
	})
}
