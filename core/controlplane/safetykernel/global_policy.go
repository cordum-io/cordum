// Package safetykernel — global_policy.go defines the unified GlobalPolicy
// façade that all four evaluators (Cordum job, Edge action, MCP tool call,
// output scan) consult. The façade is a typed view over a single merged
// *config.SafetyPolicy plus a separately-authored Invariants security
// floor. It is read-only and intentionally backed by config.PolicyRule /
// config.OutputPolicyRule slices so the existing matchers continue to work
// unchanged.
package safetykernel

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// EdgeActionTopic is the synthetic Safety-Kernel topic used by Edge action
// rules. Centralised here so split predicates and tests share one source.
const EdgeActionTopic = "job.edge.action"

// GlobalPolicy is the typed cross-evaluator view of a merged safety
// policy. The four section slices project SafetyPolicy.Rules /
// SafetyPolicy.OutputRules into per-evaluator buckets, while Invariants
// and OutputInvariants hold the separately-authored security floor that
// applies to ALL four surfaces (DENY-uncrossable precedence enforced by
// the merger; see policybundles.MergeSafetyPolicies).
//
// The buckets are not exclusive in semantics — the kernel's existing
// rule iterator continues to operate on the unified policy.Rules slice —
// they are an authoring/inspection view used by the dashboard and the
// /api/v1/policy/global endpoint to expose the unified shape.
type GlobalPolicy struct {
	InputRules       []config.PolicyRule
	OutputRules      []config.OutputPolicyRule
	EdgeActionRules  []config.PolicyRule
	MCPToolRules     []config.PolicyRule
	Invariants       []config.PolicyRule
	OutputInvariants []config.OutputPolicyRule
	SnapshotVersion  string
	SnapshotHash     string
}

// FromSafetyPolicy projects a merged *config.SafetyPolicy plus a separately-
// authored Invariants overlay into the typed GlobalPolicy view. The merged
// policy is the output of policybundles.BuildPolicyFromBundles; the snapshot
// argument is the "cfg:<sha256>" string produced by that same call.
//
// invariants and outputInvariants are the rules sourced from the dedicated
// secops/invariants bundle. They are stored separately so the evaluator can
// enforce DENY-uncrossable precedence; see policybundles.MergeSafetyPolicies
// for the merger that bakes this precedence into the compiled policy.
//
// nil policy is permitted and yields an empty GlobalPolicy with the snapshot
// fields populated — this is the fail-closed shape the kernel uses when no
// policy is loaded.
func FromSafetyPolicy(
	policy *config.SafetyPolicy,
	invariants []config.PolicyRule,
	outputInvariants []config.OutputPolicyRule,
	snapshot string,
) *GlobalPolicy {
	g := &GlobalPolicy{
		SnapshotVersion: snapshot,
		SnapshotHash:    snapshot,
	}
	if len(invariants) > 0 {
		g.Invariants = append([]config.PolicyRule{}, invariants...)
	}
	if len(outputInvariants) > 0 {
		g.OutputInvariants = append([]config.OutputPolicyRule{}, outputInvariants...)
	}
	if policy == nil {
		return g
	}
	if len(policy.OutputRules) > 0 {
		g.OutputRules = append([]config.OutputPolicyRule{}, policy.OutputRules...)
	}
	for _, rule := range policy.Rules {
		switch {
		case isEdgeActionRule(rule):
			g.EdgeActionRules = append(g.EdgeActionRules, rule)
		case isMCPToolRule(rule):
			g.MCPToolRules = append(g.MCPToolRules, rule)
		default:
			g.InputRules = append(g.InputRules, rule)
		}
	}
	return g
}

// RulesForInput returns the rules a Cordum-job evaluator must consider:
// Invariants prepended, then the Input bucket. Invariants come first so a
// matcher that returns on first match honours DENY-uncrossable precedence.
func (g *GlobalPolicy) RulesForInput() []config.PolicyRule {
	if g == nil {
		return nil
	}
	return concatRules(g.Invariants, g.InputRules)
}

// RulesForEdgeAction returns the rules an Edge evaluator must consider:
// Invariants prepended, then the EdgeAction bucket.
func (g *GlobalPolicy) RulesForEdgeAction() []config.PolicyRule {
	if g == nil {
		return nil
	}
	return concatRules(g.Invariants, g.EdgeActionRules)
}

// RulesForMCPTool returns the rules an MCP-tool gate must consider:
// Invariants prepended, then the MCP bucket.
func (g *GlobalPolicy) RulesForMCPTool() []config.PolicyRule {
	if g == nil {
		return nil
	}
	return concatRules(g.Invariants, g.MCPToolRules)
}

// RulesForOutput returns the output-policy rules an output scanner must
// consider: OutputInvariants prepended, then the policy's OutputRules.
func (g *GlobalPolicy) RulesForOutput() []config.OutputPolicyRule {
	if g == nil {
		return nil
	}
	if len(g.OutputInvariants) == 0 {
		return append([]config.OutputPolicyRule{}, g.OutputRules...)
	}
	out := make([]config.OutputPolicyRule, 0, len(g.OutputInvariants)+len(g.OutputRules))
	out = append(out, g.OutputInvariants...)
	out = append(out, g.OutputRules...)
	return out
}

func concatRules(invariants, section []config.PolicyRule) []config.PolicyRule {
	if len(invariants) == 0 {
		return append([]config.PolicyRule{}, section...)
	}
	out := make([]config.PolicyRule, 0, len(invariants)+len(section))
	out = append(out, invariants...)
	out = append(out, section...)
	return out
}

// isEdgeActionRule reports whether a PolicyRule's match terms target the
// Edge action namespace. A rule is considered an Edge rule if any topic
// pattern in Match.Topics is exactly EdgeActionTopic — wildcard "job.*"
// patterns are treated as cross-cutting (Cordum jobs) and stay in the
// Input bucket so they do not collide with Edge-specific authoring.
func isEdgeActionRule(rule config.PolicyRule) bool {
	for _, raw := range rule.Match.Topics {
		if strings.TrimSpace(raw) == EdgeActionTopic {
			return true
		}
	}
	return false
}

// isMCPToolRule reports whether a PolicyRule has any MCP-shaped matcher.
// Any non-empty allow_*/deny_* field on Match.MCP marks the rule as an
// MCP-gate rule for the typed view. The kernel's matcher continues to
// evaluate MCP fields uniformly across all rules — the bucket is just
// for the GlobalPolicy authoring/inspection surface.
func isMCPToolRule(rule config.PolicyRule) bool {
	mcp := rule.Match.MCP
	return len(mcp.AllowServers) > 0 ||
		len(mcp.DenyServers) > 0 ||
		len(mcp.AllowTools) > 0 ||
		len(mcp.DenyTools) > 0 ||
		len(mcp.AllowResources) > 0 ||
		len(mcp.DenyResources) > 0 ||
		len(mcp.AllowActions) > 0 ||
		len(mcp.DenyActions) > 0
}
