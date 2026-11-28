package agentd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestEvaluatorCoalescedFlightDoesNotWriteReturnedCallersWriter(t *testing.T) {
	t.Parallel()
	client := newBlockingEvaluateClient(coalescedAllowResponse(), nil)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(client.release) }) })
	writer := &concurrentCaptureEventWriter{}
	evaluator := NewEvaluator(EvaluatorConfig{
		Client: uncancelledEvaluateClient{client},
		State:  evaluatorTestState(edgecore.PolicyModeEnforce), HookTimeout: 5 * time.Second,
	})
	waiting := make(chan struct{}, 2)
	evaluator.coalesceWaitHook = func() { waiting <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leader := make(chan coalesceHookResult, 1)
	follower := make(chan coalesceHookResult, 1)
	go func() {
		decision, err := evaluator.EvaluateHookWithEventWriter(ctx, evaluatorCoalesceRequest(), writer)
		leader <- coalesceHookResult{decision, err}
	}()
	waitCoalesceValue(t, client.started)
	go func() {
		decision, err := evaluator.EvaluateHookWithEventWriter(context.Background(), evaluatorCoalesceRequest(), &concurrentCaptureEventWriter{})
		follower <- coalesceHookResult{decision, err}
	}()
	waitCoalesceValue(t, waiting)
	waitCoalesceValue(t, waiting)
	cancel()
	if result := waitCoalesceValue(t, leader); !errors.Is(result.err, context.Canceled) {
		t.Fatalf("leader error = %v, want context.Canceled", result.err)
	}
	release.Do(func() { close(client.release) })
	if result := waitCoalesceValue(t, follower); result.err != nil || result.decision.Decision != claude.DecisionAllow {
		t.Fatalf("follower = %#v, want ALLOW and nil error", result)
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("gateway evaluate calls = %d, want 1", calls)
	}
	if events := writer.snapshot(); len(events) != 0 {
		t.Fatalf("returned caller events = %d, want 0", len(events))
	}
}

type uncancelledEvaluateClient struct{ *blockingEvaluateClient }

func (c uncancelledEvaluateClient) Evaluate(ctx context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	return c.blockingEvaluateClient.Evaluate(context.WithoutCancel(ctx), req)
}

type coalesceHookResult struct {
	decision claude.AgentdDecision
	err      error
}

func waitCoalesceValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for coalesced evaluate")
		var zero T
		return zero
	}
}
