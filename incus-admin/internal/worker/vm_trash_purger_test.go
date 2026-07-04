package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/cluster"
	"github.com/incuscloud/incus-admin/internal/model"
)

// TestIsAlreadyGoneErr 锁定 OPS-052 收窄后的语义：只有 Incus 明确的"实例不存在"
// 措辞才算已删，无关的 project/network/storage not found 不得被吞。
func TestIsAlreadyGoneErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"instance not found", errors.New("incus error: Instance not found"), true},
		{"no such object", errors.New("Error: No such object: vm-x"), true},
		{"bare not found is NOT swallowed", errors.New("network not found"), false},
		{"project not found is NOT swallowed", errors.New("Project not found"), false},
		{"storage pool not found is NOT swallowed", errors.New("storage pool \"default\" not found"), false},
		{"unrelated error", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAlreadyGoneErr(tc.err); got != tc.want {
				t.Errorf("isAlreadyGoneErr(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

type fakeTrashRepo struct {
	mu      sync.Mutex
	rows    []model.VM
	served  bool
	deleted chan int64
}

func (f *fakeTrashRepo) ListTrashedBefore(_ context.Context, _ time.Time) ([]model.VM, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.served {
		return nil, nil
	}
	f.served = true
	return f.rows, nil
}

func (f *fakeTrashRepo) Delete(_ context.Context, id int64) error {
	f.deleted <- id
	return nil
}

type fakeClusterResolver struct {
	name string
	id   int64
}

func (f *fakeClusterResolver) List() []*cluster.Client { return []*cluster.Client{{Name: f.name}} }
func (f *fakeClusterResolver) IDByName(name string) int64 {
	if name == f.name {
		return f.id
	}
	return 0
}

type reclaimRecord struct {
	vm      model.VM
	cluster string
	project string
}

// TestRunVMTrashPurger_ReclaimThenDelete 验证 purge 成功后：先调用 reclaimFn 释放
// 关联资源，再落 DB deleted（OPS-052 P0-3 / P1-5 收口）。
func TestRunVMTrashPurger_ReclaimThenDelete(t *testing.T) {
	ip := "10.0.0.5"
	repo := &fakeTrashRepo{
		rows:    []model.VM{{ID: 42, Name: "vm-x", ClusterID: 7, IP: &ip}},
		deleted: make(chan int64, 1),
	}
	clusters := &fakeClusterResolver{name: "ph-c0", id: 7}

	var mu sync.Mutex
	var reclaimed []reclaimRecord
	reclaimFn := func(_ context.Context, vm model.VM, clusterName, project string) error {
		mu.Lock()
		reclaimed = append(reclaimed, reclaimRecord{vm: vm, cluster: clusterName, project: project})
		mu.Unlock()
		return nil
	}
	purgeFn := func(_ context.Context, _, _, _ string) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunVMTrashPurger(ctx, repo, clusters, purgeFn, reclaimFn, time.Second, 5*time.Millisecond)

	select {
	case id := <-repo.deleted:
		if id != 42 {
			t.Fatalf("deleted id = %d; want 42", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for db delete")
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(reclaimed) != 1 {
		t.Fatalf("reclaim called %d times; want 1", len(reclaimed))
	}
	r := reclaimed[0]
	if r.vm.ID != 42 || r.cluster != "ph-c0" || r.project != "customers" {
		t.Errorf("reclaim got vm=%d cluster=%q project=%q; want 42/ph-c0/customers", r.vm.ID, r.cluster, r.project)
	}
	if r.vm.IP == nil || *r.vm.IP != ip {
		t.Errorf("reclaim vm.IP = %v; want %q", r.vm.IP, ip)
	}
}

// TestRunVMTrashPurger_PurgeErrorSkipsReclaim 验证非"已删"purge 错误时不 reclaim、
// 不落 deleted（下个周期重试），避免资源被过早释放。
func TestRunVMTrashPurger_PurgeErrorSkipsReclaim(t *testing.T) {
	repo := &fakeTrashRepo{
		rows:    []model.VM{{ID: 1, Name: "vm-y", ClusterID: 7}},
		deleted: make(chan int64, 1),
	}
	clusters := &fakeClusterResolver{name: "ph-c0", id: 7}

	var reclaimCalled int32
	reclaimFn := func(_ context.Context, _ model.VM, _, _ string) error {
		reclaimCalled++
		return nil
	}
	purgeFn := func(_ context.Context, _, _, _ string) error { return errors.New("connection refused") }

	ctx, cancel := context.WithCancel(context.Background())
	go RunVMTrashPurger(ctx, repo, clusters, purgeFn, reclaimFn, time.Second, 5*time.Millisecond)

	select {
	case <-repo.deleted:
		cancel()
		t.Fatal("db delete happened despite purge error")
	case <-time.After(60 * time.Millisecond):
		// 预期：无 delete。
	}
	cancel()
	if reclaimCalled != 0 {
		t.Errorf("reclaim called %d times on purge error; want 0", reclaimCalled)
	}
}
