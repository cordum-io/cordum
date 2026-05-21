package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/mcp"
)

// TestMCPDenyAuditorDefaultsTenantWhenLookupNil asserts the
// producer-side fix at mcp_deny_auditor.go:ToolDenied: when the
// auditor was constructed with a nil tenant-lookup (dev deploys, or
// production paths that route invariant denies before any tenant
// resolution), the emitted SIEMEvent MUST carry TenantID =
// model.DefaultTenant rather than empty. Mutation-resistant: asserts
// the literal "default" value, not just non-empty.
func TestMCPDenyAuditorDefaultsTenantWhenLookupNil(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	a := &mcpDenyAuditor{sender: cap /* tenant lookup intentionally nil */}

	a.ToolDenied(context.Background(), mcp.DenyEvent{
		ToolName:  "payments.send",
		AgentID:   "agent-1",
		SubReason: "scope_denied",
	})

	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].TenantID != "default" {
		t.Fatalf("TenantID = %q, want %q (nil tenant-lookup must default to model.DefaultTenant)",
			events[0].TenantID, "default")
	}
}

// TestMCPDenyAuditorPreservesResolvedTenant asserts that a non-nil
// tenant-lookup returning a real tenant is NOT overwritten by the
// default — confirms the resolver chain (resolved > default) order.
func TestMCPDenyAuditorPreservesResolvedTenant(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	a := &mcpDenyAuditor{
		sender: cap,
		tenant: func(_ context.Context) string { return "tenant-resolved" },
	}

	a.ToolDenied(context.Background(), mcp.DenyEvent{
		ToolName:  "fs.delete",
		AgentID:   "agent-1",
		SubReason: "scope_denied",
	})

	events := cap.snapshot()
	if events[0].TenantID != "tenant-resolved" {
		t.Fatalf("TenantID = %q, want %q (resolved tenant must be preserved)",
			events[0].TenantID, "tenant-resolved")
	}
}
