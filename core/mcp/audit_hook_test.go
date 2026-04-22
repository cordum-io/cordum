package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

type captureHook struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (c *captureHook) send(e audit.SIEMEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureHook) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func (c *captureHook) first() audit.SIEMEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return audit.SIEMEvent{}
	}
	return c.events[0]
}

func TestToolCallAudit_EmitsOnSuccess(t *testing.T) {
	reg := NewToolRegistry()
	cap := &captureHook{}
	reg.WithToolCallAudit(cap.send)

	// Register a trivial tool that succeeds with a small result.
	if err := reg.Register(Tool{Name: "echo"}, func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx := ContextWithIdentity(context.Background(), &AgentIdentity{ID: "agent-alpha"})
	ctx = WithTenant(ctx, "tenant-1")
	_, err := reg.Call(ctx, "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if cap.count() != 1 {
		t.Fatalf("want 1 event, got %d", cap.count())
	}
	ev := cap.first()
	if ev.EventType != EventMCPToolCalled {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.Extra["tool_name"] != "echo" {
		t.Errorf("tool_name = %q", ev.Extra["tool_name"])
	}
	if ev.Extra["agent_id"] != "agent-alpha" {
		t.Errorf("agent_id = %q", ev.Extra["agent_id"])
	}
	if ev.Extra["tenant"] != "tenant-1" {
		t.Errorf("tenant = %q", ev.Extra["tenant"])
	}
	if !strings.ContainsAny(ev.Extra["duration_ms"], "0123456789") {
		t.Errorf("duration_ms = %q", ev.Extra["duration_ms"])
	}
}

func TestToolCallAudit_NoHookNoPanic(t *testing.T) {
	reg := NewToolRegistry()
	// No hook attached — Call must succeed without panicking.
	if err := reg.Register(Tool{Name: "noop"}, func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := reg.Call(context.Background(), "noop", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
}

func TestToolCallAudit_SkippedOnError(t *testing.T) {
	reg := NewToolRegistry()
	cap := &captureHook{}
	reg.WithToolCallAudit(cap.send)
	if err := reg.Register(Tool{Name: "fail"}, func(ctx context.Context, params json.RawMessage) (*ToolCallResult, error) {
		return nil, errFakeFailure
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _ = reg.Call(context.Background(), "fail", json.RawMessage(`{}`))
	if cap.count() != 0 {
		t.Errorf("want 0 events on handler error, got %d", cap.count())
	}
}

var errFakeFailure = func() error {
	return &fakeErr{msg: "boom"}
}()

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
