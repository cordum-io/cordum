package policy

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecisionBrokerSubscribeAndPublishDeliversToAllSubscribers(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	a, unsubA := broker.Subscribe(8)
	t.Cleanup(unsubA)
	b, unsubB := broker.Subscribe(8)
	t.Cleanup(unsubB)

	d := Decision{
		Source: DecisionSourceJob,
		RuleID: "rule-x",
		Type:   DecisionAllow,
	}
	broker.Publish(context.Background(), d)

	got := <-a
	require.Equal(t, "rule-x", got.RuleID)
	got = <-b
	require.Equal(t, "rule-x", got.RuleID)
}

func TestDecisionBrokerPublishIsNonBlockingWhenSubscriberFull(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	// Buffer size 1 — fill it without draining, additional publishes must drop.
	ch, unsub := broker.Subscribe(1)
	t.Cleanup(unsub)

	d := Decision{Source: DecisionSourceJob, RuleID: "r", Type: DecisionAllow}
	broker.Publish(context.Background(), d) // accepted
	// All further publishes must complete in <100ms even though the channel is
	// full and never drained — proves the broker drops on full instead of
	// blocking the emit path.
	deadline := time.Now().Add(100 * time.Millisecond)
	for i := 0; i < 20; i++ {
		broker.Publish(context.Background(), d)
		require.Less(t, time.Since(deadline.Add(-100*time.Millisecond)).Milliseconds(), int64(100))
	}
	require.True(t, time.Now().Before(deadline), "Publish blocked instead of dropping")

	// Drain the one buffered message.
	<-ch
}

func TestDecisionBrokerAutoUnsubscribesAfterRepeatedDrops(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	ch, _ := broker.Subscribe(1)
	require.Equal(t, 1, broker.SubscriberCount())

	d := Decision{Source: DecisionSourceJob, RuleID: "r", Type: DecisionAllow}
	// 1 accepted, then >3 consecutive drops without drain — broker should
	// drop the slow subscriber and close its channel.
	for i := 0; i < 10; i++ {
		broker.Publish(context.Background(), d)
	}

	// Channel closes once auto-unsub fires.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case _, ok := <-ch:
			if !ok {
				require.Equal(t, 0, broker.SubscriberCount())
				return
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("subscriber was not auto-unsubscribed after repeated drops")
}

func TestDecisionBrokerUnsubscribeIsIdempotent(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	_, unsub := broker.Subscribe(4)
	require.Equal(t, 1, broker.SubscriberCount())
	unsub()
	require.Equal(t, 0, broker.SubscriberCount())
	unsub() // second call must not panic or underflow
	require.Equal(t, 0, broker.SubscriberCount())
}

func TestDecisionBrokerSubscribeRejectsNonPositiveBuffer(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	require.Panics(t, func() { broker.Subscribe(0) })
	require.Panics(t, func() { broker.Subscribe(-1) })
}

func TestDecisionBrokerCloseClosesAllSubscriberChannels(t *testing.T) {
	broker := NewDecisionBroker()

	a, _ := broker.Subscribe(4)
	b, _ := broker.Subscribe(4)
	broker.Close()

	_, ok := <-a
	require.False(t, ok, "channel a should be closed after broker.Close()")
	_, ok = <-b
	require.False(t, ok, "channel b should be closed after broker.Close()")
}

func TestDecisionBrokerCloseIsIdempotent(t *testing.T) {
	broker := NewDecisionBroker()
	require.NotPanics(t, broker.Close)
	require.NotPanics(t, broker.Close)
}

func TestDecisionBrokerConcurrentPublishAndSubscribe(t *testing.T) {
	broker := NewDecisionBroker()
	t.Cleanup(broker.Close)

	// Mix publishers and subscribers concurrently to exercise the registry
	// mutex. Run with -race; correctness is "no data race + no deadlock".
	const publishers = 8
	const subscribers = 8
	const messagesPerPublisher = 50

	var subWg sync.WaitGroup
	var receivedTotal atomic.Int64
	for i := 0; i < subscribers; i++ {
		subWg.Add(1)
		go func() {
			defer subWg.Done()
			ch, unsub := broker.Subscribe(64)
			defer unsub()
			deadline := time.After(2 * time.Second)
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
					receivedTotal.Add(1)
				case <-deadline:
					return
				}
			}
		}()
	}

	// Give subscribers a beat to register.
	time.Sleep(50 * time.Millisecond)

	var pubWg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			d := Decision{Source: DecisionSourceJob, RuleID: "r", Type: DecisionAllow}
			for j := 0; j < messagesPerPublisher; j++ {
				broker.Publish(context.Background(), d)
			}
		}()
	}

	pubWg.Wait()
	// Allow subscribers a moment to drain pending messages, then close to
	// release them and complete the wait group.
	time.Sleep(100 * time.Millisecond)
	broker.Close()
	subWg.Wait()

	require.Greater(t, receivedTotal.Load(), int64(0),
		"at least one subscriber should have received some messages")
}
