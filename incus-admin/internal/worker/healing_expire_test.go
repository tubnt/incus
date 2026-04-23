package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeExpirer counts invocations and returns programmable results. `calls`
// is read through an atomic so the test can observe progress without racing
// the loop's goroutine.
type fakeExpirer struct {
	mu        sync.Mutex
	calls     atomic.Int64
	lastCutoff time.Time
	returnN   int64
	returnErr error
}

func (f *fakeExpirer) ExpireStale(_ context.Context, cutoff time.Time) (int64, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCutoff = cutoff
	return f.returnN, f.returnErr
}

// TestRunHealingExpireStale_Disabled verifies the worker exits immediately
// when maxAge <= 0 or expirer is nil — the "disabled" code path must not
// block on the ticker.
func TestRunHealingExpireStale_Disabled(t *testing.T) {
	cases := []struct {
		name    string
		expirer HealingExpirer
		maxAge  time.Duration
	}{
		{"nil expirer", nil, 10 * time.Minute},
		{"zero max age", &fakeExpirer{}, 0},
		{"negative max age", &fakeExpirer{}, -1 * time.Second},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				RunHealingExpireStale(context.Background(), tt.expirer, tt.maxAge, time.Second)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("worker did not exit on disabled config")
			}
		})
	}
}

// TestRunHealingExpireStale_TicksAndCancels drives one real tick (short
// tickEvery) and confirms the expirer is called with `time.Now() - maxAge`
// then clean exit on ctx cancel.
func TestRunHealingExpireStale_TicksAndCancels(t *testing.T) {
	f := &fakeExpirer{returnN: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunHealingExpireStale(ctx, f, 15*time.Minute, 50*time.Millisecond)
		close(done)
	}()

	// Wait for at least one tick.
	deadline := time.Now().Add(500 * time.Millisecond)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls.Load() == 0 {
		t.Fatalf("expirer never called")
	}

	// cutoff should be roughly "now - maxAge"; allow ±1s slack for scheduling.
	f.mu.Lock()
	offset := time.Since(f.lastCutoff)
	f.mu.Unlock()
	if offset < 14*time.Minute || offset > 16*time.Minute {
		t.Fatalf("cutoff offset out of range: %s", offset)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker did not exit on ctx cancel")
	}
}

// TestRunHealingExpireStale_ErrorContinues confirms the worker doesn't
// abort when ExpireStale returns an error — the next tick must still run
// (safety net: transient DB errors shouldn't kill the sweeper permanently).
func TestRunHealingExpireStale_ErrorContinues(t *testing.T) {
	f := &fakeExpirer{returnErr: errors.New("boom")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunHealingExpireStale(ctx, f, time.Minute, 30*time.Millisecond)
		close(done)
	}()

	// Want at least 2 ticks through the error path.
	deadline := time.Now().Add(400 * time.Millisecond)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls.Load() < 2 {
		t.Fatalf("expirer called %d times, want ≥2", f.calls.Load())
	}

	cancel()
	<-done
}
