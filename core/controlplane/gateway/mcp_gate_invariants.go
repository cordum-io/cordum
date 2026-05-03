package gateway

import (
	"context"
	"errors"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
)

// ErrMCPInvariantDeny signals that an MCP tool call was hard-denied by a
// SecOps invariant rule. Distinct from the approval-required path: when
// this fires, the gate does NOT enqueue an approval — the tool is simply
// not callable until the invariant is changed (which requires SecOps
// authorship of the secops/invariants bundle, not a pack install).
//
// Wraps mcp.ErrToolDisabled so the JSON-RPC layer maps it to "method
// not found" semantics — from the client's point of view, the tool is
// unavailable, the same shape it would see for a disabled tool. The
// rule.Reason and rule.ID land in the wrapped error message so audit
// consumers can identify which invariant fired.
var ErrMCPInvariantDeny = errors.New("mcp gate: denied by invariant")

// MCPInvariantLookup returns the invariant rules currently in effect for
// MCP tool-call evaluation. Implementations MUST be safe for concurrent
// use and MUST NOT block — gate.Check is on the request hot path. A nil
// or empty result signals "no invariants" and the gate proceeds to its
// normal approval flow.
type MCPInvariantLookup interface {
	InvariantsForMCPTool(ctx context.Context) []config.PolicyRule
}

// MCPInvariantLookupFunc is a function adapter for MCPInvariantLookup so
// callers (notably tests) can attach an inline closure without declaring
// a struct.
type MCPInvariantLookupFunc func(ctx context.Context) []config.PolicyRule

// InvariantsForMCPTool implements MCPInvariantLookup.
func (f MCPInvariantLookupFunc) InvariantsForMCPTool(ctx context.Context) []config.PolicyRule {
	if f == nil {
		return nil
	}
	return f(ctx)
}

// matchMCPInvariantDeny iterates invariant rules looking for a DENY-class
// rule whose MCP matchers cover the candidate tool call. Returns the
// matching rule and true when a hard deny applies; (zero rule, false)
// otherwise. The matcher considers (in order):
//
//   - Match.MCP.DenyTools containing tool.Name (most common case)
//   - Match.MCP.DenyServers containing tool.Server
//   - Match.MCP.DenyResources / DenyActions where the tool advertises one
//   - Match.MCP.AllowTools non-empty AND tool.Name not in it (implicit deny)
//
// Tenant scoping: when Match.Tenants is non-empty, the rule fires only
// for the matching tenant. Empty Match.Tenants means "all tenants".
func matchMCPInvariantDeny(rules []config.PolicyRule, tool mcp.Tool, meta MCPCallMetadata) (config.PolicyRule, bool) {
	for _, rule := range rules {
		if !isMCPInvariantDenyDecision(rule.Decision) {
			continue
		}
		if !matchTenantScope(rule.Match.Tenants, meta.Tenant) {
			continue
		}
		if mcpMatchHits(rule.Match.MCP, tool) {
			return rule, true
		}
	}
	return config.PolicyRule{}, false
}

// isMCPInvariantDenyDecision returns true for the decision strings that
// represent a DENY-class outcome (block + escalate). Mirrors
// policybundles.isInvariantDeny but lives gateway-side to avoid a
// cross-package call from the MCP gate hot path.
func isMCPInvariantDenyDecision(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "require_approval", "require-approval", "require_human", "throttle":
		return true
	default:
		return false
	}
}

// matchTenantScope reports whether the invariant rule applies to the
// candidate tenant. Empty Tenants list = applies to all tenants.
func matchTenantScope(tenants []string, candidate string) bool {
	if len(tenants) == 0 {
		return true
	}
	candidate = strings.TrimSpace(candidate)
	for _, t := range tenants {
		if strings.EqualFold(strings.TrimSpace(t), candidate) {
			return true
		}
	}
	return false
}

// mcpMatchHits reports whether an invariant rule's MCP matchers cover the
// candidate tool. The check is intentionally permissive about WHICH
// matcher fires — any DENY hit is a deny. Allow-list emptiness is treated
// as "no allow constraint", consistent with the existing config.MCPAllowed
// semantics elsewhere in the codebase. mcp.Tool has no Server field, so
// server-scope invariants must match by ApprovalScope or naming
// convention (e.g. tool names prefixed by server) rather than a discrete
// Server attribute. Tag-based matching is not in scope for v1.
func mcpMatchHits(mcpMatch config.MCPPolicy, tool mcp.Tool) bool {
	if listContainsFold(mcpMatch.DenyTools, tool.Name) {
		return true
	}
	if tool.ApprovalScope != "" && listContainsFold(mcpMatch.DenyServers, tool.ApprovalScope) {
		return true
	}
	// AllowTools non-empty and tool not in it = implicit deny by allowlist
	// inversion. This lets SecOps author "MCP allow_tools: [calculator,
	// search]" as an invariant that denies everything else.
	if len(mcpMatch.AllowTools) > 0 && !listContainsFold(mcpMatch.AllowTools, tool.Name) {
		return true
	}
	return false
}

// listContainsFold returns true when needle (case-insensitive) appears in
// the haystack. Whitespace around entries is trimmed.
func listContainsFold(haystack []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, item := range haystack {
		if strings.EqualFold(strings.TrimSpace(item), needle) {
			return true
		}
	}
	return false
}

// loadMCPInvariantsFromBundles is the gateway-server-side helper that
// reads the active policy bundles from configsvc and projects the
// invariant rules. Used by the gateway's MCPInvariantLookup
// implementation so the MCP gate sees the same security floor that the
// kernel applies to Cordum-job and Edge evaluations.
//
// Returns nil when the policy config doc is absent or the invariants
// bundle is unset — both signal "no invariants authored yet".
func loadMCPInvariantsFromBundles(bundles map[string]any) ([]config.PolicyRule, error) {
	rules, _, err := policybundles.SplitInvariantsFromBundles(bundles)
	return rules, err
}
