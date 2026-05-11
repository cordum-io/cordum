package safetykernel

import (
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// JobContext carries job-side scope + identity fields used by unified Rule
// envelopes and by the JOB Decision emitter. The Tenant + WorkflowID + JobID
// triplet was the original scope-matching surface (RuleScopeMatchesJob); the
// AgentID + PrincipalID + Topic fields were added for Backend 5e so JOB
// Decisions can carry the full evaluation identity (unblocks dashboard
// per-decision Replay + cross-link work).
type JobContext struct {
	Tenant      string
	WorkflowID  string
	JobID       string
	AgentID     string
	PrincipalID string
	Topic       string
}

// RuleToCompiledInput adapts a unified input Rule into the existing matcher.
func RuleToCompiledInput(rule policy.Rule) (compiledInputRule, error) {
	if rule.Type != policy.RuleTypeInput {
		return compiledInputRule{}, ruleTypeError(rule.Type, policy.RuleTypeInput)
	}
	match, err := decodeInputMatch(rule.Match)
	if err != nil {
		return compiledInputRule{}, err
	}
	decide, err := decodeRuleDecision(rule.Decide)
	if err != nil {
		return compiledInputRule{}, err
	}
	decision, ok := normalizeInputDecision(decide.Decision)
	if !ok {
		return compiledInputRule{}, fmt.Errorf("unsupported input decision %q", decide.Decision)
	}
	return buildCompiledInput(rule, match, decide, decision)
}

// RuleToCompiledOutput adapts a unified output Rule into the existing matcher.
func RuleToCompiledOutput(rule policy.Rule) (compiledOutputRule, error) {
	if rule.Type != policy.RuleTypeOutput {
		return compiledOutputRule{}, ruleTypeError(rule.Type, policy.RuleTypeOutput)
	}
	match, err := decodeOutputMatch(rule.Match)
	if err != nil {
		return compiledOutputRule{}, err
	}
	decide, err := decodeRuleDecision(rule.Decide)
	if err != nil {
		return compiledOutputRule{}, err
	}
	decision, ok := parseOutputDecision(decide.Decision)
	if !ok {
		return compiledOutputRule{}, fmt.Errorf("unsupported output decision %q", decide.Decision)
	}
	return buildCompiledOutput(rule, match, decide, decision)
}

// RuleToCompiledVelocity adapts a unified velocity Rule into legacy PolicyRule.
func RuleToCompiledVelocity(rule policy.Rule) (config.PolicyRule, error) {
	if rule.Type != policy.RuleTypeVelocity {
		return config.PolicyRule{}, ruleTypeError(rule.Type, policy.RuleTypeVelocity)
	}
	match, err := decodeVelocityMatch(rule.Match)
	if err != nil {
		return config.PolicyRule{}, err
	}
	decide, err := decodeRuleDecision(rule.Decide)
	if err != nil {
		return config.PolicyRule{}, err
	}
	if decide.Velocity == nil {
		return config.PolicyRule{}, fmt.Errorf("missing velocity config")
	}
	if err := decide.Velocity.Validate(strings.TrimSpace(rule.ID)); err != nil {
		return config.PolicyRule{}, err
	}
	return buildVelocityRule(rule, match, decide), nil
}

// RuleScopeMatchesJob reports whether a unified job-side scope includes a job.
func RuleScopeMatchesJob(scope policy.RuleScope, job JobContext) bool {
	value := strings.TrimSpace(scope.Value)
	switch scope.Kind {
	case "", policy.RuleScopeGlobal:
		return true
	case policy.RuleScopeTenant:
		return value != "" && value == strings.TrimSpace(job.Tenant)
	case policy.RuleScopeWorkflow:
		return value != "" && value == strings.TrimSpace(job.WorkflowID)
	case policy.RuleScopeEdgeFleet, policy.RuleScopeEdgeUser:
		return false
	default:
		return false
	}
}

func buildCompiledInput(rule policy.Rule, match inputRuleMatchWire, decide ruleDecisionWire, decision string) (compiledInputRule, error) {
	patterns, err := compilePolicyPatterns(rule.ID, match.ContentPatterns)
	if err != nil {
		return compiledInputRule{}, err
	}
	if match.Scope != nil {
		if err := validateScopeConfig(match.Scope); err != nil {
			return compiledInputRule{}, fmt.Errorf("invalid input scope: %w", err)
		}
	}
	maxBytes := maxPositive(match.MaxInputBytes, match.InputSizeGt)
	tier, selector := legacyTierSelector(rule.Scope)
	return compiledInputRule{
		id:           strings.TrimSpace(rule.ID),
		tier:         tier,
		selector:     selector,
		decision:     decision,
		reason:       strings.TrimSpace(decide.Reason),
		severity:     normalizeSeverity(decide.Severity),
		tenants:      normalizeList(match.Tenants),
		topics:       normalizeList(match.Topics),
		capabilities: normalizeList(match.Capabilities),
		riskTags:     normalizeList(match.RiskTags),
		contentTypes: normalizeList(match.ContentTypes),
		scanners:     mergeScannerLists(match.Scanners, match.Detectors),
		patterns:     patterns,
		keywords:     normalizeList(match.Keywords),
		maxBytes:     maxBytes,
		scope:        match.Scope,
	}, nil
}

func buildCompiledOutput(rule policy.Rule, match outputRuleMatchWire, decide ruleDecisionWire, decision pb.OutputDecision) (compiledOutputRule, error) {
	patterns, err := compilePolicyPatterns(rule.ID, match.ContentPatterns)
	if err != nil {
		return compiledOutputRule{}, err
	}
	return compiledOutputRule{
		id:             strings.TrimSpace(rule.ID),
		decision:       decision,
		reason:         strings.TrimSpace(decide.Reason),
		severity:       normalizeSeverity(decide.Severity),
		tenants:        normalizeList(match.Tenants),
		topics:         normalizeList(match.Topics),
		capabilities:   normalizeList(match.Capabilities),
		riskTags:       normalizeList(match.RiskTags),
		contentTypes:   normalizeList(match.ContentTypes),
		scanners:       mergeScannerLists(match.Scanners, match.Detectors),
		patterns:       patterns,
		keywords:       normalizeList(match.Keywords),
		maxOutputBytes: maxPositive(match.MaxOutputBytes, match.OutputSizeGt),
		hasError:       match.HasError,
	}, nil
}

func buildVelocityRule(rule policy.Rule, match velocityRuleMatchWire, decide ruleDecisionWire) config.PolicyRule {
	tier, selector := legacyTierSelector(rule.Scope)
	return config.PolicyRule{
		ID:          strings.TrimSpace(rule.ID),
		Tier:        tier,
		Selector:    selector,
		Match:       legacyPolicyMatch(match),
		Velocity:    decide.Velocity,
		Decision:    normalizeVelocityDecision(decide.Decision),
		Reason:      strings.TrimSpace(decide.Reason),
		Constraints: decide.Constraints,
	}
}

func ruleTypeError(got, want policy.RuleType) error {
	return fmt.Errorf("rule type mismatch: got %q want %q", got, want)
}

func normalizeInputDecision(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "deny":
		return "deny", true
	case "require_approval", "require-approval", "require_human":
		return "require_approval", true
	default:
		return "", false
	}
}

func normalizeVelocityDecision(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "throttle":
		return "throttle"
	case "deny", "block":
		return "deny"
	case "require_approval", "require-approval", "require_human":
		return "require_approval"
	case "allow_with_constraints", "allow-with-constraints":
		return "allow_with_constraints"
	default:
		return "throttle"
	}
}

func legacyTierSelector(scope policy.RuleScope) (string, config.PolicySelector) {
	value := strings.TrimSpace(scope.Value)
	switch scope.Kind {
	case policy.RuleScopeWorkflow:
		return config.PolicyTierWorkflow, config.PolicySelector{WorkflowID: value}
	default:
		return config.PolicyTierGlobal, config.PolicySelector{}
	}
}

func legacyPolicyMatch(match velocityRuleMatchWire) config.PolicyMatch {
	return config.PolicyMatch{
		Tenants:                  normalizeList(match.Tenants),
		Topics:                   normalizeList(match.Topics),
		Capabilities:             normalizeList(match.Capabilities),
		RiskTags:                 normalizeList(match.RiskTags),
		Requires:                 normalizeList(match.Requires),
		PackIDs:                  normalizeList(match.PackIDs),
		ActorIDs:                 normalizeList(match.ActorIDs),
		ActorTypes:               normalizeList(match.ActorTypes),
		AgentRiskTiers:           normalizeList(match.AgentRiskTiers),
		AgentDataClassifications: normalizeList(match.AgentDataClassifications),
		Labels:                   match.Labels,
		LabelAllowlist:           match.LabelAllowlist,
		LabelThreshold:           match.LabelThreshold,
		SecretsPresent:           match.SecretsPresent,
		Predicate:                strings.TrimSpace(match.Predicate),
		Delegation:               match.Delegation,
		MCP:                      match.MCP,
	}
}

func maxPositive(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
