package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCharger 计 ChargeDue 被调次数；可注入返回值 / err。
type fakeCharger struct {
	calls atomic.Int32
	stats BillingChargeStats
	err   error
}

func (f *fakeCharger) ChargeDue(_ context.Context) (BillingChargeStats, error) {
	f.calls.Add(1)
	return f.stats, f.err
}

func TestRunBillingDailyCharger_NilCharger(t *testing.T) {
	// nil charger → 立即 return，不阻塞。1s 超时 ctx 保护测试不挂死。
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		RunBillingDailyCharger(ctx, nil, 100*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		// expected
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RunBillingDailyCharger with nil charger should return immediately")
	}
}

func TestRunBillingDailyCharger_StartupCatchupAndTick(t *testing.T) {
	// 期望：启动立即跑一次 catchup；ticker 触发后再跑。
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	fake := &fakeCharger{stats: BillingChargeStats{Processed: 3, Paid: 2, Suspended: 1}}
	done := make(chan struct{})
	go func() {
		RunBillingDailyCharger(ctx, fake, 100*time.Millisecond)
		close(done)
	}()

	// 等 ctx 到期 + worker stop
	<-done
	// 启动 catchup (1) + 至少 1 次 tick → >= 2
	got := fake.calls.Load()
	if got < 2 {
		t.Fatalf("expected at least 2 ChargeDue calls (startup + tick), got %d", got)
	}
}

func TestRunBillingDailyCharger_ErrorDoesNotStop(t *testing.T) {
	// ChargeDue 返 err 不应让 worker 退出。
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	fake := &fakeCharger{err: errors.New("db down")}
	done := make(chan struct{})
	go func() {
		RunBillingDailyCharger(ctx, fake, 80*time.Millisecond)
		close(done)
	}()
	<-done
	if got := fake.calls.Load(); got < 2 {
		t.Fatalf("worker should keep ticking despite errors, got %d calls", got)
	}
}
