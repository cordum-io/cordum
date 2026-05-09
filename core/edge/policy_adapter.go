package edge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
)

// EdgeRuleAdapterOptions carries bundle-level context for unified edge rules.
type EdgeRuleAdapterOptions struct {
	Bundle       *policy.Bundle
	FallbackMode PolicyMode
}

// AdaptedEdgeRule is the legacy edge policy shape plus unified bundle context.
type AdaptedEdgeRule struct {
	Rule          config.PolicyRule
	PolicyMode    PolicyMode
	BundleID      string
	BundleVersion string
}

// EdgeRuleScopeContext is the runtime identity used to match edge rule scopes.
type EdgeRuleScopeContext struct {
	TenantID    string
	PrincipalID string
	FleetID     string
}

// AdaptUnifiedEdgeRule converts a unified edge Rule into the legacy policy
// rule shape consumed by the current edge/Safety Kernel compatibility path.
func AdaptUnifiedEdgeRule(rule policy.Rule, opts EdgeRuleAdapterOptions) (AdaptedEdgeRule, error) {
	if rule.Type != policy.RuleTypeEdge {
		return AdaptedEdgeRule{}, fmt.Errorf("edge rule adapter requires type edge, got %q", rule.Type)
	}
	match, err := parseEdgeRuleMatch(rule.Match)
	if err != nil {
		return AdaptedEdgeRule{}, err
	}
	decide, err := parseEdgeRuleDecide(rule.Decide)
	if err != nil {
		return AdaptedEdgeRule{}, err
	}
	legacy := config.PolicyRule{
		ID:          strings.TrimSpace(rule.ID),
		Tier:        config.PolicyTierGlobal,
		Match:       match.toPolicyMatch(),
		Decision:    decide.decision,
		Reason:      strings.TrimSpace(decide.reason),
		Constraints: config.PolicyConstraints{RedactionLevel: strings.TrimSpace(decide.redactionLevel)},
	}
	bundle := edgeBundleMetadata(opts.Bundle)
	return AdaptedEdgeRule{
		Rule:          legacy,
		PolicyMode:    PolicyModeFromBundleMetadata(bundle, opts.FallbackMode),
		BundleID:      strings.TrimSpace(bundle.ID),
		BundleVersion: latestBundleVersion(bundle),
	}, nil
}

// RuleScopeMatchesEdge reports whether a unified rule scope applies to an
// edge evaluation context. Job-only scopes intentionally never match edge.
func RuleScopeMatchesEdge(scope policy.RuleScope, ctx EdgeRuleScopeContext) bool {
	value := strings.TrimSpace(scope.Value)
	switch scope.Kind {
	case policy.RuleScopeGlobal:
		return true
	case policy.RuleScopeTenant:
		return value != "" && value == strings.TrimSpace(ctx.TenantID)
	case policy.RuleScopeEdgeFleet:
		return value != "" && value == strings.TrimSpace(ctx.FleetID)
	case policy.RuleScopeEdgeUser:
		return value != "" && value == strings.TrimSpace(ctx.PrincipalID)
	default:
		return false
	}
}

// PolicyModeFromBundleMetadata resolves per-bundle edge mode, falling back to
// the legacy session/global mode while migration tooling backfills bundles.
func PolicyModeFromBundleMetadata(bundle policy.Bundle, fallback PolicyMode) PolicyMode {
	switch bundle.Metadata.EdgeMode {
	case policy.EdgeModeObserve:
		return PolicyModeObserve
	case policy.EdgeModeEnforce:
		return PolicyModeEnforce
	case policy.EdgeModeEnterpriseStrict:
		return PolicyModeEnterpriseStrict
	default:
		return fallback
	}
}

type edgeRuleMatchPayload struct {
	Tenants        []string            `json:"tenants"`
	Topics         []string            `json:"topics"`
	Capabilities   []string            `json:"capabilities"`
	RiskTags       []string            `json:"risk_tags"`
	Labels         map[string]string   `json:"labels"`
	LabelAllowlist map[string][]string `json:"label_allowlist"`
	LabelThreshold map[string]float64  `json:"label_threshold"`
}

func parseEdgeRuleMatch(raw json.RawMessage) (edgeRuleMatchPayload, error) {
	if len(raw) == 0 {
		return edgeRuleMatchPayload{}, fmt.Errorf("edge rule match is required")
	}
	var match edgeRuleMatchPayload
	if err := json.Unmarshal(raw, &match); err != nil {
		return edgeRuleMatchPayload{}, fmt.Errorf("parse edge rule match: %w", err)
	}
	if len(match.Topics) == 0 {
		match.Topics = []string{EdgePolicyTopic}
	}
	return match, nil
}

func (m edgeRuleMatchPayload) toPolicyMatch() config.PolicyMatch {
	return config.PolicyMatch{
		Tenants:        trimSlice(m.Tenants),
		Topics:         trimSlice(m.Topics),
		Capabilities:   trimSlice(m.Capabilities),
		RiskTags:       trimSlice(m.RiskTags),
		Labels:         cloneRuleStringMap(m.Labels),
		LabelAllowlist: cloneRuleStringSliceMap(m.LabelAllowlist),
		LabelThreshold: cloneRuleFloatMap(m.LabelThreshold),
	}
}

type edgeRuleDecidePayload struct {
	decision       string
	reason         string
	redactionLevel string
}

func parseEdgeRuleDecide(raw json.RawMessage) (edgeRuleDecidePayload, error) {
	if len(raw) == 0 {
		return edgeRuleDecidePayload{}, fmt.Errorf("edge rule decide is required")
	}
	var wire struct {
		Decision    string `json:"decision"`
		Reason      string `json:"reason"`
		Constraints struct {
			RedactionLevel string `json:"redaction_level"`
		} `json:"constraints"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return edgeRuleDecidePayload{}, fmt.Errorf("parse edge rule decide: %w", err)
	}
	decision, err := normalizeEdgeRuleDecision(wire.Decision)
	if err != nil {
		return edgeRuleDecidePayload{}, err
	}
	return edgeRuleDecidePayload{decision: decision, reason: wire.Reason, redactionLevel: wire.Constraints.RedactionLevel}, nil
}

func normalizeEdgeRuleDecision(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow":
		return "allow", nil
	case "deny":
		return "deny", nil
	case "require_approval", "require_human":
		return "require_approval", nil
	case "throttle":
		return "throttle", nil
	case "constrain", "allow_with_constraints":
		return "allow_with_constraints", nil
	default:
		return "", fmt.Errorf("unsupported edge rule decision %q", strings.TrimSpace(raw))
	}
}

func edgeBundleMetadata(bundle *policy.Bundle) policy.Bundle {
	if bundle == nil {
		return policy.Bundle{}
	}
	return *bundle
}

func latestBundleVersion(bundle policy.Bundle) string {
	if len(bundle.Versions) == 0 {
		return ""
	}
	return strings.TrimSpace(bundle.Versions[len(bundle.Versions)-1].Version)
}

func trimSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func cloneRuleStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func cloneRuleStringSliceMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = trimSlice(value)
	}
	return out
}

func cloneRuleFloatMap(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = value
	}
	return out
}
