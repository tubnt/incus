package worker

import (
	"context"
	"log/slog"
	"time"
)

// APITokenCleaner is the minimal slice of APITokenRepo the cleanup loop needs.
type APITokenCleaner interface {
	DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// RunAPITokenCleanup deletes api_tokens rows whose expires_at predates
// NOW() - gracePeriod. The grace period preserves invalidated tokens long
// enough to stay cross-referenceable from audit_logs (token reuse
// investigations, leaked-token tracing). 30d is the default.
//
// Runs hourly after a 60s warmup so the worker doesn't compete with startup
// migrations.
func RunAPITokenCleanup(ctx context.Context, cleaner APITokenCleaner, gracePeriod time.Duration) {
	if gracePeriod <= 0 {
		gracePeriod = 30 * 24 * time.Hour
	}
	slog.Info("api token cleanup worker started", "grace_period_hours", gracePeriod.Hours())

	initial := time.NewTimer(60 * time.Second)
	defer initial.Stop()
	tick := time.NewTicker(1 * time.Hour)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("api token cleanup worker stopping")
			return
		case <-initial.C:
			runTokenCleanupOnce(ctx, cleaner, gracePeriod)
		case <-tick.C:
			runTokenCleanupOnce(ctx, cleaner, gracePeriod)
		}
	}
}

func runTokenCleanupOnce(ctx context.Context, cleaner APITokenCleaner, gracePeriod time.Duration) {
	cutoff := time.Now().Add(-gracePeriod)
	n, err := cleaner.DeleteExpiredBefore(ctx, cutoff)
	if err != nil {
		slog.Error("api token cleanup failed", "error", err, "cutoff", cutoff)
		return
	}
	if n > 0 {
		slog.Info("api token cleanup run", "deleted", n, "cutoff", cutoff)
	}
}
