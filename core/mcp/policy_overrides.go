package mcp

import "strings"

// mergeScopePolicyForTool merges runtime config overrides into the
// tool's scope metadata. Safety-first precedence: runtime can ONLY
// tighten a tool's requirements (raise min risk tier, add required
// data classifications). Attempts to loosen a code-registered tool
// are rejected so a compromised config can't widen an agent's scope.
//
// Config shape (stored at /api/v1/config?scope=system&id=mcp_tool_policy):
//
//	{
//	  "mcp_tool_policy": {
//	    "tools": [
//	      {"tool_pattern": "fs.*",
//	       "min_risk_tier": "high",
//	       "required_data_classifications": ["pii"]}
//	    ]
//	  }
//	}
//
// Rules are evaluated in order; the first pattern match wins. The
// plain `{"tools": [...]}` shape is also accepted for parity with
// mcp_policy (approval gate config) so operators can keep a single
// document structure.
func (r *ToolRegistry) mergeScopePolicyForTool(tool Tool) Tool {
	r.cfgMu.RLock()
	cfg := r.cfgData
	r.cfgMu.RUnlock()
	if cfg == nil {
		return tool
	}
	rules, ok := extractScopePolicyRules(cfg)
	if !ok {
		return tool
	}
	for _, raw := range rules {
		rule, isMap := raw.(map[string]any)
		if !isMap {
			continue
		}
		pattern, _ := rule["tool_pattern"].(string)
		if pattern == "" {
			// Accept `tool_name_pattern` for symmetry with mcp_policy entries.
			pattern, _ = rule["tool_name_pattern"].(string)
		}
		if pattern == "" || !globMatch(pattern, tool.Name) {
			continue
		}
		if v, present := rule["min_risk_tier"]; present {
			if s, okS := v.(string); okS {
				if tightened := tightenRiskTier(tool.RiskTier, s); tightened != "" {
					tool.RiskTier = tightened
				}
			}
		}
		if v, present := rule["required_data_classifications"]; present {
			if list, okL := v.([]any); okL {
				tool.DataClassifications = unionClassifications(tool.DataClassifications, list)
			}
		}
		break
	}
	return tool
}

// extractScopePolicyRules finds the `tools` list under either
// mcp_tool_policy, mcp_policy, or at the root of the config map.
func extractScopePolicyRules(cfg map[string]any) ([]any, bool) {
	if inner, ok := cfg["mcp_tool_policy"].(map[string]any); ok {
		if list, okL := inner["tools"].([]any); okL {
			return list, true
		}
	}
	if list, ok := cfg["tools"].([]any); ok {
		return list, true
	}
	if inner, ok := cfg["mcp_policy"].(map[string]any); ok {
		if list, okL := inner["tools"].([]any); okL {
			return list, true
		}
	}
	return nil, false
}

// tightenRiskTier returns proposed if it represents a strictly higher
// tier than current. When the proposed tier would weaken the policy
// (e.g. code says "critical", runtime asks for "low") the current
// value is preserved — runtime cannot loosen code-time requirements.
// Returns empty string when no change is warranted.
func tightenRiskTier(current, proposed string) string {
	prop := ParseRiskTier(proposed)
	if prop == RiskTierUnknown {
		return ""
	}
	cur := ParseRiskTier(current)
	if cur == RiskTierUnknown {
		// No current tier declared → runtime may set any.
		return prop.String()
	}
	if prop > cur {
		return prop.String()
	}
	return ""
}

// unionClassifications adds entries from extras (case-insensitive) to
// current without introducing duplicates. Only tightens — entries in
// current are never dropped.
func unionClassifications(current []string, extras []any) []string {
	seen := make(map[string]struct{}, len(current)+len(extras))
	out := make([]string, 0, len(current)+len(extras))
	for _, c := range current {
		key := strings.ToLower(strings.TrimSpace(c))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	for _, e := range extras {
		s, ok := e.(string)
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(s))
	}
	return out
}
