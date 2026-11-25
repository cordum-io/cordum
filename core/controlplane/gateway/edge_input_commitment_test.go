package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestEdgeEvaluateCommitmentKeepsOriginalInputOffGateway(t *testing.T) {
	const secret = "synthetic-never-sent-4b1d"
	s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink
	gatewayURL, sent := edgeCommitmentRecordingServer(t, handler)
	evaluator := edgeCommitmentEvaluator(t, gatewayURL, session)
	command := "echo Bearer " + secret
	originalInputHook(t, evaluator, session, command)
	originalInputApprove(t, s, session)
	if _, err := os.Stat(originalInputRunTool(t, originalInputHook(t, evaluator, session, command))); err != nil {
		t.Fatalf("approved retry did not run: %v", err)
	}

	evaluates := 0
	for _, req := range sent() {
		if bytes.Contains(req.body, []byte(secret)) {
			t.Fatalf("%s request carried the original input: %s", req.path, req.body)
		}
		if req.path != "/api/v1/edge/evaluate" {
			continue
		}
		evaluates++
		var body struct {
			InputCommitment string `json:"input_commitment"`
		}
		if err := json.Unmarshal(req.body, &body); err != nil || !edgeCommitmentFormat.MatchString(body.InputCommitment) {
			t.Fatalf("evaluate input_commitment = %q (%v), want hmac-sha256:<64 hex>", body.InputCommitment, err)
		}
	}
	if evaluates != 2 {
		t.Fatalf("evaluate requests = %d, want one per hook call", evaluates)
	}
	approvals, err := s.edgeStore.ListApprovals(context.Background(), edgecore.ListApprovalsQuery{
		TenantID: edgeRouteTenant, Status: edgecore.ApprovalStatusApproved, SessionID: session.SessionID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := listEdgeEvaluateEvents(t, s, session.SessionID, session.ExecutionID)
	audits := make([]any, sink.Len())
	for i := range audits {
		audits[i] = sink.Get(i)
	}
	if len(approvals.Items) == 0 || len(events) == 0 || len(audits) == 0 {
		t.Fatalf("approvals=%d events=%d audit=%d, want each recorded", len(approvals.Items), len(events), len(audits))
	}
	for name, records := range map[string]any{"approval": approvals.Items, "event": events, "audit": audits} {
		data, err := json.Marshal(records)
		if err != nil || bytes.Contains(data, []byte(secret)) {
			t.Fatalf("stored %s records hold the original input (marshal err %v): %s", name, err, data)
		}
	}
}

var edgeCommitmentFormat = regexp.MustCompile(`^hmac-sha256:[0-9a-f]{64}$`)

type edgeCommitmentRequest struct {
	path string
	body []byte
}

// edgeCommitmentRecordingServer serves handler over HTTP and keeps every
// request body it receives.
func edgeCommitmentRecordingServer(t *testing.T, handler http.Handler) (string, func() []edgeCommitmentRequest) {
	t.Helper()
	var mu sync.Mutex
	var sent []edgeCommitmentRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusBadRequest)
			return
		}
		mu.Lock()
		sent = append(sent, edgeCommitmentRequest{path: r.URL.Path, body: body})
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []edgeCommitmentRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]edgeCommitmentRequest(nil), sent...)
	}
}

// edgeCommitmentEvaluator is an agentd evaluator that, like cordum-agentd,
// also writes its decision evidence to the Gateway.
func edgeCommitmentEvaluator(t *testing.T, gatewayURL string, session edgeSessionCreateResponseJSON) *agentd.Evaluator {
	t.Helper()
	client, err := agentd.NewGatewayClient(agentd.GatewayClientConfig{
		BaseURL: gatewayURL, APIKey: edgeRouteTestAPIKey, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agentd.NewEvaluator(agentd.EvaluatorConfig{Client: client, EventWriter: client, State: agentd.SessionState{
		SessionID: session.SessionID, ExecutionID: session.ExecutionID, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", PolicyMode: edgecore.PolicyModeEnforce, PolicySnapshot: session.PolicySnapshot,
	}})
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
