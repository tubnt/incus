package worker

import (
	"context"
	"log/slog"
	"time"
)

// BillingGracer 是 grace_expire 需要的最小切片；由 service/billing.Service 实现。
type BillingGracer interface {
	ExpireGrace(ctx context.Context) (BillingGraceStats, error)
}

// BillingGraceStats 与 service/billing.GraceStats 同形态。
type BillingGraceStats struct {
	Processed int
	Trashed   int
	Errors    int
}

// RunBillingGraceExpire 每 tickEvery 跑一次 ExpireGrace。
//
// 与 daily charger 错开 cadence（默认 charger 1h tick / grace 1h tick + 15min
// initial delay），避免两 worker 同一时刻打 users.balance FOR UPDATE → 锁等待。
// 实际错位由 main.go 注入 firstDelay 控制；本函数仅在 firstDelay > 0 时延迟首
// 次执行。
//
// tickEvery <= 0 默认 1 小时；gracer == nil 直接 return。
func RunBillingGraceExpire(ctx context.Context, gracer BillingGracer, tickEvery, firstDelay time.Duration) {
	if gracer == nil {
		slog.Info("billing grace expire disabled")
		return
	}
	if tickEvery <= 0 {
		tickEvery = time.Hour
	}
	slog.Info("billing grace expire started", "tick", tickEvery, "first_delay", firstDelay)

	runOnce := func() {
		stats, err := gracer.ExpireGrace(ctx)
		if err != nil {
			slog.Error("billing grace expire run failed", "error", err)
			return
		}
		if stats.Processed > 0 || stats.Errors > 0 {
			slog.Info("billing grace expire run",
				"processed", stats.Processed,
				"trashed", stats.Trashed,
				"errors", stats.Errors,
			)
		}
	}

	if firstDelay > 0 {
		select {
		case <-ctx.Done():
			slog.Info("billing grace expire stopping (during first-delay)")
			return
		case <-time.After(firstDelay):
		}
	}
	// First tick after firstDelay completes — covers backlog accumulated while
	// the process was offline.
	runOnce()

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("billing grace expire stopping")
			return
		case <-tick.C:
			runOnce()
		}
	}
}
