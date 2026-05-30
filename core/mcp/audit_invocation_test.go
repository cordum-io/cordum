package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

type recordingSender struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (s *recordingSender) Send(e audit.SIEMEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *recordingSender) Close() error { return nil }

func (s *recordingSender) last() *audit.SIEMEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	last := s.events[len(s.events)-1]
	return &last
}

func TestToolInvocationAuditor_InboundSuccess(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "tenant-a", "jobs.submit", json.RawMessage(`{"topic":"job.echo","password":"s3cr3t"}`))
	a.FinishInbound(h, &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event emitted")
	}
	if ev.EventType != audit.EventMCPToolInvocation {
		t.Errorf("EventType = %q, want %q", ev.EventType, audit.EventMCPToolInvocation)
	}
	if ev.TenantID != "tenant-a" || ev.AgentID != "agent-1" {
		t.Errorf("identity not preserved: %+v", ev)
	}
	if ev.Extra["tool_name"] != "jobs.submit" {
		t.Errorf("tool_name missing: %+v", ev.Extra)
	}
	if ev.Extra["result_type"] != "ok" {
		t.Errorf("result_type = %q", ev.Extra["result_type"])
	}
	// Redaction: args_redacted must NOT contain the plaintext secret.
	if got := ev.Extra["args_redacted"]; got == "" || contains(got, "s3cr3t") {
		t.Errorf("args not redacted: %q", got)
	}
	if ev.Extra["direction"] != "inbound" {
		t.Errorf("direction = %q", ev.Extra["direction"])
	}
	if ev.Extra["latency_ms"] == "" {
		t.Errorf("latency_ms missing")
	}
}

// TestToolInvocationAuditor_DefaultsEmptyTenantInbound asserts the
// EDGE / EDGE-104 follow-up contract: a StartInbound call with empty
// tenantID must produce a SIEMEvent attributed to model.DefaultTenant
// rather than echoing an empty TenantID through to the audit chain
// (where the sink-level fallback would catch it but at slog.Warn).
// Mutation-resistant: asserts the exact "default" string, not just
// non-empty.
func TestToolInvocationAuditor_DefaultsEmptyTenantInbound(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "" /* empty tenant */, "jobs.submit", json.RawMessage(`{}`))
	a.FinishInbound(h, &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event emitted")
	}
	if ev.TenantID != "default" {
		t.Fatalf("ev.TenantID = %q; want %q (model.DefaultTenant — producer must default empty tenant)", ev.TenantID, "default")
	}
}

// TestToolInvocationAuditor_DefaultsEmptyTenantOutbound mirrors the
// inbound test for StartOutbound — both code paths feed into emit()
// which stamps TenantID from the handle, so both must default empty.
func TestToolInvocationAuditor_DefaultsEmptyTenantOutbound(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartOutbound(context.Background(), "agent-1", "" /* empty tenant */, "srv-1", "fetch", json.RawMessage(`{}`))
	a.FinishOutbound(h, &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event emitted")
	}
	if ev.TenantID != "default" {
		t.Fatalf("ev.TenantID = %q; want %q (model.DefaultTenant)", ev.TenantID, "default")
	}
}

func TestToolInvocationAuditor_InboundError(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "tenant-a", "jobs.submit", json.RawMessage(`{}`))
	a.FinishInbound(h, nil, errors.New("handler exploded"))

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q", ev.Extra["result_type"])
	}
	if ev.Extra["error_code"] == "" {
		t.Errorf("error_code missing")
	}
}

func TestToolInvocationAuditor_PolicyDeniedResultAuditsDeny(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "tenant-a", "all_monday_api", json.RawMessage(`{}`))
	h.MarkPolicyDenied("destructive action blocked", "session_tainted_prompt_injection", map[string]string{
		"taint_snippet": "SYSTEM OVERRIDE:",
	})
	a.FinishInbound(h, &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: "destructive action blocked"}},
		IsError: true,
	}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.Decision != "deny" {
		t.Fatalf("Decision = %q, want deny", ev.Decision)
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q, want error", ev.Extra["result_type"])
	}
	if ev.Extra["sub_reason"] != "session_tainted_prompt_injection" {
		t.Errorf("sub_reason = %q", ev.Extra["sub_reason"])
	}
	if ev.Extra["taint_snippet"] != "SYSTEM OVERRIDE:" {
		t.Errorf("taint_snippet = %q", ev.Extra["taint_snippet"])
	}
	// Pin the EVIDENCE, not just the decision label: emit() copies the
	// policy reason into Extra["error_code"] so a SIEM consumer can see
	// WHY the call was denied. A regression that drops the reason would
	// keep Decision=deny but strip the evidence — assert it explicitly.
	if ev.Extra["error_code"] != "destructive action blocked" {
		t.Errorf("error_code = %q, want %q (deny reason as evidence)", ev.Extra["error_code"], "destructive action blocked")
	}
}

func TestToolInvocationAuditor_IdentityMissing(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "", "tenant-a", "jobs.submit", nil)
	a.FinishInbound(h, &ToolCallResult{}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.AgentID != "unknown" {
		t.Errorf("AgentID = %q, want unknown", ev.AgentID)
	}
	if ev.Extra["identity_missing"] != "true" {
		t.Errorf("identity_missing marker absent")
	}
}

// TestToolInvocationAuditor_UpstreamIsErrorNotPolicyDeny is the DoD#3
// blast-radius guard: an ordinary upstream/bridge error (e.g. the
// BridgeError -> IsError payload at tools_mutating.go:489-505) returns an
// IsError result with NO Go error and NO MarkPolicyDenied, and MUST stay
// decision=allow with NO policy-deny evidence. Without this the fix could
// regress into mislabeling every upstream 500 as a policy DENY.
func TestToolInvocationAuditor_UpstreamIsErrorNotPolicyDeny(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "tenant-a", "all_monday_api", json.RawMessage(`{}`))
	a.FinishInbound(h, &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: `{"error":"upstream 500","code":"internal_error"}`}},
		IsError: true,
	}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.Decision != "allow" {
		t.Fatalf("Decision = %q, want allow (an ordinary upstream isError must NOT be a policy deny)", ev.Decision)
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q, want error", ev.Extra["result_type"])
	}
	if got, ok := ev.Extra["sub_reason"]; ok {
		t.Errorf("sub_reason must be absent on a non-policy error; got %q", got)
	}
	for _, k := range []string{"taint_snippet", "taint_source_tool", "taint_pattern"} {
		if got, ok := ev.Extra[k]; ok {
			t.Errorf("policy-evidence key %q must be absent on a non-policy error; got %q", k, got)
		}
	}
}

// TestToolInvocationAuditor_ApprovalRequiredStaysAllow locks the approval
// shape: an *ApprovalRequired (the tool body never ran, awaiting an
// operator) is NOT a policy DENY. A future change to emit() must not
// silently flip approval-required into decision=deny.
func TestToolInvocationAuditor_ApprovalRequiredStaysAllow(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartInbound(context.Background(), "agent-1", "tenant-a", "fs.write", json.RawMessage(`{}`))
	a.FinishInbound(h, nil, &ApprovalRequired{ApprovalID: "appr-1", Tool: "fs.write"})

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.Decision != "allow" {
		t.Fatalf("Decision = %q, want allow (approval-required must NOT be a deny)", ev.Decision)
	}
	if ev.Extra["result_type"] != "error" {
		t.Errorf("result_type = %q, want error", ev.Extra["result_type"])
	}
	if ev.Extra["approval_status"] != "required" {
		t.Errorf("approval_status = %q, want required", ev.Extra["approval_status"])
	}
}

func TestToolInvocationAuditor_Outbound(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	_, h := a.StartOutbound(context.Background(), "agent-1", "tenant-a", "srv.ai", "tools.search",
		json.RawMessage(`{"query":"hi"}`))
	a.FinishOutbound(h, &ToolCallResult{}, nil)

	ev := sender.last()
	if ev == nil {
		t.Fatal("no event")
	}
	if ev.EventType != audit.EventMCPToolOutboundInvocation {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.Extra["server_id"] != "srv.ai" {
		t.Errorf("server_id = %q", ev.Extra["server_id"])
	}
	if ev.Extra["direction"] != "outbound" {
		t.Errorf("direction = %q", ev.Extra["direction"])
	}
}

func TestToolInvocationAuditor_NilHandleSafe(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())

	a.FinishInbound(nil, nil, nil)
	a.FinishOutbound(nil, nil, nil)
	if len(sender.events) != 0 {
		t.Errorf("nil handle produced events: %+v", sender.events)
	}
}

// fakeOutbound records every Call invocation so the wrapper test can
// assert the inner client was actually used.
type fakeOutbound struct {
	called    int
	lastArgs  json.RawMessage
	returnErr error
}

func (f *fakeOutbound) Call(_ context.Context, _ string, _ string, args json.RawMessage) (*ToolCallResult, error) {
	f.called++
	f.lastArgs = args
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
}

func TestAuditedOutboundClient_WrapsCall(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())
	inner := &fakeOutbound{}
	wrapper := &AuditedOutboundClient{Inner: inner, Auditor: a}

	result, err := wrapper.Call(context.Background(), "srv.x", "tools.y", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("wrapper returned err: %v", err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("inner result not propagated: %+v", result)
	}
	if inner.called != 1 {
		t.Errorf("inner called %d times", inner.called)
	}
	ev := sender.last()
	if ev == nil || ev.EventType != audit.EventMCPToolOutboundInvocation {
		t.Errorf("outbound audit event missing: %+v", ev)
	}
}

func TestAuditedOutboundClient_PanicRecoversAndAudits(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, DefaultRedactor())
	inner := panickingOutbound{}
	wrapper := &AuditedOutboundClient{Inner: inner, Auditor: a}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to re-surface")
		}
		ev := sender.last()
		if ev == nil {
			t.Fatal("audit event should fire before re-panic")
		}
		if ev.Extra["result_type"] != "error" {
			t.Errorf("expected error result type, got %q", ev.Extra["result_type"])
		}
	}()
	_, _ = wrapper.Call(context.Background(), "srv.x", "tools.y", nil)
}

type panickingOutbound struct{}

func (panickingOutbound) Call(context.Context, string, string, json.RawMessage) (*ToolCallResult, error) {
	panic("inner exploded")
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
