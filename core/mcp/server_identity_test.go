package mcp

// No-identity tests (task-f6d83860). With the policy gate wired, a tools/call
// whose request carries no bound agent identity must answer -32098 with
// sub_reason no_identity, exactly as the gate-off registry scope filter does,
// instead of -32603 (ref-less) or -32097 (ref-bearing) missing_mcp_metadata.
// The check is fail-closed: it fires before the approval claim, so an
// unidentified caller never touches an approval, and it never fires when
// tenant, session or execution metadata is missing (those keep failing closed
// with their existing codes). Fakes are the rs* fixtures of
// policy_evaluate_resume_test.go.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// idArgs is a plain call; idRefArgs carries an `_approval_ref`, so
// ProcessApprovalClaim would try to claim it.
var (
	idArgs    = map[string]any{"path": "/data/rec-1", "record": "rec-1"}
	idRefArgs = map[string]any{"path": "/data/rec-1", "record": "rec-1", "_approval_ref": "edge_appr_id_1"}
)

// idCtx is a fully attributed call whose AgentID is blank, as the gateway
// builds it for an unbound or contradicted principal. mut edits it.
func idCtx(mut func(*CallMetadata)) context.Context {
	meta := CallMetadata{Tenant: rsTenant, Principal: "p1", SessionID: "sess_42", ExecutionID: "exec_99"}
	if mut != nil {
		mut(&meta)
	}
	return WithCallMetadata(context.Background(), meta)
}

// idFixture is a gate-on server with the approval hold and a claim store that
// counts every claim. Pass an auditor to record the invocation row.
type idFixture struct {
	rs    *rsFixture
	store *fakeApprovalClaimStore
	srv   *MCPServer
}

func newIDFixture(decision pb.DecisionType, auditor ToolInvocationAuditor) *idFixture {
	f := &idFixture{
		rs:    newRSFixture(decision, NewInProcessDedupeStore()),
		store: &fakeApprovalClaimStore{},
	}
	f.srv = NewServer(newChannelTransport(), NewToolRegistry(), NewResourceRegistry(), ServerConfig{
		Name: "test", Version: "0.0.0", ProtocolVersion: DefaultProtocolVersion,
	}).WithPolicyGate(rsServer, f.rs.deps).WithApprovalHold(ApprovalHoldDeps{
		Store: f.store, PolicySnapshot: func(context.Context) string { return "policy-v1" }, ServerName: rsServer,
	})
	if auditor != nil {
		f.srv.WithAuditor(auditor)
	}
	return f
}

func (f *idFixture) call(ctx context.Context, args map[string]any) (*ToolCallResult, *JSONRPCError) {
	params, _ := json.Marshal(map[string]any{"name": rsTool, "arguments": args})
	return f.srv.handleToolsCall(ctx, params)
}

// requireUntouched asserts nothing downstream of the identity check ran: no
// policy dispatch, no upstream execution, no approval mint and no claim.
func (f *idFixture) requireUntouched(t *testing.T) {
	t.Helper()
	if n := f.rs.pipeline.calls.Load(); n != 0 {
		t.Errorf("policy dispatches = %d, want 0", n)
	}
	f.rs.requireCounts(t, 0, 0)
	if f.store.calls != 0 {
		t.Errorf("approval claims = %d, want 0", f.store.calls)
	}
}

// requireNoIdentity asserts the -32098 no_identity denial for rsTool.
func requireNoIdentity(t *testing.T, res *ToolCallResult, rpcErr *JSONRPCError) {
	t.Helper()
	if res != nil || rpcErr == nil || rpcErr.Code != jsonRPCNotAuthorizedCode {
		t.Fatalf("tools/call = %+v, %+v; want code %d", res, rpcErr, jsonRPCNotAuthorizedCode)
	}
	denied, ok := rpcErr.Data.(*NotAuthorized)
	if !ok || denied.SubReason != DenyReasonNoIdentity || denied.Tool != rsTool || denied.AgentID != "" {
		t.Fatalf("error.data = %#v; want NotAuthorized{tool=%s, sub_reason=no_identity, agent_id empty}", rpcErr.Data, rsTool)
	}
}

func TestNoIdentity_PolicyGateRefless(t *testing.T) {
	t.Parallel()
	f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
	res, rpcErr := f.call(idCtx(nil), idArgs)
	requireNoIdentity(t, res, rpcErr)
	f.requireUntouched(t)
}

func TestNoIdentity_PolicyGateRefBearing(t *testing.T) {
	t.Parallel()
	f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
	res, rpcErr := f.call(idCtx(nil), idRefArgs)
	requireNoIdentity(t, res, rpcErr)
	f.requireUntouched(t)
}

func TestNoIdentity_WhitespaceAgentID(t *testing.T) {
	t.Parallel()
	for _, args := range []map[string]any{idArgs, idRefArgs} {
		f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
		res, rpcErr := f.call(idCtx(func(m *CallMetadata) { m.AgentID = " \t " }), args)
		requireNoIdentity(t, res, rpcErr)
		f.requireUntouched(t)
	}
}

// TestNoIdentity_BoundIdentityUnaffected: the check keys on the blank agent ID
// alone, so a bound caller still reaches the tool through the gate.
func TestNoIdentity_BoundIdentityUnaffected(t *testing.T) {
	t.Parallel()
	f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
	res, rpcErr := f.call(idCtx(func(m *CallMetadata) { m.AgentID = rsAgent }), idArgs)
	if rpcErr != nil || res != f.rs.upstream.result {
		t.Fatalf("bound tools/call = %+v, %+v; want the upstream result", res, rpcErr)
	}
	f.rs.requireCounts(t, 1, 0)
}

// TestNoIdentity_MissingTenantSessionExecutionStillFailsClosed: metadata gaps
// other than the agent ID keep today's codes, with or without a bound agent:
// -32603 for a plain call and -32097 for a ref-bearing one, never -32098.
func TestNoIdentity_MissingTenantSessionExecutionStillFailsClosed(t *testing.T) {
	t.Parallel()
	blank := map[string]func(*CallMetadata){
		"tenant":    func(m *CallMetadata) { m.Tenant = "" },
		"session":   func(m *CallMetadata) { m.SessionID = "" },
		"execution": func(m *CallMetadata) { m.ExecutionID = "" },
	}
	for field, mut := range blank {
		for _, agent := range []string{"", rsAgent} {
			for _, tc := range []struct {
				name string
				args map[string]any
				code int
			}{
				{"refless", idArgs, jsonRPCInternalErrorCode},
				{"ref_bearing", idRefArgs, jsonRPCGatewayMisconfiguredCode},
			} {
				t.Run(field+"/agent="+agent+"/"+tc.name, func(t *testing.T) {
					f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
					res, rpcErr := f.call(idCtx(func(m *CallMetadata) { mut(m); m.AgentID = agent }), tc.args)
					if res != nil || rpcErr == nil || rpcErr.Code != tc.code {
						t.Fatalf("tools/call = %+v, %+v; want code %d", res, rpcErr, tc.code)
					}
					f.requireUntouched(t)
				})
			}
		}
	}
	t.Run("no_call_metadata", func(t *testing.T) {
		for _, tc := range []struct {
			args map[string]any
			code int
		}{{idArgs, jsonRPCInternalErrorCode}, {idRefArgs, jsonRPCGatewayMisconfiguredCode}} {
			f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, nil)
			res, rpcErr := f.call(context.Background(), tc.args)
			if res != nil || rpcErr == nil || rpcErr.Code != tc.code {
				t.Fatalf("tools/call = %+v, %+v; want code %d", res, rpcErr, tc.code)
			}
			f.requireUntouched(t)
		}
	})
}

// TestNoIdentity_AuditRow: the denial lands on the invocation audit chain as
// decision=deny with sub_reason=no_identity, like a registry scope denial.
func TestNoIdentity_AuditRow(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	f := newIDFixture(pb.DecisionType_DECISION_TYPE_ALLOW, NewToolInvocationAuditor(sender, DefaultRedactor()))
	res, rpcErr := f.call(idCtx(nil), idRefArgs)
	requireNoIdentity(t, res, rpcErr)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.events) != 1 {
		t.Fatalf("audit events = %d, want exactly 1", len(sender.events))
	}
	ev := sender.events[0]
	if ev.EventType != audit.EventMCPToolInvocation || ev.Decision != "deny" || ev.TenantID != rsTenant {
		t.Errorf("event = type %q decision %q tenant %q; want %q deny %q", ev.EventType, ev.Decision, ev.TenantID, audit.EventMCPToolInvocation, rsTenant)
	}
	if ev.Extra["sub_reason"] != string(DenyReasonNoIdentity) || ev.Extra["tool_name"] != rsTool || ev.Extra["direction"] != "inbound" {
		t.Errorf("event extra = %v; want sub_reason=no_identity tool_name=%s direction=inbound", ev.Extra, rsTool)
	}
}

// TestNoIdentity_GateOffUnchanged: without the policy gate the registry scope
// filter keeps answering -32098 no_identity, and the tool never runs.
func TestNoIdentity_GateOffUnchanged(t *testing.T) {
	t.Parallel()
	reg := NewToolRegistry()
	ran := 0
	if err := reg.Register(Tool{Name: rsTool, RiskTier: "low"}, func(context.Context, json.RawMessage) (*ToolCallResult, error) {
		ran++
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.SetScopeEnforcement(true)
	srv := NewServer(newChannelTransport(), reg, NewResourceRegistry(), ServerConfig{})
	params, _ := json.Marshal(map[string]any{"name": rsTool, "arguments": idArgs})
	res, rpcErr := srv.handleToolsCall(idCtx(nil), params)
	requireNoIdentity(t, res, rpcErr)
	if ran != 0 {
		t.Errorf("tool executions = %d, want 0", ran)
	}
}
