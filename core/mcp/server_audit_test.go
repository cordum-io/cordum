package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

// TestServer_HandleToolsCall_EmitsInvocationAudit drives the full
// handleToolsCall path via the MCPServer with WithAuditor installed.
// Validates the DoD: every inbound tools/call produces an audit event
// with agent_id, tool_name, args_redacted (password masked), result
// summary, latency_ms, and approval_status.
func TestServer_HandleToolsCall_EmitsInvocationAudit(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "login"}, func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	identity := &AgentIdentity{
		ID:           "agent-alice",
		RiskTier:     "critical",
		AllowedTools: []string{"*"},
	}
	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`1`),
		Method:   MethodToolsCall,
		Params:   json.RawMessage(`{"name":"login","arguments":{"user":"alice","password":"s3cr3t"}}`),
		identity: identity,
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.EventType != audit.EventMCPToolInvocation {
		t.Errorf("EventType = %q, want %q", ev.EventType, audit.EventMCPToolInvocation)
	}
	if ev.AgentID != "agent-alice" {
		t.Errorf("agent_id = %q", ev.AgentID)
	}
	if ev.Extra["tool_name"] != "login" {
		t.Errorf("tool_name = %q", ev.Extra["tool_name"])
	}
	if ev.Extra["result_type"] != "ok" {
		t.Errorf("result_type = %q", ev.Extra["result_type"])
	}
	if ev.Extra["direction"] != "inbound" {
		t.Errorf("direction = %q", ev.Extra["direction"])
	}
	if ev.Extra["latency_ms"] == "" {
		t.Errorf("latency_ms missing")
	}
	if ev.Extra["approval_status"] == "" {
		t.Errorf("approval_status missing")
	}
	// The critical DoD check: sensitive arguments are redacted.
	if args := ev.Extra["args_redacted"]; args == "" {
		t.Errorf("args_redacted missing")
	} else if strings.Contains(args, "s3cr3t") {
		t.Errorf("password not redacted in args_redacted: %q", args)
	} else if !strings.Contains(args, "[REDACTED:") {
		t.Errorf("args_redacted lacks redaction marker: %q", args)
	}

	closeServer(t, transport, errCh)
}

// TestServer_HandleToolsCall_ErrorEmitsAudit asserts a handler-
// returned error still emits an audit event with result_type=error
// and an error_code populated.
func TestServer_HandleToolsCall_ErrorEmitsAudit(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "flaky"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return nil, errors.New("upstream flake")
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`2`),
		Method:   MethodToolsCall,
		Params:   json.RawMessage(`{"name":"flaky","arguments":{}}`),
		identity: &AgentIdentity{ID: "agent-x", RiskTier: "critical", AllowedTools: []string{"*"}},
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil {
		t.Fatalf("expected rpc error, got result %+v", resp.Result)
	}

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q", ev.Extra["result_type"])
	}
	if ev.Extra["error_code"] == "" {
		t.Errorf("error_code missing")
	}

	closeServer(t, transport, errCh)
}

// TestServer_HandleToolsCall_DenyAuditsWithDecisionDeny pins QA
// reopen fix #2: scope-denied calls must audit as decision=deny with
// a result_type=error + sub_reason so SIEM consumers can filter on
// the decision field. Previously emit() hard-coded decision="allow"
// and the reviewer could not distinguish a denied call from a
// successful one in the invocation event alone.
func TestServer_HandleToolsCall_DenyAuditsWithDecisionDeny(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "restricted", RiskTier: "critical"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.SetScopeEnforcement(true)

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	// Identity with a lower risk tier than the tool — EvaluateForIdentity
	// returns NotAuthorized with SubReason=risk_tier_too_low.
	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`10`),
		Method:   MethodToolsCall,
		Params:   json.RawMessage(`{"name":"restricted","arguments":{}}`),
		identity: &AgentIdentity{ID: "agent-low", RiskTier: "low", AllowedTools: []string{"*"}},
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil {
		t.Fatalf("expected deny rpc error, got %+v", resp.Result)
	}

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", ev.Decision)
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q, want error", ev.Extra["result_type"])
	}
	if ev.Extra["sub_reason"] == "" {
		t.Errorf("sub_reason missing on deny event")
	}
	if ev.Extra["approval_status"] != "none" {
		t.Errorf("approval_status = %q, want none", ev.Extra["approval_status"])
	}

	closeServer(t, transport, errCh)
}

// stubApprovalGate implements mcp.ApprovalGate with a scripted
// response so the test can exercise the approval-required and
// consumed paths without a Redis store.
type stubApprovalGate struct {
	required  *ApprovalRequired
	claimedID string
	calls     int
}

func (g *stubApprovalGate) Check(ctx context.Context, _ Tool, _ json.RawMessage) (*ApprovalRequired, error) {
	g.calls++
	if g.claimedID != "" {
		// Simulate the real gateway flow: write the consumed approval
		// onto the in-flight invocation handle so FinishInbound emits
		// approval_status=consumed + approval_id=<id>.
		if h := InvocationHandleFromContext(ctx); h != nil {
			h.MarkApprovalConsumed(g.claimedID)
		}
		return nil, nil
	}
	if g.required != nil {
		if h := InvocationHandleFromContext(ctx); h != nil {
			h.MarkApprovalRequired(g.required.ApprovalID)
		}
		return g.required, nil
	}
	return nil, nil
}

// TestServer_HandleToolsCall_ApprovalRequiredAudit pins QA reopen
// fix #2: approval-required calls audit with approval_status=required,
// approval_id populated, and result_type=error (the tool body did not
// run). Previously the status was misreported as "none".
func TestServer_HandleToolsCall_ApprovalRequiredAudit(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "gated", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatalf("handler should not run when approval required")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.SetApprovalGate(&stubApprovalGate{
		required: &ApprovalRequired{ApprovalID: "apr-42", Reason: "manual review", Tool: "gated"},
	})

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`11`),
		Method:   MethodToolsCall,
		Params:   json.RawMessage(`{"name":"gated","arguments":{}}`),
		identity: &AgentIdentity{ID: "agent-g", RiskTier: "critical", AllowedTools: []string{"*"}},
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error == nil {
		t.Fatalf("expected -32099 approval-required, got %+v", resp.Result)
	}

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.Extra["approval_status"] != "required" {
		t.Errorf("approval_status = %q, want required", ev.Extra["approval_status"])
	}
	if ev.Extra["approval_id"] != "apr-42" {
		t.Errorf("approval_id = %q, want apr-42", ev.Extra["approval_id"])
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q, want error", ev.Extra["result_type"])
	}

	closeServer(t, transport, errCh)
}

// TestServer_HandleToolsCall_ApprovalConsumedAudit pins QA reopen
// fix #3: a consumed pre-approval stamps approval_id +
// approval_status=consumed on the invocation event, enabling SIEM
// correlation with mcp.tool_approval(outcome=consume).
func TestServer_HandleToolsCall_ApprovalConsumedAudit(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "gated", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ran"}}}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.SetApprovalGate(&stubApprovalGate{claimedID: "apr-ok"})

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC:  JSONRPCVersion,
		ID:       json.RawMessage(`12`),
		Method:   MethodToolsCall,
		Params:   json.RawMessage(`{"name":"gated","arguments":{}}`),
		identity: &AgentIdentity{ID: "agent-g", RiskTier: "critical", AllowedTools: []string{"*"}},
	}
	resp := awaitResponse(t, transport.out)
	if resp.Error != nil {
		t.Fatalf("expected success, got %+v", resp.Error)
	}

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.Extra["approval_status"] != "consumed" {
		t.Errorf("approval_status = %q, want consumed", ev.Extra["approval_status"])
	}
	if ev.Extra["approval_id"] != "apr-ok" {
		t.Errorf("approval_id = %q, want apr-ok", ev.Extra["approval_id"])
	}
	if ev.Decision != "allow" {
		t.Errorf("Decision = %q, want allow", ev.Decision)
	}

	closeServer(t, transport, errCh)
}

// TestServer_HandleToolsCall_RequestCtxPropagation pins QA reopen
// fix #1: when the transport attaches a requestCtx to the message
// the dispatcher honours it instead of rebuilding from Background.
// Values installed upstream (tenant, MCPCallMetadata-ish keys) must
// reach the approval gate + invocation auditor.
func TestServer_HandleToolsCall_RequestCtxPropagation(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	var gateObservedTenant string
	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "echo"}, func(ctx context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		gateObservedTenant = TenantFromContext(ctx)
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC:    JSONRPCVersion,
		ID:         json.RawMessage(`13`),
		Method:     MethodToolsCall,
		Params:     json.RawMessage(`{"name":"echo","arguments":{}}`),
		identity:   &AgentIdentity{ID: "agent-a", RiskTier: "critical", AllowedTools: []string{"*"}},
		requestCtx: WithTenant(context.Background(), "tenant-42"),
	}
	_ = awaitResponse(t, transport.out)

	if gateObservedTenant != "tenant-42" {
		t.Errorf("handler ctx tenant = %q, want tenant-42", gateObservedTenant)
	}
	if len(sender.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(sender.events))
	}
	if got := sender.events[0].TenantID; got != "tenant-42" {
		t.Errorf("audit TenantID = %q, want tenant-42", got)
	}

	closeServer(t, transport, errCh)
}

// TestServer_HandleToolsCall_IdentityMissing confirms that a call
// without an identity still emits an audit event with agent_id=unknown
// and identity_missing=true — a non-breaking audit trail for legacy
// call paths flagged for cleanup.
func TestServer_HandleToolsCall_IdentityMissing(t *testing.T) {
	t.Parallel()

	sender := &recordingSender{}
	auditor := NewToolInvocationAuditor(sender, DefaultRedactor())

	reg := NewToolRegistry()
	if err := reg.Register(Tool{Name: "probe"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	transport := newChannelTransport()
	srv := NewServer(transport, reg, NewResourceRegistry(), ServerConfig{}).WithAuditor(auditor)
	errCh := startServer(t, srv, transport)

	transport.in <- &JSONRPCMessage{
		JSONRPC: JSONRPCVersion,
		ID:      json.RawMessage(`3`),
		Method:  MethodToolsCall,
		Params:  json.RawMessage(`{"name":"probe","arguments":{}}`),
		// No identity set — simulates a legacy transport path.
	}
	_ = awaitResponse(t, transport.out)

	if len(sender.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sender.events))
	}
	ev := sender.events[0]
	if ev.AgentID != "unknown" {
		t.Errorf("agent_id = %q, want unknown", ev.AgentID)
	}
	if ev.Extra["identity_missing"] != "true" {
		t.Errorf("identity_missing marker absent: %+v", ev.Extra)
	}

	closeServer(t, transport, errCh)
}
