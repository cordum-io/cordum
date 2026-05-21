package mcp

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

// TestMCPServer_WithPolicyGate_AllZeroDepsDisablesGate locks the
// c530c1c0 contract: passing a zero-value ToolCallDeps is the explicit
// opt-out path that leaves the gate off. The bridge then falls through
// to the legacy direct-dispatch ToolService.Call, so dev/test deploys
// boot without rewiring a full policy backend.
func TestMCPServer_WithPolicyGate_AllZeroDepsDisablesGate(t *testing.T) {
	t.Parallel()
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithPolicyGate("cordum.builtin", ToolCallDeps{})
	if srv.HasPolicyGate() {
		t.Fatalf("HasPolicyGate() = true after all-zero deps; want false (c530c1c0 explicit-opt-out path)")
	}
	if name := srv.PolicyServerName(); name != "" {
		t.Fatalf("PolicyServerName() = %q after all-zero deps; want empty", name)
	}
	if emitter := srv.PolicyEventEmitter(); emitter != nil {
		t.Fatalf("PolicyEventEmitter() = %T; want nil for all-zero deps", emitter)
	}
}

// TestMCPServer_WithPolicyGate_PipelineMissingDisablesGate locks the
// partial-wiring guard added in c530c1c0: when Pipeline is nil but
// EventEmitter is wired, the guard MUST reset policyDeps to nil so
// the failure surfaces at boot (HasPolicyGate=false) rather than as
// -32603 on every tools/call.
func TestMCPServer_WithPolicyGate_PipelineMissingDisablesGate(t *testing.T) {
	t.Parallel()
	emitter := &fakeEventEmitter{}
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithPolicyGate("cordum.builtin", ToolCallDeps{
		EventEmitter:  emitter,
		ArtifactStore: &fakeArtifactStore{},
	})
	if srv.HasPolicyGate() {
		t.Fatalf("HasPolicyGate() = true with nil Pipeline; want false (partial-wiring guard must fire)")
	}
	if name := srv.PolicyServerName(); name != "" {
		t.Fatalf("PolicyServerName() = %q after partial wiring; want empty (guard must reset)", name)
	}
}

// TestMCPServer_WithPolicyGate_EventEmitterMissingDisablesGate locks
// the symmetric partial-wiring guard: when Pipeline is wired but
// EventEmitter is nil, EvaluateToolCall would fail at the pre-event
// emit step. Disabling at boot keeps the failure greppable instead of
// per-request.
func TestMCPServer_WithPolicyGate_EventEmitterMissingDisablesGate(t *testing.T) {
	t.Parallel()
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithPolicyGate("cordum.builtin", ToolCallDeps{
		Pipeline:      &fakePolicyDispatcher{},
		ArtifactStore: &fakeArtifactStore{},
	})
	if srv.HasPolicyGate() {
		t.Fatalf("HasPolicyGate() = true with nil EventEmitter; want false (partial-wiring guard must fire)")
	}
}

// TestMCPServer_WithPolicyGate_BothDepsPresentEnablesGate is the happy
// path: with Pipeline AND EventEmitter both wired, the gate stays on
// and the accessors expose the wiring state for boot-log assertions.
func TestMCPServer_WithPolicyGate_BothDepsPresentEnablesGate(t *testing.T) {
	t.Parallel()
	emitter := &fakeEventEmitter{}
	store := &fakeArtifactStore{}
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithPolicyGate("cordum.builtin", ToolCallDeps{
		Pipeline:      &fakePolicyDispatcher{},
		EventEmitter:  emitter,
		ArtifactStore: store,
	})
	if !srv.HasPolicyGate() {
		t.Fatalf("HasPolicyGate() = false after happy-path wiring; want true")
	}
	if got := srv.PolicyServerName(); got != "cordum.builtin" {
		t.Fatalf("PolicyServerName() = %q; want %q", got, "cordum.builtin")
	}
	if got := srv.PolicyEventEmitter(); got != emitter {
		t.Fatalf("PolicyEventEmitter() = %T; want the *fakeEventEmitter passed in (must NOT be a noop fallback)", got)
	}
	if got := srv.PolicyArtifactStore(); got != store {
		t.Fatalf("PolicyArtifactStore() = %T; want the *fakeArtifactStore passed in (must NOT be a noop fallback)", got)
	}
}

// TestMCPServer_WithApprovalHold_NoStoreDisablesHold locks the
// existing guard at server.go:170: passing zero-value
// ApprovalHoldDeps (Store nil) disables the resume path so legacy
// servers boot unchanged.
func TestMCPServer_WithApprovalHold_NoStoreDisablesHold(t *testing.T) {
	t.Parallel()
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithApprovalHold(ApprovalHoldDeps{})
	if srv.HasApprovalHold() {
		t.Fatalf("HasApprovalHold() = true with nil Store; want false (zero-deps guard must fire)")
	}
}

// TestMCPServer_WithApprovalHold_NoSnapshotDisablesHold locks the
// c530c1c0 guard at server.go:179: PolicySnapshot is required (the
// ProcessApprovalClaim ApprovalClaimRequest validation rejects empty
// snapshot). Without a snapshot provider the entire resume path would
// fail closed at runtime — refuse to enable here.
func TestMCPServer_WithApprovalHold_NoSnapshotDisablesHold(t *testing.T) {
	t.Parallel()
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{})
	srv = srv.WithApprovalHold(ApprovalHoldDeps{
		Store: stubApprovalStore{},
		// PolicySnapshot intentionally nil
		ServerName: "cordum.builtin",
	})
	if srv.HasApprovalHold() {
		t.Fatalf("HasApprovalHold() = true with nil PolicySnapshot; want false (c530c1c0 snapshot guard must fire)")
	}
}

// stubApprovalStore is the smallest non-nil ApprovalClaimStore so the
// nil-Store guard does not fire before the nil-PolicySnapshot guard.
type stubApprovalStore struct{}

func (stubApprovalStore) ClaimApproval(_ context.Context, _ edge.ApprovalClaimRequest) (*edge.EdgeApproval, bool, error) {
	return nil, false, nil
}
