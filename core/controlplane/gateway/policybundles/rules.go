package policybundles

import (
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/infra/config"
	"gopkg.in/yaml.v3"
)

// RulesFromPolicyContent parses YAML policy content and returns normalized rules.
func RulesFromPolicyContent(fragmentID string, bundle map[string]any, content string) ([]map[string]any, error) {
	var payload any
	if err := yaml.Unmarshal([]byte(content), &payload); err != nil {
		return nil, err
	}
	root, _ := packs.NormalizeJSON(payload).(map[string]any)
	if root == nil {
		return nil, nil
	}
	rules := NormalizePolicyRules(root["rules"])
	if len(rules) == 0 {
		rules = LegacyPolicyRules(root["tenants"])
	}
	for _, rule := range rules {
		source := PolicyRuleSourceFromBundle(fragmentID, bundle)
		source.Tier = policyRuleTier(root, rule)
		if _, ok := rule["tier"]; !ok {
			rule["tier"] = source.Tier
		}
		if _, ok := rule["selector"]; !ok && root["selector"] != nil {
			rule["selector"] = root["selector"]
		}
		rule["source"] = source
	}
	return rules, nil
}

// OutputRulesFromPolicyContent parses YAML policy content and returns normalized output rules.
func OutputRulesFromPolicyContent(fragmentID string, bundle map[string]any, content string) ([]map[string]any, error) {
	var payload any
	if err := yaml.Unmarshal([]byte(content), &payload); err != nil {
		return nil, err
	}
	root, _ := packs.NormalizeJSON(payload).(map[string]any)
	if root == nil {
		return nil, nil
	}
	rules := NormalizePolicyRules(root["output_rules"])
	source := PolicyRuleSourceFromBundle(fragmentID, bundle)
	source.Tier = config.NormalizePolicyTier(StringFromAny(root["tier"]))
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		normalized := NormalizeOutputRule(rule)
		normalized["source"] = source
		out = append(out, normalized)
	}
	return out, nil
}

// NormalizeOutputRule normalizes a single output rule map into a canonical form.
func NormalizeOutputRule(rule map[string]any) map[string]any {
	match, _ := rule["match"].(map[string]any)
	if match == nil {
		match = map[string]any{}
	}
	id := strings.TrimSpace(StringFromAny(rule["id"]))
	if id == "" {
		id = "output-rule"
	}
	description := strings.TrimSpace(StringFromAny(rule["description"]))
	decision := strings.ToLower(strings.TrimSpace(StringFromAny(rule["decision"])))
	if decision == "" {
		decision = "allow"
	}
	severity := strings.ToLower(strings.TrimSpace(StringFromAny(rule["severity"])))
	if severity == "" {
		severity = "medium"
	}
	enabled := true
	if raw, ok := rule["enabled"]; ok {
		switch v := raw.(type) {
		case bool:
			enabled = v
		default:
			enabled = ParseBool(fmt.Sprint(v))
		}
	}
	topics := StringSliceFromAny(match["topics"])
	scanners := MergeUniqueStrings(
		StringSliceFromAny(match["scanners"]),
		StringSliceFromAny(match["detectors"]),
	)
	patterns := StringSliceFromAny(match["content_patterns"])
	patternPreview := ""
	if len(patterns) > 0 {
		patternPreview = strings.TrimSpace(patterns[0])
		if len(patternPreview) > 100 {
			patternPreview = patternPreview[:100] + "..."
		}
	}
	normalized := map[string]any{
		"id":              id,
		"description":     description,
		"topics":          topics,
		"scanners":        scanners,
		"patterns":        patterns,
		"pattern_preview": patternPreview,
		"decision":        decision,
		"severity":        severity,
		"reason":          strings.TrimSpace(StringFromAny(rule["reason"])),
		"enabled":         enabled,
		"match":           match,
	}
	// Pass through new rule types for dashboard display
	if velocity, ok := rule["velocity"]; ok && velocity != nil {
		normalized["velocity"] = velocity
	}
	if constraints, ok := rule["constraints"]; ok && constraints != nil {
		normalized["constraints"] = constraints
	}
	if raw, ok := rule["last_triggered"]; ok {
		normalized["last_triggered"] = strings.TrimSpace(StringFromAny(raw))
	}
	if raw, ok := rule["trigger_count_24h"]; ok {
		normalized["trigger_count_24h"] = raw
	}
	return normalized
}

// NormalizePolicyRules coerces an interface value to a slice of rule maps.
func NormalizePolicyRules(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, rule)
	}
	return out
}

// LegacyPolicyRules parses legacy tenant-based policy rules.
func LegacyPolicyRules(value any) []map[string]any {
	tenants, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for tenant, raw := range tenants {
		data, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		mcp := data["mcp"]
		denyTopics := StringSliceFromAny(data["deny_topics"])
		for idx, topic := range denyTopics {
			match := map[string]any{
				"tenants": []string{tenant},
				"topics":  []string{topic},
			}
			if mcp != nil {
				match["mcp"] = mcp
			}
			out = append(out, map[string]any{
				"id":       fmt.Sprintf("legacy:%s:deny:%d", tenant, idx+1),
				"decision": "deny",
				"reason":   fmt.Sprintf("topic %q denied by tenant policy", topic),
				"match":    match,
			})
		}
		allowTopics := StringSliceFromAny(data["allow_topics"])
		for idx, topic := range allowTopics {
			match := map[string]any{
				"tenants": []string{tenant},
				"topics":  []string{topic},
			}
			if mcp != nil {
				match["mcp"] = mcp
			}
			out = append(out, map[string]any{
				"id":       fmt.Sprintf("legacy:%s:allow:%d", tenant, idx+1),
				"decision": "allow",
				"match":    match,
			})
		}
	}
	return out
}

// PolicyRuleSourceFromBundle constructs a PolicyRuleSource from fragment metadata.
func PolicyRuleSourceFromBundle(fragmentID string, bundle map[string]any) PolicyRuleSource {
	source := PolicyRuleSource{
		FragmentID: fragmentID,
	}
	if fragmentID != "" {
		parts := strings.SplitN(fragmentID, "/", 2)
		source.PackID = parts[0]
		if len(parts) > 1 {
			source.OverlayName = parts[1]
		}
	}
	source.Version = strings.TrimSpace(StringFromAny(bundle["version"]))
	source.InstalledAt = strings.TrimSpace(StringFromAny(bundle["installed_at"]))
	source.Sha256 = strings.TrimSpace(StringFromAny(bundle["sha256"]))
	return source
}

func policyRuleTier(root, rule map[string]any) string {
	if raw := strings.TrimSpace(StringFromAny(rule["tier"])); raw != "" {
		return config.NormalizePolicyTier(raw)
	}
	return config.NormalizePolicyTier(StringFromAny(root["tier"]))
}
