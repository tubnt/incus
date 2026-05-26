package portal

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
	"github.com/incuscloud/incus-admin/internal/repository"
)

// cancelSubscriptionOnTrash 在 VM trash 成功后把对应 vm_subscriptions 行切到
// cancelled（PLAN-054 / INFRA-013）。repo 为 nil 时直接 return（未注入兼容路径）；
// 失败仅记日志：trash 主动作已成功，sub 不一致由 backfill / 人工兜底，不应让
// HTTP 失败回滚 trash（用户视角的"删除"已生效）。
func cancelSubscriptionOnTrash(ctx context.Context, r *http.Request, subs *repository.SubscriptionRepo, vmID int64) {
	if subs == nil {
		return
	}
	n, err := subs.CancelByVM(ctx, vmID)
	if err != nil {
		slog.Error("cancel subscription on trash failed", "vm_id", vmID, "error", err)
		return
	}
	if n == 0 {
		// 无 active sub 是正常 case：旧 VM（PLAN-054 之前创建）没记账行。
		return
	}
	audit(ctx, r, "subscription_cancelled", "vm", vmID, map[string]any{"rows": n})
}

// reactivateSubscriptionOnRestore 在 VM restore 成功后把最新一条 cancelled
// 订阅恢复为 active 并把 paid_until 重置为 NOW + 周期。重置是免费"恢复"语义
// —— restore 不再扣费，相当于把 cancelled 之前剩余的窗口延长成一个完整周期。
// 与 trash 对称，失败仅记日志，不阻塞 restore 主路径。
func reactivateSubscriptionOnRestore(ctx context.Context, r *http.Request, subs *repository.SubscriptionRepo, vmID int64) {
	if subs == nil {
		return
	}
	latest, err := subs.GetLatestByVM(ctx, vmID)
	if err != nil {
		slog.Error("lookup subscription on restore failed", "vm_id", vmID, "error", err)
		return
	}
	if latest == nil {
		// 旧 VM 无订阅记录；restore 后由 backfill 工具补齐
		return
	}
	if latest.Status != model.SubscriptionStatusCancelled {
		// 设计上 trash 必然把 active → cancelled；走到这里说明上游漂了，记 warn
		slog.Warn("restore: latest sub not cancelled", "vm_id", vmID, "sub_id", latest.ID, "status", latest.Status)
		return
	}
	dur := model.BillingPeriodDuration(latest.Period)
	if dur == 0 {
		slog.Error("restore: invalid sub period", "vm_id", vmID, "sub_id", latest.ID, "period", latest.Period)
		return
	}
	paidUntil := time.Now().Add(dur)
	n, err := subs.ReactivateByVM(ctx, vmID, paidUntil)
	if err != nil {
		slog.Error("reactivate subscription on restore failed", "vm_id", vmID, "error", err)
		return
	}
	if n == 0 {
		// 并发场景：被另一个请求抢先恢复
		return
	}
	audit(ctx, r, "subscription_restored", "vm", vmID, map[string]any{
		"sub_id":     latest.ID,
		"period":     latest.Period,
		"paid_until": paidUntil,
	})
}
