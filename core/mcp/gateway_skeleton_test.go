package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// Step-2 RED tests for EDGE-100 MCP Gateway skeleton.
//
// Plan amendment (governor-e932e549 comment-a305f5a3 2026-05-16):
//   - Routes register in the API gateway's main mux (core/controlplane/gateway/gateway.go);
//     the implementation surface for THIS package is the library
//     RegisterGatewayRoutes(mux, deps) — pure http.ServeMux registration,
//     no coupling to the *server type.
//   - cfg.MCP.GatewayEnabled lives on the existing MCPPolicy struct in
//     core/infra/config/safety_policy.go:363 (per-tenant), not on a new
//     core/infra/config/mcp.go file.
//   - DoD #3 event-kind constants already exist; covered by
//     core/edge/event_mcp_server_test.go (constant assertions).
//
// All symbols referenced below are intentionally absent at RED-time:
//   - mcp.GatewayDeps, mcp.GatewayConfig, mcp.RegisterGatewayRoutes
// They are introduced by step-3 (gateway_skeleton.go + gateway_deps.go).

// fakeGatewayStore is a thin in-memory edge.Store double sufficient for the
// gateway skeleton's session/execution create + event append calls. Any
// method outside that surface panics so an accidental gateway dependency
// on broader Store API is immediately visible.
type fakeGatewayStore struct {
	mu         sync.Mutex
	sessions   []edge.EdgeSession
	executions []edge.AgentExecution
	events     []edge.AgentActionEvent
	createErr  error
	appendErr  error
}

// CreateSession runs the real edge.EdgeSession.Validate() before recording —
// EDGE-100 QA reopen #1 (msg-91a05b16) called this out as the masking gap
// that let the prior submit pass with empty enum values (PrincipalType,
// RiskSummary.MaxRisk). With validation here, any future regression that
// constructs an invalid EdgeSession surfaces in unit tests instead of only
// in a miniredis-backed integration test.
func (f *fakeGatewayStore) CreateSession(_ context.Context, s edge.EdgeSession) error {
	if err := s.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.sessions = append(f.sessions, s)
	return nil
}

// CreateExecution validates the AgentExecution against the real
// edge.AgentExecution.Validate() rules (e.g. non-empty Mode) before
// recording. See CreateSession comment for QA-reopen rationale.
func (f *fakeGatewayStore) CreateExecution(_ context.Context, e edge.AgentExecution) error {
	if err := e.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.executions = append(f.executions, e)
	return nil
}

// AppendEvent validates the AgentActionEvent against the real
// edge.AgentActionEvent.Validate() rules (e.g. non-empty SessionID,
// ExecutionID, Decision, Status) before recording. See CreateSession
// comment for QA-reopen rationale.
func (f *fakeGatewayStore) AppendEvent(_ context.Context, ev edge.AgentActionEvent) (edge.AgentActionEvent, error) {
	if err := ev.Validate(); err != nil {
		return edge.AgentActionEvent{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.appendErr != nil {
		return edge.AgentActionEvent{}, f.appendErr
	}
	if !f.hasMatchingExecution(ev) {
		return edge.AgentActionEvent{}, edge.ErrNotFound
	}
	f.events = append(f.events, ev)
	return ev, nil
}

func (f *fakeGatewayStore) hasMatchingExecution(ev edge.AgentActionEvent) bool {
	for _, execution := range f.executions {
		if execution.ExecutionID == ev.ExecutionID &&
			execution.SessionID == ev.SessionID &&
			execution.TenantID == ev.TenantID {
			return true
		}
	}
	return false
}

// Unused methods of edge.Store — panic on any unexpected call so the
// gateway skeleton cannot quietly expand its dependency surface.
func (f *fakeGatewayStore) GetSession(context.Context, string, string) (*edge.EdgeSession, bool, error) {
	panic("fakeGatewayStore: GetSession not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ListSessions(context.Context, edge.ListSessionsQuery) (edge.SessionPage, error) {
	panic("fakeGatewayStore: ListSessions not expected from gateway skeleton")
}
func (f *fakeGatewayStore) EndSession(context.Context, string, string, time.Time, edge.SessionStatus) (*edge.EdgeSession, error) {
	panic("fakeGatewayStore: EndSession not expected from gateway skeleton")
}
func (f *fakeGatewayStore) DeleteSession(context.Context, string, string) error {
	panic("fakeGatewayStore: DeleteSession not expected from gateway skeleton")
}
func (f *fakeGatewayStore) TouchHeartbeat(context.Context, string, string) error {
	panic("fakeGatewayStore: TouchHeartbeat not expected from gateway skeleton")
}
func (f *fakeGatewayStore) HeartbeatAlive(context.Context, string, string) (bool, error) {
	panic("fakeGatewayStore: HeartbeatAlive not expected from gateway skeleton")
}
func (f *fakeGatewayStore) GetExecution(context.Context, string, string) (*edge.AgentExecution, bool, error) {
	panic("fakeGatewayStore: GetExecution not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ListExecutions(context.Context, edge.ListExecutionsQuery) (edge.ExecutionPage, error) {
	panic("fakeGatewayStore: ListExecutions not expected from gateway skeleton")
}
func (f *fakeGatewayStore) CountSessionExecutions(context.Context, string, string) (int64, error) {
	panic("fakeGatewayStore: CountSessionExecutions not expected from gateway skeleton")
}
func (f *fakeGatewayStore) EndExecution(context.Context, string, string, time.Time, edge.ExecutionStatus) (*edge.AgentExecution, error) {
	panic("fakeGatewayStore: EndExecution not expected from gateway skeleton")
}
func (f *fakeGatewayStore) AppendEvents(context.Context, []edge.AgentActionEvent) ([]edge.AgentActionEvent, error) {
	panic("fakeGatewayStore: AppendEvents not expected from gateway skeleton")
}
func (f *fakeGatewayStore) AppendEventsWithIdempotency(context.Context, edge.EdgeIdempotencyRequest, []edge.AgentActionEvent, edge.EdgeIdempotencyResponseBuilder) (edge.EdgeIdempotentAppendResult, error) {
	panic("fakeGatewayStore: AppendEventsWithIdempotency not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ListEvents(context.Context, edge.ListEventsQuery) (edge.EventPage, error) {
	panic("fakeGatewayStore: ListEvents not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ReserveIdempotency(context.Context, edge.EdgeIdempotencyRequest) (edge.EdgeIdempotencyReservation, error) {
	panic("fakeGatewayStore: ReserveIdempotency not expected from gateway skeleton")
}
func (f *fakeGatewayStore) CompleteIdempotency(context.Context, edge.EdgeIdempotencyRequest, edge.EdgeIdempotencyResponse) (*edge.EdgeIdempotencyRecord, error) {
	panic("fakeGatewayStore: CompleteIdempotency not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ReleaseIdempotency(context.Context, edge.EdgeIdempotencyRequest) error {
	panic("fakeGatewayStore: ReleaseIdempotency not expected from gateway skeleton")
}
func (f *fakeGatewayStore) EnqueueApproval(context.Context, edge.EdgeApprovalRequest) (*edge.EdgeApproval, error) {
	panic("fakeGatewayStore: EnqueueApproval not expected from gateway skeleton")
}
func (f *fakeGatewayStore) GetApproval(context.Context, string, string) (*edge.EdgeApproval, bool, error) {
	panic("fakeGatewayStore: GetApproval not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ListApprovals(context.Context, edge.ListApprovalsQuery) (edge.ApprovalPage, error) {
	panic("fakeGatewayStore: ListApprovals not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ApproveApproval(context.Context, edge.ApprovalResolution) (*edge.EdgeApproval, error) {
	panic("fakeGatewayStore: ApproveApproval not expected from gateway skeleton")
}
func (f *fakeGatewayStore) RejectApproval(context.Context, edge.ApprovalResolution) (*edge.EdgeApproval, error) {
	panic("fakeGatewayStore: RejectApproval not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ClaimApproval(context.Context, edge.ApprovalClaimRequest) (*edge.EdgeApproval, bool, error) {
	panic("fakeGatewayStore: ClaimApproval not expected from gateway skeleton")
}
func (f *fakeGatewayStore) ExpireApprovals(context.Context, string, time.Time) (int, error) {
	panic("fakeGatewayStore: ExpireApprovals not expected from gateway skeleton")
}

// testDeps builds a GatewayDeps wired to a fakeGatewayStore + the supplied
// gating function. The tenant resolver returns the X-Tenant-ID header verbatim
// (or "" + an error if absent) and a fixed principal so tenant/principal-
// attribution tests are deterministic.
func testDeps(store *fakeGatewayStore, gatewayEnabled bool) GatewayDeps {
	return GatewayDeps{
		Store: store,
		GatewayEnabled: func(_ context.Context, _ string) (bool, error) {
			return gatewayEnabled, nil
		},
		ResolveTenant: func(r *http.Request) (string, string, error) {
			t := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			if t == "" {
				return "", "", errors.New("tenant_missing")
			}
			return t, "principal-test", nil
		},
	}
}

func newGatewayTestMux(t *testing.T, deps GatewayDeps) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	if err := RegisterGatewayRoutes(mux, deps); err != nil {
		t.Fatalf("RegisterGatewayRoutes: %v", err)
	}
	return mux
}

// TestMCPGatewayHealthRouteAlwaysOn — /api/v1/mcp/gateway/health is always
// reachable, regardless of GatewayEnabled. Health is an operator probe; the
// disabled-default upstream-routes gating is documented in the body so a
// "gateway disabled" deployment is still observable.
func TestMCPGatewayHealthRouteAlwaysOn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		store := &fakeGatewayStore{}
		mux := newGatewayTestMux(t, testDeps(store, enabled))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/gateway/health", nil)
		req.Header.Set("X-Tenant-ID", "tenant-a")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("health route enabled=%v: got status %d body=%q", enabled, rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("health route enabled=%v: body is not JSON: %v body=%q", enabled, err, rr.Body.String())
		}
		if _, ok := body["status"]; !ok {
			t.Fatalf("health route enabled=%v: body missing 'status' key: %v", enabled, body)
		}
		if len(store.sessions) != 0 || len(store.executions) != 0 {
			t.Fatalf("health route created unexpected session/execution: sessions=%d executions=%d", len(store.sessions), len(store.executions))
		}
	}
}

func TestHandleHealth_RejectsUnauthenticated(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/gateway/health", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("health without tenant = %d body=%q, want 401", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "gateway_enabled") {
		t.Fatalf("health unauthenticated leaked gateway status body=%q", rr.Body.String())
	}
}

// TestMCPGatewayConfigRouteRedacted — /api/v1/mcp/gateway/config returns the
// effective gateway config without leaking secret-shaped values. Even if a
// future operator misconfigures an upstream URL with an embedded bearer
// token, this route MUST redact before emitting; mirrors the policy of
// existing /api/v1/mcp/* endpoints.
func TestMCPGatewayConfigRouteRedacted(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/gateway/config", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("config route: got status %d body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, marker := range []string{"sk-", "ghp_", "AKIA", "Bearer "} {
		if strings.Contains(body, marker) {
			t.Fatalf("config route leaked secret marker %q: body=%q", marker, body)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("config route: body is not JSON: %v body=%q", err, body)
	}
	if _, ok := doc["gateway_enabled"]; !ok {
		t.Fatalf("config route: body missing 'gateway_enabled' key: %v", doc)
	}
}

func TestMustGatewayID_NoCollisionUnderRandFailure(t *testing.T) {
	prevRandRead := gatewayRandRead
	gatewayRandRead = func([]byte) (int, error) {
		return 0, errors.New("rand unavailable")
	}
	t.Cleanup(func() { gatewayRandRead = prevRandRead })

	const calls = 1000
	ids := make(chan string, calls)
	var wg sync.WaitGroup
	wg.Add(calls)
	for i := 0; i < calls; i++ {
		go func() {
			defer wg.Done()
			ids <- mustGatewayID("event")
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, calls)
	for id := range ids {
		if !strings.Contains(id, "_fb_") {
			t.Fatalf("fallback gateway ID uses unexpected form %q", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate fallback gateway ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != calls {
		t.Fatalf("unique fallback IDs = %d, want %d", len(seen), calls)
	}
}

// TestMCPGatewayDisabledByDefault — when GatewayEnabled=false the upstream
// route family returns 503 with a structured error body, and crucially does
// NOT touch the Store (no spurious session/execution creation).
func TestMCPGatewayDisabledByDefault(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, false))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/upstream/connect", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled-default upstream: got status %d want 503; body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "gateway disabled") {
		t.Fatalf("disabled-default: body missing 'gateway disabled': %q", rr.Body.String())
	}
	if len(store.sessions) != 0 || len(store.executions) != 0 {
		t.Fatalf("disabled-default: unexpected store writes sessions=%d executions=%d", len(store.sessions), len(store.executions))
	}
}

// TestMCPGatewayEnabledNoOpForwarding — when GatewayEnabled=true and the
// upstream registry is empty (EDGE-101 will populate), the upstream route
// returns a 503 'no upstream configured' so a misconfigured deploy fails
// loudly and emits a store-supported failed event.
func TestMCPGatewayEnabledNoOpForwarding(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/upstream/connect", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("enabled-no-op upstream: got status %d want 503; body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "no upstream configured") {
		t.Fatalf("enabled-no-op: body missing 'no upstream configured': %q", rr.Body.String())
	}
	if len(store.sessions) != 1 || len(store.executions) != 1 || len(store.events) != 1 {
		t.Fatalf("enabled-no-op evidence = sessions:%d executions:%d events:%d, want 1/1/1", len(store.sessions), len(store.executions), len(store.events))
	}
	if got := store.events[0].Kind; got != edge.EventKindMCPServerFailed {
		t.Fatalf("enabled-no-op event.Kind = %q, want %q", got, edge.EventKindMCPServerFailed)
	}
	if got := store.events[0].Decision; got != edge.DecisionDeny {
		t.Fatalf("enabled-no-op event.Decision = %q, want %q", got, edge.DecisionDeny)
	}
}

// TestMCPGatewayTenantAttribution — a successful client connect MUST create
// EdgeSession + AgentExecution with the tenant resolved from the request
// (X-Tenant-ID + auth context) — NEVER from client-supplied claims (task
// rail #3). PrincipalID comes from the authenticated principal.
func TestMCPGatewayTenantAttribution(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", strings.NewReader(`{"mcp_version":"2025-06-18","claimed_tenant":"tenant-spoofed"}`))
	req.Header.Set("X-Tenant-ID", "tenant-a")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusAccepted {
		t.Fatalf("connect: got status %d want 200/202; body=%q", rr.Code, rr.Body.String())
	}
	if len(store.sessions) != 1 {
		t.Fatalf("expected 1 session created; got %d", len(store.sessions))
	}
	if got := store.sessions[0].TenantID; got != "tenant-a" {
		t.Fatalf("session.TenantID: got %q want %q (must come from X-Tenant-ID, not body claims)", got, "tenant-a")
	}
	if got := store.sessions[0].PrincipalID; got != "principal-test" {
		t.Fatalf("session.PrincipalID: got %q want %q (must come from auth context)", got, "principal-test")
	}
	if len(store.executions) != 1 {
		t.Fatalf("expected 1 execution created; got %d", len(store.executions))
	}
	if got := store.executions[0].TenantID; got != "tenant-a" {
		t.Fatalf("execution.TenantID: got %q want %q", got, "tenant-a")
	}
	if got := store.executions[0].SessionID; got == "" || got != store.sessions[0].SessionID {
		t.Fatalf("execution.SessionID: got %q want session %q", got, store.sessions[0].SessionID)
	}
	// Connect-success event emitted with the MCP layer + connected kind.
	if len(store.events) != 1 {
		t.Fatalf("expected 1 connect event; got %d", len(store.events))
	}
	if got := store.events[0].Kind; got != edge.EventKindMCPServerConnected {
		t.Fatalf("event.Kind: got %q want %q", got, edge.EventKindMCPServerConnected)
	}
	if got := store.events[0].Layer; got != edge.LayerMCP {
		t.Fatalf("event.Layer: got %q want %q", got, edge.LayerMCP)
	}
	if got := store.events[0].TenantID; got != "tenant-a" {
		t.Fatalf("event.TenantID: got %q want %q", got, "tenant-a")
	}
}

// TestMCPGatewayConnectFailureDoesNotEmitOrphanFailedEvent locks the QA
// rejection fix: if session creation fails, RedisStore cannot persist an
// AgentActionEvent because no AgentExecution exists. The gateway must not
// rely on fake-store orphan events to claim durable mcp.server.failed evidence.
func TestMCPGatewayConnectFailureDoesNotEmitOrphanFailedEvent(t *testing.T) {
	store := &fakeGatewayStore{createErr: errors.New("redis unavailable")}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code < 500 {
		t.Fatalf("connect-failure: got status %d want 5xx; body=%q", rr.Code, rr.Body.String())
	}
	// CreateSession failed before any execution attempt, so no execution
	// must be present — no dangling state.
	if len(store.executions) != 0 {
		t.Fatalf("connect-failure left dangling execution: count=%d", len(store.executions))
	}
	if len(store.events) != 0 {
		t.Fatalf("connect-failure created orphan event: got %d events", len(store.events))
	}
}

func TestMCPGatewayClientConnectAppendFailureReturns500(t *testing.T) {
	store := &fakeGatewayStore{appendErr: errors.New("redis unavailable")}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("append-failure: got status %d want 500; body=%q", rr.Code, rr.Body.String())
	}
	if len(store.sessions) != 1 || len(store.executions) != 1 {
		t.Fatalf("append-failure evidence root = sessions:%d executions:%d, want 1/1", len(store.sessions), len(store.executions))
	}
	if len(store.events) != 0 {
		t.Fatalf("append-failure persisted event despite append error: %d", len(store.events))
	}
}

// TestMCPGatewayMissingTenantRejected — a connect with no X-Tenant-ID is
// rejected at the boundary before touching the Store. Tenant attribution
// is mandatory (epic rail "All /api/v1/edge endpoints require existing
// auth, X-Tenant-ID, tenant isolation").
func TestMCPGatewayMissingTenantRejected(t *testing.T) {
	store := &fakeGatewayStore{}
	mux := newGatewayTestMux(t, testDeps(store, true))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/gateway/clients/connect", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("missing tenant: got status %d want 401/403; body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(strings.ToLower(rr.Body.String()), "tenant") {
		t.Fatalf("missing tenant: body should mention tenant: %q", rr.Body.String())
	}
	if len(store.sessions) != 0 || len(store.executions) != 0 || len(store.events) != 0 {
		t.Fatalf("missing tenant must NOT touch store; got sessions=%d executions=%d events=%d", len(store.sessions), len(store.executions), len(store.events))
	}
}
