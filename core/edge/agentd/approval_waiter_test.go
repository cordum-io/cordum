package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestAgentdDecisionFromEvaluateRequiresApprovalDefaultsToImmediateRetryDeny(t *testing.T) {
	t.Parallel()

	waiter := &fakeApprovalWaiter{}
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{}, waiter, nil)

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

func TestAgentdDecisionFromEvaluateInlineWaitApprovedConsumesThenAllows(t *testing.T) {
	t.Parallel()

	waiter := &fakeApprovalWaiter{result: ApprovalWaitResult{
		Status:       ApprovalWaitApproved,
		Reason:       "approved by reviewer",
		UpdatedInput: map[string]any{"command": "npm test -- --runInBand"},
	}}
	cfg := ApprovalDecisionConfig{InlineWaitEnabled: true, InlineWaitTimeout: 2 * time.Second, PolicyMode: edgecore.PolicyModeEnforce}

	// The approved inline wait must consume the approval (single-use CAS) before
	// ALLOW — a passive "approved" poll is not authorization.
	var consumedRef string
	consumeCalls := 0
	consume := func(_ context.Context, ref string) (*EvaluateResponse, error) {
		consumeCalls++
		consumedRef = ref
		return &EvaluateResponse{Decision: string(edgecore.DecisionAllow), Reason: "consumed"}, nil
	}
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), cfg, waiter, consume)

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
	if consumeCalls != 1 || consumedRef != "edge_appr_123" {
		t.Fatalf("consume calls = %d ref = %q, want 1 consume of edge_appr_123 (ALLOW must run the single-use CAS)", consumeCalls, consumedRef)
	}
	if decision.Decision != claude.DecisionAllow {
		t.Fatalf("approved+consumed decision = %q, want allow", decision.Decision)
	}
	if got := decision.UpdatedInput["command"]; got != "npm test -- --runInBand" {
		t.Fatalf("updated input command = %#v, want reviewer update", got)
	}
	if decision.ApprovalRef != "" {
		t.Fatalf("approved decision must not return approval_ref for caching/retry, got %q", decision.ApprovalRef)
	}
}

func TestAgentdDecisionFromEvaluateInlineWaitRejectedDenies(t *testing.T) {
	t.Parallel()

	waiter := &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitRejected, Reason: "too risky"}}
	decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{
		InlineWaitEnabled: true,
		InlineWaitTimeout: 500 * time.Millisecond,
		PolicyMode:        edgecore.PolicyModeEnterpriseStrict,
	}, waiter, nil)
	if decision.Decision != claude.DecisionDeny {
		t.Fatalf("decision = %q, want deny", decision.Decision)
	}
	if !strings.Contains(decision.Reason, "rejected") || !strings.Contains(decision.Reason, "edge_appr_123") {
		t.Fatalf("reason = %q, want rejection naming the approval ref", decision.Reason)
	}
}

// An inline wait that ends unresolved (Gateway timeout/pending status, the
// wait budget running out, or a wait error) must answer exactly like the
// non-inline REQUIRE_APPROVAL path: the hook maps that to a deny carrying
// approval_ref and retry guidance.
func TestAgentdDecisionFromEvaluateInlineWaitUnresolvedReturnsPendingApproval(t *testing.T) {
	t.Parallel()

	want := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{}, nil, nil)
	cases := []struct {
		name   string
		waiter ApprovalWaiter
	}{
		{name: "timeout status", waiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitTimeout}}},
		{name: "pending status", waiter: &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitPending}}},
		{name: "gateway error", waiter: &fakeApprovalWaiter{err: errors.New("gateway unavailable: Bearer should-not-leak")}},
		{name: "budget runs out", waiter: blockingApprovalWaiter{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := AgentdDecisionFromEvaluateResponse(context.Background(), approvalRequiredResponse(), ApprovalDecisionConfig{
				InlineWaitEnabled: true,
				InlineWaitTimeout: 50 * time.Millisecond,
				PolicyMode:        edgecore.PolicyModeEnterpriseStrict,
			}, tc.waiter, nil)
			if !reflect.DeepEqual(decision, want) {
				t.Fatalf("decision = %#v, want the non-inline approval decision %#v", decision, want)
			}
			if strings.Contains(decision.Reason, "should-not-leak") || strings.Contains(decision.AdditionalContext, "should-not-leak") {
				t.Fatalf("decision leaked waiter error secret: %#v", decision)
			}
		})
	}
}

func TestInlineApprovalWaitBudgetLeavesAnswerReserve(t *testing.T) {
	t.Parallel()

	answerBy := func(d time.Duration) context.Context {
		return withHookAnswerBy(context.Background(), time.Now().Add(d))
	}
	if got := inlineApprovalWaitBudget(context.Background(), 30*time.Second); got != 30*time.Second {
		t.Fatalf("no answer-by: budget = %s, want the configured 30s", got)
	}
	got := inlineApprovalWaitBudget(answerBy(4*time.Second), 30*time.Second)
	if got > 4*time.Second-inlineApprovalAnswerReserve || got < 4*time.Second-inlineApprovalAnswerReserve-100*time.Millisecond {
		t.Fatalf("4s hook budget: wait budget = %s, want about %s", got, 4*time.Second-inlineApprovalAnswerReserve)
	}
	if got := inlineApprovalWaitBudget(answerBy(4*time.Second), time.Second); got != time.Second {
		t.Fatalf("short configured wait: budget = %s, want 1s", got)
	}
	if got := inlineApprovalWaitBudget(answerBy(inlineApprovalAnswerReserve/2), 30*time.Second); got > 0 {
		t.Fatalf("hook budget inside the reserve: wait budget = %s, want none", got)
	}
	parent, cancel := context.WithTimeout(answerBy(4*time.Second), 200*time.Millisecond)
	defer cancel()
	if got := inlineApprovalWaitBudget(parent, 30*time.Second); got > 200*time.Millisecond {
		t.Fatalf("earlier parent deadline: budget = %s, want <= 200ms", got)
	}
}

func TestAgentdDecisionFromEvaluateMapsAllowQuietlyAndRiskyDenyWithConciseCopy(t *testing.T) {
	t.Parallel()

	allow := AgentdDecisionFromEvaluateResponse(context.Background(), EvaluateResponse{
		Decision:           string(edgecore.DecisionAllow),
		Reason:             "safe command allowed by policy",
		PermissionDecision: "allow",
		CacheEligible:      true,
	}, ApprovalDecisionConfig{}, nil, nil)
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
	}, ApprovalDecisionConfig{}, nil, nil)
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

// blockingApprovalWaiter never resolves on its own — it blocks until the
// context it is given is done, then reports that as an error. This models a
// human reviewer who has not responded yet.
type blockingApprovalWaiter struct{}

func (blockingApprovalWaiter) WaitForApproval(ctx context.Context, _ ApprovalWaitRequest) (ApprovalWaitResult, error) {
	<-ctx.Done()
	return ApprovalWaitResult{}, ctx.Err()
}

// TestApprovalRequiredDecisionClampedByParentContextNotNominalTimeout is the
// regression test for the EDGE-4 doctor finding that
// CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT defaults to 30s, which exceeds
// the `"timeout": 5` Cordum generates on each Claude Code command hook
// (Claude Code's own default is 600s): does an unresolved inline wait fail
// OPEN when Claude Code kills the hook first?
//
// It doesn't. approvalRequiredDecision derives its wait context from the
// PARENT context via context.WithTimeout(ctx, timeout) — Go's context package
// always honors the EARLIER of the two deadlines. In production the parent is
// ultimately the incoming hook HTTP request's context, which is already
// bounded well under that generated 5s timeout (agentd's own per-request
// evaluationContext, and cordum-hook's own CORDUM_AGENTD_HOOK_TIMEOUT client
// budget, are both configured/validated to stay under 5s independent of
// CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT). So a 30s nominal wait timeout
// can never actually run anywhere near 30s here: this test proves the call
// returns promptly once the (short) parent deadline elapses, not the (long)
// nominal one, and that an unresolved wait returns the pending approval (which
// the hook denies) rather than allowing the action through.
func TestApprovalRequiredDecisionClampedByParentContextNotNominalTimeout(t *testing.T) {
	t.Parallel()

	parentCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cfg := ApprovalDecisionConfig{
		InlineWaitEnabled: true,
		InlineWaitTimeout: 30 * time.Second, // the doctor-flagged default
		PolicyMode:        edgecore.PolicyModeEnforce,
	}

	started := time.Now()
	decision := AgentdDecisionFromEvaluateResponse(parentCtx, approvalRequiredResponse(), cfg, blockingApprovalWaiter{}, nil)
	elapsed := time.Since(started)

	if elapsed > 2*time.Second {
		t.Fatalf("approval wait ran for %s, want it clamped to the ~50ms parent deadline (not the 30s nominal InlineWaitTimeout)", elapsed)
	}
	if decision.Decision != claude.DecisionRequireApproval || decision.ApprovalRef != "edge_appr_123" {
		t.Fatalf("decision = %#v, want the pending approval (the hook denies it) — an unresolved wait must fail closed, not allow the action through", decision)
	}
}
