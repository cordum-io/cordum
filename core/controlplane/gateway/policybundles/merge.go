package policybundles

import (
	"github.com/cordum/cordum/core/infra/config"
)

// The policy-bundle merge + clone logic lives in core/infra/config
// (policy_merge.go), shared with the safety kernel so the gateway bundle
// compiler and the kernel loader can never re-diverge (task-48567e75; the two
// were aligned in task-0ba2bc0d). The functions below are thin delegators that
// preserve this package's exported API. OutputPolicy cross-fragment merge
// (any-enable + first-non-empty FailMode) is now the single canonical behavior
// shared with the kernel (task-198acafd) — no per-caller option.

// MergeSafetyPolicies merges fragment extra into base. See config.MergePolicies.
func MergeSafetyPolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	return config.MergePolicies(base, extra)
}

// CloneSafetyPolicy deep-copies a safety policy. See config.CloneSafetyPolicy.
func CloneSafetyPolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	return config.CloneSafetyPolicy(policy)
}

// CloneOutputPolicyRules deep-copies a slice of output policy rules.
func CloneOutputPolicyRules(rules []config.OutputPolicyRule) []config.OutputPolicyRule {
	return config.CloneOutputPolicyRules(rules)
}

// MergeTenantPolicies merges two tenant policy maps. See config.MergeTenantPolicies.
func MergeTenantPolicies(base map[string]config.TenantPolicy, extra map[string]config.TenantPolicy) map[string]config.TenantPolicy {
	return config.MergeTenantPolicies(base, extra)
}

// CloneTenantPolicy deep-copies a tenant policy. See config.CloneTenantPolicy.
func CloneTenantPolicy(policy config.TenantPolicy) config.TenantPolicy {
	return config.CloneTenantPolicy(policy)
}

// MergeMCPPolicy merges two MCP policies. See config.MergeMCPPolicy.
func MergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MergeMCPPolicy(base, extra)
}
