package policybundles

import (
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

// MergeSafetyPolicies merges two safety policies into one.
func MergeSafetyPolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	if base == nil {
		return cloneWithTierMetadata(extra)
	}
	if extra == nil {
		return cloneWithTierMetadata(base)
	}
	out := cloneWithTierMetadata(base)
	add := cloneWithTierMetadata(extra)
	out.Tier = config.PolicyTierGlobal
	out.Selector = config.PolicySelector{}
	if out.Version == "" {
		out.Version = add.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = add.DefaultTenant
	}
	if strings.TrimSpace(out.DefaultDecision) == "" {
		out.DefaultDecision = strings.TrimSpace(add.DefaultDecision)
	}
	if !out.OutputPolicy.Enabled && add.OutputPolicy.Enabled {
		out.OutputPolicy.Enabled = true
	}
	if strings.TrimSpace(out.OutputPolicy.FailMode) == "" {
		out.OutputPolicy.FailMode = strings.TrimSpace(add.OutputPolicy.FailMode)
	}
	// Merge rule sets with cross-bundle duplicate-ID detection (last-seen wins,
	// replace-in-place) so the install-recency order in BuildPolicyFromBundles
	// makes the most-recently-installed bundle win a duplicate id — matching the
	// authoritative kernel merge (safetykernel mergePolicies) so replay/evals/
	// simulation predict the same first-match decision production enforces.
	out.Rules = mergeRulesByID(out.Rules, add.Rules, func(r config.PolicyRule) string { return r.ID })
	out.OutputRules = mergeRulesByID(out.OutputRules, CloneOutputPolicyRules(add.OutputRules), func(r config.OutputPolicyRule) string { return r.ID })
	out.InputRules = mergeRulesByID(out.InputRules, add.InputRules, func(r config.InputPolicyRule) string { return r.ID })
	out.TierDefaults = append(out.TierDefaults, add.TierDefaults...)
	out.Tenants = MergeTenantPolicies(out.Tenants, add.Tenants)
	return out
}

// mergeRulesByID appends add's rules to base, but a rule whose id already exists
// in base REPLACES the existing one in place (last-seen wins) rather than being
// appended a second time. id extracts a rule's id. Rules with an empty id are
// always appended (no dedup key). This mirrors the kernel's mergePolicies so a
// duplicate rule id across bundles resolves identically (the caller controls
// precedence via merge order: install-recency last in BuildPolicyFromBundles).
func mergeRulesByID[T any](base, add []T, id func(T) string) []T {
	if len(add) == 0 {
		return base
	}
	seen := make(map[string]int, len(base))
	for i, r := range base {
		if rid := id(r); rid != "" {
			seen[rid] = i
		}
	}
	for _, r := range add {
		if rid := id(r); rid != "" {
			if idx, dup := seen[rid]; dup {
				slog.Warn("duplicate policy rule ID in bundle merge — replacing with latest", "rule_id", rid)
				base[idx] = r
				continue
			}
			seen[rid] = len(base)
		}
		base = append(base, r)
	}
	return base
}

// CloneSafetyPolicy deep-copies a safety policy.
func CloneSafetyPolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &config.SafetyPolicy{
		Version:         policy.Version,
		Tier:            policy.Tier,
		Selector:        config.TrimPolicySelector(policy.Selector),
		DefaultTenant:   policy.DefaultTenant,
		DefaultDecision: policy.DefaultDecision,
		InputPolicy:     policy.InputPolicy,
		InputRules:      append([]config.InputPolicyRule{}, policy.InputRules...),
		Rules:           ClonePolicyRules(policy.Rules),
		OutputPolicy:    policy.OutputPolicy,
		OutputRules:     CloneOutputPolicyRules(policy.OutputRules),
		TierDefaults:    ClonePolicyTierDefaults(policy.TierDefaults),
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
		MCP:              cloneMCPPolicy(policy.MCP),
	}
}

// MergeMCPPolicy merges two MCP policies.
func MergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append(append([]string{}, base.AllowServers...), extra.AllowServers...),
		DenyServers:    append(append([]string{}, base.DenyServers...), extra.DenyServers...),
		AllowTools:     append(append([]string{}, base.AllowTools...), extra.AllowTools...),
		DenyTools:      append(append([]string{}, base.DenyTools...), extra.DenyTools...),
		AllowResources: append(append([]string{}, base.AllowResources...), extra.AllowResources...),
		DenyResources:  append(append([]string{}, base.DenyResources...), extra.DenyResources...),
		AllowActions:   append(append([]string{}, base.AllowActions...), extra.AllowActions...),
		DenyActions:    append(append([]string{}, base.DenyActions...), extra.DenyActions...),
	}
}
