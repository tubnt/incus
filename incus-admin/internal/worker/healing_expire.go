package worker

import (
	"context"
	"log/slog"
	"time"
)

// HealingExpirer covers the slice of HealingEventRepo the sweeper needs.
// ExpireStale returns the number of rows flipped from 'in_progress' to
// 'partial' based on the cutoff passed in.
type HealingExpirer interface {
	ExpireStale(ctx context.Context, cutoff time.Time) (int64, error)
}

// RunHealingExpireStale periodically sweeps in_progress healing events
// older than maxAge and flips them to 'partial'. Catches cases where the
// Incus event flow never delivered the completion signal — we'd rather
// surface a "partial" than let a stuck row pollute the history forever.
//
// tickEvery <= 0 defaults to 5 minutes. maxAge <= 0 disables the worker.
// The first tick fires after `tickEvery` so startup stays cheap.
func RunHealingExpireStale(ctx context.Context, expirer HealingExpirer, maxAge, tickEvery time.Duration) {
	if expirer == nil || maxAge <= 0 {
		slog.Info("healing expire worker disabled", "max_age", maxAge)
		return
	}
	if tickEvery <= 0 {
		tickEvery = 5 * time.Minute
	}
	slog.Info("healing expire worker started", "max_age", maxAge, "tick", tickEvery)

	tick := time.NewTicker(tickEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("healing expire worker stopping")
			return
		case <-tick.C:
			cutoff := time.Now().Add(-maxAge)
			n, err := expirer.ExpireStale(ctx, cutoff)
			if err != nil {
				slog.Error("healing expire failed", "error", err, "cutoff", cutoff)
				continue
			}
			if n > 0 {
				slog.Info("healing expire run", "expired", n, "cutoff", cutoff)
			}
		}
	}
}
