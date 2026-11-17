package gateway

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// Pause one selector after its session count has been read. Other selectors
// keep using the same real store while this caller retains a stale count.
type pausedMCPTraceStore struct {
	*edge.RedisStore
	paused    atomic.Bool
	countRead chan struct{}
	resume    chan struct{}
}

func (s *pausedMCPTraceStore) CountSessionExecutions(ctx context.Context, tenant, session string) (int64, error) {
	n, err := s.RedisStore.CountSessionExecutions(ctx, tenant, session)
	if err == nil && s.paused.CompareAndSwap(false, true) {
		close(s.countRead)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return n, err
}

func TestMCPEdgeTraceRechecksSuccessorAfterConcurrentRotation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := newSharedTestRedisClient(t, newTestRedisBackend(t))
	store := edge.NewRedisStoreFromClient(client,
		edge.WithMaxEventsPerExecution(12), edge.WithMaxExecutionsPerSession(2))
	current := &server{edgeStore: store}
	trace := func() (string, string) {
		t.Helper()
		session, execution, err := current.mcpEdgeTrace(ctx, "default", "agent-alpha", "tester", "sse-race")
		if err != nil {
			t.Fatal(err)
		}
		return session, execution
	}
	fill := func(session, execution string, n int) {
		t.Helper()
		for i := range n {
			if _, err := store.AppendEvent(ctx, edge.AgentActionEvent{
				EventID: fmt.Sprintf("%s-%d", execution, i), TenantID: "default",
				SessionID: session, ExecutionID: execution, PrincipalID: "tester", Timestamp: time.Now().UTC(),
				Layer: edge.LayerMCP, Kind: edge.EventKindMCPToolPre, ToolName: "t",
				Decision: edge.DecisionAllow, Status: edge.ActionStatusOK,
			}); err != nil {
				t.Fatalf("append event %d to %s: %v", i, execution, err)
			}
		}
	}
	firstSession, firstExecution := trace()
	paused := &pausedMCPTraceStore{RedisStore: store, countRead: make(chan struct{}), resume: make(chan struct{})}
	stale := &server{edgeStore: paused}
	type selection struct {
		session, execution string
		err                error
	}
	selected := make(chan selection, 1)
	go func() {
		session, execution, err := stale.mcpEdgeTrace(ctx, "default", "agent-alpha", "tester", "sse-race")
		selected <- selection{session, execution, err}
	}()
	select {
	case <-paused.countRead:
	case <-ctx.Done():
		t.Fatal("selector did not read the initial execution count")
	}
	fill(firstSession, firstExecution, 10)
	nextSession, nextExecution := trace()
	if nextSession != firstSession || nextExecution != "mcp-sse-race.e1" {
		t.Fatalf("concurrent rotation = %s/%s", nextSession, nextExecution)
	}
	fill(nextSession, nextExecution, 12)
	close(paused.resume)
	select {
	case got := <-selected:
		if got.err != nil || got.session != "sse-race.s1" || got.execution != "mcp-sse-race.s1" {
			t.Fatalf("stale selection = %+v, want next session after full successor", got)
		}
		fill(got.session, got.execution, 1)
	case <-ctx.Done():
		t.Fatal("selector did not finish after concurrent rotation")
	}
}
