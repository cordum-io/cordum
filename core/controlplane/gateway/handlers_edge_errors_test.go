package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeTenantRequired)
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
