package policybundles

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// MergeSafetyPolicies merges two safety policies into one.
func MergeSafetyPolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	if base == nil {
		return CloneSafetyPolicy(extra)
	}
	if extra == nil {
		return CloneSafetyPolicy(base)
	}
	out := CloneSafetyPolicy(base)
	if out.Version == "" {
		out.Version = extra.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = extra.DefaultTenant
	}
	if strings.TrimSpace(out.DefaultDecision) == "" {
		out.DefaultDecision = strings.TrimSpace(extra.DefaultDecision)
	}
	if !out.OutputPolicy.Enabled && extra.OutputPolicy.Enabled {
		out.OutputPolicy.Enabled = true
	}
	if strings.TrimSpace(out.OutputPolicy.FailMode) == "" {
		out.OutputPolicy.FailMode = strings.TrimSpace(extra.OutputPolicy.FailMode)
	}
	out.Rules = append(out.Rules, extra.Rules...)
	out.OutputRules = append(out.OutputRules, CloneOutputPolicyRules(extra.OutputRules)...)
	out.Tenants = MergeTenantPolicies(out.Tenants, extra.Tenants)
	return out
}

// CloneSafetyPolicy deep-copies a safety policy.
func CloneSafetyPolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &config.SafetyPolicy{
		Version:         policy.Version,
		DefaultTenant:   policy.DefaultTenant,
		DefaultDecision: policy.DefaultDecision,
		Rules:           append([]config.PolicyRule{}, policy.Rules...),
		OutputPolicy:    policy.OutputPolicy,
		OutputRules:     CloneOutputPolicyRules(policy.OutputRules),
		Tenants:         map[string]config.TenantPolicy{},
	}
	if policy.Tenants != nil {
		for k, v := range policy.Tenants {
			out.Tenants[k] = CloneTenantPolicy(v)
		}
	}
	return out
}

// CloneOutputPolicyRules deep-copies a slice of output policy rules.
func CloneOutputPolicyRules(rules []config.OutputPolicyRule) []config.OutputPolicyRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]config.OutputPolicyRule, 0, len(rules))
	for _, rule := range rules {
		cloned := config.OutputPolicyRule{
			ID:       strings.TrimSpace(rule.ID),
			Severity: strings.TrimSpace(rule.Severity),
			Desc:     rule.Desc,
			Decision: strings.TrimSpace(rule.Decision),
			Reason:   rule.Reason,
			Match: config.OutputPolicyMatch{
				Tenants:         append([]string{}, rule.Match.Tenants...),
				Topics:          append([]string{}, rule.Match.Topics...),
				Capabilities:    append([]string{}, rule.Match.Capabilities...),
				RiskTags:        append([]string{}, rule.Match.RiskTags...),
				Scanners:        append([]string{}, rule.Match.Scanners...),
				ContentPatterns: append([]string{}, rule.Match.ContentPatterns...),
				Keywords:        append([]string{}, rule.Match.Keywords...),
				ContentTypes:    append([]string{}, rule.Match.ContentTypes...),
				Detectors:       append([]string{}, rule.Match.Detectors...),
				OutputSizeGt:    rule.Match.OutputSizeGt,
				MaxOutputBytes:  rule.Match.MaxOutputBytes,
			},
		}
		if rule.Enabled != nil {
			enabled := *rule.Enabled
			cloned.Enabled = &enabled
		}
		if rule.Match.HasError != nil {
			hasError := *rule.Match.HasError
			cloned.Match.HasError = &hasError
		}
		out = append(out, cloned)
	}
	return out
}

// MergeTenantPolicies merges two tenant policy maps.
func MergeTenantPolicies(base map[string]config.TenantPolicy, extra map[string]config.TenantPolicy) map[string]config.TenantPolicy {
	out := map[string]config.TenantPolicy{}
	for k, v := range base {
		out[k] = CloneTenantPolicy(v)
	}
	for tenant, add := range extra {
		current, ok := out[tenant]
		if !ok {
			out[tenant] = CloneTenantPolicy(add)
			continue
		}
		merged := current
		merged.AllowTopics = append(merged.AllowTopics, add.AllowTopics...)
		merged.DenyTopics = append(merged.DenyTopics, add.DenyTopics...)
		merged.AllowedRepoHosts = append(merged.AllowedRepoHosts, add.AllowedRepoHosts...)
		merged.DeniedRepoHosts = append(merged.DeniedRepoHosts, add.DeniedRepoHosts...)
		if add.MaxConcurrent > 0 && (merged.MaxConcurrent == 0 || add.MaxConcurrent < merged.MaxConcurrent) {
			merged.MaxConcurrent = add.MaxConcurrent
		}
		merged.MCP = MergeMCPPolicy(merged.MCP, add.MCP)
		out[tenant] = merged
	}
	return out
}

// CloneTenantPolicy deep-copies a tenant policy.
func CloneTenantPolicy(policy config.TenantPolicy) config.TenantPolicy {
	return config.TenantPolicy{
		AllowTopics:      append([]string{}, policy.AllowTopics...),
		DenyTopics:       append([]string{}, policy.DenyTopics...),
		AllowedRepoHosts: append([]string{}, policy.AllowedRepoHosts...),
		DeniedRepoHosts:  append([]string{}, policy.DeniedRepoHosts...),
		MaxConcurrent:    policy.MaxConcurrent,
		MCP:              policy.MCP,
	}
}

// MergeMCPPolicy merges two MCP policies.
func MergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append(base.AllowServers, extra.AllowServers...),
		DenyServers:    append(base.DenyServers, extra.DenyServers...),
		AllowTools:     append(base.AllowTools, extra.AllowTools...),
		DenyTools:      append(base.DenyTools, extra.DenyTools...),
		AllowResources: append(base.AllowResources, extra.AllowResources...),
		DenyResources:  append(base.DenyResources, extra.DenyResources...),
		AllowActions:   append(base.AllowActions, extra.AllowActions...),
		DenyActions:    append(base.DenyActions, extra.DenyActions...),
	}
}
