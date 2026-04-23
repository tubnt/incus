// Package worker hosts background goroutines spawned from main. Each worker
// takes its own context (so main can cancel on shutdown) and logs what it
// does — avoid silent workers.
package worker

import (
	"context"
	"log/slog"
	"time"
)

// AuditCleaner is the minimal slice of AuditRepo the cleanup loop needs.
// Keeping the interface tiny lets unit tests pass a fake without pulling in a
// full repo.
type AuditCleaner interface {
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// RunAuditCleanup removes audit_logs rows older than retentionDays once per
// 24h. retentionDays <= 0 disables the worker entirely (for dev / test
// environments that want indefinite retention).
//
// The first tick runs 30s after startup so concurrent migrations / upgrades
// finish first; subsequent ticks are every 24h. Deletions emit structured
// logs; failed attempts don't stop the loop.
func RunAuditCleanup(ctx context.Context, cleaner AuditCleaner, retentionDays int) {
	if retentionDays <= 0 {
		slog.Info("audit cleanup worker disabled", "retention_days", retentionDays)
		return
	}
	slog.Info("audit cleanup worker started", "retention_days", retentionDays)

	// Initial delay so the worker doesn't hammer the DB during a flapping
	// restart loop; also gives migrations time to finish on first deploy.
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()

	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("audit cleanup worker stopping")
			return
		case <-initial.C:
			runCleanupOnce(ctx, cleaner, retentionDays)
		case <-tick.C:
			runCleanupOnce(ctx, cleaner, retentionDays)
		}
	}
}

func runCleanupOnce(ctx context.Context, cleaner AuditCleaner, retentionDays int) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	n, err := cleaner.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		slog.Error("audit cleanup failed", "error", err, "cutoff", cutoff)
		return
	}
	if n > 0 {
		slog.Info("audit cleanup run", "deleted", n, "cutoff", cutoff)
	}
}
