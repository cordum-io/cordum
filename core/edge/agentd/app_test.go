package agentd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestRunRegistersHeartbeatsAndEndsSessionOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-run",
				ExecutionID:    "exec-run",
				TraceID:        "trace-run",
				PolicySnapshot: "snap-run",
				DashboardURL:   "/edge/sessions/sess-run",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{SessionID: "sess-run", HeartbeatAlive: true}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config: Config{
				GatewayURL:        "http://127.0.0.1:8081",
				APIKey:            "api-key",
				TenantID:          "tenant-a",
				PolicyMode:        edgecore.PolicyModeObserve,
				BindURL:           "http://127.0.0.1:0/v1/edge/hooks/claude",
				HookTimeout:       100 * time.Millisecond,
				GatewayTimeout:    100 * time.Millisecond,
				HeartbeatTTL:      100 * time.Millisecond,
				HeartbeatInterval: 10 * time.Millisecond,
				StateDir:          t.TempDir(),
			},
			Metadata: LocalSessionMetadata{
				TenantID:      "tenant-a",
				PrincipalID:   "principal-a",
				PrincipalType: edgecore.PrincipalTypeHuman,
				CWD:           "D:/Cordum/cordum",
			},
			Gateway:    gateway,
			StateStore: NewMemoryStateStore(),
			Clock:      realClock{},
		})
	}()
	eventually(t, time.Second, func() bool { return gateway.heartbeatCount() > 0 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	if gateway.endExecutionCount() != 1 || gateway.endSessionCount() != 1 {
		t.Fatalf("end calls = exec:%d session:%d, want 1/1", gateway.endExecutionCount(), gateway.endSessionCount())
	}
}

func TestRunMarksPersistedStateDegradedAfterHeartbeatFailures(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewMemoryStateStore()
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-heartbeat",
				ExecutionID:    "exec-heartbeat",
				TraceID:        "trace-heartbeat",
				PolicySnapshot: "snap-heartbeat",
				DashboardURL:   "/edge/sessions/sess-heartbeat",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{}, ErrGatewayTimeout
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config: Config{
				GatewayURL:        "http://127.0.0.1:8081",
				APIKey:            "api-key",
				TenantID:          "tenant-a",
				PolicyMode:        edgecore.PolicyModeObserve,
				BindURL:           "http://127.0.0.1:0/v1/edge/hooks/claude",
				HookTimeout:       100 * time.Millisecond,
				GatewayTimeout:    100 * time.Millisecond,
				HeartbeatTTL:      100 * time.Millisecond,
				HeartbeatInterval: 10 * time.Millisecond,
				StateDir:          t.TempDir(),
			},
			Metadata: LocalSessionMetadata{
				TenantID:      "tenant-a",
				PrincipalID:   "principal-a",
				PrincipalType: edgecore.PrincipalTypeHuman,
				CWD:           "D:/Cordum/cordum",
			},
			Gateway:    gateway,
			StateStore: store,
			Clock:      realClock{},
		})
	}()
	eventually(t, 2*time.Second, func() bool {
		state, ok, err := store.Load(context.Background(), "sess-heartbeat")
		return err == nil &&
			ok &&
			state.Status == edgecore.SessionStatusDegraded &&
			strings.Contains(strings.ToLower(state.DegradedReason), "heartbeat") &&
			!state.FailClosed &&
			gateway.degradedCount() > 0
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after degraded heartbeat state: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunReturnsFailClosedAfterHeartbeatFailuresInStrictMode(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-heartbeat-strict",
				ExecutionID:    "exec-heartbeat-strict",
				TraceID:        "trace-heartbeat-strict",
				PolicySnapshot: "snap-heartbeat-strict",
				DashboardURL:   "/edge/sessions/sess-heartbeat-strict",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{}, ErrGatewayTimeout
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), RunOptions{
			Config: Config{
				GatewayURL:        "http://127.0.0.1:8081",
				APIKey:            "api-key",
				TenantID:          "tenant-a",
				PolicyMode:        edgecore.PolicyModeEnterpriseStrict,
				BindURL:           "http://127.0.0.1:0/v1/edge/hooks/claude",
				HookTimeout:       100 * time.Millisecond,
				GatewayTimeout:    100 * time.Millisecond,
				HeartbeatTTL:      100 * time.Millisecond,
				HeartbeatInterval: 10 * time.Millisecond,
				StateDir:          t.TempDir(),
			},
			Metadata: LocalSessionMetadata{
				TenantID:      "tenant-a",
				PrincipalID:   "principal-a",
				PrincipalType: edgecore.PrincipalTypeHuman,
				CWD:           "D:/Cordum/cordum",
			},
			Gateway:    gateway,
			StateStore: store,
			Clock:      realClock{},
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFailClosed) {
			t.Fatalf("Run error = %v, want ErrFailClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not fail closed after repeated heartbeat failures")
	}
	state, ok, err := store.Load(context.Background(), "sess-heartbeat-strict")
	if err != nil || !ok {
		t.Fatalf("load state ok=%v err=%v", ok, err)
	}
	if state.Status != edgecore.SessionStatusFailed || !state.FailClosed {
		t.Fatalf("state after fail-closed heartbeat = %#v", state)
	}
	if gateway.endExecutionCount() != 1 || gateway.endSessionCount() != 1 {
		t.Fatalf("end calls = exec:%d session:%d, want 1/1", gateway.endExecutionCount(), gateway.endSessionCount())
	}
}

type stubRunGateway struct {
	mu            sync.Mutex
	createSession func(context.Context, CreateSessionRequest) (CreateSessionResponse, error)
	heartbeat     func(context.Context, string) (HeartbeatResponse, error)
	endExec       int
	endSess       int
	heartbeats    int
	degraded      int
}

func (s *stubRunGateway) CreateSession(ctx context.Context, req CreateSessionRequest) (CreateSessionResponse, error) {
	return s.createSession(ctx, req)
}

func (s *stubRunGateway) Heartbeat(ctx context.Context, sessionID string) (HeartbeatResponse, error) {
	s.mu.Lock()
	s.heartbeats++
	s.mu.Unlock()
	return s.heartbeat(ctx, sessionID)
}

func (s *stubRunGateway) EndExecution(context.Context, string, EndExecutionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endExec++
	return nil
}

func (s *stubRunGateway) EndSession(context.Context, string, EndSessionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endSess++
	return nil
}

func (s *stubRunGateway) MarkSessionDegraded(context.Context, SessionState, string) (edgecore.AgentActionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.degraded++
	return edgecore.AgentActionEvent{}, nil
}

func (s *stubRunGateway) heartbeatCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats
}

func (s *stubRunGateway) endExecutionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endExec
}

func (s *stubRunGateway) endSessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endSess
}

func (s *stubRunGateway) degradedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.degraded
}
