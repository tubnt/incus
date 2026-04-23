package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

// EventListenerRepo is the minimal persistence surface needed by the event
// listener. Intersects with ReconcileVMRepo but keeps the two tight so
// either can evolve independently.
type EventListenerRepo interface {
	MarkGoneByName(ctx context.Context, clusterID int64, name string) error
	UpdateNodeByName(ctx context.Context, clusterID int64, name string, node string) error
	// LookupForEvent returns (id, currentNode) for a VM so instance-updated
	// handling can record the from-node before overwriting it. Zero id +
	// empty node means the row is unknown — skip tracking.
	LookupForEvent(ctx context.Context, clusterID int64, name string) (int64, string, error)
}

// HealingTracker is the subset of HealingEventRepo the listener needs for
// Phase D.2 (auto rows on cluster-member transitions) + Phase D.3 (append
// VM movements during evacuate). Optional — main.go passes nil in dev/test
// setups that don't exercise healing_events.
type HealingTracker interface {
	FindInProgressByNode(ctx context.Context, clusterID int64, nodeName, trigger string) (int64, error)
	Create(ctx context.Context, clusterID int64, nodeName, trigger string, actorID *int64) (int64, error)
	AppendEvacuatedVM(ctx context.Context, eventID int64, vm HealingEvacuatedVM) error
	CompleteByNode(ctx context.Context, clusterID int64, nodeName string) (int64, error)
}

// HealingEvacuatedVM mirrors repository.EvacuatedVM without importing the
// repository package here — the listener stays consumer-side and doesn't
// leak that dependency into its interface contract.
type HealingEvacuatedVM struct {
	VMID     int64  `json:"vm_id"`
	Name     string `json:"name"`
	FromNode string `json:"from_node"`
	ToNode   string `json:"to_node"`
}

// ReconcileOnDemand is invoked after a successful reconnect so the listener
// catches up on any drift that happened while we were disconnected. Kept as
// a callback (not a type) so the adapter can build a bound closure over the
// existing reconciler knobs.
type ReconcileOnDemand func(ctx context.Context)

// ClusterStream encapsulates what the listener needs about a single cluster:
// its DB id for repository calls, a human-readable name for logs, plus the
// live API URL + TLS config for the WebSocket dial. Rebuilt each reconnect
// in case AddCluster / UpdateConfig changed things.
type ClusterStream struct {
	ID     int64
	Name   string
	APIURL string
	TLS    *tls.Config
}

// ClusterStreamFn is typically implemented as a closure over cluster.Manager;
// the worker calls it once per reconnect cycle so dynamic cluster config
// changes take effect without restarting the process.
type ClusterStreamFn func() []ClusterStream

// EventListenerConfig carries tunables so main.go can change timing without
// editing the loop body. Zero values map to production-sane defaults.
type EventListenerConfig struct {
	// EventTypes sent as ?type= param. Default: []string{"lifecycle","cluster"}.
	EventTypes []string
	// MinBackoff is the initial retry delay on connect/read failure. Default 5s.
	MinBackoff time.Duration
	// MaxBackoff caps the reconnect delay. Default 60s.
	MaxBackoff time.Duration
	// JitterFraction adds [0, jf)*currentBackoff of randomness. Default 0.25.
	JitterFraction float64
}

// RunEventListener spawns one goroutine per cluster that subscribes to
// Incus /1.0/events and drives repository updates. On drop, it backs off
// exponentially up to MaxBackoff, then reconnects — *and* invokes
// reconcileOnDemand so any state the listener missed gets caught by the
// reconciler's full-scan pass.
//
// The function returns immediately; the listeners run until ctx cancels.
func RunEventListener(
	ctx context.Context,
	cfg EventListenerConfig,
	streamFn ClusterStreamFn,
	repo EventListenerRepo,
	healing HealingTracker,
	reconcileOnDemand ReconcileOnDemand,
) {
	if len(cfg.EventTypes) == 0 {
		// Incus only accepts "logging" / "operation" / "lifecycle" as top-
		// level types. Cluster-member transitions surface as lifecycle
		// actions (cluster-member-updated, etc.) distinguished by
		// metadata.source, so a single "lifecycle" subscription covers
		// both instance + cluster events.
		cfg.EventTypes = []string{"lifecycle"}
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = 5 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 60 * time.Second
	}
	if cfg.JitterFraction <= 0 {
		cfg.JitterFraction = 0.25
	}

	streams := streamFn()
	if len(streams) == 0 {
		slog.Info("event listener: no clusters, skipping")
		return
	}

	slog.Info("event listener starting", "clusters", len(streams), "types", cfg.EventTypes)
	for _, s := range streams {
		go runClusterListener(ctx, cfg, s, streamFn, repo, healing, reconcileOnDemand)
	}
}

// runClusterListener holds the reconnect loop for one cluster. Each
// iteration re-fetches the ClusterStream so dynamic config updates (cluster
// URL change, TLS pin update after a rotation) get picked up without a
// process restart.
func runClusterListener(
	ctx context.Context,
	cfg EventListenerConfig,
	initial ClusterStream,
	streamFn ClusterStreamFn,
	repo EventListenerRepo,
	healing HealingTracker,
	reconcileOnDemand ReconcileOnDemand,
) {
	stream := initial
	backoff := cfg.MinBackoff

	// healthyResetAfter 决定"一次连接存活多久就重置 backoff"。避免把一个一口气跑
	// 几小时的连接的 disconnect 视为高频 flap：长连接断开应从 MinBackoff 重试，
	// 而不是继承上一次已经被 cap 到 MaxBackoff 的值。
	const healthyResetAfter = 5 * time.Minute

	for {
		if ctx.Err() != nil {
			return
		}

		connectedAt := time.Now()
		err := cluster.StreamEvents(ctx, stream.TLS, stream.APIURL, cfg.EventTypes, func(ev cluster.Event) error {
			dispatchEvent(ctx, stream, ev, repo, healing)
			return nil
		})
		connectionLifetime := time.Since(connectedAt)

		if ctx.Err() != nil {
			slog.Info("event listener stopping", "cluster", stream.Name)
			return
		}

		// 上一次连接存活够久 → 视为"曾健康"，从 MinBackoff 重试；否则按指数退避。
		if connectionLifetime >= healthyResetAfter {
			backoff = cfg.MinBackoff
		}

		if err != nil {
			slog.Warn("event listener disconnected", "cluster", stream.Name, "error", err, "backoff", backoff, "lifetime", connectionLifetime)
		} else {
			slog.Info("event listener closed, reconnecting", "cluster", stream.Name, "backoff", backoff, "lifetime", connectionLifetime)
		}

		// Sleep with jitter.
		jitter := time.Duration(rand.Float64() * cfg.JitterFraction * float64(backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff + jitter):
		}

		// Trigger a full reconcile so drift that happened during the outage
		// surfaces on the next round even before we're reconnected.
		if reconcileOnDemand != nil {
			reconcileOnDemand(ctx)
		}

		// Refresh stream (URL / TLS may have changed) and advance backoff.
		for _, s := range streamFn() {
			if s.ID == stream.ID {
				stream = s
				break
			}
		}
		backoff *= 2
		if backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}
}

// dispatchEvent maps one Incus event to a repository side-effect. Unknown
// types / actions are skipped — the reconciler remains the safety net.
func dispatchEvent(ctx context.Context, stream ClusterStream, ev cluster.Event, repo EventListenerRepo, healing HealingTracker) {
	if ev.Type != "lifecycle" {
		return
	}
	handleLifecycle(ctx, stream, ev, repo, healing)
}

func handleLifecycle(ctx context.Context, stream ClusterStream, ev cluster.Event, repo EventListenerRepo, healing HealingTracker) {
	var md cluster.LifecycleMetadata
	if err := json.Unmarshal(ev.Metadata, &md); err != nil {
		return
	}
	// Distinguish instance lifecycle from cluster-member lifecycle by
	// Source path prefix.
	if memberName := cluster.ClusterMemberNameFromSource(md.Source); memberName != "" {
		handleClusterMember(ctx, stream, memberName, md, healing)
		return
	}
	name := cluster.InstanceNameFromSource(md.Source)
	if name == "" {
		return
	}

	switch md.Action {
	case "instance-deleted":
		// Only flip `gone` when the deletion wasn't initiated through our
		// own API. But telling those apart reliably needs request context
		// we don't have here, so we always mark gone; the regular DELETE
		// handler has already flipped status to 'deleted' and MarkGoneByName
		// skips rows in 'deleted' state. Net effect: in-band deletes stay
		// 'deleted', out-of-band delete (which is what we care about)
		// becomes 'gone' — same invariant the reconciler enforces, just
		// faster.
		if err := repo.MarkGoneByName(ctx, stream.ID, name); err != nil {
			slog.Error("event listener: mark gone", "cluster", stream.Name, "name", name, "error", err)
			return
		}
		slog.Info("event listener: instance-deleted → gone", "cluster", stream.Name, "name", name)

	case "instance-updated":
		// Extract the new location/node if present. evacuate + migrate
		// surface here with context.location set to the destination node.
		var ctxPayload struct {
			Location string `json:"location,omitempty"`
		}
		_ = json.Unmarshal(md.Context, &ctxPayload)
		if ctxPayload.Location == "" || ctxPayload.Location == ev.Location {
			return
		}
		// Phase D.3: resolve (vm_id, from_node) before overwriting so we
		// can record the movement against any in-progress healing event on
		// the source node. Order matters — look up first, then update.
		vmID, fromNode, lookupErr := repo.LookupForEvent(ctx, stream.ID, name)
		if lookupErr != nil {
			slog.Warn("event listener: lookup for evacuated vm", "cluster", stream.Name, "name", name, "error", lookupErr)
		}
		if err := repo.UpdateNodeByName(ctx, stream.ID, name, ctxPayload.Location); err != nil {
			slog.Error("event listener: update node", "cluster", stream.Name, "name", name, "error", err)
			return
		}
		slog.Info("event listener: instance moved", "cluster", stream.Name, "name", name, "node", ctxPayload.Location)
		if healing != nil && vmID > 0 && fromNode != "" && fromNode != ctxPayload.Location {
			trackVMMovement(ctx, stream, healing, HealingEvacuatedVM{
				VMID:     vmID,
				Name:     name,
				FromNode: fromNode,
				ToNode:   ctxPayload.Location,
			})
		}
	}
}

// handleClusterMember drives Phase D.2: auto-create a healing_events row
// when a node transitions offline/evacuated, and close it when the node
// returns online. The Incus lifecycle action on its own doesn't tell us
// the new status, so we also peek at metadata.context.status when present.
//
// When healing is nil (dev/test setup) we drop to DEBUG so operators still
// see the stream is live but nothing persists.
func handleClusterMember(ctx context.Context, stream ClusterStream, memberName string, md cluster.LifecycleMetadata, healing HealingTracker) {
	if healing == nil {
		slog.Debug("cluster member event (healing disabled)", "cluster", stream.Name, "member", memberName, "action", md.Action)
		return
	}

	var ctxPayload struct {
		Status string `json:"status,omitempty"`
	}
	_ = json.Unmarshal(md.Context, &ctxPayload)
	status := normaliseMemberStatus(ctxPayload.Status)

	switch status {
	case "offline", "evacuated":
		// Idempotent: if an auto row already exists for this node, don't
		// create a second one. Manual/chaos rows are left untouched so
		// admin-initiated drills don't collide with auto-detection.
		existing, err := healing.FindInProgressByNode(ctx, stream.ID, memberName, "auto")
		if err != nil {
			slog.Error("cluster member event: find in-progress", "cluster", stream.Name, "member", memberName, "error", err)
			return
		}
		if existing > 0 {
			slog.Debug("cluster member offline, healing already tracked", "cluster", stream.Name, "member", memberName, "healing_id", existing)
			return
		}
		id, err := healing.Create(ctx, stream.ID, memberName, "auto", nil)
		if err != nil {
			slog.Error("cluster member event: create healing", "cluster", stream.Name, "member", memberName, "error", err)
			return
		}
		slog.Info("cluster member offline, healing auto-tracked", "cluster", stream.Name, "member", memberName, "status", status, "healing_id", id)

	case "online":
		n, err := healing.CompleteByNode(ctx, stream.ID, memberName)
		if err != nil {
			slog.Error("cluster member event: complete healing", "cluster", stream.Name, "member", memberName, "error", err)
			return
		}
		if n > 0 {
			slog.Info("cluster member online, healing completed", "cluster", stream.Name, "member", memberName, "rows", n)
		}

	default:
		// No status context on this cluster-member-updated event — log at
		// DEBUG so diagnostics are possible without flooding prod logs.
		slog.Debug("cluster member event (no actionable status)", "cluster", stream.Name, "member", memberName, "action", md.Action, "status", ctxPayload.Status)
	}
}

// normaliseMemberStatus lowercases Incus member status strings (they vary
// between "Online" / "ONLINE" / "online" depending on client path) so the
// switch above can match consistently.
func normaliseMemberStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "online":
		return "online"
	case "offline":
		return "offline"
	case "evacuated":
		return "evacuated"
	}
	return ""
}

// trackVMMovement finds an in-progress healing row for the source node and
// appends the VM movement. Prefers auto > chaos > manual implicitly via
// FindInProgressByNode's newest-first ordering. Silent no-op when nothing
// active matches — partial healing tracking shouldn't break the event loop.
func trackVMMovement(ctx context.Context, stream ClusterStream, healing HealingTracker, mv HealingEvacuatedVM) {
	eventID, err := healing.FindInProgressByNode(ctx, stream.ID, mv.FromNode, "")
	if err != nil {
		slog.Warn("track vm movement: lookup", "cluster", stream.Name, "from", mv.FromNode, "error", err)
		return
	}
	if eventID == 0 {
		return
	}
	if err := healing.AppendEvacuatedVM(ctx, eventID, mv); err != nil {
		slog.Warn("track vm movement: append", "cluster", stream.Name, "event_id", eventID, "error", err)
		return
	}
	slog.Info("healing: recorded vm movement", "cluster", stream.Name, "event_id", eventID, "vm", mv.Name, "from", mv.FromNode, "to", mv.ToNode)
}
