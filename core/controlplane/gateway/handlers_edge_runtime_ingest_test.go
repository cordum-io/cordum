package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/runtimeingest"
	"github.com/cordum/cordum/core/licensing"
)

// enableRuntimeIngest flips the gateway feature flag on for the lifetime of
// the test. The default (unset) must stay 503 service_unavailable.
func enableRuntimeIngest(t *testing.T) {
	t.Helper()
	t.Setenv(envRuntimeIngestEnabled, "true")
}

func disableRuntimeIngest(t *testing.T) {
	t.Helper()
	t.Setenv(envRuntimeIngestEnabled, "")
}

func runtimeIngestBody(sessionID, executionID, tenantID, sourceEventID string) string {
	return runtimeIngestBodyForSource("tetragon-test", sessionID, executionID, tenantID, sourceEventID)
}

func runtimeIngestBodyForSource(sourceID, sessionID, executionID, tenantID, sourceEventID string) string {
	return runtimeIngestBodyWithNonceForSource(
		sourceID,
		sessionID,
		executionID,
		tenantID,
		runtimeIngestNonce(sourceEventID),
		sourceEventID,
	)
}

func runtimeIngestBodyWithNonceForSource(sourceID, sessionID, executionID, tenantID, nonce, sourceEventID string) string {
	return `{
		"source": {"source_id":"` + sourceID + `"},
		"nonce":"` + nonce + `",
		"batch_id":"batch-` + sourceEventID + `",
		"events": [{
			"tenant_id":"` + tenantID + `",
			"session_id":"` + sessionID + `",
			"execution_id":"` + executionID + `",
			"source_event_id":"` + sourceEventID + `",
			"observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"process": {"executable_basename":"curl","argument_count":1}
		}]
	}`
}

func runtimeIngestBatchBodyForEvents(sourceID, sessionID, executionID, tenantID, nonce string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"source":{"source_id":%q},"nonce":%q,"events":[`, sourceID, nonce)
	for i := range count {
		if i > 0 {
			b.WriteByte(',')
		}
		sourceEventID := fmt.Sprintf("rt-n1-%03d", i)
		fmt.Fprintf(
			&b,
			`{"tenant_id":%q,"session_id":%q,"execution_id":%q,"source_event_id":%q,`+
				`"observed_at":"2026-05-17T12:00:00Z","kind":"runtime.process.exec",`+
				`"process":{"executable_basename":"curl","argument_count":1}}`,
			tenantID,
			sessionID,
			executionID,
			sourceEventID,
		)
	}
	b.WriteString(`]}`)
	return b.String()
}

func runtimeIngestNonce(seed string) string {
	var b strings.Builder
	for _, r := range seed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	value := "nonce-" + b.String()
	if len(value) < 16 {
		value += strings.Repeat("0", 16-len(value))
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

const testRuntimeCollectorRole = "runtime_collector"

func setupRuntimeIngestRBAC(t *testing.T, s *server) {
	t.Helper()
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        "user",
		Description: "test same-tenant job writer without runtime ingest permission",
		Permissions: []string{
			auth.PermJobsWrite,
		},
	}); err != nil {
		t.Fatalf("put user role: %v", err)
	}
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        testRuntimeCollectorRole,
		Description: "test trusted runtime collector",
		Permissions: []string{
			auth.PermJobsWrite, // Keeps this role accepted by the vulnerable handler before the fix.
			auth.PermRuntimeIngest,
		},
	}); err != nil {
		t.Fatalf("put runtime collector role: %v", err)
	}
}

func runtimeIngestAuthContext(role, principalID string) *auth.AuthContext {
	return runtimeIngestAuthContextForTenant(edgeRouteTenant, role, principalID)
}

func runtimeIngestAuthContextForTenant(tenantID, role, principalID string) *auth.AuthContext {
	return &auth.AuthContext{
		Tenant:      tenantID,
		PrincipalID: principalID,
		Role:        role,
		AuthSource:  auth.AuthSourceAPIKey,
	}
}

func postRuntimeIngestWithAuth(t *testing.T, s *server, authCtx *auth.AuthContext, body string) *httptest.ResponseRecorder {
	return postRuntimeIngestWithAuthForTenant(t, s, authCtx, edgeRouteTenant, body)
}

func postRuntimeIngestWithAuthForTenant(t *testing.T, s *server, authCtx *auth.AuthContext, headerTenant, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/runtime/events", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", headerTenant)
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, authCtx)
	rr := httptest.NewRecorder()
	s.handleEdgeRuntimeIngest(rr, req)
	return rr
}

func postRuntimeIngestWithAuthAndContext(
	t *testing.T,
	s *server,
	ctx context.Context,
	authCtx *auth.AuthContext,
	headerTenant string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/runtime/events", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-Tenant-ID", headerTenant)
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, authCtx)
	rr := httptest.NewRecorder()
	s.handleEdgeRuntimeIngest(rr, req)
	return rr
}

func createRuntimeIngestBoundParents(t *testing.T, s *server, suffix, collectorID string) (edgecore.EdgeSession, edgecore.AgentExecution) {
	return createRuntimeIngestBoundParentsForTenant(t, s, edgeRouteTenant, suffix, collectorID)
}

func createRuntimeIngestBoundParentsForTenant(t *testing.T, s *server, tenantID, suffix, collectorID string) (edgecore.EdgeSession, edgecore.AgentExecution) {
	t.Helper()
	now := time.Now().UTC()
	session := edgecore.EdgeSession{
		SessionID:      "sess-runtime-" + suffix,
		TenantID:       tenantID,
		PrincipalID:    collectorID,
		PrincipalType:  edgecore.PrincipalTypeService,
		AgentProduct:   "runtime-sidecar",
		AgentVersion:   "test",
		Mode:           edgecore.SessionModeLocalDev,
		TraceID:        "trace-runtime-" + suffix,
		PolicySnapshot: "snap-runtime",
		PolicyMode:     edgecore.PolicyModeObserve,
		Status:         edgecore.SessionStatusRunning,
		RiskSummary: edgecore.RiskSummary{
			MaxRisk: edgecore.RiskLevelLow,
		},
		StartedAt: now,
	}
	if err := s.edgeStore.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create runtime session: %v", err)
	}
	execution := edgecore.AgentExecution{
		ExecutionID:    "exec-runtime-" + suffix,
		SessionID:      session.SessionID,
		TenantID:       tenantID,
		Adapter:        edgecore.AdapterRuntimeSidecar,
		Mode:           edgecore.ExecutionModeLocalDev,
		TraceID:        session.TraceID,
		WorkerID:       collectorID,
		PolicySnapshot: session.PolicySnapshot,
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      now,
	}
	if err := s.edgeStore.CreateExecution(context.Background(), execution); err != nil {
		t.Fatalf("create runtime execution: %v", err)
	}
	return session, execution
}

func TestRuntimeIngest_RejectsRegularUserWithJobsWritePerm(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "regular-user", "principal-edge-user")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext("user", "principal-edge-user"),
		runtimeIngestBodyForSource("principal-edge-user", session.SessionID, execution.ExecutionID, edgeRouteTenant, "forged-user"))

	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
}

func TestRuntimeIngest_RejectsAdminWithoutRuntimeCollectorPermission(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        "admin",
		Description: "test admin wildcard without runtime ingest permission",
		Permissions: []string{
			auth.PermAdminAll,
			auth.PermJobsWrite,
		},
	}); err != nil {
		t.Fatalf("put admin role: %v", err)
	}
	session, execution := createRuntimeIngestBoundParents(t, s, "admin-user", "principal-admin")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext("admin", "principal-admin"),
		runtimeIngestBodyForSource("principal-admin", session.SessionID, execution.ExecutionID, edgeRouteTenant, "forged-admin"))

	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
}

func TestRuntimeIngest_RejectsMismatchedSourceID(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "mismatched-source", "collector-a")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"),
		runtimeIngestBodyForSource("collector-b", session.SessionID, execution.ExecutionID, edgeRouteTenant, "source-mismatch"))

	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
		t.Fatalf("mismatched source status = %d, want 400 or 403 body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeIngest_RejectsUnauthorizedSession(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	_, _ = createRuntimeIngestBoundParents(t, s, "authorized-session", "collector-a")
	otherSession, otherExecution := createRuntimeIngestBoundParents(t, s, "unauthorized-session", "collector-b")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"),
		runtimeIngestBodyForSource("collector-a", otherSession.SessionID, otherExecution.ExecutionID, edgeRouteTenant, "unauthorized-session"))

	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
}

func TestRuntimeIngest_RejectsMissingCollectorIdentity(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "missing-identity", "collector-a")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, ""),
		runtimeIngestBodyForSource("collector-a", session.SessionID, execution.ExecutionID, edgeRouteTenant, "missing-collector-identity"))

	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("missing collector identity status = %d, want 401 or 403 body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeIngest_AcceptsTrustedCollectorOnBoundSource(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "trusted-collector", "collector-a")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"),
		runtimeIngestBodyForSource("collector-a", session.SessionID, execution.ExecutionID, edgeRouteTenant, "trusted-collector"))

	if rr.Code != http.StatusCreated {
		t.Fatalf("trusted collector status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeIngestDisabledReturns503(t *testing.T) {
	disableRuntimeIngest(t)
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	execution := createEdgeRouteExecution(t, handler, session.SessionID)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/runtime/events",
		runtimeIngestBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "rt-evt-disabled"))

	assertEdgeErrorShape(t, rr, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable)
}

func TestRuntimeIngestEnabledRequiresTenantHeader(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/runtime/events",
		strings.NewReader(runtimeIngestBody("ignored", "ignored", edgeRouteTenant, "no-tenant-header")))
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"))
	// Deliberately omit X-Tenant-ID.
	rr := httptest.NewRecorder()
	s.handleEdgeRuntimeIngest(rr, req)

	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeTenantRequired)
}

func TestRuntimeIngestEnabledRequiresAuth(t *testing.T) {
	enableRuntimeIngest(t)
	_, handler := newEdgeRouteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/runtime/events",
		strings.NewReader(runtimeIngestBody("s", "e", edgeRouteTenant, "no-auth")))
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	// Deliberately omit X-API-Key.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("no-auth status = %d, want 401 or 403 body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeIngestEnabledRejectsBodyTenantMismatch(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "tenant-mismatch", "collector-a")
	// header=A, body events stamp tenant=B → 403 tenant_mismatch.
	body := runtimeIngestBodyForSource("collector-a", session.SessionID, execution.ExecutionID, edgeRouteOtherTenant, "mismatch-1")
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("body-tenant mismatch status = %d, want 403 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeTenantMismatch) {
		t.Fatalf("missing %q in body: %s", edgeErrCodeTenantMismatch, rr.Body.String())
	}
}

func TestRuntimeIngestEnabledRejectsCrossTenantSession(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	tenantBSession, tenantBExecution := createRuntimeIngestBoundParentsForTenant(t, s, edgeRouteOtherTenant, "tenant-b", "collector-b")
	body := runtimeIngestBodyForSource("collector-a", tenantBSession.SessionID, tenantBExecution.ExecutionID, edgeRouteTenant, "xtenant-sess")
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)

	assertCrossTenantBlocked(t, rr, "runtime ingest cross-tenant parents")
	if strings.Contains(rr.Body.String(), tenantBSession.SessionID) || strings.Contains(rr.Body.String(), tenantBExecution.ExecutionID) {
		t.Fatalf("runtime ingest cross-tenant parents leaked tenant B IDs: %s", rr.Body.String())
	}
}

func TestRuntimeIngestEnabledRejectsMissingParent(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	body := runtimeIngestBodyForSource("collector-a", "edge-session-does-not-exist", "edge-exec-does-not-exist", edgeRouteTenant, "missing-parent")
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

func TestRuntimeIngestEnabledRejectsExecutionSessionMismatch(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "exec-mismatch-a", "collector-a")
	otherSession, _ := createRuntimeIngestBoundParents(t, s, "exec-mismatch-b", "collector-a")
	body := runtimeIngestBodyForSource("collector-a", otherSession.SessionID, execution.ExecutionID, edgeRouteTenant, "exec-mismatch")
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)
	_ = session
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("execution mismatch status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), edgeErrCodeExecutionMismatch) {
		t.Fatalf("missing %q: %s", edgeErrCodeExecutionMismatch, rr.Body.String())
	}
}

func TestRuntimeIngestEnabledRejectsEmptyBatch(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	body := `{"source":{"source_id":"collector-a"},"nonce":"nonce-empty-batch-1","events":[]}`
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

func TestRuntimeIngestEnabledRejectsMissingSourceID(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "missing-source", "collector-a")
	body := `{
		"source":{"source_id":""},
		"nonce":"nonce-missing-source-1",
		"events":[{
			"tenant_id":"` + edgeRouteTenant + `",
			"session_id":"` + session.SessionID + `",
			"execution_id":"` + execution.ExecutionID + `",
			"source_event_id":"se","observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"process":{"executable_basename":"curl","argument_count":1}
		}]
	}`
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

func TestRuntimeIngestEnabledRejectsForbiddenRawKey(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "forbidden-raw-key", "collector-a")
	body := `{
		"source":{"source_id":"collector-a"},
		"nonce":"nonce-forbidden-raw-1",
		"events":[{
			"tenant_id":"` + edgeRouteTenant + `",
			"session_id":"` + session.SessionID + `",
			"execution_id":"` + execution.ExecutionID + `",
			"source_event_id":"se","observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"argv":["curl","https://evil.example.com"]
		}]
	}`
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("forbidden-key status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	// The error must not echo the raw argv values back to the client.
	if strings.Contains(rr.Body.String(), "evil.example.com") {
		t.Fatalf("error response leaked raw argv: %s", rr.Body.String())
	}
}

func TestRuntimeIngestEnabledRejectsOversizeBatch(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "oversize", "collector-a")
	// Build a batch with too many events to exceed MaxRuntimeBatchEvents.
	var sb strings.Builder
	sb.WriteString(`{"source":{"source_id":"collector-a"},"nonce":"nonce-oversize-batch-1","events":[`)
	for i := range 300 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{
			"tenant_id":"` + edgeRouteTenant + `",
			"session_id":"` + session.SessionID + `",
			"execution_id":"` + execution.ExecutionID + `",
			"source_event_id":"se-` + strings.Repeat("z", i+1) + `",
			"observed_at":"2026-05-17T12:00:00Z",
			"kind":"runtime.process.exec",
			"process":{"executable_basename":"curl","argument_count":1}
		}`)
	}
	sb.WriteString("]}")
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), sb.String())
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize batch status = %d, want 413 body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeIngestEnabledAppendsEventsThroughStore(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "append", "tetragon-test")

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"),
		runtimeIngestBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "rt-append-1"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}

	var resp runtimeIngestResponseShape
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if resp.AcceptedCount != 1 {
		t.Fatalf("accepted_count = %d, want 1", resp.AcceptedCount)
	}

	page := readEdgeEventsFromStore(t, s, execution.ExecutionID)
	var runtimeEvents []edgecore.AgentActionEvent
	for _, ev := range page.Items {
		if ev.Layer == edgecore.LayerRuntime {
			runtimeEvents = append(runtimeEvents, ev)
		}
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("stored runtime events = %d, want 1", len(runtimeEvents))
	}
	got := runtimeEvents[0]
	if got.Kind != edgecore.EventKindRuntimeProcessExec {
		t.Fatalf("stored Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeProcessExec)
	}
	if got.Decision != edgecore.DecisionRecorded {
		t.Fatalf("stored Decision = %q, want %q", got.Decision, edgecore.DecisionRecorded)
	}
	if got.TenantID != edgeRouteTenant {
		t.Fatalf("stored TenantID = %q, want %q", got.TenantID, edgeRouteTenant)
	}
}

func TestRuntimeIngestDuplicateBatchReturnsIdempotentSuccess(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "duplicate", "tetragon-test")
	body := runtimeIngestBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "rt-replay-duplicate")

	first := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first ingest status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	second := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate ingest status = %d, want 200 body=%s", second.Code, second.Body.String())
	}
	var resp runtimeIngestResponseShape
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode replay response: %v body=%s", err, second.Body.String())
	}
	if !resp.Replayed || resp.AcceptedCount != 0 || resp.DroppedCount != 0 {
		t.Fatalf("replay response = %+v, want replayed true and zero counts", resp)
	}
	if got := len(runtimeEventsForExecution(t, s, execution.ExecutionID)); got != 1 {
		t.Fatalf("stored runtime events after duplicate = %d, want 1", got)
	}
}

func TestRuntimeIngestLegitimateRetryAfterFailureSucceeds(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "retry", "tetragon-test")
	s.edgeStore = &runtimeIngestAppendFailStore{
		Store:     s.edgeStore,
		failCount: 1,
		err:       errors.New("forced transient append failure"),
	}
	body := runtimeIngestBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "rt-retry-after-failure")

	first := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first ingest status = %d, want 500 body=%s", first.Code, first.Body.String())
	}
	retry := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry ingest status = %d, want 201 body=%s", retry.Code, retry.Body.String())
	}
	var resp runtimeIngestResponseShape
	if err := json.Unmarshal(retry.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode retry response: %v body=%s", err, retry.Body.String())
	}
	if resp.Replayed || resp.AcceptedCount != 1 {
		t.Fatalf("retry response = %+v, want first-seen accepted retry", resp)
	}
	if got := len(runtimeEventsForExecution(t, s, execution.ExecutionID)); got != 1 {
		t.Fatalf("stored runtime events after retry = %d, want 1", got)
	}
}

func TestRuntimeIngestReplayWindowFullRefuses(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	const collectorID = "collector-a"
	s.runtimeReplayWindow = runtimeingest.NewReplayWindow(s.jobStore.Client(), time.Hour, 1)
	ok, err := s.runtimeReplayWindow.Reserve(context.Background(), edgeRouteTenant, collectorID, "nonce-window-full-seed")
	if err != nil || !ok {
		t.Fatalf("seed replay window Reserve = (%v, %v); want (true, nil)", ok, err)
	}
	session, execution := createRuntimeIngestBoundParents(t, s, "window-full", collectorID)
	body := runtimeIngestBodyWithNonceForSource(
		collectorID,
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		"nonce-window-full-next",
		"rt-window-full-next",
	)

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, collectorID), body)
	assertEdgeErrorShape(t, rr, http.StatusTooManyRequests, edgeErrCodeReplayWindowFull)
	if got := len(runtimeEventsForExecution(t, s, execution.ExecutionID)); got != 0 {
		t.Fatalf("stored runtime events after window-full refusal = %d, want 0", got)
	}
}

func TestRuntimeReplay_SharedRedisDedup(t *testing.T) {
	enableRuntimeIngest(t)
	s1, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s1)

	s2, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s2)
	if s1 == s2 {
		t.Fatal("newEdgeRouteTestServer returned aliased server pointers; want independent instances")
	}
	sharedClient := s1.jobStore.Client()
	s2.edgeStore = edgecore.NewRedisStoreFromClient(sharedClient)
	s2.runtimeReplayWindow = runtimeingest.NewReplayWindow(
		sharedClient,
		runtimeingest.ReplayWindowTTL,
		runtimeingest.MaxReplayWindowCardinality,
	)
	session, execution := createRuntimeIngestBoundParents(t, s1, "cross-instance", "tetragon-test")
	body := runtimeIngestBody(session.SessionID, execution.ExecutionID, edgeRouteTenant, "rt-cross-instance")

	first := postRuntimeIngestWithAuth(t, s1, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first instance ingest status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	second := postRuntimeIngestWithAuth(t, s2, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if second.Code != http.StatusOK {
		t.Fatalf("second instance replay status = %d, want 200 body=%s", second.Code, second.Body.String())
	}
	var resp runtimeIngestResponseShape
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode cross-instance replay response: %v body=%s", err, second.Body.String())
	}
	if !resp.Replayed {
		t.Fatalf("cross-instance response = %+v, want replayed true", resp)
	}
	if got := len(runtimeEventsForExecution(t, s1, execution.ExecutionID)); got != 1 {
		t.Fatalf("stored runtime events after cross-instance replay = %d, want 1", got)
	}
}

func TestReplayRelease_SurvivesClientDisconnect(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "release-cancel", "tetragon-test")
	ctx, cancel := context.WithCancel(context.Background())
	s.edgeStore = &runtimeIngestCancelAppendStore{Store: s.edgeStore, cancel: cancel}
	const nonce = "nonce-release-cancel-1"
	body := runtimeIngestBodyWithNonceForSource(
		"tetragon-test",
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		nonce,
		"rt-release-cancel",
	)

	first := postRuntimeIngestWithAuthAndContext(t, s, ctx, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), edgeRouteTenant, body)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first ingest status = %d, want 500 body=%s", first.Code, first.Body.String())
	}
	retry := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry after canceled append status = %d, want 201 body=%s", retry.Code, retry.Body.String())
	}
	if got := len(runtimeEventsForExecution(t, s, execution.ExecutionID)); got != 1 {
		t.Fatalf("stored runtime events after retry = %d, want 1", got)
	}
}

func TestRuntimeIngest_NoN1QueryPerEvent(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "n1", "tetragon-test")
	counting := &runtimeIngestParentCountingStore{Store: s.edgeStore}
	s.edgeStore = counting

	body := runtimeIngestBatchBodyForEvents(
		"tetragon-test",
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		"nonce-n1-query-cache",
		runtimeingest.MaxRuntimeBatchEvents,
	)
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("batch ingest status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	sessionGets, executionGets := counting.counts()
	if sessionGets > 1 || executionGets > 1 {
		t.Fatalf("parent lookups = session:%d execution:%d, want <=1 each for shared parents", sessionGets, executionGets)
	}
}

func TestRuntimeIngestNoNonceLeakInLogs(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "log-leak", "tetragon-test")
	s.edgeStore = &runtimeIngestAppendFailStore{
		Store:     s.edgeStore,
		failCount: 1,
		err:       errors.New("forced append failure for log capture"),
	}
	const nonce = "nonce-log-canary-0001"
	body := runtimeIngestBodyWithNonceForSource(
		"tetragon-test",
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		nonce,
		"rt-log-leak",
	)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("ingest status = %d, want 500 body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(logs.String(), nonce) {
		t.Fatalf("runtime ingest logs leaked nonce %q: %s", nonce, logs.String())
	}
	if strings.Contains(rr.Body.String(), nonce) {
		t.Fatalf("runtime ingest error response leaked nonce %q: %s", nonce, rr.Body.String())
	}
}

func TestRuntimeIngestPreservesCollectorBindingPostFix(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "binding", "collector-a")
	const nonce = "nonce-binding-postfix-1"
	forged := runtimeIngestBodyWithNonceForSource(
		"collector-b",
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		nonce,
		"rt-binding",
	)

	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), forged)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
		t.Fatalf("forged collector status = %d, want 400/403 body=%s", rr.Code, rr.Body.String())
	}
	valid := runtimeIngestBodyWithNonceForSource(
		"collector-a",
		session.SessionID,
		execution.ExecutionID,
		edgeRouteTenant,
		nonce,
		"rt-binding",
	)
	accepted := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "collector-a"), valid)
	if accepted.Code != http.StatusCreated {
		t.Fatalf("valid collector retry with same nonce status = %d, want 201 body=%s", accepted.Code, accepted.Body.String())
	}
	if got := len(runtimeEventsForExecution(t, s, execution.ExecutionID)); got != 1 {
		t.Fatalf("stored runtime events after binding test = %d, want 1", got)
	}
}

func TestRuntimeIngestEnabledMethodGETIsRejected(t *testing.T) {
	enableRuntimeIngest(t)
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRouteGET(t, handler, "/api/v1/edge/runtime/events")
	if rr.Code == http.StatusOK {
		t.Fatalf("GET runtime/events returned 200; want 404/405. body=%s", rr.Body.String())
	}
}

// runtimeIngestResponseShape mirrors handlers_edge_runtime_ingest.go's
// response struct for test decoding without exporting the handler-private
// type. Tests assert on the field via JSON tag, so this shape must stay in
// sync with the production response.
type runtimeIngestResponseShape struct {
	AcceptedCount int  `json:"accepted_count"`
	DroppedCount  int  `json:"dropped_count"`
	Replayed      bool `json:"replayed,omitempty"`
}

func runtimeEventsForExecution(t *testing.T, s *server, executionID string) []edgecore.AgentActionEvent {
	t.Helper()
	page := readEdgeEventsFromStore(t, s, executionID)
	events := make([]edgecore.AgentActionEvent, 0, len(page.Items))
	for _, ev := range page.Items {
		if ev.Layer == edgecore.LayerRuntime {
			events = append(events, ev)
		}
	}
	return events
}

type runtimeIngestAppendFailStore struct {
	edgecore.Store
	mu        sync.Mutex
	failCount int
	err       error
}

func (s *runtimeIngestAppendFailStore) AppendEvents(
	ctx context.Context,
	events []edgecore.AgentActionEvent,
) ([]edgecore.AgentActionEvent, error) {
	s.mu.Lock()
	if s.failCount > 0 {
		s.failCount--
		err := s.err
		s.mu.Unlock()
		if err == nil {
			err = errors.New("forced runtime ingest append failure")
		}
		return nil, err
	}
	s.mu.Unlock()
	return s.Store.AppendEvents(ctx, events)
}

type runtimeIngestCancelAppendStore struct {
	edgecore.Store
	mu     sync.Mutex
	cancel context.CancelFunc
	failed bool
}

func (s *runtimeIngestCancelAppendStore) AppendEvents(
	ctx context.Context,
	events []edgecore.AgentActionEvent,
) ([]edgecore.AgentActionEvent, error) {
	s.mu.Lock()
	if !s.failed {
		s.failed = true
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil, context.Canceled
	}
	s.mu.Unlock()
	return s.Store.AppendEvents(ctx, events)
}

type runtimeIngestParentCountingStore struct {
	edgecore.Store
	mu            sync.Mutex
	sessionGets   int
	executionGets int
}

func (s *runtimeIngestParentCountingStore) GetSession(
	ctx context.Context,
	tenantID string,
	sessionID string,
) (*edgecore.EdgeSession, bool, error) {
	s.mu.Lock()
	s.sessionGets++
	s.mu.Unlock()
	return s.Store.GetSession(ctx, tenantID, sessionID)
}

func (s *runtimeIngestParentCountingStore) GetExecution(
	ctx context.Context,
	tenantID string,
	executionID string,
) (*edgecore.AgentExecution, bool, error) {
	s.mu.Lock()
	s.executionGets++
	s.mu.Unlock()
	return s.Store.GetExecution(ctx, tenantID, executionID)
}

func (s *runtimeIngestParentCountingStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionGets, s.executionGets
}

// TestRuntimeIngestEnabledStoresRedactedRuntimeEvent is the
// persistence/integration test mandated by Phase 7. It creates a real
// session + execution through the gateway (Redis-backed via miniredis),
// posts a runtime batch carrying an AWS-key-shaped path, then reads the
// execution's events back from the store and asserts:
//   - the stored event has Layer=runtime, Kind=runtime.process.exec,
//     Decision=RECORDED, and the correct tenant/session/execution,
//   - Labels carry runtime.source_id stamped by the adapter,
//   - InputRedacted does NOT carry the AWS-key marker,
//   - the response shape matches the documented contract.
func TestRuntimeIngestEnabledStoresRedactedRuntimeEvent(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "redacted", "tetragon-clusterA")

	body := `{
		"source":{"source_id":"tetragon-clusterA"},
		"nonce":"nonce-redacted-integ-1",
		"events":[{
			"tenant_id":"` + edgeRouteTenant + `",
			"session_id":"` + session.SessionID + `",
			"execution_id":"` + execution.ExecutionID + `",
			"source_event_id":"rt-integ-1",
			"observed_at":"2026-05-17T12:34:56Z",
			"kind":"runtime.file.read",
			"file":{"operation":"read","path_redacted":"/var/AKIAIOSFODNN7EXAMPLE/data"},
			"labels":{"node":"node-7"}
		}]
	}`
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-clusterA"), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var resp runtimeIngestResponseShape
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if resp.AcceptedCount != 1 || resp.DroppedCount != 0 {
		t.Fatalf("response counts = accepted:%d dropped:%d, want 1/0 body=%s", resp.AcceptedCount, resp.DroppedCount, rr.Body.String())
	}

	page := readEdgeEventsFromStore(t, s, execution.ExecutionID)
	var runtimeEvents []edgecore.AgentActionEvent
	for _, ev := range page.Items {
		if ev.Layer == edgecore.LayerRuntime {
			runtimeEvents = append(runtimeEvents, ev)
		}
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("stored runtime events = %d, want 1; all events=%#v", len(runtimeEvents), page.Items)
	}
	got := runtimeEvents[0]
	if got.Kind != edgecore.EventKindRuntimeFileRead {
		t.Fatalf("Kind = %q, want %q", got.Kind, edgecore.EventKindRuntimeFileRead)
	}
	if got.Decision != edgecore.DecisionRecorded {
		t.Fatalf("Decision = %q, want %q", got.Decision, edgecore.DecisionRecorded)
	}
	if got.TenantID != edgeRouteTenant {
		t.Fatalf("TenantID = %q, want %q", got.TenantID, edgeRouteTenant)
	}
	if got.SessionID != session.SessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, session.SessionID)
	}
	if got.ExecutionID != execution.ExecutionID {
		t.Fatalf("ExecutionID = %q, want %q", got.ExecutionID, execution.ExecutionID)
	}
	if got.Labels["runtime.source_id"] != "tetragon-clusterA" {
		t.Fatalf("Labels.runtime.source_id = %q, want %q", got.Labels["runtime.source_id"], "tetragon-clusterA")
	}
	if got.Labels["node"] != "node-7" {
		t.Fatalf("Labels.node = %q, want %q", got.Labels["node"], "node-7")
	}
	// The AWS-key-shaped substring must NOT survive into the stored
	// AgentActionEvent — that is the entire point of edge redaction.
	storedJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal stored event: %v", err)
	}
	if strings.Contains(string(storedJSON), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("stored event leaked AKIA token: %s", storedJSON)
	}
}

// TestRuntimeIngestAllEventKinds_EndToEnd drives every supported
// runtime event kind — process.exec, file.read, file.write,
// network.connect, dns.query — through `s.registerRoutes`' mux to
// `/api/v1/edge/runtime/events` with a miniredis-backed store, then
// reads each event back to assert it carries the correct Kind, the
// correct tenant/session/execution, Decision=Recorded, Layer=runtime,
// and that an AWS-key-shaped canary placed in a redactable field of
// each summary struct does NOT survive into the stored event JSON.
//
// Existing coverage only exercised process.exec + (via
// TestRuntimeIngestEnabledStoresRedactedRuntimeEvent) file.read; this
// test closes the gap so all 5 known kinds — including the network /
// DNS pair the K8s detector emits — round-trip end-to-end.
func TestRuntimeIngestAllEventKinds_EndToEnd(t *testing.T) {
	enableRuntimeIngest(t)
	const canary = "AKIAIOSFODNN7EXAMPLE"

	cases := []struct {
		// kind is the wire `kind` string + the persisted EventKind.
		kind edgecore.EventKind
		// summaryJSON is the per-kind summary-block fragment. The
		// canary is embedded in a redactable field so we can assert
		// it does not survive into the stored event JSON.
		summaryJSON string
		// labelKey/labelValue is appended to the envelope's labels so
		// each subtest carries a unique marker the store filter can
		// pin against (so a partial-rejection regression cannot silently
		// pass by re-persisting an earlier kind).
		labelKey   string
		labelValue string
	}{
		{
			kind: edgecore.EventKindRuntimeProcessExec,
			// Canary placed with explicit word boundaries on both sides
			// (hyphen + period) so awsKeyPattern \bAKIA[0-9A-Z]{16}\b
			// can match; lowercase trailers would suppress the trailing
			// \b boundary and mask the regex.
			summaryJSON: `"process":{"executable_basename":"curl-` + canary + `.bin","argument_count":2}`,
			labelKey:    "kind_marker",
			labelValue:  "process-exec",
		},
		{
			kind:        edgecore.EventKindRuntimeFileRead,
			summaryJSON: `"file":{"operation":"read","path_redacted":"/var/log/` + canary + `/audit.log"}`,
			labelKey:    "kind_marker",
			labelValue:  "file-read",
		},
		{
			kind:        edgecore.EventKindRuntimeFileWrite,
			summaryJSON: `"file":{"operation":"write","path_redacted":"/srv/data/` + canary + `/staging.bin"}`,
			labelKey:    "kind_marker",
			labelValue:  "file-write",
		},
		{
			kind:        edgecore.EventKindRuntimeNetworkConnect,
			summaryJSON: `"network":{"host_redacted":"` + canary + `.s3.amazonaws.com","port":443,"protocol":"tcp"}`,
			labelKey:    "kind_marker",
			labelValue:  "network-connect",
		},
		{
			kind:        edgecore.EventKindRuntimeDNSQuery,
			summaryJSON: `"dns":{"qname_redacted":"` + canary + `.example.internal","qtype":"A"}`,
			labelKey:    "kind_marker",
			labelValue:  "dns-query",
		},
	}

	// Share the server + session + execution across subtests so the
	// table emits 5 events into the same execution, then each
	// subtest filters by its own kind+marker. This catches partial-
	// rejection / wrong-kind-persistence regressions.
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "all-kinds", "tetragon-all-kinds")

	for i, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			sourceEventID := "rt-all-kinds-" + string(tc.kind)
			body := `{
				"source":{"source_id":"tetragon-all-kinds"},
				"nonce":"` + runtimeIngestNonce(sourceEventID) + `",
				"batch_id":"batch-all-kinds-` + tc.labelValue + `",
				"events":[{
					"tenant_id":"` + edgeRouteTenant + `",
					"session_id":"` + session.SessionID + `",
					"execution_id":"` + execution.ExecutionID + `",
					"source_event_id":"` + sourceEventID + `",
					"observed_at":"2026-05-17T12:00:0` + string(rune('0'+i)) + `Z",
					"kind":"` + string(tc.kind) + `",
					` + tc.summaryJSON + `,
					"labels":{"` + tc.labelKey + `":"` + tc.labelValue + `"}
				}]
			}`
			rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-all-kinds"), body)
			if rr.Code != http.StatusCreated {
				t.Fatalf("kind=%q POST status = %d, want 201 body=%s", tc.kind, rr.Code, rr.Body.String())
			}
			var resp runtimeIngestResponseShape
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("kind=%q decode response: %v body=%s", tc.kind, err, rr.Body.String())
			}
			if resp.AcceptedCount != 1 || resp.DroppedCount != 0 {
				t.Fatalf("kind=%q counts accepted=%d dropped=%d, want 1/0 body=%s",
					tc.kind, resp.AcceptedCount, resp.DroppedCount, rr.Body.String())
			}

			page := readEdgeEventsFromStore(t, s, execution.ExecutionID)
			var matched *edgecore.AgentActionEvent
			for j := range page.Items {
				ev := &page.Items[j]
				if ev.Layer == edgecore.LayerRuntime && ev.Kind == tc.kind && ev.Labels[tc.labelKey] == tc.labelValue {
					matched = ev
					break
				}
			}
			if matched == nil {
				t.Fatalf("kind=%q: no stored runtime event matched marker %q=%q; events=%#v",
					tc.kind, tc.labelKey, tc.labelValue, page.Items)
			}
			if matched.Decision != edgecore.DecisionRecorded {
				t.Fatalf("kind=%q Decision = %q, want %q", tc.kind, matched.Decision, edgecore.DecisionRecorded)
			}
			if matched.TenantID != edgeRouteTenant {
				t.Fatalf("kind=%q TenantID = %q, want %q", tc.kind, matched.TenantID, edgeRouteTenant)
			}
			if matched.SessionID != session.SessionID {
				t.Fatalf("kind=%q SessionID = %q, want %q", tc.kind, matched.SessionID, session.SessionID)
			}
			if matched.ExecutionID != execution.ExecutionID {
				t.Fatalf("kind=%q ExecutionID = %q, want %q", tc.kind, matched.ExecutionID, execution.ExecutionID)
			}
			if matched.Labels["runtime.source_id"] != "tetragon-all-kinds" {
				t.Fatalf("kind=%q Labels.runtime.source_id = %q, want %q",
					tc.kind, matched.Labels["runtime.source_id"], "tetragon-all-kinds")
			}
			storedJSON, err := json.Marshal(matched)
			if err != nil {
				t.Fatalf("kind=%q marshal: %v", tc.kind, err)
			}
			if strings.Contains(string(storedJSON), canary) {
				t.Fatalf("kind=%q stored event leaked AKIA canary %q: %s", tc.kind, canary, storedJSON)
			}
		})
	}
}

// TestRuntimeIngestEnabledAllOrNothingOnOneBadEnvelope locks in the
// all-or-nothing partial-rejection contract: a batch with one valid
// envelope and one invalid envelope must reject the whole batch and
// persist zero events. This prevents a future refactor from silently
// permitting partial persistence — partial appends would defeat the
// idempotency story and create gaps in the evidence stream.
func TestRuntimeIngestEnabledAllOrNothingOnOneBadEnvelope(t *testing.T) {
	enableRuntimeIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupRuntimeIngestRBAC(t, s)
	session, execution := createRuntimeIngestBoundParents(t, s, "partial-batch", "tetragon-test")
	body := `{
		"source":{"source_id":"tetragon-test"},
		"nonce":"nonce-partial-batch-1",
		"events":[
			{
				"tenant_id":"` + edgeRouteTenant + `",
				"session_id":"` + session.SessionID + `",
				"execution_id":"` + execution.ExecutionID + `",
				"source_event_id":"good-1","observed_at":"2026-05-17T12:00:00Z",
				"kind":"runtime.process.exec",
				"process":{"executable_basename":"curl","argument_count":1}
			},
			{
				"tenant_id":"` + edgeRouteTenant + `",
				"session_id":"` + session.SessionID + `",
				"execution_id":"` + execution.ExecutionID + `",
				"source_event_id":"","observed_at":"2026-05-17T12:00:01Z",
				"kind":"runtime.process.exec",
				"process":{"executable_basename":"curl","argument_count":1}
			}
		]
	}`
	rr := postRuntimeIngestWithAuth(t, s, runtimeIngestAuthContext(testRuntimeCollectorRole, "tetragon-test"), body)
	if rr.Code == http.StatusCreated {
		t.Fatalf("partial-batch must not return 201; got %d body=%s", rr.Code, rr.Body.String())
	}
	page := readEdgeEventsFromStore(t, s, execution.ExecutionID)
	for _, ev := range page.Items {
		if ev.Layer == edgecore.LayerRuntime {
			t.Fatalf("partial-batch leaked runtime event %q (Layer=%q) into store", ev.EventID, ev.Layer)
		}
	}
}
