package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests assert that representative /api/v1/edge/* error paths emit the
// standard envelope `{ code, message, request_id, details? }` per PRD_ROADMAP
// §7.10. They use the shared assertEdgeErrorShape helper. Coverage spans
// sessions, events, batch events, evaluate, approvals, /wait, and export so a
// future regression that re-introduces the legacy `{error,status}` shape on
// any of these surfaces fails immediately.

func TestEdgeErrorShapeSessionsMissingTenantHeader(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", strings.NewReader(`{"agent_product":"x"}`))
	addEdgeRouteAuth(req)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeTenantRequired)
}

func TestEdgeErrorShapeSessionsBadJSON(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
}

func TestEdgeErrorShapeSessionsNotFound(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRouteGET(t, handler, "/api/v1/edge/sessions/sess-does-not-exist")
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

func TestEdgeErrorShapeEventsBadJSON(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
}

func TestEdgeErrorShapeEventsBatchEmpty(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/events/batch", `{"events":[]}`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

func TestEdgeErrorShapeEvaluateBadJSON(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", `{`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
}

func TestEdgeErrorShapeEvaluateMissingSession(t *testing.T) {
	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "ok"}}
	_, handler := newEdgeEvaluateTestServer(t, stub)
	// Empty session_id triggers the missing-required-field branch before the
	// session/execution lookup; principal is resolved from auth context.
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", `{"tenant_id":"`+edgeRouteTenant+`","session_id":"","execution_id":""}`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeMissingField)
}

func TestEdgeErrorShapeEvaluateTerminalSession(t *testing.T) {
	stub := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "ok"}}
	s, handler := newEdgeEvaluateTestServer(t, stub)
	session := createEdgeRouteSession(t, handler)
	endedAt := session.Session.StartedAt.Add(1)
	if _, err := s.edgeStore.EndSession(context.Background(), edgeRouteTenant, session.SessionID, endedAt, edgecore.SessionStatusEnded); err != nil {
		t.Fatalf("end session fixture: %v", err)
	}
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"})
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	assertEdgeErrorShape(t, rr, http.StatusConflict, edgeErrCodeSessionTerminal)
}

func TestEdgeErrorShapeApprovalsNotFound(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRouteGET(t, handler, "/api/v1/edge/approvals/edge_appr_does-not-exist")
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

func TestEdgeErrorShapeApprovalsBadJSONOnApprove(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "shape-bad-json")
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{`)
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
}

func TestEdgeErrorShapeApprovalsSelfApprovalDenied(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "shape-self-approve")
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteTestAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"self"}`)
	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeSelfApprovalDenied)
}

func TestEdgeErrorShapeApprovalsWaitNotFound(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/edge_appr_missing/wait", `{"timeout_ms":50}`)
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

func TestEdgeErrorShapeExportSessionMissing(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/sess-missing/export", `{}`)
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

// TestGatewayEdgeExportEmitsAuditEventForSuccessAndMissing pins
// EDGE-014 step-10 audit instrumentation for the export handler.
// Successful export emits result=ok with severity info; missing-session
// 404 emits result=missing with severity medium.
func TestGatewayEdgeExportEmitsAuditEventForSuccessAndMissing(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink
	session := createEdgeRouteSession(t, handler)
	before := sink.Len()

	// Successful export.
	ok := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", ok.Code, ok.Body.String())
	}
	if sink.Len()-before != 1 {
		t.Fatalf("after export ok: emitted = %d, want 1", sink.Len()-before)
	}
	ev := sink.Get(sink.Len() - 1)
	if ev.EventType != audit.EventEdgeArtifactExported {
		t.Errorf("EventType = %q, want %q", ev.EventType, audit.EventEdgeArtifactExported)
	}
	if ev.Severity != audit.SeverityInfo {
		t.Errorf("Severity = %q, want info", ev.Severity)
	}
	if ev.Extra["result"] != "ok" {
		t.Errorf("Extra[result] = %q, want ok", ev.Extra["result"])
	}
	if ev.Extra["artifact_type"] != "edge.session_export" {
		t.Errorf("Extra[artifact_type] = %q, want edge.session_export", ev.Extra["artifact_type"])
	}

	// Missing session -> 404 + audit result=missing.
	beforeMissing := sink.Len()
	miss := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/sess-missing-audit/export", `{}`)
	if miss.Code != http.StatusNotFound {
		t.Fatalf("missing export status = %d body=%s", miss.Code, miss.Body.String())
	}
	if sink.Len()-beforeMissing != 1 {
		t.Fatalf("after missing export: emitted = %d, want 1", sink.Len()-beforeMissing)
	}
	ev = sink.Get(sink.Len() - 1)
	if ev.Extra["result"] != "missing" {
		t.Errorf("missing Extra[result] = %q, want missing", ev.Extra["result"])
	}
	if ev.Severity != audit.SeverityMedium {
		t.Errorf("missing Severity = %q, want medium", ev.Severity)
	}
}

func TestEdgeErrorShapeRequestIdFieldAlwaysPresent(t *testing.T) {
	// The standard envelope must always include the request_id field, even when
	// the test handler chain doesn't wrap the request-id middleware (the field
	// should still appear, possibly as empty string, so callers can rely on its
	// presence). Production routing wraps the middleware so the field carries a
	// real id; we don't depend on that here.
	_, handler := newEdgeRouteTestServer(t)
	rr := edgeRouteGET(t, handler, "/api/v1/edge/approvals/edge_appr_missing")
	assertEdgeErrorShape(t, rr, http.StatusNotFound, edgeErrCodeNotFound)
}

func TestEdgeErrorShapeApprovalConflictHasCode(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	approval := seedGatewayEdgeApproval(t, s, edgeRouteTenant, "principal-edge-a", "shape-conflict")
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID:    edgeRouteTenant,
		ApprovalRef: approval.ApprovalRef,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "principal:principal-reviewer",
		Reason:      "first approval",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	rr := edgeApprovalRoutePOSTAs(t, handler, edgeRouteReviewerAPIKey, "/api/v1/edge/approvals/"+approval.ApprovalRef+"/approve", `{"reason":"again"}`)
	assertEdgeErrorShape(t, rr, http.StatusConflict, edgeErrCodeApprovalConflict)
}

func TestWriteEdgeErrorRedactsSecretDetails(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/events", nil)
	req.Header.Set("X-Request-Id", "req-edge014-secret-details")
	rr := httptest.NewRecorder()
	writeEdgeError(rr, req, http.StatusConflict, edgeErrCodeIdempotencyConflict, "idempotency key already used with a different request", map[string]any{
		"idempotency_key": "sk-edge014-error-detail-secret-000000",
		"authorization":   "Authorization: Bearer edge014-error-detail-token",
		"nested": map[string]any{
			"signed_url": "https://blob.example/evidence?token=ghp_edge014errordetailtoken0000",
			"aws_key":    "AKIAIOSFODNN7EXAMPLE",
		},
		"safe_code": "idempotency_conflict",
	})

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{
		"sk-edge014-error-detail-secret",
		"Authorization: Bearer",
		"edge014-error-detail-token",
		"ghp_edge014errordetailtoken",
		"AKIAIOSFODNN7EXAMPLE",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("edge error details leaked %q in %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"safe_code":"idempotency_conflict"`) {
		t.Fatalf("sanitized details dropped safe_code: %s", body)
	}
	assertEdgeErrorShape(t, rr, http.StatusConflict, edgeErrCodeIdempotencyConflict)
}

// EDGE-038: typed-error sentinels at the gateway/store boundary should drive
// `isEdgeValidationError` via errors.Is instead of substring matching. A
// validation error wrapped with `edgecore.ErrValidation` MUST be detected
// regardless of its message text — including a message that the legacy
// substring fallback would not match.
func TestIsEdgeValidationErrorRecognizesErrValidationSentinel(t *testing.T) {
	wrapped := fmt.Errorf("%w: tenant_id is required", edgecore.ErrValidation)
	if !isEdgeValidationError(wrapped) {
		t.Fatalf("isEdgeValidationError did not recognize ErrValidation-wrapped error: %v", wrapped)
	}
	// A message that the legacy substring fallback would NOT catch must still
	// be detected when the sentinel is present. This proves the typed path
	// runs first, not the substring fallback.
	wrappedExoticMsg := fmt.Errorf("%w: arbitrary downstream copy", edgecore.ErrValidation)
	if !isEdgeValidationError(wrappedExoticMsg) {
		t.Fatalf("isEdgeValidationError must detect ErrValidation regardless of message text: %v", wrappedExoticMsg)
	}
	// nil and unrelated errors must remain unmatched.
	if isEdgeValidationError(nil) {
		t.Fatal("isEdgeValidationError(nil) = true, want false")
	}
	if isEdgeValidationError(errors.New("redis unavailable")) {
		t.Fatal("isEdgeValidationError matched an unrelated infra error")
	}
}

// EDGE-038: ErrInvalidCursor short-circuits writeEdgeEventStoreError before
// the substring fallback. An error wrapped with the sentinel that does NOT
// contain the literal "invalid cursor" string must still produce 400
// edge_invalid_request via the typed path.
func TestWriteEdgeEventStoreErrorRoutesErrInvalidCursorSentinel(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions/x/events?cursor=garbage", nil)
	wrapped := fmt.Errorf("%w: opaque downstream message", edgecore.ErrInvalidCursor)
	writeEdgeEventStoreError(rr, req, wrapped, "list events")
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidRequest)
}

// EDGE-038: ErrRequestTooLarge short-circuits writeEdgeEventStoreError to
// 413 edge_request_too_large via the typed path, irrespective of message.
func TestWriteEdgeEventStoreErrorRoutesErrRequestTooLargeSentinel(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/events", nil)
	wrapped := fmt.Errorf("%w: payload bound", edgecore.ErrRequestTooLarge)
	writeEdgeEventStoreError(rr, req, wrapped, "append event")
	assertEdgeErrorShape(t, rr, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge)
}
