package safetykernel

import (
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

func (g *GlobalPolicy) addRule(rule config.PolicyRule) {
	rule.Tier = config.NormalizePolicyTier(rule.Tier)
	rule.Selector = config.TrimPolicySelector(rule.Selector)
	switch rule.Tier {
	case config.PolicyTierWorkflow:
		key := config.PolicySelectorKey(rule.Tier, rule.Selector)
		if key != "" {
			g.WorkflowOverrides = appendScopedRule(g.WorkflowOverrides, key, rule)
			return
		}
	case config.PolicyTierJob:
		key := config.PolicySelectorKey(rule.Tier, rule.Selector)
		if key != "" {
			g.JobOverrides = appendScopedRule(g.JobOverrides, key, rule)
			return
		}
	}
	rule.Tier = config.PolicyTierGlobal
	rule.Selector = config.PolicySelector{}
	switch {
	case isEdgeActionRule(rule):
		g.EdgeActionRules = append(g.EdgeActionRules, rule)
	case isMCPToolRule(rule):
		g.MCPToolRules = append(g.MCPToolRules, rule)
	default:
		g.InputRules = append(g.InputRules, rule)
	}
}

func (g *GlobalPolicy) addPolicyLevelDefault(policy *config.SafetyPolicy) {
	decision := normalizedOptionalDecision(policy.DefaultDecision)
	if decision == "" {
		return
	}
	tier := config.NormalizePolicyTier(policy.Tier)
	if tier == config.PolicyTierGlobal {
		return
	}
	g.addTierDefault(config.PolicyTierDefault{
		Tier:     tier,
		Selector: config.TrimPolicySelector(policy.Selector),
		Decision: decision,
	})
}

func (g *GlobalPolicy) addTierDefault(def config.PolicyTierDefault) {
	decision := normalizedOptionalDecision(def.Decision)
	if decision == "" {
		return
	}
	key := config.PolicySelectorKey(def.Tier, def.Selector)
	switch config.NormalizePolicyTier(def.Tier) {
	case config.PolicyTierWorkflow:
		if key != "" {
			if g.WorkflowDefaultDecisions == nil {
				g.WorkflowDefaultDecisions = map[string]string{}
			}
			g.WorkflowDefaultDecisions[key] = decision
		}
	case config.PolicyTierJob:
		if key != "" {
			if g.JobDefaultDecisions == nil {
				g.JobDefaultDecisions = map[string]string{}
			}
			g.JobDefaultDecisions[key] = decision
		}
	}
}

func concatRules(invariants, section []config.PolicyRule) []config.PolicyRule {
	denies, allows := splitGlobalInvariantRules(invariants)
	if len(denies) == 0 && len(allows) == 0 {
		return append([]config.PolicyRule{}, section...)
	}
	out := make([]config.PolicyRule, 0, len(invariants)+len(section))
	out = append(out, denies...)
	out = append(out, section...)
	out = append(out, allows...)
	return out
}

func splitGlobalInvariantRules(rules []config.PolicyRule) (denies, allows []config.PolicyRule) {
	for _, rule := range rules {
		if isGlobalInvariantDeny(rule.Decision) {
			denies = append(denies, rule)
			continue
		}
		allows = append(allows, rule)
	}
	return denies, allows
}

func isGlobalInvariantDeny(decision string) bool {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "require_approval", "require-approval", "require_human", "throttle":
		return true
	default:
		return false
	}
}

func appendScopedRule(
	rules map[string][]config.PolicyRule,
	key string,
	rule config.PolicyRule,
) map[string][]config.PolicyRule {
	if rules == nil {
		rules = map[string][]config.PolicyRule{}
	}
	rules[key] = append(rules[key], rule)
	return rules
}

func appendInputScopedRules(out, rules []config.PolicyRule) []config.PolicyRule {
	for _, rule := range rules {
		if isEdgeActionRule(rule) || isMCPToolRule(rule) {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func appendScopedRulesForSection(
	out []config.PolicyRule,
	rules []config.PolicyRule,
	include func(config.PolicyRule) bool,
) []config.PolicyRule {
	for _, rule := range rules {
		if include(rule) {
			out = append(out, rule)
		}
	}
	return out
}

func cloneRulesWithTier(rules []config.PolicyRule, tier string) []config.PolicyRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]config.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		rule.Tier = config.NormalizePolicyTier(tier)
		rule.Selector = config.PolicySelector{}
		out = append(out, rule)
	}
	return out
}

func normalizedOptionalDecision(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return config.NormalizeDecision(raw)
}

func scopedDefault(
	defaults map[string]string,
	rules map[string][]config.PolicyRule,
	key string,
) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	if decision := defaults[key]; decision != "" {
		return decision, true
	}
	if len(rules[key]) > 0 {
		return "deny", true
	}
	return "", false
}
