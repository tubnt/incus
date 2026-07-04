package jobs

import (
	"sync"

	"github.com/incuscloud/incus-admin/internal/model"
)

// StepEvent 是 SSE 推送给客户端的最小载荷。worker 在 AppendStep / UpdateStep
// 之后调 Broker.Publish；SSE handler 通过 Subscribe 拿一个只读 channel 收事件。
type StepEvent struct {
	JobID    int64                      `json:"job_id"`
	Step     model.ProvisioningJobStep  `json:"step"`
	Terminal bool                       `json:"terminal"` // job 已到终态（succeeded/failed/partial），SSE 收到后应 close
	Status   string                     `json:"status,omitempty"` // job 终态时填写
}

// Broker 是 job_id → []chan StepEvent 的薄路由。订阅者断连后 Unsubscribe 释放
// channel。Publish 永远 non-blocking（满 buffer 直接丢，订阅者错过的 step 通过
// SSE 重连 Last-Event-ID 从 DB 重放补齐）。
//
// Session-2 F-40 / PLAN-051 §2-K：用 RWMutex —— Publish 走 RLock 让多 publisher
// 与读 SubscriberCount 不互锁；Subscribe / Unsubscribe 走 Lock。原版 Mutex 在
// 多 SSE 订阅者 + 多 publisher 时序列化所有路径。
type Broker struct {
	mu   sync.RWMutex
	subs map[int64]map[int]chan StepEvent
	nextID int
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[int64]map[int]chan StepEvent)}
}

// Subscribe 返回一个 buffered chan + 取消函数。Buffer 32 够覆盖一次完整 job
// 的全部 step（vm.create 5–6 步），订阅者不需要立刻消费也不会阻塞 publisher。
func (b *Broker) Subscribe(jobID int64) (<-chan StepEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan StepEvent, 32)
	if b.subs[jobID] == nil {
		b.subs[jobID] = make(map[int]chan StepEvent)
	}
	id := b.nextID
	b.nextID++
	b.subs[jobID][id] = ch

	// P1-3：Unsubscribe 只从 map 删除，绝不 close(channel)。
	// 原版 close(c) 与 Publish 存在竞态：Publish 在 RLock 内拷贝 channel 到本地
	// 切片、释锁后再发送；若此刻 cancel() 抢到写锁 close 掉该 channel，Publish
	// 的 `ch <- ev` 就会向已关闭 channel 写入而 panic。改为不 close —— 订阅者
	// 靠 Terminal 事件或请求 ctx 取消退出读循环，未 close 的 channel 无引用后由
	// GC 回收，不会泄漏。
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if m := b.subs[jobID]; m != nil {
			delete(m, id)
			if len(m) == 0 {
				delete(b.subs, jobID)
			}
		}
	}
	return ch, cancel
}

// Publish 把事件推给所有订阅者。Buffer 满时直接丢（non-blocking），由 SSE 端
// reconnect 时通过 Last-Event-ID 从 DB 拉缺失 step 补齐。
//
// Session-2 F-40：先 RLock 拷贝订阅 channel 到本地切片，释锁后再发——避免持
// 写锁时被慢消费者拖住，多 publisher 不互相阻塞。
func (b *Broker) Publish(ev StepEvent) {
	b.mu.RLock()
	channels := make([]chan StepEvent, 0, len(b.subs[ev.JobID]))
	for _, ch := range b.subs[ev.JobID] {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()
	for _, ch := range channels {
		select {
		case ch <- ev:
		default:
			// Drop on full buffer — DB has authoritative copy, client resyncs on reconnect.
		}
	}
}

// SubscriberCountForUser 给 SSE handler 做 per-user conn cap 用。需要外部传入
// jobID→userID 映射；这里直接 sum 所有 jobID 的订阅数（Broker 不知道 user）。
// per-user 限流由 handler 层用独立 map 实现，Broker 只负责按 job 路由。
func (b *Broker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	total := 0
	for _, m := range b.subs {
		total += len(m)
	}
	return total
}
