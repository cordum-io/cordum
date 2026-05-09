package safetykernel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
)

type ruleDecisionWire struct {
	Decision    string                 `json:"decision"`
	Reason      string                 `json:"reason"`
	Severity    string                 `json:"severity"`
	Velocity    *config.VelocityConfig `json:"velocity"`
	Constraints config.PolicyConstraints
}

type inputRuleMatchWire struct {
	Tenants         []string            `json:"tenants"`
	Topics          []string            `json:"topics"`
	Capabilities    []string            `json:"capabilities"`
	RiskTags        []string            `json:"risk_tags"`
	Scanners        []string            `json:"scanners"`
	ContentPatterns []string            `json:"content_patterns"`
	Keywords        []string            `json:"keywords"`
	ContentTypes    []string            `json:"content_types"`
	Detectors       []string            `json:"detectors"`
	InputSizeGt     int64               `json:"input_size_gt"`
	MaxInputBytes   int64               `json:"max_input_bytes"`
	Scope           *config.ScopeConfig `json:"scope"`
}

type outputRuleMatchWire struct {
	Tenants         []string `json:"tenants"`
	Topics          []string `json:"topics"`
	Capabilities    []string `json:"capabilities"`
	RiskTags        []string `json:"risk_tags"`
	Scanners        []string `json:"scanners"`
	ContentPatterns []string `json:"content_patterns"`
	Keywords        []string `json:"keywords"`
	ContentTypes    []string `json:"content_types"`
	Detectors       []string `json:"detectors"`
	OutputSizeGt    int64    `json:"output_size_gt"`
	MaxOutputBytes  int64    `json:"max_output_bytes"`
	HasError        *bool    `json:"has_error"`
}

type velocityRuleMatchWire struct {
	Tenants                  []string                `json:"tenants"`
	Topics                   []string                `json:"topics"`
	Capabilities             []string                `json:"capabilities"`
	RiskTags                 []string                `json:"risk_tags"`
	Requires                 []string                `json:"requires"`
	PackIDs                  []string                `json:"pack_ids"`
	ActorIDs                 []string                `json:"actor_ids"`
	ActorTypes               []string                `json:"actor_types"`
	AgentRiskTiers           []string                `json:"agent_risk_tiers"`
	AgentDataClassifications []string                `json:"agent_data_classifications"`
	Labels                   map[string]string       `json:"labels"`
	LabelAllowlist           map[string][]string     `json:"label_allowlist"`
	LabelThreshold           map[string]float64      `json:"label_threshold"`
	SecretsPresent           *bool                   `json:"secrets_present"`
	Predicate                string                  `json:"predicate"`
	Delegation               *config.DelegationMatch `json:"delegation"`
	MCP                      config.MCPPolicy        `json:"mcp"`
}

func compilePolicyPatterns(ruleID string, rawPatterns []string) ([]compiledOutputPattern, error) {
	patterns := make([]compiledOutputPattern, 0, len(rawPatterns))
	for _, raw := range rawPatterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" {
			continue
		}
		if err := validateRegexComplexity(pattern); err != nil {
			return nil, fmt.Errorf("compile rule %q pattern: %w", ruleID, err)
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile rule %q pattern: %w", ruleID, err)
		}
		patterns = append(patterns, compiledOutputPattern{raw: pattern, re: compiled})
	}
	return patterns, nil
}

func decodeInputMatch(raw json.RawMessage) (inputRuleMatchWire, error) {
	var out inputRuleMatchWire
	if ruleJSONMissing(raw) {
		return out, fmt.Errorf("missing rule match")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode rule match: %w", err)
	}
	return out, nil
}

func decodeOutputMatch(raw json.RawMessage) (outputRuleMatchWire, error) {
	var out outputRuleMatchWire
	if ruleJSONMissing(raw) {
		return out, fmt.Errorf("missing rule match")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode rule match: %w", err)
	}
	return out, nil
}

func decodeVelocityMatch(raw json.RawMessage) (velocityRuleMatchWire, error) {
	var out velocityRuleMatchWire
	if ruleJSONMissing(raw) {
		return out, fmt.Errorf("missing rule match")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode rule match: %w", err)
	}
	return out, nil
}

func decodeRuleDecision(raw json.RawMessage) (ruleDecisionWire, error) {
	var out ruleDecisionWire
	if ruleJSONMissing(raw) {
		return out, fmt.Errorf("missing rule decision")
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode rule decision: %w", err)
	}
	return out, nil
}

func ruleJSONMissing(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}
