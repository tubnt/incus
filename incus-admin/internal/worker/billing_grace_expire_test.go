package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGracer struct {
	calls atomic.Int32
	stats BillingGraceStats
	err   error
}

func (f *fakeGracer) ExpireGrace(_ context.Context) (BillingGraceStats, error) {
	f.calls.Add(1)
	return f.stats, f.err
}

func TestRunBillingGraceExpire_NilGracer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunBillingGraceExpire(ctx, nil, 50*time.Millisecond, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("nil gracer should make worker return immediately")
	}
}

func TestRunBillingGraceExpire_FirstDelayThenTick(t *testing.T) {
	// firstDelay=50ms 之前不应跑；之后立即跑 + 之后每 tick。
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	fake := &fakeGracer{}
	done := make(chan struct{})
	go func() {
		RunBillingGraceExpire(ctx, fake, 100*time.Millisecond, 50*time.Millisecond)
		close(done)
	}()

	// 25ms 时尚未过 firstDelay
	time.Sleep(25 * time.Millisecond)
	if got := fake.calls.Load(); got != 0 {
		t.Fatalf("expected 0 calls before first delay, got %d", got)
	}
	<-done
	if got := fake.calls.Load(); got < 2 {
		t.Fatalf("expected initial + at least 1 tick = >=2 calls, got %d", got)
	}
}

func TestRunBillingGraceExpire_ContextCancelDuringFirstDelay(t *testing.T) {
	// firstDelay 期间 ctx 取消应让 worker 立刻退出（不跑 runOnce）。
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeGracer{}
	done := make(chan struct{})
	go func() {
		RunBillingGraceExpire(ctx, fake, 1*time.Second, 5*time.Second)
		close(done)
	}()
	// 给 worker 一点启动时间，然后取消
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker should exit on ctx cancel during firstDelay")
	}
	if got := fake.calls.Load(); got != 0 {
		t.Fatalf("expected 0 calls when cancelled during firstDelay, got %d", got)
	}
}
