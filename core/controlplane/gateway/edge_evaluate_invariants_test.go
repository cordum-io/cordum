package gateway

import (
	"net/http"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestInvariantDenyBlocksEdgeEvaluate is the gateway-side confirmation
// for EDGE-052 step 5 — when the Safety Kernel returns DENY because an
// invariant fired (proven kernel-side by
// TestKernelInvariants_DenyBlocksOtherwiseAllowedInput), the Edge
// evaluate handler forwards the deny to the Claude Code hook with
// permission_decision=deny + exit_code=2 + the invariant rule_id intact.
//
// The test does NOT need to load a real invariants bundle because the
// gateway's evaluateEdgeSafety just forwards safetyClient.Evaluate. It
// stubs the kernel response to simulate the result of an invariant fire
// — the architect's "Confirm with focused gateway-side test rather than
// re-routing" note (step 5).
func TestInvariantDenyBlocksEdgeEvaluate(t *testing.T) {
	safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_DENY,
		Reason:   "SecOps invariant — secret-path access forbidden",
		RuleId:   "inv-deny-secret-paths",
	}}
	_, handler := newEdgeEvaluateTestServer(t, safety)
	session := createEdgeRouteSession(t, handler)
	safety.response.PolicySnapshot = session.PolicySnapshot

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", edgeEvaluateBody(
		session.SessionID, session.ExecutionID, edgeRouteTenant, "Read",
		map[string]any{"path": ".env"},
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}

	var resp edgeEvaluateResponseJSON
	decodeEdgeRouteJSON(t, rr, &resp)
	if resp.Decision != string(edgecore.DecisionDeny) {
		t.Fatalf("decision = %q, want DENY (forwarded from kernel invariant) body=%s", resp.Decision, rr.Body.String())
	}
	if resp.RuleID != "inv-deny-secret-paths" {
		t.Fatalf("rule_id = %q, want invariant rule id forwarded; body=%s", resp.RuleID, rr.Body.String())
	}
	if !strings.Contains(strings.ToLower(resp.Reason), "invariant") {
		t.Fatalf("reason = %q, want substring 'invariant' (kernel reason forwarded); body=%s", resp.Reason, rr.Body.String())
	}
	if resp.PermissionDecision != "deny" || resp.ExitCode != 2 {
		t.Fatalf("hook permission/exit = %q/%d, want deny/2 body=%s",
			resp.PermissionDecision, resp.ExitCode, rr.Body.String())
	}
	if resp.PolicySnapshot != session.PolicySnapshot {
		t.Fatalf("policy_snapshot = %q, want forwarded session snapshot %q",
			resp.PolicySnapshot, session.PolicySnapshot)
	}
}
