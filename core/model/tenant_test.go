package model

import "testing"

// TestResolveTenantForAudit_AuthContextWins asserts the helper returns
// the authenticated tenant when present, even if a header is supplied.
// The auth layer is the source of truth; headers are a fallback for
// pre-auth or anonymous paths.
func TestResolveTenantForAudit_AuthContextWins(t *testing.T) {
	t.Parallel()
	got := ResolveTenantForAudit("tenant-a", "tenant-b")
	if got != "tenant-a" {
		t.Fatalf("got %q, want %q (authCtx should outrank header)", got, "tenant-a")
	}
}

// TestResolveTenantForAudit_FallsThroughToHeader asserts the X-Tenant-ID
// header is consulted when the auth context has no tenant — covers the
// anonymous-read / pre-auth path in auditReadMiddleware.
func TestResolveTenantForAudit_FallsThroughToHeader(t *testing.T) {
	t.Parallel()
	got := ResolveTenantForAudit("", "tenant-b")
	if got != "tenant-b" {
		t.Fatalf("got %q, want %q (header fallback)", got, "tenant-b")
	}
}

// TestResolveTenantForAudit_DefaultsWhenBothMissing asserts the helper
// never returns an empty string — the canonical default closes the
// audit-chain tenantless-event gap that motivated this task.
func TestResolveTenantForAudit_DefaultsWhenBothMissing(t *testing.T) {
	t.Parallel()
	got := ResolveTenantForAudit("", "")
	if got != DefaultTenant {
		t.Fatalf("got %q, want %q (DefaultTenant)", got, DefaultTenant)
	}
}

// TestResolveTenantForAudit_TrimsWhitespace asserts surrounding
// whitespace at either layer does not flow through as a "non-empty"
// tenant ID — `X-Tenant-ID: ` and `authCtx.Tenant = "  "` both fall
// through to the next layer.
func TestResolveTenantForAudit_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	got := ResolveTenantForAudit("  ", "  ")
	if got != DefaultTenant {
		t.Fatalf("got %q, want %q (whitespace-only should fall through)", got, DefaultTenant)
	}
	got = ResolveTenantForAudit("  ", "  tenant-b  ")
	if got != "tenant-b" {
		t.Fatalf("got %q, want %q (trimmed header should win)", got, "tenant-b")
	}
}
