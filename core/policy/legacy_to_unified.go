package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
)

// ErrInvalidLegacyRule is returned by LegacyMapToUnifiedRule when the
// legacy rule shape (e.g. from `policybundles.RulesFromPolicyContent`)
// is missing required fields or carries unparseable values. Errors are
// wrapped via %w so callers can errors.Is() against the sentinel
// without losing the per-field context.
var ErrInvalidLegacyRule = errors.New("policy: invalid legacy rule shape")

// LegacyMapToUnifiedRule translates a legacy YAML-derived rule map (as
// emitted by `policybundles.RulesFromPolicyContent` or
// `OutputRulesFromPolicyContent`) into the unified `Rule` envelope. The
// caller must specify which bucket the map came from via ruleType
// because the bucket discriminator does NOT appear in the map itself —
// the legacy parser dropped it. Pack source attribution is supplied by
// the caller via source (nil leaves Audit.PackSource nil for
// RuleStore-authored or unattributed rules).
//
// Field mapping:
//   - id (required) → Rule.ID. Missing → ErrInvalidLegacyRule.
//   - name (optional) → Rule.Name. Falls back to id.
//   - description (optional) → Rule.Description. Falls back to reason.
//   - tier + selector → Rule.Scope. Defaults to RuleScopeGlobal when
//     unset; tenant scope reads selector.tenants[0]; workflow scope
//     reads selector.workflows[0]; edge fleet reads selector.fleets[0].
//   - status (optional) → Rule.Status. Defaults to published — YAML
//     bundles ARE production state; only RuleStore-authored rules
//     default to draft.
//   - match (optional) → Rule.Match. For velocity rules, the top-level
//     `velocity` field is embedded under match.velocity so downstream
//     evaluators can branch by Type without re-parsing.
//   - decision + reason + severity → Rule.Decide.
func LegacyMapToUnifiedRule(legacy map[string]any, ruleType RuleType, source *PackSource) (*Rule, error) {
	if _, err := ParseRuleType(string(ruleType)); err != nil {
		return nil, err
	}
	if legacy == nil {
		return nil, fmt.Errorf("%w: legacy map is nil", ErrInvalidLegacyRule)
	}
	id := strings.TrimSpace(stringField(legacy, "id"))
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidLegacyRule)
	}
	name := strings.TrimSpace(stringField(legacy, "name"))
	if name == "" {
		name = id
	}
	description := strings.TrimSpace(stringField(legacy, "description"))
	if description == "" {
		description = strings.TrimSpace(stringField(legacy, "reason"))
	}

	scope := scopeFromLegacy(legacy)
	status, err := statusFromLegacy(legacy)
	if err != nil {
		return nil, err
	}

	matchJSON, err := buildMatchJSON(legacy, ruleType)
	if err != nil {
		return nil, err
	}
	decideJSON, err := buildDecideJSON(legacy)
	if err != nil {
		return nil, err
	}

	rule := &Rule{
		ID:          id,
		Name:        name,
		Type:        ruleType,
		Scope:       scope,
		Status:      status,
		Version:     "v1",
		Audit:       AuditMetadata{PackSource: source},
		Match:       matchJSON,
		Decide:      decideJSON,
		Description: description,
	}
	return rule, nil
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch typed := v.(type) {
		case string:
			return typed
		case fmt.Stringer:
			return typed.String()
		}
	}
	return ""
}

// scopeFromLegacy reads tier + selector to build a unified RuleScope.
// Defaults to global when neither is present so YAML rules without
// explicit scope still parse cleanly.
func scopeFromLegacy(legacy map[string]any) RuleScope {
	tier := strings.TrimSpace(stringField(legacy, "tier"))
	selector, _ := legacy["selector"].(map[string]any)
	switch strings.ToLower(tier) {
	case "tenant":
		return RuleScope{Kind: RuleScopeTenant, Value: firstSelectorValue(selector, "tenants")}
	case "workflow":
		return RuleScope{Kind: RuleScopeWorkflow, Value: firstSelectorValue(selector, "workflows")}
	case "edge_fleet", "edge-fleet":
		return RuleScope{Kind: RuleScopeEdgeFleet, Value: firstSelectorValue(selector, "fleets")}
	case "edge_user", "edge-user":
		return RuleScope{Kind: RuleScopeEdgeUser, Value: firstSelectorValue(selector, "users")}
	}
	return RuleScope{Kind: RuleScopeGlobal}
}

func firstSelectorValue(selector map[string]any, key string) string {
	if selector == nil {
		return ""
	}
	values, ok := selector[key].([]any)
	if !ok || len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[0]))
}

func statusFromLegacy(legacy map[string]any) (RuleStatus, error) {
	raw := strings.TrimSpace(stringField(legacy, "status"))
	if raw == "" {
		return RuleStatusPublished, nil
	}
	parsed, err := ParseRuleStatus(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidLegacyRule, err)
	}
	return parsed, nil
}

func buildMatchJSON(legacy map[string]any, ruleType RuleType) (json.RawMessage, error) {
	match, _ := legacy["match"].(map[string]any)
	if match == nil {
		match = map[string]any{}
	}
	// Velocity rules carry a top-level `velocity` block that's not part
	// of `match` in the legacy YAML; embed it under match.velocity so
	// downstream evaluators receive a single contiguous payload keyed
	// by RuleType.
	if ruleType == RuleTypeVelocity {
		if velocity, ok := legacy["velocity"]; ok && velocity != nil {
			merged := make(map[string]any, len(match)+1)
			maps.Copy(merged, match)
			merged["velocity"] = velocity
			match = merged
		}
	}
	return json.Marshal(match)
}

func buildDecideJSON(legacy map[string]any) (json.RawMessage, error) {
	decide := map[string]any{}
	if decision := strings.TrimSpace(stringField(legacy, "decision")); decision != "" {
		decide["decision"] = decision
	}
	if reason := strings.TrimSpace(stringField(legacy, "reason")); reason != "" {
		decide["reason"] = reason
	}
	if severity := strings.TrimSpace(stringField(legacy, "severity")); severity != "" {
		decide["severity"] = severity
	}
	return json.Marshal(decide)
}
