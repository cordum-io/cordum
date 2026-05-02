package agentd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestRunUsesConfiguredNonceForLocalHookServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nonce := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	bindURL := "http://" + freeLoopbackAddr(t) + "/v1/edge/hooks/claude"
	gateway := &stubRunEvaluateGateway{stubRunGateway: &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-nonce",
				ExecutionID:    "exec-nonce",
				TraceID:        "trace-nonce",
				PolicySnapshot: "snap-nonce",
				DashboardURL:   "/edge/sessions/sess-nonce",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{SessionID: "sess-nonce", HeartbeatAlive: true}, nil
		},
	}}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config:     testRunConfig(t, bindURL),
			Gateway:    gateway,
			StateStore: NewMemoryStateStore(),
			Clock:      realClock{},
			Nonce:      nonce,
		})
	}()
	waitForHookStatus(t, done, bindURL, nonce, `{"event_name":"PreToolUse","session_id":"sess-nonce","execution_id":"exec-nonce"}`, http.StatusOK)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func waitForHookStatus(t *testing.T, done <-chan error, bindURL, nonce, body string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	last := "no request attempted"
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("Run returned before hook status %d: %v", want, err)
		default:
		}
		req, err := http.NewRequest(http.MethodPost, bindURL, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build hook request: %v", err)
		}
		if strings.TrimSpace(nonce) != "" {
			req.Header.Set("X-Cordum-Agentd-Nonce", nonce)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			last = err.Error()
			time.Sleep(5 * time.Millisecond)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		last = fmt.Sprintf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
		_ = resp.Body.Close()
		if resp.StatusCode == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("hook status %d not observed within 2s; last=%s", want, last)
}

func TestRunRejectsInvalidExternalNonceBeforeStarting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nonce string
	}{
		{name: "too short", nonce: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))},
		{name: "malformed", nonce: "not-base64-!!@@"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			err := Run(context.Background(), RunOptions{
				Config: testRunConfig(t, "http://127.0.0.1:0/v1/edge/hooks/claude"),
				Gateway: &stubRunGateway{
					createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
						called = true
						return CreateSessionResponse{}, nil
					},
					heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
						return HeartbeatResponse{}, nil
					},
				},
				StateStore: NewMemoryStateStore(),
				Nonce:      tc.nonce,
			})
			if !errors.Is(err, errInvalidExternalNonce) {
				t.Fatalf("Run error = %v, want invalid nonce error", err)
			}
			if strings.Contains(err.Error(), tc.nonce) {
				t.Fatalf("invalid nonce error leaked supplied value: %v", err)
			}
			if called {
				t.Fatal("gateway CreateSession called despite invalid nonce")
			}
		})
	}
}

func TestRunAutoGeneratesNonceWhenUnset(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bindURL := "http://" + freeLoopbackAddr(t) + "/v1/edge/hooks/claude"
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-auto-nonce",
				ExecutionID:    "exec-auto-nonce",
				PolicySnapshot: "snap-auto-nonce",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{SessionID: "sess-auto-nonce", HeartbeatAlive: true}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config:     testRunConfig(t, bindURL),
			Gateway:    gateway,
			StateStore: NewMemoryStateStore(),
			Clock:      realClock{},
		})
	}()
	eventually(t, 2*time.Second, func() bool {
		resp, err := http.Post(bindURL, "application/json", strings.NewReader(`{"event_name":"PreToolUse"}`))
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusUnauthorized
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after auto nonce startup: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

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

func TestRunHeartbeatDegradedDoesNotOverwriteShutdownState(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := newShutdownRaceStateStore()
	thirdHeartbeatStarted := make(chan struct{})
	var callsMu sync.Mutex
	var calls int
	var closeThird sync.Once
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-heartbeat-shutdown",
				ExecutionID:    "exec-heartbeat-shutdown",
				TraceID:        "trace-heartbeat-shutdown",
				PolicySnapshot: "snap-heartbeat-shutdown",
				DashboardURL:   "/edge/sessions/sess-heartbeat-shutdown",
			}, nil
		},
		heartbeat: func(ctx context.Context, sessionID string) (HeartbeatResponse, error) {
			callsMu.Lock()
			calls++
			call := calls
			callsMu.Unlock()
			if call < 3 {
				return HeartbeatResponse{}, ErrGatewayTimeout
			}
			closeThird.Do(func() { close(thirdHeartbeatStarted) })
			<-ctx.Done()
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
				GatewayTimeout:    500 * time.Millisecond,
				HeartbeatTTL:      20 * time.Millisecond,
				HeartbeatInterval: time.Millisecond,
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
	case <-thirdHeartbeatStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("third heartbeat did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
	state, ok, err := store.Load(context.Background(), "sess-heartbeat-shutdown")
	if err != nil || !ok {
		t.Fatalf("load final state ok=%v err=%v", ok, err)
	}
	if state.Status != edgecore.SessionStatusEnded && state.Status != edgecore.SessionStatusFailed {
		t.Fatalf("final state status = %q, want ended or failed; state=%#v", state.Status, state)
	}
	if state.EndedAt == nil {
		t.Fatalf("final state EndedAt = nil; state=%#v", state)
	}
	if state.Status == edgecore.SessionStatusDegraded && state.EndedAt == nil {
		t.Fatalf("degraded heartbeat overwrote shutdown state: %#v", state)
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

type stubRunEvaluateGateway struct {
	*stubRunGateway
}

func (s *stubRunEvaluateGateway) Evaluate(context.Context, EvaluateRequest) (*EvaluateResponse, error) {
	return &EvaluateResponse{
		Decision:                 string(edgecore.DecisionAllow),
		Reason:                   "nonce accepted",
		PolicySnapshot:           "snap-nonce",
		EventID:                  "evt-nonce",
		PermissionDecision:       "allow",
		PermissionDecisionReason: "nonce accepted",
	}, nil
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

func testRunConfig(t *testing.T, bindURL string) Config {
	t.Helper()
	return Config{
		GatewayURL:        "http://127.0.0.1:8081",
		APIKey:            "api-key",
		TenantID:          "tenant-a",
		PolicyMode:        edgecore.PolicyModeObserve,
		BindURL:           bindURL,
		HookTimeout:       100 * time.Millisecond,
		GatewayTimeout:    100 * time.Millisecond,
		HeartbeatTTL:      100 * time.Millisecond,
		HeartbeatInterval: 10 * time.Millisecond,
		StateDir:          t.TempDir(),
	}
}

type shutdownRaceStateStore struct {
	inner         *MemoryStateStore
	terminalOnce  sync.Once
	terminalSaved chan struct{}
}

func newShutdownRaceStateStore() *shutdownRaceStateStore {
	return &shutdownRaceStateStore{
		inner:         NewMemoryStateStore(),
		terminalSaved: make(chan struct{}),
	}
}

func (s *shutdownRaceStateStore) Save(ctx context.Context, state SessionState) error {
	if state.Status == edgecore.SessionStatusDegraded && state.EndedAt == nil {
		select {
		case <-s.terminalSaved:
		case <-time.After(2 * time.Second):
			return errors.New("terminal state was not saved before degraded heartbeat save")
		}
	}
	err := s.inner.Save(ctx, state)
	if err == nil && state.EndedAt != nil {
		s.terminalOnce.Do(func() { close(s.terminalSaved) })
	}
	return err
}

func (s *shutdownRaceStateStore) Load(ctx context.Context, sessionID string) (SessionState, bool, error) {
	return s.inner.Load(ctx, sessionID)
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved loopback port: %v", err)
	}
	return addr
}
