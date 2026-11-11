package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policy/actiongates"
)

// fakeInvariantLookup returns a deny rule keyed by tool name. The
// tests use this to simulate the SecOps invariant SECURITY FLOOR
// taking priority over any actiongate decision.
type fakeInvariantLookup struct {
	denyTool string
	denyRule config.PolicyRule
}

func (f fakeInvariantLookup) InvariantsForMCPTool(_ context.Context) []config.PolicyRule {
	if f.denyTool == "" {
		return nil
	}
	return []config.PolicyRule{f.denyRule}
}

// fakePreapprovalLookup returns IsPreapproved=true for a configured
// (tenant, agent, tool) tuple and counts consultations, so tests can
// assert the action-gate handoff does NOT consult preapproval
// (task-06784e6d R1).
type fakePreapprovalLookup struct {
	tenant   string
	agentID  string
	toolName string
	calls    int
}

func (f *fakePreapprovalLookup) IsPreapproved(_ context.Context, tenant, agentID, toolName string) bool {
	f.calls++
	return tenant == f.tenant && agentID == f.agentID && toolName == f.toolName
}

// TestInvariantBeatsActionGate asserts the SECURITY FLOOR contract: an
// MCPInvariantLookup DENY rule blocks the tool even when the upstream
// actiongate decision was REQUIRE_HUMAN (or anything else). This must
// hold at the ConsumeActionGateDecision boundary because the policy
// wrapper hands off only on REQUIRE_HUMAN — invariant denies that
// catch destructive tools after action-gate processing would
// otherwise bypass the SecOps floor.
func TestInvariantBeatsActionGate(t *testing.T) {
	t.Parallel()
	denyRule := config.PolicyRule{
		ID:       "secops.no_payments",
		Decision: "deny",
		Match: config.PolicyMatch{
			MCP: config.MCPPolicy{DenyTools: []string{"payments.send"}},
		},
	}
	gate := &gatewayApprovalGate{
		store:      &MCPApprovalStore{},
		invariants: fakeInvariantLookup{denyTool: "payments.send", denyRule: denyRule},
	}
	_, err := gate.ConsumeActionGateDecision(context.Background(),
		mcp.PolicyDecision{Decision: 3 /* REQUIRE_HUMAN */},
		mcp.ToolCallApprovalContext{
			Tenant:     "tnt_a",
			AgentID:    "agent_alpha",
			Tool:       "payments.send",
			ActionHash: "deadbeef",
		})
	if err == nil {
		t.Fatal("expected invariant deny error, got nil")
	}
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("error not wrapping ErrMCPInvariantDeny: %v", err)
	}
}

// paActionHash is paApprovalContext's 64-hex action hash: the legacy
// MCPApprovalStore ArgsHash the pre-fix fallback claimed under.
const paActionHash = "abababababababababababababababababababababababababababababababab"

// paApprovalContext is the REQUIRE_HUMAN handoff context of a destructive
// fs.delete_record call by agent_alpha in tnt_a.
func paApprovalContext() mcp.ToolCallApprovalContext {
	return mcp.ToolCallApprovalContext{
		Tenant:     "tnt_a",
		AgentID:    "agent_alpha",
		Server:     "cordum.builtin",
		Tool:       "fs.delete_record",
		ActionHash: paActionHash,
		Args:       json.RawMessage(`{"record":"rec-pa"}`),
		EventID:    "evt-pre-pa",
	}
}

// paCallMetadata is the Edge session metadata of paApprovalContext's call.
func paCallMetadata() mcp.CallMetadata {
	return mcp.CallMetadata{Tenant: "tnt_a", Principal: "alice", AgentID: "agent_alpha",
		SessionID: "sess_pa", ExecutionID: "exec_pa"}
}

// paPreapproved preapproves paApprovalContext's (tenant, agent, tool) tuple.
func paPreapproved() *fakePreapprovalLookup {
	return &fakePreapprovalLookup{tenant: "tnt_a", agentID: "agent_alpha", toolName: "fs.delete_record"}
}

// TestActionGateHold_PreapprovalNotConsulted (task-06784e6d R1): scope
// preapproval never waives an action-gate REQUIRE_HUMAN. A preapproved
// identity's held call mints the Edge approval bound to its pre-event and
// returns that resumable ref, never an empty one, without consulting the
// preapproval lookup.
func TestActionGateHold_PreapprovalNotConsulted(t *testing.T) {
	t.Parallel()
	preapproval := paPreapproved()
	edgeStore := &captureEdgeStore{approvalID: "edge_appr_pa_1"}
	gate := &gatewayApprovalGate{
		store:          &MCPApprovalStore{},
		preapproval:    preapproval,
		edgeStore:      edgeStore,
		policySnapshot: func(context.Context) string { return "policy-pa" },
		serverName:     "cordum.builtin",
	}
	ctx := mcp.WithCallMetadata(context.Background(), paCallMetadata())
	ref, err := gate.ConsumeActionGateDecision(ctx, mcp.PolicyDecision{Decision: 3 /* REQUIRE_HUMAN */}, paApprovalContext())
	if err != nil || ref != "edge_appr_pa_1" {
		t.Errorf("ConsumeActionGateDecision = %q, %v; want the Edge ref edge_appr_pa_1", ref, err)
	}
	if preapproval.calls != 0 {
		t.Errorf("preapproval consulted %d times; want 0", preapproval.calls)
	}
	if edgeStore.enqueueCalls != 1 || edgeStore.lastReq.EventID != "evt-pre-pa" {
		t.Errorf("Edge enqueues = %d bound to event %q; want 1 bound to evt-pre-pa",
			edgeStore.enqueueCalls, edgeStore.lastReq.EventID)
	}
}

// TestActionGateHandoff_EdgeMintUnwiredFailsClosed (task-06784e6d R2): when
// the Edge mint cannot run (Edge store unwired, or no Edge SessionID), the
// handoff fails closed with ErrApprovalGateMisconfigured (-32097). The legacy
// MCPApprovalStore is not an approval authority under the policy gate: an
// approved record for the same tuple is not claimed, no PENDING record is
// enqueued, and the preapproval lookup is not consulted.
func TestActionGateHandoff_EdgeMintUnwiredFailsClosed(t *testing.T) {
	t.Parallel()
	noSession := paCallMetadata()
	noSession.SessionID = ""
	cases := []struct {
		name        string
		edgeStore   *captureEdgeStore
		preapproval *fakePreapprovalLookup
		meta        mcp.CallMetadata
	}{
		{name: "edge_store_unwired", meta: paCallMetadata()},
		{name: "edge_store_unwired_preapproved", preapproval: paPreapproved(), meta: paCallMetadata()},
		{name: "no_edge_session_id", edgeStore: &captureEdgeStore{approvalID: "edge_appr_pa_2"}, meta: noSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := newTestMCPStore(t)
			seeded := paSeedApprovedLegacy(t, store)
			gate := &gatewayApprovalGate{store: store, serverName: "cordum.builtin",
				policySnapshot: func(context.Context) string { return "policy-pa" }}
			// Only non-nil fakes: a typed nil pointer in an interface field is not nil.
			if tc.edgeStore != nil {
				gate.edgeStore = tc.edgeStore
			}
			if tc.preapproval != nil {
				gate.preapproval = tc.preapproval
			}
			ctx := mcp.WithCallMetadata(context.Background(), tc.meta)
			ref, err := gate.ConsumeActionGateDecision(ctx, mcp.PolicyDecision{Decision: 3 /* REQUIRE_HUMAN */}, paApprovalContext())
			if ref != "" || !errors.Is(err, mcp.ErrApprovalGateMisconfigured) {
				t.Errorf("ConsumeActionGateDecision = %q, %v; want \"\" and ErrApprovalGateMisconfigured", ref, err)
			}
			paRequireLegacyUntouched(t, store, seeded.ID)
			if tc.edgeStore != nil && tc.edgeStore.enqueueCalls != 0 {
				t.Errorf("Edge enqueues = %d; want 0", tc.edgeStore.enqueueCalls)
			}
			if tc.preapproval != nil && tc.preapproval.calls != 0 {
				t.Errorf("preapproval consulted %d times; want 0", tc.preapproval.calls)
			}
		})
	}
}

// paSeedApprovedLegacy seeds an APPROVED, unconsumed legacy MCPApprovalStore
// record for paApprovalContext's tuple: the record the pre-fix fallback
// claimed and returned as the "approval ref".
func paSeedApprovedLegacy(t *testing.T, store *MCPApprovalStore) *MCPApprovalRecord {
	t.Helper()
	ctx := context.Background()
	rec, err := store.EnqueueMCPApproval(ctx, &MCPApprovalRequest{Tenant: "tnt_a", AgentID: "agent_alpha",
		ToolName: "fs.delete_record", ArgsHash: paActionHash, Requester: "agent_alpha", TTL: 5 * time.Minute})
	if err != nil {
		t.Fatalf("seed legacy approval: %v", err)
	}
	if _, err := store.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "resolver-pa", "seeded"); err != nil {
		t.Fatalf("approve legacy approval %s: %v", rec.ID, err)
	}
	return rec
}

// paRequireLegacyUntouched asserts the seeded legacy record is still APPROVED,
// unconsumed and claimable for its tuple, and that no PENDING record exists.
func paRequireLegacyUntouched(t *testing.T, store *MCPApprovalStore, id string) {
	t.Helper()
	ctx := context.Background()
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get legacy approval %s: %v", id, err)
	}
	if got.Status != model.ApprovalStatusApproved || got.ConsumedAt != 0 {
		t.Errorf("legacy approval %s status %q consumed_at %d; want approved and unconsumed", id, got.Status, got.ConsumedAt)
	}
	found, err := store.FindPreApproved(ctx, "tnt_a", "agent_alpha", "fs.delete_record", paActionHash)
	if err != nil || found == nil || found.ID != id {
		t.Errorf("FindPreApproved = %+v, %v; want the seeded approval %s, still claimable", found, err, id)
	}
	pending, _, err := store.ListByStatus(ctx, string(model.ApprovalStatusPending), 0, 50)
	if err != nil || len(pending) != 0 {
		t.Errorf("PENDING legacy approvals = %d, %v; want 0 (no legacy enqueue)", len(pending), err)
	}
}

// TestActionGateAllowSkipsApprovalStore asserts that the gate-side
// adapter is NOT consulted at all when the action-gate decision is
// ALLOW. ConsumeActionGateDecision is only invoked by the bridge
// wrapper on REQUIRE_HUMAN — for ALLOW the bridge forwards directly
// to upstream. This test documents the contract by asserting that
// when the bridge does NOT call ConsumeActionGateDecision, no
// approval-store side effects occur. We exercise the contract by
// confirming the dispatcher adapter is the only adapter consulted on
// ALLOW paths in the policy_evaluate flow (see core/mcp/bridge_policy_test.go
// TestInvokeToolWithPolicy_AllowEmitsPreAndPost asserting upstream
// reached + no approval store interaction).
func TestActionGateAllowSkipsApprovalStore(t *testing.T) {
	t.Parallel()
	// The core/mcp test TestInvokeToolWithPolicy_AllowEmitsPreAndPost
	// already asserts that on ALLOW the upstream tool service is
	// invoked exactly once AND no ApprovalHandoff call is made.
	// Here we lock the contract from the gateway side: if a future
	// refactor accidentally invokes ConsumeActionGateDecision on an
	// ALLOW decision, the gate's nil-store guard must still return
	// an error rather than silently creating phantom approvals.
	gate := &gatewayApprovalGate{store: nil}
	_, err := gate.ConsumeActionGateDecision(context.Background(),
		mcp.PolicyDecision{},
		mcp.ToolCallApprovalContext{Tool: "fs.read_file"})
	if err == nil {
		t.Fatal("nil-store gate must fail closed on direct ConsumeActionGateDecision invocation")
	}
}

// TestActionGateRequireHumanRoutesToApprovalStore asserts a
// REQUIRE_HUMAN handoff with no invariant deny and no Edge approval
// mint wired fails closed: the gate returns an
// mcp.ErrApprovalGateMisconfigured error (JSON-RPC -32097) and never
// falls back to the legacy MCPApprovalStore (task-06784e6d R2).
func TestActionGateRequireHumanRoutesToApprovalStore(t *testing.T) {
	t.Parallel()
	// Real store with no Redis client; ClaimPreApproved will fail at
	// the readiness check. The adapter must propagate the failure
	// instead of silently treating it as a successful claim.
	gate := &gatewayApprovalGate{
		store: &MCPApprovalStore{},
	}
	_, err := gate.ConsumeActionGateDecision(context.Background(),
		mcp.PolicyDecision{Decision: 3 /* REQUIRE_HUMAN */},
		mcp.ToolCallApprovalContext{
			Tenant:     "tnt_a",
			AgentID:    "agent_alpha",
			Tool:       "fs.delete",
			ActionHash: "deadbeef",
		})
	if err == nil {
		t.Fatal("unwired Redis store should propagate error, got nil")
	}
}

// TestPolicyDispatcherAdapter_NilPipelineReturnsZero asserts the
// gateway-side dispatcher adapter fails open on a nil pipeline:
// returns the zero decision with fired=false so the legacy approval
// flow takes over. This protects gateway deploys that boot without
// the action gate pipeline wired (older configs, dev mode) from
// breaking the MCP tool-call path entirely.
func TestPolicyDispatcherAdapter_NilPipelineReturnsZero(t *testing.T) {
	t.Parallel()
	adapter := policyDispatcherAdapter{pipeline: nil}
	dec, fired := adapter.Dispatch(context.Background(), &config.PolicyInput{Tenant: "tnt_a"})
	if fired {
		t.Fatalf("nil pipeline should not fire; got fired=true dec=%v", dec)
	}
	if dec.Decision != 0 {
		t.Fatalf("nil pipeline should return zero decision; got %v", dec.Decision)
	}
}

// fakeMCPEventEmitter / fakeMCPArtifactStore are test stand-ins for the
// production adapters. They satisfy the mcp interfaces so the test
// builds a fully-wired ToolCallDeps from non-nil deps without touching
// the production edge.RedisStore / artifacts.Store machinery.
type fakeMCPEventEmitter struct{}

func (fakeMCPEventEmitter) Emit(_ context.Context, _ *edge.AgentActionEvent) error { return nil }

type fakeMCPArtifactStore struct{}

func (fakeMCPArtifactStore) Put(_ context.Context, _ mcp.ArtifactPutRequest) (*edge.ArtifactPointer, error) {
	return &edge.ArtifactPointer{}, nil
}

// TestBuildMCPPolicyDeps_FailsClosedOnNilPipeline asserts the EDGE-102
// follow-up fail-closed contract: a nil action-gate pipeline produces a
// zero ToolCallDeps so MCPServer.WithPolicyGate's partial-wiring guard
// resets the gate and HasPolicyGate() reports false. Closes the loophole
// where the noop policyDispatcherAdapter wrapping a nil pipeline used to
// satisfy the interface check while silently downgrading every call to
// ALLOW.
func TestBuildMCPPolicyDeps_FailsClosedOnNilPipeline(t *testing.T) {
	t.Parallel()
	deps := BuildMCPPolicyDeps(nil, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, nil)
	assertZeroToolCallDeps(t, deps, "nil pipeline")
}

// TestBuildMCPPolicyDeps_FailsClosedOnNilEmitter asserts a nil
// EventEmitter produces a zero ToolCallDeps. Without this guard, every
// mcp.tool.pre / mcp.tool.post event would be silently dropped while
// the boot log claimed the gate was active.
func TestBuildMCPPolicyDeps_FailsClosedOnNilEmitter(t *testing.T) {
	t.Parallel()
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, nil, fakeMCPArtifactStore{}, nil)
	assertZeroToolCallDeps(t, deps, "nil emitter")
}

// TestBuildMCPPolicyDeps_FailsClosedOnNilArtifactStore asserts a nil
// ArtifactStore produces a zero ToolCallDeps. Without this guard,
// oversized redacted payloads would fail at materializeRedactedPayload
// time with -32603 on every tools/call instead of one boot-time signal.
func TestBuildMCPPolicyDeps_FailsClosedOnNilArtifactStore(t *testing.T) {
	t.Parallel()
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, nil, nil)
	assertZeroToolCallDeps(t, deps, "nil artifact store")
}

// TestBuildMCPPolicyDeps_WiresAllDepsWhenComplete asserts the positive
// branch: with every required dep non-nil, the builder returns a fully
// wired ToolCallDeps that MCPServer.WithPolicyGate will accept. A nil
// gate (approval-handoff) is allowed — handlers_mcp.go legitimately
// passes nil when Redis is unavailable.
func TestBuildMCPPolicyDeps_WiresAllDepsWhenComplete(t *testing.T) {
	t.Parallel()
	pipeline := &actiongates.Pipeline{}
	deps := BuildMCPPolicyDeps(pipeline, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, nil)
	if deps.Pipeline == nil {
		t.Fatal("deps.Pipeline nil; want policyDispatcherAdapter wrapping the supplied pipeline")
	}
	if _, ok := deps.Pipeline.(policyDispatcherAdapter); !ok {
		t.Fatalf("deps.Pipeline type = %T; want policyDispatcherAdapter", deps.Pipeline)
	}
	if deps.EventEmitter == nil {
		t.Fatal("deps.EventEmitter nil; want the supplied emitter")
	}
	if deps.ArtifactStore == nil {
		t.Fatal("deps.ArtifactStore nil; want the supplied store")
	}
	if deps.ApprovalHandoff == nil {
		t.Fatal("deps.ApprovalHandoff nil; want the supplied gate")
	}
	if deps.Redactor == nil {
		t.Fatal("deps.Redactor nil; want mcp.DefaultRedactor")
	}
}

// TestBuildMCPPolicyDeps_NilGateStillWires asserts the contract that a
// nil gatewayApprovalGate (ApprovalHandoff) does NOT trip the fail-
// closed guard — handlers_mcp.go disables the MCP approval store when
// Redis is unavailable, and the EvaluateToolCall path skips the
// REQUIRE_HUMAN handoff branch in that case. Pipeline + emitter + store
// remain mandatory.
func TestBuildMCPPolicyDeps_NilGateStillWires(t *testing.T) {
	t.Parallel()
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, nil, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, nil)
	if deps.Pipeline == nil {
		t.Fatal("nil gate must not zero the deps; want pipeline wired")
	}
	if deps.EventEmitter == nil {
		t.Fatal("nil gate must not zero the deps; want emitter wired")
	}
	if deps.ArtifactStore == nil {
		t.Fatal("nil gate must not zero the deps; want artifact store wired")
	}
	if deps.ApprovalHandoff == nil {
		t.Fatal("ApprovalHandoff should be the supplied (nil) gate — typed-nil interface")
	}
}

func assertZeroToolCallDeps(t *testing.T, deps mcp.ToolCallDeps, scenario string) {
	t.Helper()
	if deps.Pipeline != nil {
		t.Fatalf("%s: deps.Pipeline = %v; want nil so WithPolicyGate disables the gate", scenario, deps.Pipeline)
	}
	if deps.EventEmitter != nil {
		t.Fatalf("%s: deps.EventEmitter = %v; want nil", scenario, deps.EventEmitter)
	}
	if deps.ArtifactStore != nil {
		t.Fatalf("%s: deps.ArtifactStore = %v; want nil", scenario, deps.ArtifactStore)
	}
	if deps.ApprovalHandoff != nil {
		t.Fatalf("%s: deps.ApprovalHandoff = %v; want nil", scenario, deps.ApprovalHandoff)
	}
	if deps.Redactor != nil {
		t.Fatalf("%s: deps.Redactor = %v; want nil", scenario, deps.Redactor)
	}
}
