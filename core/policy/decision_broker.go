package policy

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// DecisionBroker fans Decision values out to in-process subscribers without
// putting back-pressure on the emit path. The contract is "drop-on-full":
// a slow subscriber loses messages instead of blocking publishers, and is
// auto-unsubscribed after `maxConsecutiveDrops` consecutive drops so the
// broker doesn't accumulate dead channels. The dashboard WebSocket handler
// is the primary consumer; the broker is process-local so a multi-replica
// deployment would need a NATS-fronted variant — out of scope today.
type DecisionBroker struct {
	mu       sync.RWMutex
	subs     map[uint64]*decisionSubscriber
	nextID   atomic.Uint64
	closed   atomic.Bool
	closeOne sync.Once
}

type decisionSubscriber struct {
	id        uint64
	ch        chan Decision
	drops     int
	closeOnce sync.Once
}

// maxConsecutiveDrops bounds how many consecutive Publish-time drops the
// broker tolerates before auto-evicting the subscriber. Bumped above 1 so a
// transient burst doesn't kick a healthy subscriber that briefly fell behind.
const maxConsecutiveDrops = 3

// NewDecisionBroker returns a ready-to-use broker with no subscribers.
func NewDecisionBroker() *DecisionBroker {
	return &DecisionBroker{subs: make(map[uint64]*decisionSubscriber)}
}

// Subscribe registers a new subscriber and returns a receive-only channel +
// an idempotent unsubscribe function. bufferSize must be >0; the broker
// panics on non-positive values to surface programmer errors at call time.
func (b *DecisionBroker) Subscribe(bufferSize int) (<-chan Decision, func()) {
	if bufferSize <= 0 {
		panic("policy: DecisionBroker.Subscribe requires bufferSize > 0")
	}
	id := b.nextID.Add(1)
	sub := &decisionSubscriber{id: id, ch: make(chan Decision, bufferSize)}

	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		// Closed broker → return an immediately-closed channel + a no-op
		// unsubscribe so callers don't need to special-case shutdown.
		close(sub.ch)
		return sub.ch, func() {}
	}
	b.subs[id] = sub
	b.mu.Unlock()

	var unsubOnce sync.Once
	return sub.ch, func() {
		unsubOnce.Do(func() { b.unsubscribe(id) })
	}
}

// Publish delivers the decision to all current subscribers without blocking.
// Each subscriber whose buffered channel is full is recorded as a drop; after
// maxConsecutiveDrops consecutive drops the subscriber is auto-evicted and
// its channel closed so receivers wake up and notice the disconnect. ctx is
// reserved for future tracing hooks; cancellation does not abort delivery.
func (b *DecisionBroker) Publish(_ context.Context, d Decision) {
	if b.closed.Load() {
		return
	}

	// Hold the registry lock while attempting non-blocking sends. Closing a
	// subscriber channel also requires the same lock, so this prevents a
	// concurrent unsubscribe/eviction from closing a channel between a snapshot
	// and the send below.
	b.mu.Lock()
	if b.closed.Load() || len(b.subs) == 0 {
		b.mu.Unlock()
		return
	}

	evicted := make([]*decisionSubscriber, 0)
	for id, sub := range b.subs {
		select {
		case sub.ch <- d:
			sub.drops = 0
		default:
			sub.drops++
			if sub.drops >= maxConsecutiveDrops {
				delete(b.subs, id)
				evicted = append(evicted, sub)
			}
		}
	}
	b.mu.Unlock()

	if len(evicted) > 0 {
		closeEvictedSubscribers(evicted)
	}
}

// SubscriberCount returns the number of currently registered subscribers.
// Useful for health checks and tests.
func (b *DecisionBroker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Close evicts all subscribers and rejects future Subscribe calls. Safe to
// call multiple times.
func (b *DecisionBroker) Close() {
	b.closeOne.Do(func() {
		b.closed.Store(true)
		b.mu.Lock()
		subs := b.subs
		b.subs = make(map[uint64]*decisionSubscriber)
		b.mu.Unlock()
		for _, sub := range subs {
			sub.closeOnce.Do(func() { close(sub.ch) })
		}
	})
}

func (b *DecisionBroker) unsubscribe(id uint64) {
	b.mu.Lock()
	sub, ok := b.subs[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.subs, id)
	b.mu.Unlock()
	sub.closeOnce.Do(func() { close(sub.ch) })
}

func (b *DecisionBroker) evictAll(ids []uint64) {
	b.mu.Lock()
	evicted := make([]*decisionSubscriber, 0, len(ids))
	for _, id := range ids {
		sub, ok := b.subs[id]
		if !ok {
			continue
		}
		delete(b.subs, id)
		evicted = append(evicted, sub)
	}
	b.mu.Unlock()
	closeEvictedSubscribers(evicted)
}

func closeEvictedSubscribers(evicted []*decisionSubscriber) {
	for _, sub := range evicted {
		slog.Warn("policy decision broker evicted slow subscriber",
			"subscriber_id", sub.id,
			"reason", "buffer_full")
		sub.closeOnce.Do(func() { close(sub.ch) })
	}
}
