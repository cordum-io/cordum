package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// fakeApprovalGate returns a pre-built *ApprovalRequired so the E2E test
// can assert the JSON-RPC envelope carries the EDGE-103 contract fields
// (approval_ref, args_hash, expires_at, retry_hint, policy_snapshot)
// without depending on the gateway-package mint path. The struct lives
// in core/mcp; the gateway adapter is tested separately.
type fakeApprovalGate struct {
	required *ApprovalRequired
}

func (f *fakeApprovalGate) Check(_ context.Context, _ Tool, _ json.RawMessage) (*ApprovalRequired, error) {
	if f.required == nil {
		return nil, nil
	}
	cp := *f.required
	return &cp, nil
}

// TestJSONRPC_ApprovalRequiredEnvelopeCarriesEDGE103Fields is the DoD #1
// regression: when ToolRegistry.Call gates a mutating tool, the server's
// mapHandlerError MUST surface the *ApprovalRequired through JSON-RPC
// error code -32099 with error.data carrying approval_ref / args_hash /
// expires_at / retry_hint (alongside the legacy approval_id). Without
// this contract clients have no machine-readable way to resume the call.
func TestJSONRPC_ApprovalRequiredEnvelopeCarriesEDGE103Fields(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	tool := Tool{Name: "fs.write", RequiresApproval: true}
	if err := registry.Register(tool, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("tool handler must NOT execute on initial gated call")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	expires := time.Date(2026, 5, 16, 20, 0, 0, 0, time.UTC)
	registry.SetApprovalGate(&fakeApprovalGate{
		required: &ApprovalRequired{
			ApprovalID:     "mcp_appr_abc",
			ApprovalRef:    "edge_appr_xyz",
			ArgsHash:       "deadbeef",
			ExpiresAt:      expires,
			PolicySnapshot: "policy-v7",
			RetryHint:      "retry_with_approval_ref",
			Reason:         "tool requires human approval",
			Tool:           tool.Name,
		},
	})
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	})

	params, _ := json.Marshal(map[string]any{
		"name":      tool.Name,
		"arguments": map[string]any{"path": "/etc/hostname"},
	})
	_, rpcErr := srv.handleToolsCall(context.Background(), params)
	if rpcErr == nil {
		t.Fatal("expected -32099 JSON-RPC error on gated call; got nil")
	}
	if rpcErr.Code != jsonRPCApprovalRequiredCode {
		t.Errorf("rpcErr.Code = %d; want %d (approval_required)", rpcErr.Code, jsonRPCApprovalRequiredCode)
	}
	data, ok := rpcErr.Data.(*ApprovalRequired)
	if !ok {
		t.Fatalf("rpcErr.Data type = %T; want *ApprovalRequired (mapHandlerError must preserve the struct so JSON serialisation carries every field)", rpcErr.Data)
	}
	if data.ApprovalRef != "edge_appr_xyz" {
		t.Errorf("ApprovalRef = %q; want edge_appr_xyz", data.ApprovalRef)
	}
	if data.ApprovalID != "mcp_appr_abc" {
		t.Errorf("ApprovalID = %q; want mcp_appr_abc (legacy correlation handle)", data.ApprovalID)
	}
	if data.ArgsHash != "deadbeef" {
		t.Errorf("ArgsHash = %q; want deadbeef", data.ArgsHash)
	}
	if !data.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v; want %v (bounded-timeout client signal)", data.ExpiresAt, expires)
	}
	if data.RetryHint != "retry_with_approval_ref" {
		t.Errorf("RetryHint = %q; want retry_with_approval_ref", data.RetryHint)
	}
	if data.PolicySnapshot != "policy-v7" {
		t.Errorf("PolicySnapshot = %q; want policy-v7", data.PolicySnapshot)
	}
}

// TestJSONRPC_ApprovalRefResumeConsumesAndDispatches is the DoD #2/#4
// regression: a tools/call carrying `_approval_ref` in args MUST land on
// the Edge approval claim store BEFORE invoking the tool. On success
// the upstream tool runs exactly once and the consumed approval surfaces
// on the outcome. The handler MUST also see the stripped args (no
// `_approval_ref` field) so the tool never observes the server-reserved
// key.
func TestJSONRPC_ApprovalRefResumeConsumesAndDispatches(t *testing.T) {
	t.Parallel()
	approved := time.Date(2026, 5, 16, 20, 0, 0, 0, time.UTC)
	store := &fakeApprovalClaimStore{
		approval: &edge.EdgeApproval{
			ApprovalRef: "edge_appr_xyz",
			TenantID:    "tnt_a",
			Status:      edge.ApprovalStatusApproved,
			Decision:    edge.ApprovalDecisionApprove,
			ExpiresAt:   &approved,
		},
		consumed: true,
	}
	registry := NewToolRegistry()
	called := 0
	tool := Tool{Name: "fs.write"}
	if err := registry.Register(tool, func(_ context.Context, args json.RawMessage) (*ToolCallResult, error) {
		called++
		if strings.Contains(string(args), "_approval_ref") {
			t.Fatalf("tool handler saw `_approval_ref` in args; the consume path must strip it before dispatch: %s", args)
		}
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	}).WithApprovalHold(ApprovalHoldDeps{
		Store: store,
		PolicySnapshot: func(_ context.Context) string {
			return "policy-v7"
		},
		ServerName: "cordum.builtin",
	})
	if !srv.HasApprovalHold() {
		t.Fatal("HasApprovalHold() = false after WithApprovalHold wired; required for consume path to fire")
	}

	ctx := WithCallMetadata(context.Background(), CallMetadata{
		Tenant: "tnt_a", Principal: "alice", AgentID: "agent_alpha",
		SessionID: "sess_99", ExecutionID: "exec_88",
	})
	params, _ := json.Marshal(map[string]any{
		"name":      tool.Name,
		"arguments": map[string]any{"path": "/etc/hostname", "_approval_ref": "edge_appr_xyz"},
	})
	result, rpcErr := srv.handleToolsCall(ctx, params)
	if rpcErr != nil {
		t.Fatalf("handleToolsCall returned JSON-RPC err on valid resume: code=%d msg=%s data=%v",
			rpcErr.Code, rpcErr.Message, rpcErr.Data)
	}
	if result == nil || len(result.Content) == 0 || result.Content[0].Text != "ok" {
		t.Fatalf("resume did not reach handler; result=%+v", result)
	}
	if called != 1 {
		t.Errorf("tool handler called %d times; want exactly 1 (consume-once)", called)
	}
	if store.calls != 1 {
		t.Errorf("ClaimApproval called %d times; want exactly 1", store.calls)
	}
}

// TestJSONRPC_ApprovalRefResumeArgsMismatchSurfacesLifecycleError is the
// DoD #2 negative path: when the store returns
// ApprovalConflictKindArgsMismatch (canonical args differ between hold
// and resume), the server MUST surface JSON-RPC -32096 with
// error.data.kind = "args_mismatch", NOT execute the tool, and preserve
// the approval_ref so the client can render a precise remediation.
func TestJSONRPC_ApprovalRefResumeArgsMismatchSurfacesLifecycleError(t *testing.T) {
	t.Parallel()
	store := &fakeApprovalClaimStore{
		err: &edge.ApprovalConflictError{
			Kind:   edge.ApprovalConflictKindArgsMismatch,
			Reason: "canonical args hash differs",
		},
	}
	registry := NewToolRegistry()
	if err := registry.Register(Tool{Name: "fs.write"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("tool handler must NOT execute on args_mismatch resume")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	}).WithApprovalHold(ApprovalHoldDeps{
		Store: store,
		PolicySnapshot: func(_ context.Context) string {
			return "policy-v7"
		},
	})

	ctx := WithCallMetadata(context.Background(), CallMetadata{
		Tenant: "tnt_a", Principal: "alice", AgentID: "agent_alpha",
		SessionID: "sess_99", ExecutionID: "exec_88",
	})
	params, _ := json.Marshal(map[string]any{
		"name":      "fs.write",
		"arguments": map[string]any{"path": "/etc/hostname", "_approval_ref": "edge_appr_xyz"},
	})
	_, rpcErr := srv.handleToolsCall(ctx, params)
	if rpcErr == nil {
		t.Fatal("expected -32096 JSON-RPC error on args_mismatch resume; got nil")
	}
	if rpcErr.Code != jsonRPCApprovalLifecycleErrorCode {
		t.Errorf("rpcErr.Code = %d; want %d (approval_lifecycle_error)", rpcErr.Code, jsonRPCApprovalLifecycleErrorCode)
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("rpcErr.Data type = %T; want map[string]any with kind/approval_ref/reason", rpcErr.Data)
	}
	if got, _ := data["kind"].(string); got != string(edge.ApprovalConflictKindArgsMismatch) {
		t.Errorf("error.data.kind = %q; want args_mismatch", got)
	}
	if got, _ := data["approval_ref"].(string); got != "edge_appr_xyz" {
		t.Errorf("error.data.approval_ref = %q; want edge_appr_xyz", got)
	}
}

// TestJSONRPC_ApprovalStoreUnavailableWiresTo32096 is the EDGE-103
// reopen #2 DoD #5 wire-mapping regression. When the gateway gate
// returns `ErrApprovalStoreUnavailable` (Edge mint attempted +
// failed), MCPServer.handleToolsCall MUST surface JSON-RPC -32096
// with error.data.kind = "approval_store_unavailable", NOT the
// generic -32603 "internal error". Operators page on -32096 specifically
// for store outages.
func TestJSONRPC_ApprovalStoreUnavailableWiresTo32096(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	tool := Tool{Name: "fs.write", RequiresApproval: true}
	if err := registry.Register(tool, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("tool handler must NOT execute when gate returns approval_store_unavailable")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	registry.SetApprovalGate(approvalGateFunc(func(_ context.Context, _ Tool, _ json.RawMessage) (*ApprovalRequired, error) {
		return nil, fmt.Errorf("%w: edge store reachable but enqueue failed", ErrApprovalStoreUnavailable)
	}))
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	})

	params, _ := json.Marshal(map[string]any{
		"name":      tool.Name,
		"arguments": map[string]any{"path": "/etc/hostname"},
	})
	_, rpcErr := srv.handleToolsCall(context.Background(), params)
	if rpcErr == nil {
		t.Fatal("expected -32096 JSON-RPC error on approval_store_unavailable; got nil")
	}
	if rpcErr.Code != jsonRPCApprovalLifecycleErrorCode {
		t.Errorf("rpcErr.Code = %d; want %d (approval_lifecycle_error) — this is the DoD #5 wire-mapping regression. Was -32603 before sentinel + mapHandlerError case landed.", rpcErr.Code, jsonRPCApprovalLifecycleErrorCode)
	}
	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		t.Fatalf("rpcErr.Data type = %T; want map[string]any with kind=approval_store_unavailable", rpcErr.Data)
	}
	if got, _ := data["kind"].(string); got != "approval_store_unavailable" {
		t.Errorf("error.data.kind = %q; want approval_store_unavailable", got)
	}
}

// approvalGateFunc adapts a function literal into ApprovalGate for tests.
type approvalGateFunc func(ctx context.Context, tool Tool, paramsJSON json.RawMessage) (*ApprovalRequired, error)

func (f approvalGateFunc) Check(ctx context.Context, tool Tool, paramsJSON json.RawMessage) (*ApprovalRequired, error) {
	return f(ctx, tool, paramsJSON)
}

// TestJSONRPC_ApprovalRefResumeWithoutMetadataFailsClosed is the
// safety-floor regression: presenting an `_approval_ref` without
// mcp.CallMetadata in context MUST fail closed with -32097 (gateway
// misconfigured), not silently bypass the consume path. A middleware
// wiring bug that drops the metadata between auth and dispatch would
// otherwise let unauthenticated callers claim approvals.
func TestJSONRPC_ApprovalRefResumeWithoutMetadataFailsClosed(t *testing.T) {
	t.Parallel()
	store := &fakeApprovalClaimStore{consumed: true}
	registry := NewToolRegistry()
	if err := registry.Register(Tool{Name: "fs.write"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("tool handler must NOT execute without CallMetadata")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := NewServer(newChannelTransport(), registry, NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	}).WithApprovalHold(ApprovalHoldDeps{
		Store: store,
		PolicySnapshot: func(_ context.Context) string {
			return ""
		},
	})

	// Deliberately omit CallMetadata from ctx.
	params, _ := json.Marshal(map[string]any{
		"name":      "fs.write",
		"arguments": map[string]any{"path": "/etc/hostname", "_approval_ref": "edge_appr_xyz"},
	})
	_, rpcErr := srv.handleToolsCall(context.Background(), params)
	if rpcErr == nil {
		t.Fatal("expected -32097 JSON-RPC error on missing metadata; got nil")
	}
	if rpcErr.Code != jsonRPCGatewayMisconfiguredCode {
		t.Errorf("rpcErr.Code = %d; want %d (gateway_misconfigured)", rpcErr.Code, jsonRPCGatewayMisconfiguredCode)
	}
	if store.calls != 0 {
		t.Errorf("ClaimApproval called %d times; want 0 (fail-closed before store dispatch)", store.calls)
	}
	if !errors.Is(errMissingMCPMetadata, errMissingMCPMetadata) {
		t.Fatal("sentinel sanity")
	}
}
