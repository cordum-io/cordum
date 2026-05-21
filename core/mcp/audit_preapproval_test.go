package mcp

import (
	"testing"

	"github.com/cordum/cordum/core/audit"
)

// MarkApprovalPreapproved must stamp approval_status="preapproved"
// on the invocation handle so the downstream SIEMEvent carries the
// scope-bypass distinction. This is the audit-side contract of
// step-5's scope-preapproval feature.

func TestInvocationHandle_MarkApprovalPreapproved_StampsStatus(t *testing.T) {
	t.Parallel()
	h := &InvocationHandle{}
	h.MarkApprovalPreapproved("cordum_install_pack")
	if got := approvalStatusForHandle(h); got != "preapproved" {
		t.Fatalf("approval_status = %q, want 'preapproved'", got)
	}
	// No approval_id because no record was persisted — the audit
	// event intentionally omits approval_id for scope bypasses.
	if h.approvalID != "" {
		t.Fatalf("preapproved path must NOT stamp an approval_id; got %q", h.approvalID)
	}
}

func TestInvocationHandle_MarkApprovalPreapproved_NilReceiver(t *testing.T) {
	t.Parallel()
	var h *InvocationHandle
	// Must not panic.
	h.MarkApprovalPreapproved("cordum_install_pack")
}

// Exhaustive status matrix — every Mark* method produces a distinct
// approval_status so SIEM alerting rules can distinguish all three
// flows without string-parsing.
func TestInvocationHandle_ApprovalStatusMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		mark  func(h *InvocationHandle)
		want  string
		hasID bool
	}{
		{"required", func(h *InvocationHandle) { h.MarkApprovalRequired("apr-1") }, "required", true},
		{"consumed", func(h *InvocationHandle) { h.MarkApprovalConsumed("apr-2") }, "consumed", true},
		{"preapproved", func(h *InvocationHandle) { h.MarkApprovalPreapproved("t") }, "preapproved", false},
		{"none", func(h *InvocationHandle) {}, "none", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &InvocationHandle{}
			tc.mark(h)
			if got := approvalStatusForHandle(h); got != tc.want {
				t.Errorf("status = %q, want %q", got, tc.want)
			}
			hasID := h.approvalID != ""
			if hasID != tc.hasID {
				t.Errorf("approval_id-present = %v, want %v", hasID, tc.hasID)
			}
		})
	}
}

// Sanity-check that the audit package's SIEMEvent type carries the
// fields the invocation auditor writes. This would have caught a
// stale dependency if audit.SIEMEvent ever dropped Extra.
func TestAuditSIEMEventShape_SupportsApprovalExtras(t *testing.T) {
	t.Parallel()
	ev := audit.SIEMEvent{
		EventType: "mcp.tool_invocation",
		Extra: map[string]string{
			"approval_status": "preapproved",
			"tool_name":       "cordum_install_pack",
		},
	}
	if ev.Extra["approval_status"] != "preapproved" {
		t.Fatalf("Extra assignment round-trip broken")
	}
}
