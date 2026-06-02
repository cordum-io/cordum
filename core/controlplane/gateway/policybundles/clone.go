package policybundles

import (
	"github.com/cordum/cordum/core/infra/config"
)

// All clone + tier-metadata logic now lives in core/infra/config
// (policy_merge.go), shared with the safety kernel. These are thin delegators
// preserving this package's exported API. See task-48567e75 / task-0ba2bc0d.

func cloneWithTierMetadata(policy *config.SafetyPolicy) *config.SafetyPolicy {
	return config.CloneSafetyPolicyWithTierMetadata(policy)
}

// ClonePolicyTierDefaults deep-copies scoped default decision records.
func ClonePolicyTierDefaults(defaults []config.PolicyTierDefault) []config.PolicyTierDefault {
	return config.ClonePolicyTierDefaults(defaults)
}

// ClonePolicyRules deep-copies a slice of input/evaluation policy rules.
func ClonePolicyRules(rules []config.PolicyRule) []config.PolicyRule {
	return config.ClonePolicyRules(rules)
}

// ClonePolicyRule deep-copies a PolicyRule.
func ClonePolicyRule(rule config.PolicyRule) config.PolicyRule {
	return config.ClonePolicyRule(rule)
}
