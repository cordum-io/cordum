package agentd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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

// EDGE-059 — agentd's local HTTP server MUST set WriteTimeout (and
// IdleTimeout) so a slow-reading or hanging client cannot pin a handler
// goroutine indefinitely. Pre-fix, app.go:300 set only ReadHeaderTimeout
// — WriteTimeout==0 means no deadline → goroutine pool exhaustion DoS
// where every subsequent Claude tool call hangs (or fails closed under
// enforce mode). This test asserts the contract bytes match the
// documented constants.
func TestNewHTTPServerSetsBoundedWriteAndIdleTimeouts(t *testing.T) {
	bindURL := "http://" + freeLoopbackAddr(t) + "/v1/edge/hooks/claude"
	cfg := testRunConfig(t, bindURL)
	local := testLocalServerForBind(t, bindURL)
	srv, ln, err := newHTTPServer(cfg, local)
	if err != nil {
		t.Fatalf("newHTTPServer: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %v, want > 0 (EDGE-059 slow-loris guard); pre-fix value was the Go default 0 (infinite)", srv.WriteTimeout)
	}
	if srv.WriteTimeout != defaultLocalServerWriteTimeout {
		t.Fatalf("WriteTimeout = %v, want %v (defaultLocalServerWriteTimeout); EDGE-059 fix must use the documented constant so operators can grep the rationale", srv.WriteTimeout, defaultLocalServerWriteTimeout)
	}
	// Must be ≤ defaultHookTimeout (5s) so the agentd write completes inside
	// the hook's outer budget — otherwise the hook gives up first and the
	// developer sees a generic "hook timeout" instead of the agentd-driven
	// permission decision.
	if srv.WriteTimeout > defaultHookTimeout {
		t.Fatalf("WriteTimeout = %v, must be ≤ defaultHookTimeout %v (hook outer budget)", srv.WriteTimeout, defaultHookTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want > 0 (defense-in-depth lurker guard); pre-fix value was 0 (infinite)", srv.IdleTimeout)
	}
}

func TestNewHTTPServerUsesMatchingInheritedLoopbackListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen inherited loopback: %v", err)
	}
	bindURL := "http://" + ln.Addr().String() + "/v1/edge/hooks/claude"
	local := testLocalServerForBind(t, bindURL)

	_, got, err := newHTTPServer(testRunConfig(t, bindURL), local, ln)
	if err != nil {
		t.Fatalf("newHTTPServer with inherited listener returned error: %v", err)
	}
	if got != ln {
		t.Fatalf("listener = %p, want inherited %p", got, ln)
	}
	defer func() { _ = got.Close() }()
}

func TestNewHTTPServerRejectsMismatchedInheritedListenerAndClosesIt(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen inherited loopback: %v", err)
	}
	addr := ln.Addr().String()
	bindURL := "http://" + freeLoopbackAddr(t) + "/v1/edge/hooks/claude"
	local := testLocalServerForBind(t, bindURL)

	_, _, err = newHTTPServer(testRunConfig(t, bindURL), local, ln)
	if err == nil || !strings.Contains(err.Error(), "inherited agentd listener address does not match") {
		t.Fatalf("error = %v, want inherited listener mismatch", err)
	}
	attacker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("mismatched inherited listener was not closed; re-listen %s: %v", addr, err)
	}
	_ = attacker.Close()
}

func TestNewHTTPServerRejectsNonLoopbackInheritedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen non-loopback inherited listener: %v", err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		t.Fatalf("split listener addr: %v", err)
	}
	bindURL := "http://127.0.0.1:" + port + "/v1/edge/hooks/claude"
	local := testLocalServerForBind(t, bindURL)

	_, _, err = newHTTPServer(testRunConfig(t, bindURL), local, ln)
	if err == nil || !strings.Contains(err.Error(), "inherited agentd listener must be bound to loopback") {
		t.Fatalf("error = %v, want non-loopback inherited listener rejection", err)
	}
}

// EDGE-059 — verify isWriteTimeoutError correctly classifies the errors
// the JSON encoder will surface on a real http.Server.WriteTimeout firing
// versus a normal write error. Used to drive the bounded "reason" label
// on the new agentd_response_write_aborted_total counter — getting this
// classification wrong would muddy the metric's signal.
func TestIsWriteTimeoutErrorClassifiesNetTimeoutAndDeadlineExceeded(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "plain error", err: errors.New("some unrelated error"), want: false},
		{name: "deadline exceeded sentinel", err: os.ErrDeadlineExceeded, want: true},
		{name: "wrapped deadline", err: fmt.Errorf("write: %w", os.ErrDeadlineExceeded), want: true},
		{name: "net.Error timeout", err: &net.OpError{Op: "write", Err: timeoutNetError{}}, want: true},
		{name: "net.Error non-timeout", err: &net.OpError{Op: "write", Err: nonTimeoutNetError{}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWriteTimeoutError(tc.err); got != tc.want {
				t.Fatalf("isWriteTimeoutError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// timeoutNetError satisfies net.Error with Timeout() == true.
type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return false }

// nonTimeoutNetError satisfies net.Error with Timeout() == false.
type nonTimeoutNetError struct{}

func (nonTimeoutNetError) Error() string   { return "connection reset" }
func (nonTimeoutNetError) Timeout() bool   { return false }
func (nonTimeoutNetError) Temporary() bool { return false }

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

func testLocalServerForBind(t *testing.T, bindURL string) *LocalServer {
	t.Helper()
	local, err := NewLocalServer(LocalServerConfig{
		BindURL: bindURL,
		Nonce:   base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	})
	if err != nil {
		t.Fatalf("NewLocalServer: %v", err)
	}
	return local
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

// EDGE-063 — pure unit test of the bounded HTTP-server-join helper.
// HookTimeout default 5s -> 10s join timeout (2x). HookTimeout=0
// (operator misconfig) -> hard floor of 5s. HookTimeout=1s -> 5s
// (hard floor still wins). HookTimeout=10s -> 20s.
func TestAgentdHTTPJoinTimeoutHardFloor(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hookTimeout time.Duration
		want        time.Duration
	}{
		{name: "default_5s_doubles_to_10s", hookTimeout: 5 * time.Second, want: 10 * time.Second},
		{name: "operator_misconfig_zero_hits_hard_floor", hookTimeout: 0, want: 5 * time.Second},
		{name: "tiny_1s_hits_hard_floor", hookTimeout: 1 * time.Second, want: 5 * time.Second},
		{name: "ten_s_doubles_to_20s", hookTimeout: 10 * time.Second, want: 20 * time.Second},
		{name: "negative_hits_hard_floor", hookTimeout: -1 * time.Second, want: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := agentdHTTPJoinTimeout(tc.hookTimeout)
			if got != tc.want {
				t.Fatalf("agentdHTTPJoinTimeout(%v) = %v, want %v", tc.hookTimeout, got, tc.want)
			}
		})
	}
}

// EDGE-063 — clean shutdown must NOT emit the agentd_shutdown_forced
// metric. A false-positive on this counter would make operators chase
// a non-existent shutdown bug.
func TestRunCleanShutdownDoesNotEmitForcedMetric(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bindURL := "http://" + freeLoopbackAddr(t) + "/v1/edge/hooks/claude"
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-clean-shutdown",
				ExecutionID:    "exec-clean-shutdown",
				TraceID:        "trace-clean-shutdown",
				PolicySnapshot: "snap-clean-shutdown",
				DashboardURL:   "/edge/sessions/sess-clean-shutdown",
			}, nil
		},
		heartbeat: func(context.Context, string) (HeartbeatResponse, error) {
			return HeartbeatResponse{SessionID: "sess-clean-shutdown", HeartbeatAlive: true}, nil
		},
	}
	recorder := &captureRecorder{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config: Config{
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
			},
			Metadata: LocalSessionMetadata{
				TenantID:      "tenant-a",
				PrincipalID:   "principal-a",
				PrincipalType: edgecore.PrincipalTypeHuman,
				CWD:           "D:/Cordum/cordum",
			},
			Gateway:    gateway,
			StateStore: NewMemoryStateStore(),
			Recorder:   recorder,
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
		t.Fatal("Run did not return after context cancellation — EDGE-063 join may be hanging")
	}

	// EDGE-063 — Clean shutdown should NOT trigger the forced-shutdown
	// metric. The HTTP server's Serve goroutine exits via http.ErrServerClosed
	// before the join timeout; the heartbeat early-out fires only on a late
	// tick AFTER hbCancel.
	got := recorder.shutdownForcedSnapshot()
	for _, reason := range got {
		if reason == "http_server_drain" {
			t.Fatalf("clean shutdown emitted shutdown_forced{reason=http_server_drain}: %#v", got)
		}
	}
	// heartbeat_drain is acceptable IF it fires from a late OnStatus tick
	// after hbCancel — that's the early-out doing its job. But it should
	// be at most a small handful of emissions, not unbounded.
	hbDrain := 0
	for _, reason := range got {
		if reason == "heartbeat_drain" {
			hbDrain++
		}
	}
	if hbDrain > 5 {
		t.Fatalf("heartbeat_drain emitted %d times on clean shutdown — bound looks broken: %#v", hbDrain, got)
	}
}

func TestOnStatusHeartbeatNoFalsePositiveOnCleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gateway, thirdHeartbeatStarted := lateHeartbeatGatewayForCleanShutdown()
	recorder := &captureRecorder{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Config: testRunConfig(t, "http://127.0.0.1:0/v1/edge/hooks/claude"),
			Metadata: LocalSessionMetadata{
				TenantID:      "tenant-a",
				PrincipalID:   "principal-a",
				PrincipalType: edgecore.PrincipalTypeHuman,
				CWD:           "D:/Cordum/cordum",
			},
			Gateway:    gateway,
			StateStore: NewMemoryStateStore(),
			Recorder:   recorder,
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
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after clean cancellation")
	}
	if got := recorder.shutdownForcedSnapshot(); len(got) != 0 {
		t.Fatalf("clean shutdown emitted forced-shutdown metrics: %#v", got)
	}
}

func lateHeartbeatGatewayForCleanShutdown() (*stubRunGateway, <-chan struct{}) {
	var callsMu sync.Mutex
	var calls int
	thirdHeartbeatStarted := make(chan struct{})
	var closeThird sync.Once
	gateway := &stubRunGateway{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-clean-late-heartbeat",
				ExecutionID:    "exec-clean-late-heartbeat",
				TraceID:        "trace-clean-late-heartbeat",
				PolicySnapshot: "snap-clean-late-heartbeat",
				DashboardURL:   "/edge/sessions/sess-clean-late-heartbeat",
			}, nil
		},
		heartbeat: func(ctx context.Context, _ string) (HeartbeatResponse, error) {
			callsMu.Lock()
			calls++
			current := calls
			callsMu.Unlock()
			if current < 3 {
				return HeartbeatResponse{}, ErrGatewayTimeout
			}
			closeThird.Do(func() { close(thirdHeartbeatStarted) })
			<-ctx.Done()
			return HeartbeatResponse{}, ErrGatewayTimeout
		},
	}
	return gateway, thirdHeartbeatStarted
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
