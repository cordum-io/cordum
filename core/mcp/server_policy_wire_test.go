package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

// TestMCPServer_InternalErrorsHideDetail: failures of the MCP machinery itself
// (the Edge store refusing an event, the approval store failing a claim or a
// mint) used to reach the JSON-RPC client verbatim, internal ids included.
// The client now gets a fixed text; the detail goes to the log.
func TestMCPServer_InternalErrorsHideDetail(t *testing.T) {
	t.Parallel()
	const internal = "edge execution mcp-direct-0f3c is full"
	gated := NewServer(nil, &fakeToolService{}, nil, ServerConfig{}).WithPolicyGate("cordum.builtin", ToolCallDeps{
		Pipeline:      &fakePolicyDispatcher{},
		EventEmitter:  &fakeEventEmitter{err: errors.New(internal)},
		ArtifactStore: &fakeArtifactStore{},
	})
	held := NewServer(nil, &fakeToolService{}, nil, ServerConfig{}).WithApprovalHold(ApprovalHoldDeps{
		Store:          &fakeApprovalClaimStore{err: errors.New(internal)},
		PolicySnapshot: func(context.Context) string { return "snap" },
		ServerName:     "cordum.builtin",
	})
	conflicted := NewServer(nil, &fakeToolService{}, nil, ServerConfig{}).WithApprovalHold(ApprovalHoldDeps{
		Store:          &fakeApprovalClaimStore{err: fmt.Errorf("%w: %s", edge.ErrApprovalConflict, internal)},
		PolicySnapshot: func(context.Context) string { return "snap" },
		ServerName:     "cordum.builtin",
	})
	call := json.RawMessage(`{"name":"fs.write","arguments":{"path":"/tmp/x","_approval_ref":"edge_appr_1"}}`)
	replies := map[string]*JSONRPCError{}
	for name, srv := range map[string]*MCPServer{"pre event": gated, "claim store": held, "claim conflict": conflicted} {
		_, replies[name] = srv.handleToolsCall(newAuthedToolCallCtx(), call)
	}
	for _, err := range []error{
		fmt.Errorf("%w: edge approval mint failed: %s", ErrApprovalStoreUnavailable, internal),
		fmt.Errorf("%w: %s", ErrApprovalGateMisconfigured, internal),
	} {
		replies[err.Error()] = gated.mapHandlerError(err)
	}
	for name, reply := range replies {
		raw, _ := json.Marshal(reply)
		if reply == nil || strings.Contains(string(raw), "mcp-direct-0f3c") {
			t.Errorf("%s: JSON-RPC error %s, want one without the internal detail", name, raw)
		}
	}
	if reply := replies["pre event"]; reply == nil || reply.Code != jsonRPCInternalErrorCode {
		t.Errorf("pre event append failure = %+v, want %d internal error", reply, jsonRPCInternalErrorCode)
	}
}

// TestMCPServer_MissingCallMetadataIsGatewayMisconfigured: a tools/call that
// reaches the policy gate without CallMetadata is a gateway wiring defect. The
// approval hold already answered it with -32097 and data missing_mcp_metadata,
// but the policy gate answered a generic -32603 with the sentinel's text. Both
// now give the documented -32097 reply.
func TestMCPServer_MissingCallMetadataIsGatewayMisconfigured(t *testing.T) {
	t.Parallel()
	srv := NewServer(nil, &fakeToolService{}, nil, ServerConfig{}).
		WithPolicyGate("cordum.builtin", ToolCallDeps{
			Pipeline:      &fakePolicyDispatcher{},
			EventEmitter:  &fakeEventEmitter{},
			ArtifactStore: &fakeArtifactStore{},
		}).
		WithApprovalHold(ApprovalHoldDeps{
			Store:          &fakeApprovalClaimStore{},
			PolicySnapshot: func(context.Context) string { return "snap" },
			ServerName:     "cordum.builtin",
		})
	for name, call := range map[string]string{
		"policy gate":   `{"name":"fs.write","arguments":{"path":"/tmp/x"}}`,
		"approval hold": `{"name":"fs.write","arguments":{"path":"/tmp/x","_approval_ref":"edge_appr_1"}}`,
	} {
		_, reply := srv.handleToolsCall(context.Background(), json.RawMessage(call))
		if reply == nil || reply.Code != jsonRPCGatewayMisconfiguredCode || reply.Data != "missing_mcp_metadata" {
			t.Errorf("%s without call metadata = %+v, want %d with data missing_mcp_metadata",
				name, reply, jsonRPCGatewayMisconfiguredCode)
		}
	}
}

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
