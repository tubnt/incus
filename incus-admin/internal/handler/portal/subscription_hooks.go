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

// reactivateSubscriptionOnRestore 在 VM restore 成功后恢复该 VM 最新一条
// cancelled 订阅。区分 cancel 原因（决策#1，绝不免费续期）：
//
//   - 用户主动 trash（active → cancelled，suspended_at 为空）：免费"恢复"语义
//     —— 不扣费，paid_until 重置为 NOW + 周期。用户 trash 前已付的窗口本就有效，
//     恢复到一个完整周期是对称、公平的。
//   - 系统欠费回收（suspended → cancelled，suspended_at 非空）：restore 必须
//     补扣一个周期；余额不足则拒绝（订阅保持 cancelled），不得免费续期。
//
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

	// suspended_at 非空 = 该订阅曾被欠费挂起后回收（grace expire 只改 status，保留
	// suspended_at）。此类订阅 restore 必须补扣，不能免费恢复。
	if latest.SuspendedAt != nil {
		outcome, sub, err := subs.RestoreChargedByVM(ctx, vmID, time.Now())
		if err != nil {
			slog.Error("charged restore on arrears sub failed", "vm_id", vmID, "sub_id", latest.ID, "error", err)
			return
		}
		switch outcome {
		case repository.RestoreReactivated:
			audit(ctx, r, "subscription_restored", "vm", vmID, map[string]any{
				"sub_id":     latest.ID,
				"period":     latest.Period,
				"paid_until": sub.PaidUntil,
				"charged":    true,
			})
		case repository.RestoreInsufficient:
			slog.Warn("restore denied: insufficient balance for arrears sub", "vm_id", vmID, "sub_id", latest.ID)
			audit(ctx, r, "subscription_restore_denied", "vm", vmID, map[string]any{
				"sub_id": latest.ID,
				"reason": "insufficient_balance",
			})
		default:
			// RestoreNoop：并发被抢 / 无 cancelled 行，静默返回
		}
		return
	}

	// 用户主动 trash 的免费恢复路径（原语义）。
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
