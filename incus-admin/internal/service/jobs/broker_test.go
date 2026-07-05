package jobs

import (
	"sync"
	"testing"
	"time"

	"github.com/incuscloud/incus-admin/internal/model"
)

func TestBroker_PublishToSubscribers(t *testing.T) {
	b := NewBroker()
	ch1, c1 := b.Subscribe(42)
	defer c1()
	ch2, c2 := b.Subscribe(42)
	defer c2()

	step := model.ProvisioningJobStep{Seq: 0, Name: "submit_instance", Status: model.StepStatusRunning}
	b.Publish(StepEvent{JobID: 42, Step: step})

	for _, ch := range []<-chan StepEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Step.Name != "submit_instance" {
				t.Fatalf("got step name %q, want submit_instance", ev.Step.Name)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(7)
	cancel()

	// P1-3：Unsubscribe 只从 map 删除、不 close(channel)。取消后 Publish 既不
	// panic 也不再投递到该 channel（订阅已从路由表移除）。
	b.Publish(StepEvent{JobID: 7, Step: model.ProvisioningJobStep{Seq: 0}})

	// channel 未 close 且不再收事件 → 读应阻塞至超时（而非立即拿到 zero value）。
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("cancelled subscriber should not receive events, got seq=%d", ev.Step.Seq)
		}
		t.Fatal("channel should NOT be closed by cancel (P1-3), but recv returned ok=false")
	case <-time.After(200 * time.Millisecond):
		// expected：未 close、无投递，读阻塞。
	}
}

// TestBroker_CancelDuringPublishNoPanic 复现 P1-3 竞态：并发 cancel + Publish。
// 原版 cancel close(channel)，Publish 释锁后向已关闭 channel 写 → panic；
// 新版 cancel 不 close，无论时序都安全。
func TestBroker_CancelDuringPublishNoPanic(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		b := NewBroker()
		var wg sync.WaitGroup
		// 若干订阅者，并发取消
		cancels := make([]func(), 0, 8)
		for i := 0; i < 8; i++ {
			_, cancel := b.Subscribe(1)
			cancels = append(cancels, cancel)
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				b.Publish(StepEvent{JobID: 1, Step: model.ProvisioningJobStep{Seq: i}})
			}
		}()
		go func() {
			defer wg.Done()
			for _, c := range cancels {
				c()
			}
		}()
		wg.Wait()
	}
}

func TestBroker_RoutesByJobID(t *testing.T) {
	b := NewBroker()
	chA, cancelA := b.Subscribe(1)
	defer cancelA()
	chB, cancelB := b.Subscribe(2)
	defer cancelB()

	b.Publish(StepEvent{JobID: 1, Step: model.ProvisioningJobStep{Seq: 0, Name: "job-1-step"}})

	select {
	case ev := <-chA:
		if ev.JobID != 1 {
			t.Fatalf("subscriber A got jobID=%d", ev.JobID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("A did not receive its event")
	}

	select {
	case <-chB:
		t.Fatal("subscriber B should not receive job 1's event")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestBroker_BufferFullDropsRatherThanBlock(t *testing.T) {
	b := NewBroker()
	_, cancel := b.Subscribe(99)
	defer cancel()

	// 灌满 32 buffer + 多余 16；Publish 必须不阻塞
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 48; i++ {
			b.Publish(StepEvent{JobID: 99, Step: model.ProvisioningJobStep{Seq: i}})
		}
		close(done)
	}()

	select {
	case <-done:
		// good — 没阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked when buffer full; should drop instead")
	}
	wg.Wait()
}
