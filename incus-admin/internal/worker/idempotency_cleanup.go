package worker

import (
	"context"
	"log/slog"
	"time"
)

// IdempotencyCleaner 是 cleanup 循环需要的最小切片。
// repository.IdempotencyRepo 直接实现；测试用 fake 注入。
type IdempotencyCleaner interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// idempotencyTTL 是 cloud-gateway 标准的 24h 缓存窗口（PLAN-053 Phase E）。
const idempotencyTTL = 24 * time.Hour

// RunIdempotencyCleanup 周期性删除 idempotency_keys 表里 created_at < NOW()-24h 的行。
//
// 行为：
//   - 等价 cron `7 * * * *` 的语义（每小时第 7 分跑）；首次启动延迟 7 分钟
//     再启动 1h ticker，让迁移 / 启动暖机先完成；和 audit_cleanup 30s 暖机一致的思路
//   - tickEvery <= 0 → 用 1h 默认；测试可注入毫秒级 ticker 驱动快速 fire
//   - ctx 取消即退出；删除失败只打 slog 不中断 loop（下一轮自然重试）
//
// 注册位置：cmd/server/main.go 与 RunAuditCleanup / RunAPITokenCleanup 同级 goroutine。
func RunIdempotencyCleanup(ctx context.Context, cleaner IdempotencyCleaner, tickEvery time.Duration) {
	if cleaner == nil {
		slog.Info("idempotency cleanup worker disabled (nil cleaner)")
		return
	}
	if tickEvery <= 0 {
		tickEvery = time.Hour
	}
	slog.Info("idempotency cleanup worker started", "tick_every", tickEvery, "ttl_hours", idempotencyTTL.Hours())

	// 7 分钟暖机：cron 7 * * * * 的字面意图是每小时第 7 分；这里近似实现，
	// 不引入 cron 库，避免给一个 1 行业务加依赖。
	initial := time.NewTimer(7 * time.Minute)
	defer initial.Stop()

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("idempotency cleanup worker stopping")
			return
		case <-initial.C:
			runIdempotencyCleanupOnce(ctx, cleaner)
		case <-tick.C:
			runIdempotencyCleanupOnce(ctx, cleaner)
		}
	}
}

func runIdempotencyCleanupOnce(ctx context.Context, cleaner IdempotencyCleaner) {
	cutoff := time.Now().Add(-idempotencyTTL)
	n, err := cleaner.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		slog.Error("idempotency cleanup failed", "error", err, "cutoff", cutoff)
		return
	}
	// 不删行也写一行 info，让运维确认 worker 在跑（与 audit_cleanup 只在 n>0 才打的
	// 决策不同——idempotency 表本来就低流量，n=0 是常态，每小时报一次健康节奏）。
	slog.Info("idempotency cleanup", "deleted", n, "cutoff", cutoff)
}
