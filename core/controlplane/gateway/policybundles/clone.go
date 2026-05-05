package policybundles

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

func cloneWithTierMetadata(policy *config.SafetyPolicy) *config.SafetyPolicy {
	out := CloneSafetyPolicy(policy)
	applyTierMetadata(out)
	return out
}

func applyTierMetadata(policy *config.SafetyPolicy) {
	if policy == nil {
		return
	}
	policy.Tier = config.NormalizePolicyTier(policy.Tier)
	policy.Selector = config.TrimPolicySelector(policy.Selector)
	moveScopedDefaultDecision(policy)
	normalizeTierDefaults(policy)
	for idx, rule := range policy.Rules {
		ruleTier := rule.Tier
		if strings.TrimSpace(ruleTier) == "" {
			ruleTier = policy.Tier
		}
		rule.Tier = config.NormalizePolicyTier(ruleTier)
		rule.Selector = config.MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == config.PolicyTierGlobal {
			rule.Selector = config.PolicySelector{}
		}
		policy.Rules[idx] = rule
	}
	for idx, rule := range policy.InputRules {
		ruleTier := rule.Tier
		if strings.TrimSpace(ruleTier) == "" {
			ruleTier = policy.Tier
		}
		rule.Tier = config.NormalizePolicyTier(ruleTier)
		rule.Selector = config.MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == config.PolicyTierGlobal {
			rule.Selector = config.PolicySelector{}
		}
		policy.InputRules[idx] = rule
	}
	if policy.Tier == config.PolicyTierGlobal {
		policy.Selector = config.PolicySelector{}
	}
}

func moveScopedDefaultDecision(policy *config.SafetyPolicy) {
	decision := strings.TrimSpace(policy.DefaultDecision)
	if decision == "" || policy.Tier == config.PolicyTierGlobal {
		policy.DefaultDecision = decision
		return
	}
	policy.TierDefaults = append(policy.TierDefaults, config.PolicyTierDefault{
		Tier:     policy.Tier,
		Selector: policy.Selector,
		Decision: decision,
	})
	policy.DefaultDecision = ""
}

func normalizeTierDefaults(policy *config.SafetyPolicy) {
	defaults := make([]config.PolicyTierDefault, 0, len(policy.TierDefaults))
	for _, def := range policy.TierDefaults {
		decision := strings.TrimSpace(def.Decision)
		tier := config.NormalizePolicyTier(def.Tier)
		if decision == "" || tier == config.PolicyTierGlobal {
			continue
		}
		defaults = append(defaults, config.PolicyTierDefault{
			Tier:     tier,
			Selector: config.TrimPolicySelector(def.Selector),
			Decision: decision,
		})
	}
	policy.TierDefaults = defaults
}

// ClonePolicyTierDefaults deep-copies scoped default decision records.
func ClonePolicyTierDefaults(defaults []config.PolicyTierDefault) []config.PolicyTierDefault {
	if len(defaults) == 0 {
		return nil
	}
	out := make([]config.PolicyTierDefault, 0, len(defaults))
	for _, def := range defaults {
		out = append(out, config.PolicyTierDefault{
			Tier:     strings.TrimSpace(def.Tier),
			Selector: config.TrimPolicySelector(def.Selector),
			Decision: strings.TrimSpace(def.Decision),
		})
	}
	return out
}

// ClonePolicyRules deep-copies a slice of input/evaluation policy rules.
func ClonePolicyRules(rules []config.PolicyRule) []config.PolicyRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]config.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ClonePolicyRule(rule))
	}
	return out
}

// ClonePolicyRule deep-copies a PolicyRule.
func ClonePolicyRule(rule config.PolicyRule) config.PolicyRule {
	cloned := rule
	cloned.ID = strings.TrimSpace(rule.ID)
	cloned.Tier = strings.TrimSpace(rule.Tier)
	cloned.Selector = config.TrimPolicySelector(rule.Selector)
	cloned.Decision = strings.TrimSpace(rule.Decision)
	cloned.Match = clonePolicyMatch(rule.Match)
	cloned.Constraints = clonePolicyConstraints(rule.Constraints)
	cloned.Remediations = clonePolicyRemediations(rule.Remediations)
	if rule.Velocity != nil {
		velocity := *rule.Velocity
		cloned.Velocity = &velocity
	}
	return cloned
}

func clonePolicyMatch(match config.PolicyMatch) config.PolicyMatch {
	cloned := config.PolicyMatch{
		Tenants:                  append([]string{}, match.Tenants...),
		Topics:                   append([]string{}, match.Topics...),
		Capabilities:             append([]string{}, match.Capabilities...),
		RiskTags:                 append([]string{}, match.RiskTags...),
		Requires:                 append([]string{}, match.Requires...),
		PackIDs:                  append([]string{}, match.PackIDs...),
		ActorIDs:                 append([]string{}, match.ActorIDs...),
		ActorTypes:               append([]string{}, match.ActorTypes...),
		AgentRiskTiers:           append([]string{}, match.AgentRiskTiers...),
		AgentDataClassifications: append([]string{}, match.AgentDataClassifications...),
		Labels:                   cloneStringMap(match.Labels),
		LabelAllowlist:           cloneStringSliceMap(match.LabelAllowlist),
		LabelThreshold:           cloneFloatMap(match.LabelThreshold),
		Predicate:                match.Predicate,
		MCP:                      cloneMCPPolicy(match.MCP),
	}
	if match.SecretsPresent != nil {
		secretsPresent := *match.SecretsPresent
		cloned.SecretsPresent = &secretsPresent
	}
	cloned.Delegation = cloneDelegationMatch(match.Delegation)
	return cloned
}

func clonePolicyConstraints(c config.PolicyConstraints) config.PolicyConstraints {
	return config.PolicyConstraints{
		Budgets: c.Budgets,
		Sandbox: config.SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: append([]string{}, c.Sandbox.NetworkAllowlist...),
			FsReadOnly:       append([]string{}, c.Sandbox.FsReadOnly...),
			FsReadWrite:      append([]string{}, c.Sandbox.FsReadWrite...),
		},
		Toolchain: config.ToolchainConstraints{
			AllowedTools:    append([]string{}, c.Toolchain.AllowedTools...),
			AllowedCommands: append([]string{}, c.Toolchain.AllowedCommands...),
		},
		Diff: config.DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: append([]string{}, c.Diff.DenyPathGlobs...),
		},
		RedactionLevel: c.RedactionLevel,
	}
}

func clonePolicyRemediations(remediations []config.PolicyRemediation) []config.PolicyRemediation {
	if len(remediations) == 0 {
		return nil
	}
	out := make([]config.PolicyRemediation, 0, len(remediations))
	for _, remediation := range remediations {
		cloned := remediation
		cloned.AddLabels = cloneStringMap(remediation.AddLabels)
		cloned.RemoveLabels = append([]string{}, remediation.RemoveLabels...)
		out = append(out, cloned)
	}
	return out
}

func cloneDelegationMatch(match *config.DelegationMatch) *config.DelegationMatch {
	if match == nil {
		return nil
	}
	cloned := *match
	cloned.Issuers = append([]string{}, match.Issuers...)
	cloned.RequiredScope = append([]string{}, match.RequiredScope...)
	if match.MaxDepth != nil {
		maxDepth := *match.MaxDepth
		cloned.MaxDepth = &maxDepth
	}
	if match.DelegationRequired != nil {
		required := *match.DelegationRequired
		cloned.DelegationRequired = &required
	}
	return &cloned
}
func cloneMCPPolicy(policy config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append([]string{}, policy.AllowServers...),
		DenyServers:    append([]string{}, policy.DenyServers...),
		AllowTools:     append([]string{}, policy.AllowTools...),
		DenyTools:      append([]string{}, policy.DenyTools...),
		AllowResources: append([]string{}, policy.AllowResources...),
		DenyResources:  append([]string{}, policy.DenyResources...),
		AllowActions:   append([]string{}, policy.AllowActions...),
		DenyActions:    append([]string{}, policy.DenyActions...),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = append([]string{}, value...)
	}
	return out
}

func cloneFloatMap(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
