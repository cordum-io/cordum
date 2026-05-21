package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
)

// TestWireMCPApprovalGateAttachesInvariantLookupWithoutManualInjection is
// the EDGE-052 reopen #1 regression test. It builds the gate via the
// production wiring path (s.wireMCPApprovalGate), passes an invariant
// lookup as the helper's parameter, and verifies that:
//
//	(a) the helper attaches the lookup (gate.invariants != nil after
//	    construction — this was nil in production before the fix),
//	(b) an invariant DENY blocks a RequiresApproval=true tool call
//	    through the wired gate WITHOUT the test caller ever invoking
//	    gate.WithInvariantLookup directly.
//
// The pre-fix code at handlers_mcp.go:93-99 created the gate via
// NewGatewayApprovalGate, wired only gate.preapproval, and called
// SetApprovalGate — never WithInvariantLookup. That meant g.invariants
// stayed nil in production, matchMCPInvariantDeny returned no rules,
// and admin-authored secops/invariants MCP rules surfaced via
// RulesForMCPTool() and GET /api/v1/policy/global were silently ignored
// at the gate. This test pins the wiring contract so the bug can't
// regress without a test failure.
func TestWireMCPApprovalGateAttachesInvariantLookupWithoutManualInjection(t *testing.T) {
	t.Parallel()
	srv := &server{}
	store := newTestMCPStore(t)

	// Production wiring would pass a closure reading from
	// loadPolicyBundles + loadMCPInvariantsFromBundles. The test passes
	// a stub that returns a single invariant DENY for the synthetic
	// "secrets.read" tool. The point is NOT to exercise the bundle
	// parsing (covered by TestLoadMCPInvariantsFromBundles_*); it's to
	// prove the wiring helper attaches whatever lookup the caller
	// supplies — bug surface was the wiring NOT calling
	// WithInvariantLookup at all.
	invariantLookup := MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-secrets-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"secrets.read"}},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — secret reads forbidden",
			},
		}
	})

	rawGate, gate := srv.wireMCPApprovalGate(store, invariantLookup)
	if rawGate == nil {
		t.Fatal("wireMCPApprovalGate returned nil rawGate; production registry would lose the gate entirely")
	}
	if gate == nil {
		t.Fatal("wireMCPApprovalGate returned nil concrete gate; future-proof fallback fired unexpectedly with the standard NewGatewayApprovalGate path")
	}
	if gate.invariants == nil {
		t.Fatal("EDGE-052 reopen #1 regression: wireMCPApprovalGate did not attach invariant lookup; production gate stays permissive on secops/invariants MCP rules")
	}

	// Now exercise the deny path through the production-wired gate. The
	// test caller has NOT called gate.WithInvariantLookup directly —
	// only via wireMCPApprovalGate's parameter. If this assertion holds,
	// the production wiring + invariant consultation chain is intact.
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "secrets.read", RequiresApproval: true}
	got, err := gate.Check(ctx, tool, json.RawMessage(`{"key":"OPENAI_API_KEY"}`))
	if err == nil {
		t.Fatalf("expected ErrMCPInvariantDeny via wireMCPApprovalGate; got ApprovalRequired %+v (production wiring still failing)", got)
	}
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny via production wiring, got %v", err)
	}
	if got != nil {
		t.Fatalf("hard deny via invariant must NOT enqueue an approval; got %+v", got)
	}
}

// TestWireMCPApprovalGateNilInvariantLookupSafe confirms the helper
// degrades gracefully when the caller passes a nil lookup — the gate is
// still constructed and installable, but invariants stays nil and the
// approval flow alone applies. Useful for future configurations that
// genuinely want no invariant layer (e.g. a dev sandbox).
func TestWireMCPApprovalGateNilInvariantLookupSafe(t *testing.T) {
	t.Parallel()
	srv := &server{}
	store := newTestMCPStore(t)
	rawGate, gate := srv.wireMCPApprovalGate(store, nil)
	if rawGate == nil || gate == nil {
		t.Fatal("wireMCPApprovalGate returned nil for nil lookup; gate must still install with approval flow alone")
	}
	if gate.invariants != nil {
		t.Fatal("nil invariant lookup must leave gate.invariants nil; helper attached something unexpectedly")
	}
}

// TestInvariantDenyBeatsMCPAllow proves the SECURITY FLOOR rail:
// when an invariant DENY rule covers a tool, the MCP gate returns a hard
// deny (ErrMCPInvariantDeny) WITHOUT enqueuing an approval. This holds
// even if the tool's RequiresApproval=true would otherwise route through
// the approval store.
func TestInvariantDenyBeatsMCPAllow(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	// SecOps invariant: deny the "secrets.read" tool.
	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-secrets-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"secrets.read"}},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — secret reads forbidden",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "secrets.read", RequiresApproval: true}

	got, err := gate.Check(ctx, tool, json.RawMessage(`{"key":"OPENAI_API_KEY"}`))
	if err == nil {
		t.Fatalf("expected ErrMCPInvariantDeny; got ApprovalRequired %+v", got)
	}
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny, got %v", err)
	}
	if got != nil {
		t.Fatalf("hard deny must NOT enqueue an approval; got %+v", got)
	}
}

// TestInvariantNoMatchProceedsToApproval confirms the gate does not
// short-circuit when invariants do not cover the tool — the existing
// approval flow continues and an ApprovalRequired is enqueued.
func TestInvariantNoMatchProceedsToApproval(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	// Invariant denies a different tool — should not block our call.
	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-other",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"shell.exec"}},
				},
				Decision: "deny",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "files.delete", RequiresApproval: true}

	got, err := gate.Check(ctx, tool, json.RawMessage(`{"path":"/tmp/foo"}`))
	if err != nil {
		t.Fatalf("expected approval flow to proceed, got err %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired (not invariant-blocked); got nil")
	}
	if got.Tool != "files.delete" {
		t.Fatalf("expected tool=files.delete, got %q", got.Tool)
	}
}

// TestInvariantAllowToolsImplicitDeny proves the allowlist-inversion
// invariant pattern: SecOps authors `mcp.allow_tools: [calculator]`
// invariant rule with decision=deny, which means "if the tool is not in
// the allow list, block". Verifies that a tool not in AllowTools triggers
// the invariant.
func TestInvariantAllowToolsImplicitDeny(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-allowlist-only",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{AllowTools: []string{"calculator", "search"}},
				},
				Decision: "deny",
				Reason:   "only calculator and search are allowed in this tenant",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "agent-a"})

	// "files.delete" is NOT in the allow list — invariant must fire.
	_, err := gate.Check(ctx, mcp.Tool{Name: "files.delete", RequiresApproval: true},
		json.RawMessage(`{"path":"/"}`))
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny for non-allowlisted tool, got %v", err)
	}

	// "calculator" IS in the allow list — invariant must NOT fire and
	// the call proceeds to existing approval flow.
	got, err := gate.Check(ctx, mcp.Tool{Name: "calculator", RequiresApproval: true},
		json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("allowlisted tool should not be blocked, got %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired for allowlisted tool, got nil")
	}
}

// TestInvariantTenantScopedDeny verifies that an invariant scoped to a
// specific tenant only fires for that tenant — calls from other tenants
// proceed to the regular approval flow unhindered.
func TestInvariantTenantScopedDeny(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-tenant-restricted",
				Match: config.PolicyMatch{
					Tenants: []string{"sensitive-tenant"},
					MCP:     config.MCPPolicy{DenyTools: []string{"shell.exec"}},
				},
				Decision: "deny",
			},
		}
	}))

	tool := mcp.Tool{Name: "shell.exec", RequiresApproval: true}

	// Tenant in scope → invariant fires.
	ctxSensitive := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "sensitive-tenant", AgentID: "a"})
	if _, err := gate.Check(ctxSensitive, tool, json.RawMessage(`{}`)); !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected invariant deny for sensitive tenant, got %v", err)
	}

	// Different tenant → invariant skipped, approval flow proceeds.
	ctxOther := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "other-tenant", AgentID: "a"})
	got, err := gate.Check(ctxOther, tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("different tenant should not be blocked, got %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired for different tenant, got nil")
	}
}

// TestLoadMCPInvariantsFromBundles verifies the gateway-server-side
// helper that reads bundles via configsvc and projects the invariant
// rules — proves the wiring path the gateway uses to feed the gate's
// MCPInvariantLookup.
func TestLoadMCPInvariantsFromBundles(t *testing.T) {
	t.Parallel()
	bundles := map[string]any{
		policybundles.PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-deny-mcp-fs-read
    match:
      mcp:
        deny_tools: ["fs.read"]
    decision: deny
    reason: SecOps invariant — fs reads forbidden
`,
		},
	}
	rules, err := loadMCPInvariantsFromBundles(bundles)
	if err != nil {
		t.Fatalf("loadMCPInvariantsFromBundles: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 invariant rule, got %d (%+v)", len(rules), rules)
	}
	if rules[0].ID != "inv-deny-mcp-fs-read" {
		t.Fatalf("expected inv-deny-mcp-fs-read, got %q", rules[0].ID)
	}
	if !listContainsFold(rules[0].Match.MCP.DenyTools, "fs.read") {
		t.Fatalf("expected fs.read in DenyTools, got %+v", rules[0].Match.MCP.DenyTools)
	}
}
