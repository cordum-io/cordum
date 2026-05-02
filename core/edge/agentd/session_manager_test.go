package agentd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestSessionManagerCreateSessionTimeoutDegradesOutsideEnterpriseStrict(t *testing.T) {
	t.Parallel()

	timeoutErr := context.DeadlineExceeded
	gateway := stubGatewayLifecycleClient{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{}, timeoutErr
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:    gateway,
		StateStore: NewMemoryStateStore(),
		Metadata: LocalSessionMetadata{
			TenantID:      "tenant-a",
			PrincipalID:   "principal-a",
			PrincipalType: edgecore.PrincipalTypeHuman,
			CWD:           "D:/Cordum/cordum",
		},
		PolicyMode: edgecore.PolicyModeObserve,
		Clock:      fixedClock{now: time.Date(2026, 5, 2, 7, 10, 0, 0, time.UTC)},
	})

	state, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("Start returned error in observe mode: %v", err)
	}
	if state == nil {
		t.Fatal("Start returned nil degraded state")
	}
	if state.Status != edgecore.SessionStatusDegraded {
		t.Fatalf("state status = %q, want degraded", state.Status)
	}
	if state.FailClosed {
		t.Fatal("observe-mode degraded state unexpectedly fail-closed")
	}
	if state.TenantID != "tenant-a" || state.PrincipalID != "principal-a" {
		t.Fatalf("state identity = %q/%q", state.TenantID, state.PrincipalID)
	}
	if !strings.Contains(strings.ToLower(state.DegradedReason), "timeout") {
		t.Fatalf("degraded reason = %q, want timeout guidance", state.DegradedReason)
	}
	if state.StartedAt.IsZero() {
		t.Fatal("degraded state missing started_at")
	}
}

func TestSessionManagerCreateSessionTimeoutFailsClosedInEnterpriseStrict(t *testing.T) {
	t.Parallel()

	gateway := stubGatewayLifecycleClient{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{}, ErrGatewayTimeout
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:    gateway,
		StateStore: NewMemoryStateStore(),
		Metadata: LocalSessionMetadata{
			TenantID:      "tenant-a",
			PrincipalID:   "principal-a",
			PrincipalType: edgecore.PrincipalTypeHuman,
			CWD:           "D:/Cordum/cordum",
		},
		PolicyMode: edgecore.PolicyModeEnterpriseStrict,
		FailClosed: true,
		Clock:      fixedClock{now: time.Date(2026, 5, 2, 7, 15, 0, 0, time.UTC)},
	})

	state, err := manager.Start(context.Background())
	if !errors.Is(err, ErrFailClosed) {
		t.Fatalf("Start error = %v, want ErrFailClosed", err)
	}
	if state != nil {
		t.Fatalf("state = %#v, want nil on enterprise-strict fail-closed startup", state)
	}
}

func TestSessionManagerStartRestoresMatchingUnendedStateWithoutGatewayCall(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	restored := SessionState{
		SessionID:    "sess-restore",
		ExecutionID:  "exec-restore",
		TraceID:      "trace-restore",
		TenantID:     "tenant-a",
		PrincipalID:  "principal-a",
		PolicyMode:   edgecore.PolicyModeObserve,
		Status:       edgecore.SessionStatusRunning,
		StartedAt:    time.Date(2026, 5, 2, 8, 0, 0, 0, time.UTC),
		DashboardURL: "/edge/sessions/sess-restore",
		Metadata: map[string]string{
			"gateway_url": "http://127.0.0.1:8081",
			"cwd":         "D:/Cordum/cordum",
		},
	}
	if err := store.Save(context.Background(), restored); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	gateway := stubGatewayLifecycleClient{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			t.Fatal("CreateSession called for matching restore state")
			return CreateSessionResponse{}, nil
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:          gateway,
		StateStore:       store,
		RestoreSessionID: "sess-restore",
		GatewayURL:       "http://127.0.0.1:8081",
		Metadata: LocalSessionMetadata{
			TenantID:    "tenant-a",
			PrincipalID: "principal-a",
			CWD:         "D:/Cordum/cordum",
		},
		PolicyMode: edgecore.PolicyModeObserve,
	})

	state, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("Start restore: %v", err)
	}
	if state.SessionID != "sess-restore" || state.ExecutionID != "exec-restore" {
		t.Fatalf("restored state ids = %#v", state)
	}
}

func TestSessionManagerStartCreatesNewWhenRestoredStateEnded(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	endedAt := time.Date(2026, 5, 2, 8, 5, 0, 0, time.UTC)
	if err := store.Save(context.Background(), SessionState{
		SessionID:    "sess-ended",
		ExecutionID:  "exec-ended",
		TenantID:     "tenant-a",
		PolicyMode:   edgecore.PolicyModeObserve,
		Status:       edgecore.SessionStatusEnded,
		StartedAt:    endedAt.Add(-time.Minute),
		EndedAt:      &endedAt,
		DashboardURL: "/edge/sessions/sess-ended",
		Metadata: map[string]string{
			"gateway_url": "http://127.0.0.1:8081",
			"cwd":         "D:/Cordum/cordum",
		},
	}); err != nil {
		t.Fatalf("seed ended state: %v", err)
	}
	gateway := stubGatewayLifecycleClient{
		createSession: func(context.Context, CreateSessionRequest) (CreateSessionResponse, error) {
			return CreateSessionResponse{
				SessionID:      "sess-new",
				ExecutionID:    "exec-new",
				TraceID:        "trace-new",
				PolicySnapshot: "snap-new",
				DashboardURL:   "/edge/sessions/sess-new",
			}, nil
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:          gateway,
		StateStore:       store,
		RestoreSessionID: "sess-ended",
		GatewayURL:       "http://127.0.0.1:8081",
		Metadata: LocalSessionMetadata{
			TenantID:    "tenant-a",
			PrincipalID: "principal-a",
			CWD:         "D:/Cordum/cordum",
		},
		PolicyMode: edgecore.PolicyModeObserve,
		Clock:      fixedClock{now: time.Date(2026, 5, 2, 8, 10, 0, 0, time.UTC)},
	})

	state, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("Start create after ended restore: %v", err)
	}
	if state.SessionID != "sess-new" || state.ExecutionID != "exec-new" {
		t.Fatalf("state ids = %#v, want new Gateway ids", state)
	}
}

func TestGatherLocalMetadataUsesEnvAndCWDWithoutShelling(t *testing.T) {
	t.Parallel()

	meta := GatherLocalMetadata(LocalMetadataOptions{
		CWD: "D:/Cordum/cordum",
		Env: map[string]string{
			"CORDUM_TENANT_ID":         "tenant-a",
			"CORDUM_PRINCIPAL_ID":      "principal-a",
			"CORDUM_AGENT_PRODUCT":     "claude-code",
			"CORDUM_AGENT_VERSION":     "1.2.3",
			"CORDUM_EDGE_REPO":         "cordum",
			"CORDUM_EDGE_GIT_BRANCH":   "feature/cordum-edge-p0",
			"CORDUM_EDGE_GIT_SHA":      "abcdef123456",
			"CORDUM_EDGE_GIT_REMOTE":   "origin",
			"CORDUM_EDGE_DEVICE_ID":    "device-a",
			"CORDUM_EDGE_SESSION_MODE": "local-dev",
		},
	})
	if meta.TenantID != "tenant-a" || meta.PrincipalID != "principal-a" || meta.CWD != "D:/Cordum/cordum" {
		t.Fatalf("identity/cwd = %#v", meta)
	}
	if meta.GitBranch != "feature/cordum-edge-p0" || meta.GitSHA != "abcdef123456" || meta.DeviceID != "device-a" {
		t.Fatalf("git/device metadata = %#v", meta)
	}
}

type stubGatewayLifecycleClient struct {
	createSession func(context.Context, CreateSessionRequest) (CreateSessionResponse, error)
	endExecution  func(context.Context, string, EndExecutionRequest) error
	endSession    func(context.Context, string, EndSessionRequest) error
}

func (s stubGatewayLifecycleClient) CreateSession(ctx context.Context, req CreateSessionRequest) (CreateSessionResponse, error) {
	if s.createSession == nil {
		return CreateSessionResponse{}, nil
	}
	return s.createSession(ctx, req)
}

func (s stubGatewayLifecycleClient) EndExecution(ctx context.Context, executionID string, req EndExecutionRequest) error {
	if s.endExecution == nil {
		return nil
	}
	return s.endExecution(ctx, executionID, req)
}

func (s stubGatewayLifecycleClient) EndSession(ctx context.Context, sessionID string, req EndSessionRequest) error {
	if s.endSession == nil {
		return nil
	}
	return s.endSession(ctx, sessionID, req)
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func TestSessionManagerShutdownEndsExecutionAndSessionExactlyOnce(t *testing.T) {
	t.Parallel()

	var executionCalls, sessionCalls int
	gateway := stubGatewayLifecycleClient{
		endExecution: func(ctx context.Context, executionID string, req EndExecutionRequest) error {
			executionCalls++
			if executionID != "exec-1" {
				t.Fatalf("executionID = %q, want exec-1", executionID)
			}
			if req.Status != edgecore.ExecutionStatusSucceeded {
				t.Fatalf("execution status = %q, want succeeded", req.Status)
			}
			return nil
		},
		endSession: func(ctx context.Context, sessionID string, req EndSessionRequest) error {
			sessionCalls++
			if sessionID != "sess-1" {
				t.Fatalf("sessionID = %q, want sess-1", sessionID)
			}
			if req.Status != edgecore.SessionStatusEnded {
				t.Fatalf("session status = %q, want ended", req.Status)
			}
			return nil
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:    gateway,
		StateStore: NewMemoryStateStore(),
		InitialState: &SessionState{
			SessionID:   "sess-1",
			ExecutionID: "exec-1",
			TenantID:    "tenant-a",
			Status:      edgecore.SessionStatusRunning,
		},
		Clock: fixedClock{now: time.Date(2026, 5, 2, 7, 20, 0, 0, time.UTC)},
	})

	if err := manager.Shutdown(context.Background(), ShutdownOptions{
		ExecutionStatus: edgecore.ExecutionStatusSucceeded,
		SessionStatus:   edgecore.SessionStatusEnded,
	}); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := manager.Shutdown(context.Background(), ShutdownOptions{
		ExecutionStatus: edgecore.ExecutionStatusFailed,
		SessionStatus:   edgecore.SessionStatusFailed,
	}); err != nil {
		t.Fatalf("second Shutdown should be idempotent: %v", err)
	}
	if executionCalls != 1 || sessionCalls != 1 {
		t.Fatalf("end calls = execution:%d session:%d, want exactly 1/1", executionCalls, sessionCalls)
	}
	state := manager.State()
	if state.Status != edgecore.SessionStatusEnded {
		t.Fatalf("state status = %q, want ended", state.Status)
	}
}

func TestSessionManagerShutdownGatewayTimeoutRecordsFailedState(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	gateway := stubGatewayLifecycleClient{
		endExecution: func(context.Context, string, EndExecutionRequest) error {
			return ErrGatewayTimeout
		},
	}
	manager := NewSessionManager(SessionManagerConfig{
		Gateway:    gateway,
		StateStore: store,
		InitialState: &SessionState{
			SessionID:   "sess-timeout",
			ExecutionID: "exec-timeout",
			TenantID:    "tenant-a",
			Status:      edgecore.SessionStatusRunning,
		},
		Clock: fixedClock{now: time.Date(2026, 5, 2, 7, 30, 0, 0, time.UTC)},
	})

	err := manager.Shutdown(context.Background(), ShutdownOptions{
		ExecutionStatus: edgecore.ExecutionStatusSucceeded,
		SessionStatus:   edgecore.SessionStatusEnded,
	})
	if !errors.Is(err, ErrGatewayTimeout) {
		t.Fatalf("Shutdown error = %v, want ErrGatewayTimeout", err)
	}
	state := manager.State()
	if state.Status != edgecore.SessionStatusFailed {
		t.Fatalf("state status = %q, want failed after shutdown timeout", state.Status)
	}
	if !state.PendingGatewayEnd {
		t.Fatal("state.PendingGatewayEnd = false, want true for retryable evidence")
	}
	if !strings.Contains(strings.ToLower(state.DegradedReason), "timeout") {
		t.Fatalf("degraded reason = %q, want timeout", state.DegradedReason)
	}
}
