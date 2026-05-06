package policybundles

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// ApplyInvariants overlays rules from the secops/invariants bundle on top
// of a merged base policy with security-floor precedence:
//
//   - Invariant DENY rules (decision deny / require_approval / require_human)
//     are prepended to merged.Rules so a first-match evaluator hits them
//     before any pack/studio ALLOW that targets the same topic+match.
//   - Invariant ALLOW rules are appended to merged.Rules as a default
//     fallback — any explicit DENY (pack, studio-global, file loader)
//     listed earlier wins. Without this, pack-contributed safety DENYs
//     authored before the invariant default would become unenforceable.
//   - Invariant OutputRules are prepended to merged.OutputRules with the
//     same intent — quarantine/redact rules cannot be silently overridden
//     by an allow-mode pack output rule.
//
// base may be nil; inv must be non-nil (callers in BuildPolicyFromBundles
// guard this). The returned policy is a clone — invariants are never
// mutated, callers are free to retain references to inv.
func ApplyInvariants(base, inv *config.SafetyPolicy) *config.SafetyPolicy {
	if inv == nil {
		if base == nil {
			return nil
		}
		return CloneSafetyPolicy(base)
	}
	out := CloneSafetyPolicy(base)
	if out == nil {
		out = &config.SafetyPolicy{}
	}

	invariantRules := ClonePolicyRules(inv.Rules)
	for idx := range invariantRules {
		invariantRules[idx].Tier = config.PolicyTierGlobal
		invariantRules[idx].Selector = config.PolicySelector{}
	}
	denies, allows := splitInvariantRulesByDecision(invariantRules)
	if len(denies) > 0 {
		// Prepend invariant DENY rules so first-match evaluators short-
		// circuit before any pack/studio ALLOW with overlapping match.
		prepended := make([]config.PolicyRule, 0, len(denies))
		prepended = append(prepended, denies...)
		prepended = append(prepended, out.Rules...)
		out.Rules = prepended
	}
	if len(allows) > 0 {
		// Append invariant ALLOW rules so any explicit DENY in the merged
		// body still wins. ALLOW invariants act as a default fallback,
		// not as an unconditional override.
		out.Rules = append(out.Rules, allows...)
	}
	if len(inv.OutputRules) > 0 {
		// Prepend invariant OutputRules — output scanners iterate in
		// order and return on first match, so quarantine/redact at the
		// front cannot be overridden by an allow-mode pack rule.
		invOutputs := CloneOutputPolicyRules(inv.OutputRules)
		merged := make([]config.OutputPolicyRule, 0, len(invOutputs))
		merged = append(merged, invOutputs...)
		merged = append(merged, out.OutputRules...)
		out.OutputRules = merged
	}
	if !out.OutputPolicy.Enabled && inv.OutputPolicy.Enabled {
		out.OutputPolicy.Enabled = true
	}
	if strings.TrimSpace(out.OutputPolicy.FailMode) == "" {
		out.OutputPolicy.FailMode = strings.TrimSpace(inv.OutputPolicy.FailMode)
	}
	return out
}

// splitInvariantRulesByDecision partitions invariant rules into two slices:
// "denies" (decisions that block or escalate execution) and "allows"
// (decisions that permit or constrain). The boundary matches the
// SafetyPolicy decision vocabulary — see config/safety_policy.go PolicyRule
// + safetykernel/input_policy.go for the canonical decision string list.
func splitInvariantRulesByDecision(rules []config.PolicyRule) (denies, allows []config.PolicyRule) {
	for _, r := range rules {
		if isInvariantDeny(r.Decision) {
			denies = append(denies, r)
		} else {
			allows = append(allows, r)
		}
	}
	return denies, allows
}

// isInvariantDeny reports whether a rule decision should be treated as a
// DENY-uncrossable decision for invariants precedence. The list mirrors
// the decisions that block or escalate execution in the kernel evaluator.
func isInvariantDeny(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "require_approval", "require-approval", "require_human", "throttle":
		return true
	default:
		return false
	}
}

// SplitInvariantsFromBundles extracts the parsed invariant rule slices
// from a bundle map. Returns (rules, outputRules, error). Returns
// (nil, nil, nil) when the invariants bundle is absent or empty — this
// is the common case before SecOps authors a security floor.
func SplitInvariantsFromBundles(bundles map[string]any) ([]config.PolicyRule, []config.OutputPolicyRule, error) {
	raw, ok := bundles[PolicyInvariantsBundleKey]
	if !ok {
		return nil, nil, nil
	}
	content, ok := PolicyBundleContent(raw)
	if !ok || strings.TrimSpace(content) == "" {
		return nil, nil, nil
	}
	sanitized := SanitizePolicyBundleYAML(content)
	policy, err := config.ParseSafetyPolicy([]byte(sanitized))
	if err != nil {
		return nil, nil, err
	}
	if policy == nil {
		return nil, nil, nil
	}
	policy = cloneWithTierMetadata(policy)
	rules := ClonePolicyRules(policy.Rules)
	outputRules := CloneOutputPolicyRules(policy.OutputRules)
	return rules, outputRules, nil
}
