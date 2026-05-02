package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestAgentdDecisionFromEvaluateRequiresApprovalDefaultsToImmediateRetryDeny(t *testing.T) {
	t.Parallel()

	waiter := &fakeApprovalWaiter{}
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{}, waiter)

	if decision.Decision != claude.DecisionRequireApproval {
		t.Fatalf("decision = %q, want require_approval (hook maps to immediate deny)", decision.Decision)
	}
	if decision.ApprovalRef != "edge_appr_123" {
		t.Fatalf("approval_ref = %q, want edge_appr_123", decision.ApprovalRef)
	}
	if !strings.Contains(decision.Reason, "approval") || !strings.Contains(decision.Reason, "retry") {
		t.Fatalf("reason = %q, want approval retry guidance", decision.Reason)
	}
	if !strings.Contains(decision.AdditionalContext, "/edge/approvals/edge_appr_123") {
		t.Fatalf("additional_context = %q, want approval URL", decision.AdditionalContext)
	}
	if waiter.calls != 0 {
		t.Fatalf("waiter calls = %d, want default immediate path with no wait", waiter.calls)
	}
}

func TestAgentdDecisionFromEvaluateInlineWaitApprovedAllowsWithUpdatedInput(t *testing.T) {
	t.Parallel()

	waiter := &fakeApprovalWaiter{result: ApprovalWaitResult{
		Status:       ApprovalWaitApproved,
		Reason:       "approved by reviewer",
		UpdatedInput: map[string]any{"command": "npm test -- --runInBand"},
	}}
	cfg := ApprovalDecisionConfig{InlineWaitEnabled: true, InlineWaitTimeout: 2 * time.Second, PolicyMode: edgecore.PolicyModeEnforce}
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), cfg, waiter)

	if waiter.calls != 1 {
		t.Fatalf("waiter calls = %d, want 1", waiter.calls)
	}
	if waiter.lastReq.ApprovalRef != "edge_appr_123" {
		t.Fatalf("wait approval_ref = %q, want edge_appr_123", waiter.lastReq.ApprovalRef)
	}
	if waiter.lastReq.Timeout != 2*time.Second {
		t.Fatalf("wait timeout = %s, want 2s", waiter.lastReq.Timeout)
	}
	if _, ok := waiter.lastDeadline(); !ok {
		t.Fatal("wait context had no deadline; inline wait must be bounded")
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("approved decision = %q, want allow", decision.Decision)
	}
	if got := decision.UpdatedInput["command"]; got != "npm test -- --runInBand" {
		t.Fatalf("updated input command = %#v, want reviewer update", got)
	}
	if decision.ApprovalRef != "" {
		t.Fatalf("approved decision must not return approval_ref for caching/retry, got %q", decision.ApprovalRef)
	}
}

func TestAgentdDecisionFromEvaluateInlineWaitRejectedTimeoutAndErrorDeny(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		waiter     *fakeApprovalWaiter
		wantReason string
	}{
		{name: "rejected", waiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitRejected, Reason: "too risky"}}, wantReason: "rejected"},
		{name: "timeout status", waiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitTimeout}}, wantReason: "timed out"},
		{name: "pending status", waiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitPending}}, wantReason: "timed out"},
		{name: "gateway error", waiter: &fakeApprovalWaiter{err: errors.New("gateway unavailable: Bearer should-not-leak")}, wantReason: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{
				InlineWaitEnabled: true,
				InlineWaitTimeout: 500 * time.Millisecond,
				PolicyMode:        edgecore.PolicyModeEnterpriseStrict,
			}, tc.waiter)
			if decision.Decision != claude.DecisionDeny {
				t.Fatalf("decision = %q, want deny", decision.Decision)
			}
			if !strings.Contains(strings.ToLower(decision.Reason), tc.wantReason) {
				t.Fatalf("reason = %q, want to mention %q", decision.Reason, tc.wantReason)
			}
			if !strings.Contains(decision.Reason, "edge_appr_123") {
				t.Fatalf("reason = %q, want approval ref for retry guidance", decision.Reason)
			}
			if strings.Contains(decision.Reason, "should-not-leak") || strings.Contains(decision.AdditionalContext, "should-not-leak") {
				t.Fatalf("decision leaked waiter error secret: %#v", decision)
			}
		})
	}
}

func TestAgentdDecisionFromEvaluateMapsAllowQuietlyAndRiskyDenyWithConciseCopy(t *testing.T) {
	t.Parallel()

	allow := AgentdDecisionFromEvaluateResponse(context.Background(), EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "safe command allowed by policy",
		PermissionDecision: "allow",
		CacheEligible:      true,
	}, ApprovalDecisionConfig{}, nil)
	if allow.Decision != claude.DecisionAllow {
		t.Fatalf("allow decision = %q, want allow", allow.Decision)
	}
	if allow.Reason != "" || allow.AdditionalContext != "" || len(allow.UpdatedInput) != 0 {
		t.Fatalf("safe allow should be quiet, got %#v", allow)
	}

	deny := AgentdDecisionFromEvaluateResponse(context.Background(), EvaluateResponse{
		Decision:        string(edgecore.DecisionDeny),
		Reason:          strings.Repeat("blocked risky action ", 40),
		RuleID:          "edge.deny.risky",
		TerminalMessage: "Cordum Edge blocked this risky action. It was not run.",
	}, ApprovalDecisionConfig{}, nil)
	if deny.Decision != claude.DecisionDeny {
		t.Fatalf("deny decision = %q, want deny", deny.Decision)
	}
	if !strings.Contains(deny.Reason, "blocked") {
		t.Fatalf("deny reason = %q, want concise terminal copy", deny.Reason)
	}
	if len(deny.Reason) > MaxGatewayMetadataValueBytes+8 {
		t.Fatalf("deny reason length = %d, want bounded", len(deny.Reason))
	}
}

func TestGatewayClientWaitForApprovalPostsTimeoutAndMapsStatuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     edgecore.ApprovalStatus
		reason     string
		resolution string
		want       ApprovalWaitStatus
	}{
		{name: "approved", status: edgecore.ApprovalStatusApproved, resolution: "approved by reviewer", want: ApprovalWaitApproved},
		{name: "rejected", status: edgecore.ApprovalStatusRejected, resolution: "too risky", want: ApprovalWaitRejected},
		{name: "pending", status: edgecore.ApprovalStatusPending, reason: "still waiting", want: ApprovalWaitPending},
		{name: "expired", status: edgecore.ApprovalStatusExpired, resolution: "expired", want: ApprovalWaitRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got struct {
				method    string
				path      string
				apiKey    string
				tenant    string
				timeoutMS float64
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got.method = r.Method
				got.path = r.URL.Path
				got.apiKey = r.Header.Get("X-API-Key")
				got.tenant = r.Header.Get("X-Tenant-ID")
				body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				var req map[string]any
				_ = json.Unmarshal(body, &req)
				got.timeoutMS, _ = req["timeout_ms"].(float64)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(edgecore.EdgeApproval{
					ApprovalRef:      "edge_appr_http",
					Status:           tc.status,
					Reason:           tc.reason,
					ResolutionReason: tc.resolution,
				})
			}))
			defer server.Close()
			client, err := NewGatewayClient(GatewayClientConfig{BaseURL: server.URL, APIKey: "approval-api-key", TenantID: "tenant-approval"})
			if err != nil {
				t.Fatalf("NewGatewayClient: %v", err)
			}
			result, err := client.WaitForApproval(context.Background(), ApprovalWaitRequest{ApprovalRef: "edge_appr_http", Timeout: 1500 * time.Millisecond})
			if err != nil {
				t.Fatalf("WaitForApproval: %v", err)
			}
			if got.method != http.MethodPost || got.path != "/api/v1/edge/approvals/edge_appr_http/wait" {
				t.Fatalf("request = %s %s, want POST wait path", got.method, got.path)
			}
			if got.apiKey != "approval-api-key" || got.tenant != "tenant-approval" {
				t.Fatalf("auth headers = api:%q tenant:%q", got.apiKey, got.tenant)
			}
			if got.timeoutMS != 1500 {
				t.Fatalf("timeout_ms = %v, want 1500", got.timeoutMS)
			}
			if result.Status != tc.want {
				t.Fatalf("status = %q, want %q", result.Status, tc.want)
			}
			if tc.resolution != "" && !strings.Contains(result.Reason, tc.resolution) {
				t.Fatalf("reason = %q, want resolution %q", result.Reason, tc.resolution)
			}
		})
	}
}

func approvalRequiredResponse() EvaluateResponse {
	return EvaluateResponse{
		Decision:           string(edgecore.DecisionRequireApproval),
		Reason:             "destructive command requires approval",
		RuleID:             "edge.approval.destructive",
		ApprovalRef:        "edge_appr_123",
		ApprovalURL:        "/edge/approvals/edge_appr_123",
		ActionHash:         "sha256:action",
		InputHash:          "sha256:input",
		PermissionDecision: "deny",
		TerminalMessage:    "Cordum approval required. Action was not run.",
		WaitStrategy:       "manual_approval",
		WaitAfter:          "approve_then_retry",
	}
}

type fakeApprovalWaiter struct {
	calls   int
	lastReq ApprovalWaitRequest
	ctx     context.Context
	result  ApprovalWaitResult
	err     error
}

func (w *fakeApprovalWaiter) WaitForApproval(ctx context.Context, req ApprovalWaitRequest) (ApprovalWaitResult, error) {
	w.calls++
	w.ctx = ctx
	w.lastReq = req
	return w.result, w.err
}

func (w *fakeApprovalWaiter) lastDeadline() (time.Time, bool) {
	if w.ctx == nil {
		return time.Time{}, false
	}
	return w.ctx.Deadline()
}
