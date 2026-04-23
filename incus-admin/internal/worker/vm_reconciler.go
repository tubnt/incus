package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// ClusterSnapshot is what a ClusterSnapshotFn returns for a single cluster:
// the DB id + display name + the set of Incus instance names observed right
// now. Err is non-nil when Incus was unreachable / returned error for *this*
// cluster — in that case the worker skips the cluster without marking any
// drift (avoids false-positive gone during network blips).
type ClusterSnapshot struct {
	ID            int64
	Name          string
	InstanceNames map[string]struct{}
	Err           error
}

// ClusterSnapshotFn is implemented by a closure in main.go that walks
// cluster.Manager.List(), calls Client.GetInstances per cluster and extracts
// VM names. Defining it as a function type (not an interface) keeps the
// worker package free of cluster/repository imports — the dependency
// direction always flows inwards.
type ClusterSnapshotFn func(ctx context.Context) []ClusterSnapshot

// ReconcileVMRepo is the minimal VMRepo surface the reconciler needs. Kept
// tight so unit tests can supply a fake without pulling in the full repo.
type ReconcileVMRepo interface {
	ListActiveForReconcile(ctx context.Context, clusterID int64, cutoff time.Time) ([]model.VM, error)
	MarkGone(ctx context.Context, id int64) error
}

// ReconcileIPRepo releases IPs whose owning VM has gone missing. Failure
// here is logged but not fatal — next cycle will retry, or admin can run
// the IP cooldown recovery.
type ReconcileIPRepo interface {
	Release(ctx context.Context, ip string) error
}

// ReconcileAuditor records reconciliation outcomes; nil-safe for dev/test
// environments that skip audit wiring.
type ReconcileAuditor interface {
	Log(ctx context.Context, userID *int64, action, targetType string, targetID int64, details any, ip string)
}

// VMReconcilerConfig carries knobs so callers don't need to touch the loop
// body to tune timing. Zero values map to sensible defaults (see Run).
type VMReconcilerConfig struct {
	// Interval between full passes. <= 0 disables the worker entirely.
	Interval time.Duration
	// CreateBuffer: VMs inserted within this window are excluded from diffing
	// so a just-provisioned row isn't marked gone while Incus is still making
	// the instance visible. Default 10s.
	CreateBuffer time.Duration
	// InitialDelay before the first pass. Default 30s — lets migrations +
	// cluster manager init settle after a restart.
	InitialDelay time.Duration
	// DriftAlertThreshold: if a single pass corrects more than this many
	// drifts, emit a WARN log for alerting. Default 5.
	DriftAlertThreshold int
}

// RunVMReconciler polls the Incus cluster(s), diffs against `vms` rows in
// active statuses, and flips vanished rows to status='gone' while releasing
// their IPs. The loop exits cleanly when ctx is cancelled.
//
// Dependencies are passed in explicitly (no package-level state) so tests
// can drive the loop with fakes.
func RunVMReconciler(
	ctx context.Context,
	cfg VMReconcilerConfig,
	snapshot ClusterSnapshotFn,
	vmRepo ReconcileVMRepo,
	ipRepo ReconcileIPRepo,
	auditor ReconcileAuditor,
) {
	if cfg.Interval <= 0 {
		slog.Info("vm reconciler disabled", "interval", cfg.Interval)
		return
	}
	if cfg.CreateBuffer <= 0 {
		cfg.CreateBuffer = 10 * time.Second
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 30 * time.Second
	}
	if cfg.DriftAlertThreshold <= 0 {
		cfg.DriftAlertThreshold = 5
	}

	slog.Info("vm reconciler started",
		"interval", cfg.Interval,
		"create_buffer", cfg.CreateBuffer,
		"drift_alert_threshold", cfg.DriftAlertThreshold,
	)

	initial := time.NewTimer(cfg.InitialDelay)
	defer initial.Stop()
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("vm reconciler stopping")
			return
		case <-initial.C:
			runReconcileOnce(ctx, cfg, snapshot, vmRepo, ipRepo, auditor)
		case <-tick.C:
			runReconcileOnce(ctx, cfg, snapshot, vmRepo, ipRepo, auditor)
		}
	}
}

// RunVMReconcilerOnce exposes a single-pass invocation for callers that
// need ad-hoc reconciliation outside the timer loop — e.g. the event
// listener triggers this after a reconnect to catch up on drift that
// accumulated while disconnected. cfg's non-interval knobs (CreateBuffer /
// DriftAlertThreshold) still apply; Interval is ignored.
func RunVMReconcilerOnce(
	ctx context.Context,
	cfg VMReconcilerConfig,
	snapshot ClusterSnapshotFn,
	vmRepo ReconcileVMRepo,
	ipRepo ReconcileIPRepo,
	auditor ReconcileAuditor,
) {
	// Apply the same default defaulting the long-running loop uses so an
	// on-demand call doesn't run with a 0 buffer by accident.
	if cfg.CreateBuffer <= 0 {
		cfg.CreateBuffer = 10 * time.Second
	}
	if cfg.DriftAlertThreshold <= 0 {
		cfg.DriftAlertThreshold = 5
	}
	runReconcileOnce(ctx, cfg, snapshot, vmRepo, ipRepo, auditor)
}

// runReconcileOnce is the per-cycle body. Extracted so tests can invoke a
// single pass deterministically without timing races.
func runReconcileOnce(
	ctx context.Context,
	cfg VMReconcilerConfig,
	snapshot ClusterSnapshotFn,
	vmRepo ReconcileVMRepo,
	ipRepo ReconcileIPRepo,
	auditor ReconcileAuditor,
) {
	snapshots := snapshot(ctx)
	cutoff := time.Now().Add(-cfg.CreateBuffer)
	totalDrift := 0

	for _, snap := range snapshots {
		if snap.Err != nil {
			slog.Warn("vm reconciler: cluster unreachable, skipping",
				"cluster", snap.Name, "error", snap.Err)
			continue
		}
		driftInCluster := reconcileCluster(ctx, cutoff, snap, vmRepo, ipRepo, auditor)
		totalDrift += driftInCluster
	}

	switch {
	case totalDrift > cfg.DriftAlertThreshold:
		slog.Warn("vm reconciler: high drift count", "drift", totalDrift)
	case totalDrift > 0:
		slog.Info("vm reconciler: drift corrected", "drift", totalDrift)
	}
}

// reconcileCluster handles one cluster's DB←Incus diff. Returns drift count
// so the caller can aggregate for threshold logging.
func reconcileCluster(
	ctx context.Context,
	cutoff time.Time,
	snap ClusterSnapshot,
	vmRepo ReconcileVMRepo,
	ipRepo ReconcileIPRepo,
	auditor ReconcileAuditor,
) int {
	dbVMs, err := vmRepo.ListActiveForReconcile(ctx, snap.ID, cutoff)
	if err != nil {
		slog.Error("vm reconciler: list db vms",
			"cluster", snap.Name, "error", err)
		return 0
	}

	drift := 0
	for _, vm := range dbVMs {
		if _, alive := snap.InstanceNames[vm.Name]; alive {
			continue
		}
		// DB has it, Incus doesn't: mark gone + release IP + audit.
		if err := vmRepo.MarkGone(ctx, vm.ID); err != nil {
			slog.Error("vm reconciler: mark gone",
				"vm_id", vm.ID, "name", vm.Name, "error", err)
			continue
		}
		if vm.IP != nil && *vm.IP != "" {
			if err := ipRepo.Release(ctx, *vm.IP); err != nil {
				// Don't abort the drift handling — the next cycle retries.
				slog.Warn("vm reconciler: ip release failed",
					"vm_id", vm.ID, "ip", *vm.IP, "error", err)
			}
		}
		if auditor != nil {
			ip := ""
			if vm.IP != nil {
				ip = *vm.IP
			}
			auditor.Log(ctx, nil, "vm.reconcile.gone", "vm", vm.ID, map[string]any{
				"cluster": snap.Name,
				"name":    vm.Name,
				"ip":      ip,
			}, "")
		}
		drift++
		slog.Info("vm reconciler: marked gone",
			"cluster", snap.Name, "vm_id", vm.ID, "name", vm.Name)
	}
	return drift
}
