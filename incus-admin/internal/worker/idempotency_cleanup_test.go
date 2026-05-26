package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIdempCleaner 实现 IdempotencyCleaner；通过 atomic 计数 + mu 保存最后 cutoff。
type fakeIdempCleaner struct {
	mu         sync.Mutex
	calls      atomic.Int64
	lastCutoff time.Time
	returnN    int64
	returnErr  error
}

func (f *fakeIdempCleaner) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCutoff = cutoff
	return f.returnN, f.returnErr
}

// TestRunIdempotencyCleanup_NilCleanerExits：nil cleaner 立刻返回，不阻塞。
func TestRunIdempotencyCleanup_NilCleanerExits(t *testing.T) {
	done := make(chan struct{})
	go func() {
		RunIdempotencyCleanup(context.Background(), nil, 10*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("nil cleaner should exit immediately")
	}
}

// TestRunIdempotencyCleanup_TicksThenStops：注入快速 ticker，确认 DeleteOlderThan
// 至少调用一次，cutoff = now-24h，ctx 取消干净退出。
func TestRunIdempotencyCleanup_TicksThenStops(t *testing.T) {
	f := &fakeIdempCleaner{returnN: 5}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		// 50ms ticker → 250ms 内应跑若干次（initial 7min 但 ticker 早到）
		RunIdempotencyCleanup(ctx, f, 50*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls.Load() == 0 {
		t.Fatalf("DeleteOlderThan never called")
	}

	f.mu.Lock()
	cutoff := f.lastCutoff
	f.mu.Unlock()
	// cutoff 应在 (now-24h-1s, now-24h+1s) 区间，验证 TTL=24h
	want := time.Now().Add(-24 * time.Hour)
	delta := cutoff.Sub(want)
	if delta < -1*time.Second || delta > 1*time.Second {
		t.Errorf("cutoff drift %v outside ±1s of now-24h", delta)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker did not exit on ctx cancel")
	}
}

// TestRunIdempotencyCleanup_ErrorDoesNotKillLoop：cleaner 返错 worker 仍继续 tick。
func TestRunIdempotencyCleanup_ErrorDoesNotKillLoop(t *testing.T) {
	f := &fakeIdempCleaner{returnErr: errors.New("simulated db down")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go RunIdempotencyCleanup(ctx, f, 30*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls.Load() < 2 {
		t.Fatalf("error path should keep ticker alive; calls=%d want >=2", f.calls.Load())
	}
}

// TestRunIdempotencyCleanupOnce_LogsAndReturns：单次直调路径走通；
// 无 panic + cleaner 被调一次。
func TestRunIdempotencyCleanupOnce_LogsAndReturns(t *testing.T) {
	f := &fakeIdempCleaner{returnN: 3}
	runIdempotencyCleanupOnce(context.Background(), f)
	if f.calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", f.calls.Load())
	}
}
