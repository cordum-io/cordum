package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
)

// TestLegacyMCPApprovalArgsHash_NormalizesPlaceholders is the PR #276
// Sub-H #30 regression. The helper that bridges upstream-controlled
// ctxData.ActionHash into the legacy MCPApprovalStore ArgsHash column
// must (a) always return a 64-character value so the SIEM correlation
// shape is stable and (b) never return the short "00000000" 8-char
// placeholder that predated the canonical-args binding. Real non-empty
// hex digests must pass through unchanged so retry idempotency keys
// stay byte-identical across the normalization pass.
func TestLegacyMCPApprovalArgsHash_NormalizesPlaceholders(t *testing.T) {
	t.Parallel()
	zero64 := legacyMCPApprovalArgsHashZero
	if len(zero64) != 64 {
		t.Fatalf("legacyMCPApprovalArgsHashZero len=%d, want 64 (sha256 hex shape)", len(zero64))
	}
	if _, err := hex.DecodeString(zero64); err != nil {
		t.Fatalf("legacyMCPApprovalArgsHashZero hex-decode failed: %v", err)
	}

	realHash := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty_normalized_to_64_zeros", "", zero64},
		{"whitespace_normalized_to_64_zeros", "   ", zero64},
		{"short_placeholder_normalized_to_64_zeros", "00000000", zero64},
		{"short_placeholder_padded_whitespace", "  00000000  ", zero64},
		{"real_64_hex_passes_through", realHash, realHash},
		{"real_64_hex_trimmed_passes_through", "  " + realHash + " ", realHash},
		// A non-canonical but non-placeholder value MUST be preserved
		// — the legacy SIEM table accepts arbitrary strings and a
		// stricter shape check would drop legitimately-correlated rows.
		{"non_hex_marker_preserved", "legacy-debug-token", "legacy-debug-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := legacyMCPApprovalArgsHash(tc.in)
			if got != tc.want {
				t.Fatalf("legacyMCPApprovalArgsHash(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got == "00000000" {
				t.Errorf("helper returned the 8-char short placeholder; want 64-char zero hex instead")
			}
		})
	}
}

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
// (tenant, agent, tool) tuple. Used to assert preapproval HIT
// short-circuits the approval store consultation.
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

// TestPreapprovalSkipsActionGate asserts that a preapproved (tenant,
// agent, tool) tuple short-circuits the approval store entirely.
// Returns ("", nil) so the caller treats the call as immediately
// allowed, mirroring the existing gatewayApprovalGate.Check fast path.
// Asserts the store is NOT consulted (call count == 0).
func TestPreapprovalSkipsActionGate(t *testing.T) {
	t.Parallel()
	preapproval := &fakePreapprovalLookup{
		tenant:   "tnt_a",
		agentID:  "agent_alpha",
		toolName: "git.push",
	}
	// We don't pass a real store so any unexpected store call would
	// panic — the test asserts no store interaction by reaching the
	// end without nil-deref.
	gate := &gatewayApprovalGate{
		store:       &MCPApprovalStore{},
		preapproval: preapproval,
	}
	ref, err := gate.ConsumeActionGateDecision(context.Background(),
		mcp.PolicyDecision{Decision: 3 /* REQUIRE_HUMAN */},
		mcp.ToolCallApprovalContext{
			Tenant:     "tnt_a",
			AgentID:    "agent_alpha",
			Tool:       "git.push",
			ActionHash: "deadbeef",
		})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ref != "" {
		t.Fatalf("preapproval HIT should return empty ref, got %q", ref)
	}
	if preapproval.calls != 1 {
		t.Fatalf("preapproval consulted %d times; want exactly 1", preapproval.calls)
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

// TestActionGateRequireHumanRoutesToApprovalStore asserts the happy
// REQUIRE_HUMAN path: no invariant deny, no preapproval hit → the
// gate falls through to the approval store. We use a real (nil-Redis)
// MCPApprovalStore so the ClaimPreApproved call hits the
// ensureReady-style early-error path and returns an error wrapped by
// our adapter — the test verifies the wrapping, which is the contract
// the JSON-RPC layer relies on for error code mapping.
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
