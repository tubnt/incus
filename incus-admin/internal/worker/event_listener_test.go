package worker

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/incuscloud/incus-admin/internal/cluster"
)

// fakeEventRepo tracks MarkGoneByName / UpdateNodeByName / LookupForEvent
// invocations. Matches EventListenerRepo so dispatchEvent is exercised end
// to end without a live database.
type fakeEventRepo struct {
	mu            sync.Mutex
	gone          []string
	updatedNode   map[string]string
	lookupID      int64
	lookupNode    string
	lookupErr     error
	lookupCalls   []string
}

func (f *fakeEventRepo) MarkGoneByName(_ context.Context, _ int64, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone = append(f.gone, name)
	return nil
}

func (f *fakeEventRepo) UpdateNodeByName(_ context.Context, _ int64, name, node string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updatedNode == nil {
		f.updatedNode = map[string]string{}
	}
	f.updatedNode[name] = node
	return nil
}

func (f *fakeEventRepo) LookupForEvent(_ context.Context, _ int64, name string) (int64, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls = append(f.lookupCalls, name)
	return f.lookupID, f.lookupNode, f.lookupErr
}

// fakeHealing mirrors HealingTracker so the Phase D.2/D.3 paths can be
// asserted independently of the repository layer.
type fakeHealing struct {
	mu            sync.Mutex
	created       []createArgs
	completed     []string
	appended      []appendArgs
	findByNodeRet int64
	findErr       error
}

type createArgs struct {
	clusterID int64
	node      string
	trigger   string
	actorID   *int64
}

type appendArgs struct {
	eventID int64
	vm      HealingEvacuatedVM
}

func (f *fakeHealing) FindInProgressByNode(_ context.Context, _ int64, _, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.findErr != nil {
		return 0, f.findErr
	}
	return f.findByNodeRet, nil
}

func (f *fakeHealing) Create(_ context.Context, clusterID int64, node, trigger string, actorID *int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, createArgs{clusterID, node, trigger, actorID})
	return int64(len(f.created)), nil
}

func (f *fakeHealing) AppendEvacuatedVM(_ context.Context, eventID int64, vm HealingEvacuatedVM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appended = append(f.appended, appendArgs{eventID, vm})
	return nil
}

func (f *fakeHealing) CompleteByNode(_ context.Context, _ int64, node string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, node)
	return 1, nil
}

// makeLifecycleEvent constructs a wire-compatible cluster.Event with the
// given action + source + optional context JSON. Returned as cluster.Event
// so the same dispatcher the runtime uses can be called directly.
func makeLifecycleEvent(action, source string, ctx map[string]any) cluster.Event {
	md := map[string]any{
		"action": action,
		"source": source,
	}
	if ctx != nil {
		c, _ := json.Marshal(ctx)
		md["context"] = json.RawMessage(c)
	}
	mdBytes, _ := json.Marshal(md)
	return cluster.Event{
		Type:     "lifecycle",
		Metadata: mdBytes,
	}
}

func TestDispatchInstanceDeletedMarksGone(t *testing.T) {
	ctx := context.Background()
	repo := &fakeEventRepo{}
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("instance-deleted", "/1.0/instances/vm-42", nil)

	dispatchEvent(ctx, stream, ev, repo, nil)

	if len(repo.gone) != 1 || repo.gone[0] != "vm-42" {
		t.Fatalf("expected vm-42 marked gone, got %v", repo.gone)
	}
}

func TestDispatchInstanceUpdatedAppendsHealing(t *testing.T) {
	ctx := context.Background()
	repo := &fakeEventRepo{lookupID: 99, lookupNode: "nodeA"}
	healing := &fakeHealing{findByNodeRet: 7}
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("instance-updated", "/1.0/instances/vm-99", map[string]any{"location": "nodeB"})

	dispatchEvent(ctx, stream, ev, repo, healing)

	if repo.updatedNode["vm-99"] != "nodeB" {
		t.Fatalf("expected node update to nodeB, got %v", repo.updatedNode)
	}
	if len(healing.appended) != 1 {
		t.Fatalf("expected 1 append, got %d", len(healing.appended))
	}
	app := healing.appended[0]
	if app.eventID != 7 || app.vm.FromNode != "nodeA" || app.vm.ToNode != "nodeB" || app.vm.VMID != 99 {
		t.Fatalf("unexpected append args: %+v", app)
	}
}

func TestDispatchInstanceUpdatedNoHealingEventSkipsAppend(t *testing.T) {
	ctx := context.Background()
	repo := &fakeEventRepo{lookupID: 99, lookupNode: "nodeA"}
	healing := &fakeHealing{findByNodeRet: 0}
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("instance-updated", "/1.0/instances/vm-99", map[string]any{"location": "nodeB"})

	dispatchEvent(ctx, stream, ev, repo, healing)

	if repo.updatedNode["vm-99"] != "nodeB" {
		t.Fatalf("node update expected")
	}
	if len(healing.appended) != 0 {
		t.Fatalf("expected no append, got %d", len(healing.appended))
	}
}

func TestClusterMemberOfflineCreatesAutoHealing(t *testing.T) {
	ctx := context.Background()
	healing := &fakeHealing{findByNodeRet: 0} // nothing active yet
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("cluster-member-updated", "/1.0/cluster/members/node1", map[string]any{"status": "offline"})

	dispatchEvent(ctx, stream, ev, &fakeEventRepo{}, healing)

	if len(healing.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(healing.created))
	}
	c := healing.created[0]
	if c.node != "node1" || c.trigger != "auto" || c.actorID != nil {
		t.Fatalf("unexpected create args: %+v", c)
	}
}

func TestClusterMemberOfflineIdempotent(t *testing.T) {
	ctx := context.Background()
	healing := &fakeHealing{findByNodeRet: 5} // already in-progress auto row
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("cluster-member-updated", "/1.0/cluster/members/node1", map[string]any{"status": "offline"})

	dispatchEvent(ctx, stream, ev, &fakeEventRepo{}, healing)

	if len(healing.created) != 0 {
		t.Fatalf("expected no new create when row already exists, got %d", len(healing.created))
	}
}

func TestClusterMemberOnlineCompletesHealing(t *testing.T) {
	ctx := context.Background()
	healing := &fakeHealing{}
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("cluster-member-updated", "/1.0/cluster/members/node1", map[string]any{"status": "online"})

	dispatchEvent(ctx, stream, ev, &fakeEventRepo{}, healing)

	if len(healing.completed) != 1 || healing.completed[0] != "node1" {
		t.Fatalf("expected completion for node1, got %v", healing.completed)
	}
}

func TestClusterMemberNoHealingDisablesPath(t *testing.T) {
	ctx := context.Background()
	stream := ClusterStream{ID: 1, Name: "c1"}
	ev := makeLifecycleEvent("cluster-member-updated", "/1.0/cluster/members/node1", map[string]any{"status": "offline"})

	// healing=nil should be a silent no-op (no panic, no side effects).
	dispatchEvent(ctx, stream, ev, &fakeEventRepo{}, nil)
}
