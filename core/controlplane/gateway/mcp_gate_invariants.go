package gateway

import (
	"context"
	"errors"
	"fmt"
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

// CheckInvariants enforces the SecOps invariant floor on a tools/call,
// approval-gated or not (mcp.InvariantFloor): ToolRegistry.Call runs it
// before approval gating, so a deny invariant also blocks tools that never
// reach Check. A call without MCPCallMetadata is matched with an empty
// tenant, so tenant-scoped invariants skip it and the rest still apply.
//
// ponytail: one configsvc read and invariants YAML parse per tools/call;
// cache by bundle revision if call volume makes that matter.
func (g *gatewayApprovalGate) CheckInvariants(ctx context.Context, tool mcp.Tool) error {
	if g == nil || g.invariants == nil {
		return nil
	}
	meta, _ := MCPCallMetadataFromContext(ctx)
	rule, denied := matchMCPInvariantDeny(g.invariants.InvariantsForMCPTool(ctx), tool, meta)
	if !denied {
		return nil
	}
	reason := strings.TrimSpace(rule.Reason)
	if reason == "" {
		reason = "denied by SecOps invariant"
	}
	return fmt.Errorf("%w: tool %q denied by invariant %q: %s",
		ErrMCPInvariantDeny, tool.Name, rule.ID, reason)
}

// matchMCPInvariantDeny iterates invariant rules looking for a DENY-class
// rule (any decision config.IsAllowClassDecision rejects) whose MCP
// matchers cover the candidate tool call. Returns the
// matching rule and true when a hard deny applies; (zero rule, false)
// otherwise. Only Match.Tenants and Match.MCP are consulted; see
// mcpMatchHits for the list semantics.
//
// Tenant scoping: when Match.Tenants is non-empty, the rule fires only
// for the matching tenant. Empty Match.Tenants means "all tenants".
func matchMCPInvariantDeny(rules []config.PolicyRule, tool mcp.Tool, meta MCPCallMetadata) (config.PolicyRule, bool) {
	for _, rule := range rules {
		if config.IsAllowClassDecision(rule.Decision) {
			continue
		}
		if !matchTenantScope(rule.Match.Tenants, meta.Tenant) {
			continue
		}
		if mcpMatchHits(rule, tool) {
			return rule, true
		}
	}
	return config.PolicyRule{}, false
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

// mcpMatchHits reports whether a rule's match.mcp lists select the tool
// call. It uses config.MCPRuleMatch, the matcher the Safety Kernel applies
// to MCP jobs, so a rule reads the same in both places: for a deny-class
// rule, a tool in deny_tools (or a server in deny_servers) is hit, and a
// non-empty allow list hits every value not in it. mcp.Tool has no Server
// field, so the server is tool.ApprovalScope; the gate knows no resource or
// action, so a non-empty allow_resources or allow_actions list hits every
// call, as it does for an MCP job without those labels. A rule with no MCP
// lists never hits here.
func mcpMatchHits(rule config.PolicyRule, tool mcp.Tool) bool {
	return rule.Match.MCP.HasLists() && config.MCPRuleMatch(rule.Match.MCP, rule.Decision,
		config.MCPRequest{Server: tool.ApprovalScope, Tool: tool.Name})
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
