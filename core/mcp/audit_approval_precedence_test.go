package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

// A consumed human approval recorded on an invocation handle outranks a
// later scope-preapproval mark (task-74f4c01e, ruling P1): the action-gate
// `_approval_ref` resume of a tool that is also RequiresApproval marks the
// handle consumed, then passes the registry gate's preapproval bypass.

func TestInvocationHandle_ConsumedOutranksPreapproved(t *testing.T) {
	t.Parallel()
	startWithContextID := func() *InvocationHandle {
		ctx := WithApprovalID(context.Background(), "appr_ctx_1")
		_, h := NewToolInvocationAuditor(&recordingSender{}, nil).StartInbound(ctx, "bot-1", "tenant-a", "dual_delete", nil)
		return h
	}
	cases := []struct {
		name       string
		handle     func() *InvocationHandle
		mark       func(h *InvocationHandle)
		wantStatus string
		wantID     string
	}{
		{"consumed_then_preapproved", nil, func(h *InvocationHandle) {
			h.MarkApprovalConsumed("edge_appr_1")
			h.MarkApprovalPreapproved("dual_delete")
		}, "consumed", "edge_appr_1"},
		{"context_id_then_preapproved", startWithContextID, func(h *InvocationHandle) {
			h.MarkApprovalPreapproved("dual_delete")
		}, "consumed", "appr_ctx_1"},
		{"preapproved_then_consumed", nil, func(h *InvocationHandle) {
			h.MarkApprovalPreapproved("dual_delete")
			h.MarkApprovalConsumed("edge_appr_1")
		}, "consumed", "edge_appr_1"},
		{"fresh_preapproved", nil, func(h *InvocationHandle) {
			h.MarkApprovalPreapproved("dual_delete")
		}, "preapproved", ""},
		{"consumed_without_id_then_preapproved", nil, func(h *InvocationHandle) {
			h.MarkApprovalConsumed("")
			h.MarkApprovalPreapproved("dual_delete")
		}, "preapproved", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := &InvocationHandle{}
			if tc.handle != nil {
				h = tc.handle()
			}
			tc.mark(h)
			if got := approvalStatusForHandle(h); got != tc.wantStatus {
				t.Errorf("approval_status = %q, want %q", got, tc.wantStatus)
			}
			if h.approvalID != tc.wantID {
				t.Errorf("approval_id = %q, want %q", h.approvalID, tc.wantID)
			}
		})
	}
}

func TestToolInvocationAuditor_ConsumedResumeThroughPreapprovalEmitsConsumed(t *testing.T) {
	t.Parallel()
	sender := &recordingSender{}
	a := NewToolInvocationAuditor(sender, nil)
	_, h := a.StartInbound(context.Background(), "bot-1", "tenant-a", "dual_delete", json.RawMessage(`{"id":"1"}`))
	h.MarkApprovalConsumed("edge_appr_9")
	h.MarkApprovalPreapproved("dual_delete")
	a.FinishInbound(h, &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "deleted 1"}}}, nil)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sender.events))
	}
	ev := sender.events[0]
	if ev.EventType != audit.EventMCPToolInvocation || ev.Decision != "allow" {
		t.Errorf("event type %q decision %q, want %q allow", ev.EventType, ev.Decision, audit.EventMCPToolInvocation)
	}
	want := map[string]string{"approval_status": "consumed", "approval_id": "edge_appr_9", "result_type": "ok"}
	for k, v := range want {
		if got, ok := ev.Extra[k]; !ok || got != v {
			t.Errorf("Extra[%s] = %q (present %t), want %q", k, got, ok, v)
		}
	}
}
