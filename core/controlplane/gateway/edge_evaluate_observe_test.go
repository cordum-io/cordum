package gateway

import (
	"errors"
	"net/http"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestGatewayEdgeEvaluateObserveModeAppliesKernelDenyAndDegradesOnOutage(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kernelErr      error
		wantDecision   string
		wantPermission string
		wantDegraded   bool
		wantErrorCode  string
	}{
		{name: "reachable deny", wantDecision: "DENY", wantPermission: "deny"},
		{name: "kernel unavailable", kernelErr: errors.New("kernel unavailable"),
			wantDecision: "ALLOW", wantPermission: "allow", wantDegraded: true, wantErrorCode: "safety_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			safety := &edgeEvaluateStubSafetyClient{
				response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "policy denies action"},
				err:      tc.kernelErr,
			}
			s, handler := newEdgeEvaluateTestServer(t, safety)
			session := createEdgeEvaluateSessionWithPolicyMode(t, handler, edgecore.PolicyModeObserve)
			rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
				session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"}))
			if rr.Code != http.StatusOK {
				t.Fatalf("evaluate status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			var resp edgeEvaluateResponseJSON
			decodeEdgeRouteJSON(t, rr, &resp)
			if resp.Decision != tc.wantDecision || resp.PermissionDecision != tc.wantPermission ||
				resp.Degraded != tc.wantDegraded || resp.ErrorCode != tc.wantErrorCode {
				t.Fatalf("response = %s, want %s/%s degraded=%v error=%q",
					rr.Body.String(), tc.wantDecision, tc.wantPermission, tc.wantDegraded, tc.wantErrorCode)
			}
			if got := len(safety.capturedRequests()); got != 1 {
				t.Fatalf("kernel requests = %d, want 1", got)
			}
			assertObserveDecisionEvent(t, listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID), resp)
		})
	}
}

func assertObserveDecisionEvent(t *testing.T, events []edgecore.AgentActionEvent, resp edgeEvaluateResponseJSON) {
	t.Helper()
	if len(events) != 1 || resp.EventID == "" {
		t.Fatalf("events = %#v, response event_id = %q; want exactly one persisted decision", events, resp.EventID)
	}
	wantDecision, wantKind, wantStatus := edgecore.DecisionDeny, edgecore.EventKindHookPolicyDecision, edgecore.ActionStatusBlocked
	if resp.Degraded {
		wantDecision, wantKind, wantStatus = edgecore.DecisionRecorded, edgecore.EventKindPolicyDegraded, edgecore.ActionStatusDegraded
	}
	event := events[0]
	if event.EventID != resp.EventID || event.Decision != wantDecision || event.Decision == edgecore.DecisionAllow ||
		event.Kind != wantKind || event.Status != wantStatus || event.ErrorCode != resp.ErrorCode {
		t.Fatalf("event = %#v, want response id %q, %s/%s/%s, error %q and never ALLOW",
			event, resp.EventID, wantDecision, wantKind, wantStatus, resp.ErrorCode)
	}
}
