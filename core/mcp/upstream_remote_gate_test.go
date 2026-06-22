package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests prove P3a's DoD end-to-end through the REAL policy gate
// (WithPolicyGate -> InvokeToolWithPolicy) fronting the net-new RemoteUpstream
// proxy, exactly as the gateway wires it: NewServer(.., remoteUpstream, ..)
// .WithPolicyGate("cordum.monday", deps).
//
// Deny-representation NOTE (DoD#2): the action-gate policy DENY (the content-aware
// Act-2 path) surfaces as a tools/call RESULT with isError:true and does NOT reach
// the upstream; JSON-RPC -32098 (jsonRPCNotAuthorizedCode) is the distinct
// scope/not-authorized code. Both are asserted below.

// gatedMondayServer builds a policy-gated MCPServer fronting a Monday-like remote
// upstream (httptest). decision is the canned gate verdict. Returns the server,
// the upstream emulator (assert reached/not-reached), and the emitter (DoD#3).
func gatedMondayServer(t *testing.T, decision PolicyDecision) (*MCPServer, *fakeUpstreamServer, *fakeEventEmitter) {
	t.Helper()
	f := &fakeUpstreamServer{framing: "sse", tools: mondayLikeTools()}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	up, err := NewRemoteUpstream(context.Background(), RemoteUpstreamConfig{
		Endpoint: srv.URL, AuthHeader: "tok", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewRemoteUpstream: %v", err)
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{decision: decision}, emitter, &fakeArtifactStore{})
	deps.Upstream = up
	server := NewServer(nil, up, nil, ServerConfig{Name: "cordum"}).WithPolicyGate("cordum.monday", deps)
	if !server.HasPolicyGate() || server.PolicyServerName() != "cordum.monday" {
		t.Fatalf("gate not wired under cordum.monday: hasGate=%v name=%q", server.HasPolicyGate(), server.PolicyServerName())
	}
	return server, f, emitter
}

func eventKinds(emitter *fakeEventEmitter) []edge.EventKind {
	kinds := make([]edge.EventKind, 0, len(emitter.events))
	for _, e := range emitter.events {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// DoD#1: agent tools/list returns the Monday catalog via the Cordum gate.
func TestGatedMondayUpstream_ToolsList_ProxiesCatalog(t *testing.T) {
	server, _, _ := gatedMondayServer(t, PolicyDecision{})
	res, rpcErr := server.handleToolsList(newAuthedToolCallCtx())
	if rpcErr != nil {
		t.Fatalf("tools/list rpc error: %+v", rpcErr)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"get_board_items_page", "all_monday_api"} {
		if !got[want] {
			t.Fatalf("tools/list missing Monday tool %q via gate; got %v", want, got)
		}
	}
}

// DoD#2 (ALLOW) + DoD#3: ALLOW forwards to Monday and emits pre+post.
func TestGatedMondayUpstream_ToolsCall_AllowForwardsAndAudits(t *testing.T) {
	server, f, emitter := gatedMondayServer(t, PolicyDecision{}) // zero decision => allow
	params, _ := json.Marshal(ToolCallParams{
		Name:      "get_board_items_page",
		Arguments: json.RawMessage(`{"board_id":"5097518101"}`),
	})
	res, rpcErr := server.handleToolsCall(newAuthedToolCallCtx(), params)
	if rpcErr != nil {
		t.Fatalf("ALLOW tools/call rpc error: %+v", rpcErr)
	}
	if res == nil || res.IsError {
		t.Fatalf("ALLOW should return a non-error upstream result, got %+v", res)
	}
	if f.toolCalls != 1 {
		t.Fatalf("upstream tools/call count = %d, want 1 (ALLOW must forward to Monday)", f.toolCalls)
	}
	if kinds := eventKinds(emitter); len(kinds) != 2 || kinds[0] != edge.EventKindMCPToolPre || kinds[1] != edge.EventKindMCPToolPost {
		t.Fatalf("ALLOW audit events = %v, want [pre, post]", kinds)
	}
	if emitter.events[1].Decision != edge.DecisionAllow {
		t.Fatalf("post event decision = %q, want allow", emitter.events[1].Decision)
	}
}

// DoD#2 (DENY) + DoD#3: action-gate DENY does NOT forward (board untouched) and
// is audited as a single failed event. This is the content-aware Act-2 deny shape
// (isError result), per the shipped contract (bridge_policy_test.go).
func TestGatedMondayUpstream_ToolsCall_DenyDoesNotForward(t *testing.T) {
	deny := PolicyDecision{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		Reason:    "session tainted: prompt injection in board content",
		SubReason: "session_prompt_injection",
	}
	server, f, emitter := gatedMondayServer(t, deny)
	params, _ := json.Marshal(ToolCallParams{
		Name:      "all_monday_api",
		Arguments: json.RawMessage(`{"query":"mutation{delete_items(item_ids:[1,2]){id}}"}`),
	})
	res, rpcErr := server.handleToolsCall(newAuthedToolCallCtx(), params)
	if rpcErr != nil {
		t.Fatalf("action-gate DENY should be an isError result, not a JSON-RPC error: %+v", rpcErr)
	}
	if res == nil || !res.IsError {
		t.Fatalf("DENY result must be IsError=true, got %+v", res)
	}
	if f.toolCalls != 0 {
		t.Fatalf("upstream tools/call count = %d, want 0 (DENY must NOT forward — Monday board untouched)", f.toolCalls)
	}
	if kinds := eventKinds(emitter); len(kinds) != 1 || kinds[0] != edge.EventKindMCPToolFailed {
		t.Fatalf("DENY audit events = %v, want [failed]", kinds)
	}
	if emitter.events[0].Decision != edge.DecisionDeny {
		t.Fatalf("failed event decision = %q, want deny", emitter.events[0].Decision)
	}
}

// DoD#2 (-32098 literal): a scope/not-authorized rejection surfaces as JSON-RPC
// jsonRPCNotAuthorizedCode (-32098) through the server, distinct from the
// action-gate content-aware DENY (isError result) asserted above.
func TestGatedUpstream_ToolsCall_NotAuthorizedReturns32098(t *testing.T) {
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(&fakePolicyDispatcher{}, emitter, &fakeArtifactStore{}) // gate allows
	upstream := &fakeUpstreamToolCaller{err: &NotAuthorized{Tool: "all_monday_api", SubReason: "risk_tier_too_low"}}
	deps.Upstream = upstream
	server := NewServer(nil, &fakeToolService{}, nil, ServerConfig{Name: "cordum"}).WithPolicyGate("cordum.monday", deps)
	params, _ := json.Marshal(ToolCallParams{Name: "all_monday_api", Arguments: json.RawMessage(`{}`)})
	_, rpcErr := server.handleToolsCall(newAuthedToolCallCtx(), params)
	if rpcErr == nil {
		t.Fatal("expected a JSON-RPC error for a not-authorized tool call")
	}
	if rpcErr.Code != jsonRPCNotAuthorizedCode {
		t.Fatalf("rpc error code = %d, want %d (jsonRPCNotAuthorizedCode)", rpcErr.Code, jsonRPCNotAuthorizedCode)
	}
}
