package agentd

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

// fakeConsumeOnceGateway simulates the Gateway /api/v1/edge/evaluate contract for
// the inline-wait consume-once invariant. An evaluate WITHOUT approval_ref returns
// REQUIRE_APPROVAL; an evaluate carrying approval_ref runs the single-use CAS —
// ALLOW the first time (sets ConsumedAt), "already consumed" DENY thereafter —
// exactly like consumeEdgeEvaluateApproval -> ClaimApproval on the real gateway.
type fakeConsumeOnceGateway struct {
	mu          sync.Mutex
	approvalRef string
	actionHash  string
	consumed    bool
	requests    []EvaluateRequest
}

func (f *fakeConsumeOnceGateway) Evaluate(_ context.Context, req EvaluateRequest) (*EvaluateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if strings.TrimSpace(req.ApprovalRef) == "" {
		// Initial action evaluation → human approval required.
		return &EvaluateResponse{
			Decision:    string(edgecore.DecisionRequireApproval),
			ApprovalRef: f.approvalRef,
			ApprovalURL: "/api/v1/edge/approvals/" + f.approvalRef,
			ActionHash:  f.actionHash,
			Reason:      "approval required",
		}, nil
	}
	// Consuming re-evaluation (approval_ref present) → single-use CAS.
	if f.consumed {
		return &EvaluateResponse{
			Decision:    string(edgecore.DecisionDeny),
			ApprovalRef: req.ApprovalRef,
			Reason:      "approval already consumed; request a new approval",
		}, nil
	}
	f.consumed = true
	return &EvaluateResponse{
		Decision:   string(edgecore.DecisionAllow),
		ActionHash: f.actionHash,
		Reason:     "approved",
	}, nil
}

// consumeAttempts counts evaluate calls that carried an approval_ref (i.e. the
// consume-once CAS path). A passive 'approved' poll must NOT authorize execution,
// so an approved inline-wait turn must produce exactly one consume attempt.
func (f *fakeConsumeOnceGateway) consumeAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.TrimSpace(r.ApprovalRef) != "" {
			n++
		}
	}
	return n
}

func inlineApprovalHookRequest() claude.AgentdRequest {
	return claude.AgentdRequest{
		EventName:     "PreToolUse",
		SessionID:     "edge_sess_eval",
		ExecutionID:   "edge_exec_eval",
		TenantID:      "tenant-eval",
		PrincipalID:   "principal-eval",
		ToolName:      "Bash",
		ToolUseID:     "toolu-consume",
		InputRedacted: map[string]any{"command": "rm -rf important"},
		InputHash:     "sha256:input-eval",
		ActionHash:    "sha256:action-eval",
		Capability:    "exec.shell",
		RiskTags:      []string{"exec", "destructive"},
		Labels:        map[string]string{"command.class": "destructive"},
	}
}

func newInlineWaitEvaluator(gw EvaluateClient, waiter ApprovalWaiter) *Evaluator {
	return NewEvaluator(EvaluatorConfig{
		Client:         gw,
		EventWriter:    &captureEventWriter{},
		State:          evaluatorTestState(edgecore.PolicyModeEnforce),
		ApprovalWaiter: waiter,
		ApprovalConfig: ApprovalDecisionConfig{
			InlineWaitEnabled: true,
			InlineWaitTimeout: 2 * time.Second,
			PolicyMode:        edgecore.PolicyModeEnforce,
		},
		HookTimeout: 2 * time.Second,
	})
}

// TestEvaluator_InlineWaitApproved_ConsumesApprovalExactlyOnce reproduces the
// EDGE consume-once bypass: with inline-wait enabled (the cordumctl edge claude
// default), an approved approval was returned as ALLOW off a read-only /wait poll
// without ever running the single-use CAS, leaving Status=approved/ConsumedAt=nil
// so the SAME approval authorized a second destructive execution.
func TestEvaluator_InlineWaitApproved_ConsumesApprovalExactlyOnce(t *testing.T) {
	t.Parallel()

	gw := &fakeConsumeOnceGateway{approvalRef: "edge_appr_consume", actionHash: "sha256:action-eval"}
	waiter := &fakeApprovalWaiter{result: ApprovalWaitResult{Status: ApprovalWaitApproved, Reason: "approved by reviewer"}}
	evaluator := newInlineWaitEvaluator(gw, waiter)
	req := inlineApprovalHookRequest()

	// Turn 1: human approves inline. ALLOW must be gated on a consume-once CAS
	// performed by THIS turn — not a passive 'approved' read.
	d1, err := evaluator.EvaluateHook(context.Background(), req)
	if err != nil {
		t.Fatalf("EvaluateHook turn 1: %v", err)
	}
	if d1.Decision != claude.DecisionAllow {
		t.Fatalf("turn 1 decision = %q, want allow", d1.Decision)
	}
	if got := gw.consumeAttempts(); got != 1 {
		t.Fatalf("turn 1 consume attempts = %d, want 1 — inline-wait ALLOW must consume the approval (CAS), not authorize off a passive poll", got)
	}
	if !gw.consumed {
		t.Fatal("turn 1 left the approval UNCONSUMED (ConsumedAt never set) — single-use guarantee bypassed")
	}

	// Turn 2: the SAME human approval must NOT authorize a second execution.
	d2, err := evaluator.EvaluateHook(context.Background(), req)
	if err != nil {
		t.Fatalf("EvaluateHook turn 2: %v", err)
	}
	if d2.Decision != claude.DecisionDeny {
		t.Fatalf("turn 2 decision = %q, want deny — one approval must authorize exactly one execution (already consumed)", d2.Decision)
	}
}
