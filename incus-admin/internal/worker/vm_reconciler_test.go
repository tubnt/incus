package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

// fakeVMRepo captures MarkGone calls and feeds fixed rows from
// ListActiveForReconcile. cutoff is exposed so tests can assert the
// 10s buffer is actually applied.
type fakeVMRepo struct {
	mu           sync.Mutex
	rows         map[int64][]model.VM // clusterID → rows
	lastCutoff   time.Time
	markedGone   []int64
	markGoneErr  error
	listErr      map[int64]error
}

func (f *fakeVMRepo) ListActiveForReconcile(_ context.Context, clusterID int64, cutoff time.Time) ([]model.VM, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCutoff = cutoff
	if err, ok := f.listErr[clusterID]; ok {
		return nil, err
	}
	return f.rows[clusterID], nil
}

func (f *fakeVMRepo) MarkGone(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markGoneErr != nil {
		return f.markGoneErr
	}
	f.markedGone = append(f.markedGone, id)
	return nil
}

type fakeIPRepo struct {
	mu       sync.Mutex
	released []string
	err      error
}

func (f *fakeIPRepo) Release(_ context.Context, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.released = append(f.released, ip)
	return nil
}

type fakeAuditor struct {
	mu      sync.Mutex
	entries []struct {
		action string
		target int64
	}
}

func (f *fakeAuditor) Log(_ context.Context, _ *int64, action, _ string, target int64, _ any, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, struct {
		action string
		target int64
	}{action, target})
}

// ptr returns a pointer to the given string — helper for VM.IP.
func ptr(s string) *string { return &s }

// vm constructs a minimal model.VM for tests.
func vm(id int64, clusterID int64, name string, ip string, createdMinutesAgo int) model.VM {
	v := model.VM{
		ID:        id,
		ClusterID: clusterID,
		Name:      name,
		Status:    "running",
		CreatedAt: time.Now().Add(-time.Duration(createdMinutesAgo) * time.Minute),
	}
	if ip != "" {
		v.IP = ptr(ip)
	}
	return v
}

func TestRunReconcileOnce(t *testing.T) {
	cases := []struct {
		name            string
		snapshots       []ClusterSnapshot
		dbRows          map[int64][]model.VM
		listErrs        map[int64]error
		wantGone        []int64
		wantIPReleased  []string
		wantAuditActions []string
	}{
		{
			name: "all alive: no drift",
			snapshots: []ClusterSnapshot{{
				ID: 1, Name: "a",
				InstanceNames: map[string]struct{}{"vm-1": {}, "vm-2": {}},
			}},
			dbRows: map[int64][]model.VM{
				1: {vm(1, 1, "vm-1", "1.1.1.1", 5), vm(2, 1, "vm-2", "1.1.1.2", 5)},
			},
			wantGone:       nil,
			wantIPReleased: nil,
		},
		{
			name: "db has, incus lost: mark gone + release ip + audit",
			snapshots: []ClusterSnapshot{{
				ID: 1, Name: "a",
				InstanceNames: map[string]struct{}{"vm-1": {}},
			}},
			dbRows: map[int64][]model.VM{
				1: {vm(1, 1, "vm-1", "1.1.1.1", 5), vm(2, 1, "vm-2", "1.1.1.2", 5)},
			},
			wantGone:         []int64{2},
			wantIPReleased:   []string{"1.1.1.2"},
			wantAuditActions: []string{"vm.reconcile.gone"},
		},
		{
			name: "incus unreachable: skip cluster, no drift",
			snapshots: []ClusterSnapshot{{
				ID: 1, Name: "a",
				Err: errors.New("connection refused"),
			}},
			dbRows: map[int64][]model.VM{
				1: {vm(1, 1, "vm-1", "1.1.1.1", 5)},
			},
			wantGone:       nil,
			wantIPReleased: nil,
		},
		{
			name: "vm created inside buffer: excluded from diff by cutoff",
			snapshots: []ClusterSnapshot{{
				ID: 1, Name: "a",
				InstanceNames: map[string]struct{}{}, // incus empty
			}},
			// Fake repo obeys the cutoff: VM created 0 min ago (just now)
			// would be filtered out in real repo. Emulated here by returning
			// only the older one, mimicking cutoff < created_at behavior.
			dbRows: map[int64][]model.VM{
				1: {vm(1, 1, "vm-old", "2.2.2.2", 10)},
			},
			wantGone:       []int64{1},
			wantIPReleased: []string{"2.2.2.2"},
			// vm-new isn't in dbRows → emulates cutoff filter excluding it.
			wantAuditActions: []string{"vm.reconcile.gone"},
		},
		{
			name: "db without incus presence, vm has no ip: no ip release attempted",
			snapshots: []ClusterSnapshot{{
				ID: 1, Name: "a",
				InstanceNames: map[string]struct{}{},
			}},
			dbRows: map[int64][]model.VM{
				1: {vm(5, 1, "vm-no-ip", "", 5)},
			},
			wantGone:         []int64{5},
			wantIPReleased:   nil,
			wantAuditActions: []string{"vm.reconcile.gone"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vmRepo := &fakeVMRepo{rows: tc.dbRows, listErr: tc.listErrs}
			ipRepo := &fakeIPRepo{}
			aud := &fakeAuditor{}

			snapshot := func(_ context.Context) []ClusterSnapshot { return tc.snapshots }

			runReconcileOnce(
				context.Background(),
				VMReconcilerConfig{
					Interval: time.Minute, CreateBuffer: 10 * time.Second,
					InitialDelay: time.Second, DriftAlertThreshold: 5,
				},
				snapshot, vmRepo, ipRepo, aud,
			)

			// Assert marked-gone set matches.
			if !equalInts(vmRepo.markedGone, tc.wantGone) {
				t.Errorf("markedGone = %v; want %v", vmRepo.markedGone, tc.wantGone)
			}
			if !equalStrs(ipRepo.released, tc.wantIPReleased) {
				t.Errorf("released IPs = %v; want %v", ipRepo.released, tc.wantIPReleased)
			}
			// Assert audit actions match (compare action strings only — order
			// is deterministic given a single cluster + sequential iteration).
			got := make([]string, len(aud.entries))
			for i, e := range aud.entries {
				got[i] = e.action
			}
			if !equalStrs(got, tc.wantAuditActions) {
				t.Errorf("audit actions = %v; want %v", got, tc.wantAuditActions)
			}
		})
	}
}

// TestRunReconcileOnce_CutoffApplied checks runReconcileOnce passes a cutoff
// that reflects the configured CreateBuffer. Prevents a regression where
// the 10s buffer silently becomes 0 (which would mark just-provisioned
// rows as gone).
func TestRunReconcileOnce_CutoffApplied(t *testing.T) {
	vmRepo := &fakeVMRepo{rows: map[int64][]model.VM{}}
	ipRepo := &fakeIPRepo{}
	snapshot := func(_ context.Context) []ClusterSnapshot {
		return []ClusterSnapshot{{ID: 1, Name: "a", InstanceNames: map[string]struct{}{}}}
	}
	before := time.Now()
	runReconcileOnce(context.Background(),
		VMReconcilerConfig{CreateBuffer: 10 * time.Second, DriftAlertThreshold: 5},
		snapshot, vmRepo, ipRepo, nil)
	// cutoff must be at least 9s before "before" (allow small scheduling slack).
	gap := before.Sub(vmRepo.lastCutoff)
	if gap < 9*time.Second || gap > 12*time.Second {
		t.Errorf("cutoff gap = %s; want ~10s", gap)
	}
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
