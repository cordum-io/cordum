package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
)

// Two original inputs that redact to the same text, as the Edge client commits to them.
const (
	edgeCommitmentGranted = "hmac-sha256:1111111111111111111111111111111111111111111111111111111111111111"
	edgeCommitmentOther   = "hmac-sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func TestEdgeEvaluateCommitmentBindsApprovalClaim(t *testing.T) {
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)
	initial, ran := edgeCommitmentEvaluate(t, handler, edgeCommitmentBody(session, edgeCommitmentGranted, ""))
	if initial.Decision != string(edgecore.DecisionRequireApproval) || initial.ApprovalRef == "" || ran {
		t.Fatalf("initial evaluate = %q ref %q ran=%v, want a pending approval and no run", initial.Decision, initial.ApprovalRef, ran)
	}
	originalInputApprove(t, s, session)

	other, ran := edgeCommitmentEvaluate(t, handler, edgeCommitmentBody(session, edgeCommitmentOther, initial.ApprovalRef))
	if other.Decision != string(edgecore.DecisionDeny) || ran {
		t.Fatalf("claim for another original input = %q ran=%v, want DENY and no run", other.Decision, ran)
	}
	stored, ok, err := s.edgeStore.GetApproval(context.Background(), edgeRouteTenant, initial.ApprovalRef)
	if err != nil || !ok || stored.Status != edgecore.ApprovalStatusApproved || stored.ConsumedAt != nil {
		t.Fatalf("approval after the refused claim = %#v ok=%v err=%v, want approved and unconsumed", stored, ok, err)
	}

	granted, ran := edgeCommitmentEvaluate(t, handler, edgeCommitmentBody(session, edgeCommitmentGranted, initial.ApprovalRef))
	if granted.Decision != string(edgecore.DecisionAllow) || !ran {
		t.Fatalf("claim for the granted input = %q ran=%v, want ALLOW and a run", granted.Decision, ran)
	}
}

func TestEdgeEvaluateCommitmentRequiredInEnforcingModes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       edgecore.PolicyMode
		commitment string
		wantRun    bool
	}{
		{name: "enforce without commitment", mode: edgecore.PolicyModeEnforce, wantRun: false},
		{name: "enterprise-strict without commitment", mode: edgecore.PolicyModeEnterpriseStrict, wantRun: false},
		{name: "observe without commitment", mode: edgecore.PolicyModeObserve, wantRun: true},
		{name: "enforce with commitment", mode: edgecore.PolicyModeEnforce, commitment: edgeCommitmentGranted, wantRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
			session := createEdgeEvaluateSessionWithPolicyMode(t, handler, tc.mode)
			resp, ran := edgeCommitmentEvaluate(t, handler, edgeCommitmentBody(session, tc.commitment, ""))
			if ran != tc.wantRun {
				t.Fatalf("tool ran = %v, want %v; decision %q reason %q", ran, tc.wantRun, resp.Decision, resp.Reason)
			}
			if !tc.wantRun && !strings.Contains(resp.Reason, "input commitment") {
				t.Fatalf("deny reason = %q, want it to name the missing input commitment", resp.Reason)
			}
		})
	}
}

// edgeCommitmentBody is a hook evaluate for a Bash call whose secret was
// redacted, carrying the Edge client's commitment and an optional approval_ref.
func edgeCommitmentBody(session edgeSessionCreateResponseJSON, commitment, approvalRef string) string {
	body := map[string]any{
		"tenant_id": edgeRouteTenant, "principal_id": "principal-edge-a",
		"session_id": session.SessionID, "execution_id": session.ExecutionID,
		"agent_product": "claude-code", "layer": "hook", "kind": "hook.pre_tool_use",
		"tool_name": "Bash", "input_redacted": map[string]any{"command": "echo Bearer <redacted>"},
	}
	if commitment != "" {
		body["input_commitment"] = commitment
	}
	if approvalRef != "" {
		body["approval_ref"] = approvalRef
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// edgeCommitmentEvaluate posts the evaluate, turns the answer into the hook's
// output the way agentd and the hook do, and reports whether the tool ran.
func edgeCommitmentEvaluate(t *testing.T, handler http.Handler, body string) (agentd.EvaluateResponse, bool) {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/evaluate", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("evaluate status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var resp agentd.EvaluateResponse
	decodeEdgeRouteJSON(t, rr, &resp)
	decision := agentd.AgentdDecisionFromEvaluateResponse(context.Background(), resp, agentd.ApprovalDecisionConfig{}, nil, nil)
	_, err := os.Stat(originalInputRunTool(t, claude.ClaudeHookOutputForDecision("PreToolUse", decision)))
	return resp, err == nil
}
