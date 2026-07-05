package worker

import (
	"context"
	"log/slog"
	"time"
)

// BillingCharger 是 daily_charger 需要的最小切片；由 service/billing.Service 实现。
// 拓窄接口让 worker 不直接依赖 service 包，单测可注入 stub。
type BillingCharger interface {
	ChargeDue(ctx context.Context) (BillingChargeStats, error)
}

// BillingChargeStats 与 service/billing.ChargeStats 同形态。复制在 worker 包
// 避免反向 import service —— 与现有 jobs.MigrateOutcome 模式一致。
type BillingChargeStats struct {
	Processed int
	Paid      int
	Suspended int
	Skipped   int
	Errors    int
}

// RunBillingDailyCharger 每 tickEvery 跑一次 ChargeDue。catchup-safe：
// charger.ChargeDue 用 SELECT paid_until <= NOW 拉所有逾期 sub，与 cron 精确
// 触发的效果等价（PLAN-054 §4 决策 5）。启动时立即先跑一次。
//
// tickEvery <= 0 默认 1 小时；charger == nil 直接 return（关闭计费时启动跳过）。
func RunBillingDailyCharger(ctx context.Context, charger BillingCharger, tickEvery time.Duration) {
	if charger == nil {
		slog.Info("billing daily charger disabled")
		return
	}
	if tickEvery <= 0 {
		tickEvery = time.Hour
	}
	slog.Info("billing daily charger started", "tick", tickEvery)

	runOnce := func() {
		stats, err := charger.ChargeDue(ctx)
		if err != nil {
			slog.Error("billing daily charger run failed", "error", err)
			return
		}
		if stats.Processed > 0 || stats.Errors > 0 {
			slog.Info("billing daily charger run",
				"processed", stats.Processed,
				"paid", stats.Paid,
				"suspended", stats.Suspended,
				"skipped", stats.Skipped,
				"errors", stats.Errors,
			)
		}
	}

	// 启动 catchup：错过整点 / 进程刚重启时立刻把积压扣掉。
	runOnce()

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("billing daily charger stopping")
			return
		case <-tick.C:
			runOnce()
		}
	}
}
