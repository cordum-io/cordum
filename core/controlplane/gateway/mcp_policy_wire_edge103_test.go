package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/mcp"
)

// captureEdgeStore records the EnqueueApproval request and returns a
// canned response. Tests inspect lastReq to assert the EdgeApproval the
// production code BUILT, regardless of how the underlying store stores
// it. This sidesteps the miniredis parent-validation requirement for
// pure unit assertions on the mint-side tuple.
type captureEdgeStore struct {
	edge.Store
	lastReq    edge.EdgeApprovalRequest
	enqueueErr error
	approvalID string
}

func (c *captureEdgeStore) EnqueueApproval(_ context.Context, req edge.EdgeApprovalRequest) (*edge.EdgeApproval, error) {
	c.lastReq = req
	if c.enqueueErr != nil {
		return nil, c.enqueueErr
	}
	ref := c.approvalID
	if ref == "" {
		ref = "edge_appr_capture"
	}
	return &edge.EdgeApproval{
		ApprovalRef:    ref,
		TenantID:       req.TenantID,
		SessionID:      req.SessionID,
		ExecutionID:    req.ExecutionID,
		EventID:        req.EventID,
		PrincipalID:    req.PrincipalID,
		Requester:      req.Requester,
		Status:         edge.ApprovalStatusPending,
		Reason:         req.Reason,
		ActionHash:     req.ActionHash,
		InputHash:      req.InputHash,
		PolicySnapshot: req.PolicySnapshot,
	}, nil
}

// TestMintEdgeApproval_InputHashMatchesConsumeBinding is the EDGE-103
// reopen #1 core-bug regression. mintEdgeApprovalForActionGate (mint
// side, mcp_policy_wire.go) must store an InputHash byte-identical to
// what BuildMCPApprovalBinding (consume side, approval_hold.go) produces
// for the same canonical args. Anything else lands as
// `ApprovalConflictKindArgsMismatch` on every legitimate retry.
//
// The bug: mint side stores InputHash = ctxData.ActionHash (the
// canonical action-tuple SHA-256). Consume side computes InputHash =
// SHA-256(CanonicaliseArgs(stripped)). These never match.
//
// The fix: plumb raw args through ToolCallApprovalContext.Args; mint
// side calls BuildMCPApprovalBinding too.
func TestMintEdgeApproval_InputHashMatchesConsumeBinding(t *testing.T) {
	t.Parallel()
	const (
		tenant       = "tnt_a"
		server       = "cordum.builtin"
		tool         = "fs.write"
		policySnap   = "policy-v7"
		sessionID    = "sess_99"
		executionID  = "exec_88"
		agentID      = "agent_alpha"
		principalID  = "alice"
		actionHashIn = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	)
	args := json.RawMessage(`{"path":"/etc/hostname","contents":"hi"}`)

	store := &captureEdgeStore{approvalID: "edge_appr_capture_1"}
	gate := &gatewayApprovalGate{
		store:          &MCPApprovalStore{},
		edgeStore:      store,
		policySnapshot: func(context.Context) string { return policySnap },
		serverName:     server,
	}

	ctx := mcp.WithCallMetadata(context.Background(), mcp.CallMetadata{
		Tenant:      tenant,
		Principal:   principalID,
		AgentID:     agentID,
		SessionID:   sessionID,
		ExecutionID: executionID,
	})

	ref, err := gate.ConsumeActionGateDecision(ctx, mcp.PolicyDecision{}, mcp.ToolCallApprovalContext{
		Tenant:     tenant,
		AgentID:    agentID,
		Server:     server,
		Tool:       tool,
		ActionHash: actionHashIn,
		Args:       args,
	})
	if err != nil {
		t.Fatalf("ConsumeActionGateDecision: %v", err)
	}
	if !strings.HasPrefix(ref, "edge_appr_") {
		t.Fatalf("ref = %q; want Edge-prefixed handle (mint must route through edgeStore)", ref)
	}

	wantAction, wantInput := mcp.BuildMCPApprovalBinding(tenant, server,
		mcp.ToolCallParams{Name: tool, Arguments: args}, policySnap)
	if store.lastReq.ActionHash != wantAction {
		t.Errorf("EnqueueApproval ActionHash = %q; want %q (mint must call BuildMCPApprovalBinding)",
			store.lastReq.ActionHash, wantAction)
	}
	if store.lastReq.InputHash != wantInput {
		t.Errorf("EnqueueApproval InputHash = %q; want %q — this is the EDGE-103 reopen #1 core bug (mint InputHash diverges from consume-side BuildMCPApprovalBinding InputHash)",
			store.lastReq.InputHash, wantInput)
	}
	if store.lastReq.PolicySnapshot != policySnap {
		t.Errorf("PolicySnapshot = %q; want %q", store.lastReq.PolicySnapshot, policySnap)
	}
	if store.lastReq.Requester != principalID {
		t.Errorf("Requester = %q; want %q (self-approval defense binds to Principal)", store.lastReq.Requester, principalID)
	}
}

// TestMintEdgeApproval_FailsClosedOnEdgeStoreError is the DoD #5
// regression: when edgeStore is wired and EnqueueApproval fails, the
// gate MUST surface an error (mapped by handleToolsCall to -32096 with
// kind=approval_store_unavailable), NOT silently fall back to the
// legacy MCPApprovalStore and return a non-resumable approval_id.
func TestMintEdgeApproval_FailsClosedOnEdgeStoreError(t *testing.T) {
	t.Parallel()
	store := &captureEdgeStore{enqueueErr: errors.New("edge unavailable")}
	gate := &gatewayApprovalGate{
		store:          &MCPApprovalStore{},
		edgeStore:      store,
		policySnapshot: func(context.Context) string { return "policy-v7" },
		serverName:     "cordum.builtin",
	}
	ctx := mcp.WithCallMetadata(context.Background(), mcp.CallMetadata{
		Tenant:      "tnt_a",
		Principal:   "alice",
		AgentID:     "agent_alpha",
		SessionID:   "sess_99",
		ExecutionID: "exec_88",
	})

	_, err := gate.ConsumeActionGateDecision(ctx, mcp.PolicyDecision{},
		mcp.ToolCallApprovalContext{
			Tenant:     "tnt_a",
			AgentID:    "agent_alpha",
			Server:     "cordum.builtin",
			Tool:       "fs.write",
			ActionHash: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			Args:       json.RawMessage(`{"path":"/x"}`),
		})
	if err == nil {
		t.Fatal("expected fail-closed error on Edge store outage; got nil (silent fallback to legacy is the DoD #5 violation)")
	}
	if !strings.Contains(err.Error(), "approval_store_unavailable") {
		t.Errorf("error = %q; want substring 'approval_store_unavailable' so handleToolsCall maps to -32096 kind", err.Error())
	}
}
