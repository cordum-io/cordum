package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// fakeUpstreamToolCaller stands in for the underlying tool handler the
// bridge wraps with policy gating. It records each invocation so tests
// can assert the upstream was (or was not) reached on each decision path.
type fakeUpstreamToolCaller struct {
	calls  int
	result *ToolCallResult
	err    error
	called []ToolCallParams
}

func (f *fakeUpstreamToolCaller) Invoke(_ context.Context, params ToolCallParams) (*ToolCallResult, error) {
	f.calls++
	f.called = append(f.called, params)
	return f.result, f.err
}

// TestInvokeToolWithPolicy_AllowEmitsPreAndPost asserts the happy path:
// gate allow + upstream success → emitter receives [pre, post] in order,
// both decisions=allow, upstream called exactly once with original params.
func TestInvokeToolWithPolicy_AllowEmitsPreAndPost(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}, IsError: false},
	}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream.calls = %d, want 1", upstream.calls)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("expected 2 events (pre+post), got %d", len(emitter.events))
	}
	if emitter.events[0].Kind != edge.EventKindMCPToolPre {
		t.Fatalf("event[0] = %q, want %q", emitter.events[0].Kind, edge.EventKindMCPToolPre)
	}
	if emitter.events[1].Kind != edge.EventKindMCPToolPost {
		t.Fatalf("event[1] = %q, want %q", emitter.events[1].Kind, edge.EventKindMCPToolPost)
	}
	if emitter.events[1].Decision != edge.DecisionAllow {
		t.Fatalf("post.decision = %q, want allow", emitter.events[1].Decision)
	}
}

// TestInvokeToolWithPolicy_UpstreamErrorEmitsFailed asserts that an
// upstream transport error produces [pre, failed]. The Reason on the
// failed event MUST NOT carry raw upstream details that might contain
// URLs or credential substrings — the redactor sanitizes first.
func TestInvokeToolWithPolicy_UpstreamErrorEmitsFailed(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	leakyErr := errors.New("dial https://upstream.example/?token=sk-leaked: connection refused")
	upstream := &fakeUpstreamToolCaller{err: leakyErr}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "http.get_readonly",
		Arguments: json.RawMessage(`{"url":"https://api.example.com/data"}`),
	}, "remote-http")
	if err == nil {
		t.Fatal("expected upstream error to surface, got nil")
	}
	if len(emitter.events) != 2 {
		t.Fatalf("expected 2 events (pre+failed), got %d", len(emitter.events))
	}
	if emitter.events[1].Kind != edge.EventKindMCPToolFailed {
		t.Fatalf("event[1] = %q, want %q", emitter.events[1].Kind, edge.EventKindMCPToolFailed)
	}
	if strings.Contains(emitter.events[1].ErrorMessage, "sk-leaked") {
		t.Fatalf("failed.error_message leaks raw token from upstream error: %q", emitter.events[1].ErrorMessage)
	}
	if strings.Contains(emitter.events[1].ErrorMessage, "upstream.example") {
		t.Fatalf("failed.error_message leaks raw upstream host: %q", emitter.events[1].ErrorMessage)
	}
}

// TestInvokeToolWithPolicy_DenyDoesNotForwardUpstream asserts the deny
// short-circuit: upstream.Invoke MUST NOT be called. The caller sees an
// IsError=true result without reaching the underlying tool.
func TestInvokeToolWithPolicy_DenyDoesNotForwardUpstream(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			GateID:    "actiongate.mcp",
			Code:      "access_denied",
			Reason:    "tool denylisted",
			SubReason: "tool_denylisted",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	result, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.delete",
		Arguments: json.RawMessage(`{"path":"/var/data/x.db"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if upstream.calls != 0 {
		t.Fatalf("deny path must not forward upstream; got %d calls", upstream.calls)
	}
	if result == nil || !result.IsError {
		t.Fatalf("deny result should be IsError=true; got %+v", result)
	}
	if len(emitter.events) != 1 || emitter.events[0].Kind != edge.EventKindMCPToolFailed {
		t.Fatalf("expected single failed event on deny; got %d events: %+v", len(emitter.events), emitter.events)
	}
}

// TestInvokeToolWithPolicy_DenyStampsInvocationHandle is the GATE-LEVEL
// anti-regression for the policy-deny audit fix: a pre-dispatch DENY must
// stamp decision/reason/sub_reason + policy evidence onto the
// InvocationHandle in ctx, so the terminal mcp.tool_invocation audit event
// records decision=deny WITH evidence (not a false ALLOW). The
// auditor-only test (TestToolInvocationAuditor_PolicyDeniedResultAuditsDeny)
// calls MarkPolicyDenied by hand and would STILL pass if the gate stopped
// wiring it — this test fails if the MarkPolicyDenied call at
// policy_evaluate.go is removed, which is the real defect this task guards.
func TestInvokeToolWithPolicy_DenyStampsInvocationHandle(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			GateID:    "actiongate.mcp",
			Code:      "session_tainted",
			Reason:    "destructive action blocked",
			SubReason: "session_tainted_prompt_injection",
			Extra: map[string]string{
				"taint_snippet":     "SYSTEM OVERRIDE:",
				"taint_source_tool": "get_board_items_page",
			},
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream

	h := &InvocationHandle{}
	ctx := contextWithInvocationHandle(newAuthedToolCallCtx(), h)
	result, err := InvokeToolWithPolicy(ctx, deps, ToolCallParams{
		Name:      "all_monday_api",
		Arguments: json.RawMessage(`{"query":"mutation { delete_item(item_id: 1) { id } }"}`),
	}, "cordum.monday")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("deny result should be IsError=true; got %+v", result)
	}
	if upstream.calls != 0 {
		t.Fatalf("deny must not forward upstream; got %d calls", upstream.calls)
	}
	// Anti-regression: these are zero unless InvokeToolWithPolicy wires the
	// gate decision onto the handle via MarkPolicyDenied.
	if h.decision != "deny" {
		t.Fatalf("handle.decision = %q, want deny (MarkPolicyDenied not wired from the gate?)", h.decision)
	}
	if h.reason != "destructive action blocked" {
		t.Errorf("handle.reason = %q, want %q", h.reason, "destructive action blocked")
	}
	if h.subReason != "session_tainted_prompt_injection" {
		t.Errorf("handle.subReason = %q, want %q", h.subReason, "session_tainted_prompt_injection")
	}
	// Policy EVIDENCE (taint snippet/source) must flow from the decision
	// Extra to the handle so the audit event carries WHY it was denied.
	if h.policyExtra["taint_snippet"] != "SYSTEM OVERRIDE:" {
		t.Errorf("handle.policyExtra[taint_snippet] = %q, want %q", h.policyExtra["taint_snippet"], "SYSTEM OVERRIDE:")
	}
	if h.policyExtra["taint_source_tool"] != "get_board_items_page" {
		t.Errorf("handle.policyExtra[taint_source_tool] = %q, want %q", h.policyExtra["taint_source_tool"], "get_board_items_page")
	}
}

// TestInvokeToolWithPolicy_DedupesRetryDoubleEmit asserts the EventID-
// keyed sync.Once dedupe: when a retry passes the same EventID, the
// emitter sees pre+post (or pre+failed) exactly once each.
func TestInvokeToolWithPolicy_DedupesRetryDoubleEmit(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
	}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	// Force a stable EventID so the dedupe key is identical across calls.
	deps.EventIDFactory = func() string { return "evt_stable" }
	deps.DedupeState = NewInProcessDedupeStore()
	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}
	_, err1 := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	if err1 != nil {
		t.Fatalf("call 1: %v", err1)
	}
	_, err2 := InvokeToolWithPolicy(ctx, deps, params, "local-fs")
	if err2 != nil {
		t.Fatalf("call 2 (retry): %v", err2)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("retry with same EventID must dedupe to exactly 2 events (pre+post); got %d: %+v",
			len(emitter.events), emitter.events)
	}
}

// TestInvokeToolWithPolicy_RequireApprovalHandsOffToApprovalStore
// asserts that a REQUIRE_HUMAN gate decision routes through the
// gateway's approval-store adapter. Upstream is NOT invoked. The pre
// event carries Decision=require_approval; no post event yet (that
// arrives later when the approval is resolved).
func TestInvokeToolWithPolicy_RequireApprovalHandsOffToApprovalStore(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			GateID:    "actiongate.mutation",
			Code:      "require_human",
			Reason:    "needs human approval",
			SubReason: "require_human",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{}
	approvalHandoff := &fakeApprovalHandoff{ref: "appr_pending_42"}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.ApprovalHandoff = approvalHandoff
	result, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.delete",
		Arguments: json.RawMessage(`{"path":"/var/data/x.db"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if upstream.calls != 0 {
		t.Fatalf("require_approval must not forward upstream; got %d calls", upstream.calls)
	}
	if approvalHandoff.calls != 1 {
		t.Fatalf("require_approval should consume via gateway adapter; got %d calls", approvalHandoff.calls)
	}
	if result == nil {
		t.Fatal("result is nil; expected approval-pending sentinel")
	}
	if len(emitter.events) == 0 || emitter.events[0].Kind != edge.EventKindMCPToolPre {
		t.Fatalf("expected pre event with require_approval; got %+v", emitter.events)
	}
	if emitter.events[0].Decision != edge.DecisionRequireApproval {
		t.Fatalf("pre.decision = %q, want require_approval", emitter.events[0].Decision)
	}
}

// TestInvokeToolWithPolicy_AllowWithConstraintsPostCarriesConstraints
// asserts the AWC happy path: gate returns ALLOW_WITH_CONSTRAINTS with
// a structured constraint map → upstream still invoked (AWC counts as
// allowed) AND the matching post event records Decision=constrain
// (NOT allow) AND carries the same constraint map the gate emitted.
// Bundled scope from task-3d5c4f37: AWC constraint metadata must
// survive into the post-event audit trail so dashboards and audit
// consumers can pivot on the constraint payload.
func TestInvokeToolWithPolicy_AllowWithConstraintsPostCarriesConstraints(t *testing.T) {
	t.Parallel()
	constraints := map[string]any{
		"max_bytes": float64(1024),
		"redaction": "strict",
	}
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:    pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
			GateID:      "actiongate.mcp",
			Code:        "constrained",
			Reason:      "tier ceiling",
			Constraints: constraints,
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
	}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("AWC must still forward upstream (AWC counts as allowed); got %d calls", upstream.calls)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("expected pre+post events on AWC, got %d", len(emitter.events))
	}
	pre := emitter.events[0]
	post := emitter.events[1]
	if pre.Kind != edge.EventKindMCPToolPre {
		t.Fatalf("pre.Kind = %q, want %q", pre.Kind, edge.EventKindMCPToolPre)
	}
	if pre.Decision != edge.DecisionConstrain {
		t.Fatalf("pre.Decision = %q, want %q", pre.Decision, edge.DecisionConstrain)
	}
	if post.Kind != edge.EventKindMCPToolPost {
		t.Fatalf("post.Kind = %q, want %q", post.Kind, edge.EventKindMCPToolPost)
	}
	if post.Decision != edge.DecisionConstrain {
		t.Fatalf("post.Decision = %q, want %q (AWC must NOT degrade to allow on post; bundled fix from task-3d5c4f37)", post.Decision, edge.DecisionConstrain)
	}
	if !reflect.DeepEqual(post.Constraints, constraints) {
		t.Fatalf("post.Constraints = %#v, want %#v (constraint map must propagate to post event)", post.Constraints, constraints)
	}
}

// TestInvokeToolWithPolicy_AllowWithConstraintsUpstreamErrorCarriesConstraints
// closes the symmetric gap: when a gate fires AWC but upstream then
// fails, the emitted mcp.tool.failed event MUST still record that the
// call was AWC-bounded (Decision=constrain) and carry the constraint
// map. Without this, an AWC-on-upstream-error path silently loses the
// gate's verdict. Adversarial-self-review item (g).
func TestInvokeToolWithPolicy_AllowWithConstraintsUpstreamErrorCarriesConstraints(t *testing.T) {
	t.Parallel()
	constraints := map[string]any{
		"max_bytes": float64(1024),
	}
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:    pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
			GateID:      "actiongate.mcp",
			Code:        "constrained",
			Reason:      "tier ceiling",
			Constraints: constraints,
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{err: errors.New("upstream timeout")}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err == nil {
		t.Fatal("expected upstream timeout to surface, got nil")
	}
	if len(emitter.events) != 2 {
		t.Fatalf("expected pre+failed events on AWC + upstream error, got %d", len(emitter.events))
	}
	failed := emitter.events[1]
	if failed.Kind != edge.EventKindMCPToolFailed {
		t.Fatalf("failed.Kind = %q, want %q", failed.Kind, edge.EventKindMCPToolFailed)
	}
	if failed.Decision != edge.DecisionConstrain {
		t.Fatalf("failed.Decision = %q, want %q (upstream failure on an AWC call must record the original AWC verdict)", failed.Decision, edge.DecisionConstrain)
	}
	if !reflect.DeepEqual(failed.Constraints, constraints) {
		t.Fatalf("failed.Constraints = %#v, want %#v (failed event must still carry the AWC constraint map)", failed.Constraints, constraints)
	}
}

// fakeApprovalHandoff records calls to the gateway approval-store
// adapter so tests can assert require-human handoff happens exactly
// once per require_approval decision.
type fakeApprovalHandoff struct {
	calls int
	ref   string
	err   error
}

func (f *fakeApprovalHandoff) ConsumeActionGateDecision(_ context.Context, _ PolicyDecision, _ ToolCallApprovalContext) (string, error) {
	f.calls++
	return f.ref, f.err
}

// fakeToolService implements the existing ToolService interface so
// the MCPServer integration test can wire a recording handler under
// the policy wrapper.
type fakeToolService struct {
	called int
	result *ToolCallResult
	err    error
}

func (f *fakeToolService) ListTools(_ context.Context) []Tool {
	return nil
}

func (f *fakeToolService) Call(_ context.Context, _ string, _ json.RawMessage) (*ToolCallResult, error) {
	f.called++
	return f.result, f.err
}

// TestMCPServer_WithPolicyGate_RoutesThroughPolicyWrapper asserts the
// server integration: when WithPolicyGate is wired and the gate
// allows, the tool service is called exactly once AND the EventEmitter
// receives the matching pre+post events. This is the regression guard
// for the bridge wiring step.
func TestMCPServer_WithPolicyGate_RoutesThroughPolicyWrapper(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	tools := &fakeToolService{result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}}
	srv := NewServer(nil, tools, nil, ServerConfig{})
	srv = srv.WithPolicyGate("cordum.builtin", ToolCallDeps{
		Pipeline:     pipeline,
		EventEmitter: emitter,
		Redactor:     DefaultRedactor(),
	})
	result, err := srv.invokeTool(newAuthedToolCallCtx(), ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	})
	if err != nil {
		t.Fatalf("invokeTool returned err: %v", err)
	}
	if result == nil {
		t.Fatal("invokeTool returned nil result")
	}
	if tools.called != 1 {
		t.Fatalf("ToolService.Call invoked %d times; want 1 (policy wrapper must forward upstream on ALLOW)", tools.called)
	}
	if len(emitter.events) != 2 {
		t.Fatalf("expected 2 events (pre+post), got %d", len(emitter.events))
	}
	if emitter.events[0].Kind != edge.EventKindMCPToolPre || emitter.events[1].Kind != edge.EventKindMCPToolPost {
		t.Fatalf("event sequence wrong: %v, %v", emitter.events[0].Kind, emitter.events[1].Kind)
	}
}

// TestMCPServer_WithoutPolicyGate_PreservesLegacyDirectCall asserts
// the backward-compat path: when WithPolicyGate is not wired, the
// server uses the direct ToolService.Call path and emits zero events.
// This protects dev/test deploys from being forced to wire a full
// policy backend.
func TestMCPServer_WithoutPolicyGate_PreservesLegacyDirectCall(t *testing.T) {
	t.Parallel()
	emitter := &fakeEventEmitter{}
	tools := &fakeToolService{result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}}
	srv := NewServer(nil, tools, nil, ServerConfig{})
	result, err := srv.invokeTool(context.Background(), ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("invokeTool err: %v", err)
	}
	if result == nil || result.Content[0].Text != "ok" {
		t.Fatalf("legacy path lost ToolService result: %+v", result)
	}
	if tools.called != 1 {
		t.Fatalf("ToolService.Call invoked %d times; want 1 in legacy path", tools.called)
	}
	if len(emitter.events) != 0 {
		t.Fatalf("legacy path must not emit events without WithPolicyGate; got %d", len(emitter.events))
	}
}
