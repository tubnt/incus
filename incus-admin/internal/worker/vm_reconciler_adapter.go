package worker

import (
	"context"
	"encoding/json"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

// ClusterSnapshotFromManager returns a ClusterSnapshotFn bound to the given
// cluster.Manager + project. Each pass walks Manager.List(), asks every
// cluster's Incus client for its instances, and returns the collected name
// sets. Per-cluster Incus errors are attached to the corresponding snapshot
// (snap.Err) so the worker can skip that cluster without aborting the pass —
// a single unhealthy cluster must not starve reconciliation of the rest.
//
// Only the given project is scanned. PLAN-020 keeps this single-project for
// MVP; reconciling multiple projects means taking the union of instance
// names, which changes the "DB has it, Incus doesn't" invariant if the same
// VM name legally exists in two projects.
func ClusterSnapshotFromManager(mgr *cluster.Manager, project string) ClusterSnapshotFn {
	return func(ctx context.Context) []ClusterSnapshot {
		clients := mgr.List()
		out := make([]ClusterSnapshot, 0, len(clients))
		for _, c := range clients {
			snap := ClusterSnapshot{
				ID:   mgr.IDByName(c.Name),
				Name: c.Name,
			}
			raw, err := c.GetInstances(ctx, project)
			if err != nil {
				snap.Err = err
				out = append(out, snap)
				continue
			}
			names := make(map[string]struct{}, len(raw))
			for _, inst := range raw {
				var brief struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(inst, &brief); err == nil && brief.Name != "" {
					names[brief.Name] = struct{}{}
				}
			}
			snap.InstanceNames = names
			out = append(out, snap)
		}
		return out
	}
}
