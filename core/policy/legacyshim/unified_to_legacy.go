package legacyshim

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
)

// ErrRuleTypeMismatch is returned when a unified Rule cannot be projected
// onto the requested legacy struct because Rule.Type does not match the
// target type (e.g. RuleToInputPolicyRule called on a velocity rule).
var ErrRuleTypeMismatch = errors.New("legacyshim: rule type mismatch")

// ErrUnknownDecisionType is returned when a unified DecisionType has no
// canonical legacy equivalent. The set is closed; new DecisionType values
// must extend the table below explicitly.
var ErrUnknownDecisionType = errors.New("legacyshim: unknown decision type")

// legacyDecisionTraceEnvelope mirrors the JSON written into
// Decision.Trace[0].Constraints by LegacyPolicyDecisionToDecision so the
// inverse can reconstruct PolicyDecision without information loss.
type legacyDecisionTraceEnvelope struct {
	Constraints      config.PolicyConstraints   `json:"constraints,omitzero"`
	Remediations     []config.PolicyRemediation `json:"remediations,omitempty"`
	RuleTier         string                     `json:"rule_tier,omitempty"`
	ApprovalRequired bool                       `json:"approval_required,omitempty"`
}

// RuleToInputPolicyRule reverses InputPolicyRuleToRule. The function fails
// loudly when Rule.Type is not RuleTypeInput so callers receive a typed
// error instead of a silently wrong reconstruction.
func RuleToInputPolicyRule(rule policy.Rule) (config.InputPolicyRule, error) {
	if rule.Type != policy.RuleTypeInput {
		return config.InputPolicyRule{}, fmt.Errorf("%w: rule %q has type %q, want %q", ErrRuleTypeMismatch, rule.ID, rule.Type, policy.RuleTypeInput)
	}
	var env inputRuleEnvelope
	if err := json.Unmarshal(rule.Match, &env); err != nil {
		return config.InputPolicyRule{}, fmt.Errorf("legacyshim: decode input match for %q: %w", rule.ID, err)
	}
	dec, err := decodeDecideEnvelope(rule)
	if err != nil {
		return config.InputPolicyRule{}, err
	}
	return config.InputPolicyRule{
		ID:       rule.ID,
		Tier:     env.Tier,
		Selector: env.Selector,
		Enabled:  env.Enabled,
		Severity: env.Severity,
		Desc:     rule.Description,
		Match:    env.Match,
		Decision: dec.Decision,
		Reason:   dec.Reason,
	}, nil
}

// RuleToOutputPolicyRule reverses OutputPolicyRuleToRule.
func RuleToOutputPolicyRule(rule policy.Rule) (config.OutputPolicyRule, error) {
	if rule.Type != policy.RuleTypeOutput {
		return config.OutputPolicyRule{}, fmt.Errorf("%w: rule %q has type %q, want %q", ErrRuleTypeMismatch, rule.ID, rule.Type, policy.RuleTypeOutput)
	}
	var env outputRuleEnvelope
	if err := json.Unmarshal(rule.Match, &env); err != nil {
		return config.OutputPolicyRule{}, fmt.Errorf("legacyshim: decode output match for %q: %w", rule.ID, err)
	}
	dec, err := decodeDecideEnvelope(rule)
	if err != nil {
		return config.OutputPolicyRule{}, err
	}
	return config.OutputPolicyRule{
		ID:       rule.ID,
		Enabled:  env.Enabled,
		Severity: env.Severity,
		Desc:     rule.Description,
		Match:    env.Match,
		Decision: dec.Decision,
		Reason:   dec.Reason,
	}, nil
}

// RuleToPolicyRule reverses PolicyRuleToVelocityRule. It accepts only
// RuleTypeVelocity rules; the legacy composite PolicyRule has no place in
// the input/output authoring surfaces, and shimming non-velocity types
// through this helper would invent fields the legacy struct does not carry.
func RuleToPolicyRule(rule policy.Rule) (config.PolicyRule, error) {
	if rule.Type != policy.RuleTypeVelocity {
		return config.PolicyRule{}, fmt.Errorf("%w: rule %q has type %q, want %q", ErrRuleTypeMismatch, rule.ID, rule.Type, policy.RuleTypeVelocity)
	}
	var env velocityRuleEnvelope
	if err := json.Unmarshal(rule.Match, &env); err != nil {
		return config.PolicyRule{}, fmt.Errorf("legacyshim: decode velocity match for %q: %w", rule.ID, err)
	}
	dec, err := decodeDecideEnvelope(rule)
	if err != nil {
		return config.PolicyRule{}, err
	}
	return config.PolicyRule{
		ID:           rule.ID,
		Tier:         env.Tier,
		Selector:     env.Selector,
		Match:        env.Match,
		Velocity:     env.Velocity,
		Decision:     dec.Decision,
		Reason:       dec.Reason,
		Constraints:  env.Constraints,
		Remediations: env.Remediations,
	}, nil
}

// DecisionToLegacyPolicyDecision reverses LegacyPolicyDecisionToDecision.
// Constraints, remediations, rule_tier and approval_required ride inside
// Decision.Trace[0].Constraints under the legacyDecisionTraceEnvelope shape;
// when Trace is empty those fields default to zero values.
func DecisionToLegacyPolicyDecision(d policy.Decision) (config.PolicyDecision, error) {
	decisionStr, err := legacyStringFromDecisionType(d.Type)
	if err != nil {
		return config.PolicyDecision{}, fmt.Errorf("legacyshim: decision type %q: %w", d.Type, err)
	}
	pd := config.PolicyDecision{
		Decision: decisionStr,
		RuleID:   d.RuleID,
	}
	if len(d.Trace) > 0 {
		first := d.Trace[0]
		pd.Reason = first.Reason
		if len(first.Constraints) > 0 {
			var env legacyDecisionTraceEnvelope
			if err := json.Unmarshal(first.Constraints, &env); err != nil {
				return config.PolicyDecision{}, fmt.Errorf("legacyshim: decode trace constraints: %w", err)
			}
			pd.Constraints = env.Constraints
			pd.Remediations = env.Remediations
			pd.RuleTier = env.RuleTier
			pd.ApprovalRequired = env.ApprovalRequired
		}
	}
	if d.Type == policy.DecisionRequireHuman {
		pd.ApprovalRequired = true
	}
	return pd, nil
}

// EdgeDecisionFromUnified reverses EdgeDecisionToDecisionType. The mapping
// is lossy in one direction — DecisionAllowWithConstraints maps onto
// CONSTRAIN, but the original may equally have come from CONSTRAIN or from
// a DecisionAllow accompanied by constraints. Callers needing the precise
// edge enum must keep the original alongside the unified Decision; this
// helper is the best-effort reconstruction for the response path.
func EdgeDecisionFromUnified(d policy.Decision) (edge.EdgeDecision, error) {
	switch d.Type {
	case policy.DecisionAllow:
		if traceHasMarker(d.Trace, "recorded") {
			return edge.DecisionRecorded, nil
		}
		return edge.DecisionAllow, nil
	case policy.DecisionDeny:
		return edge.DecisionDeny, nil
	case policy.DecisionRequireHuman:
		return edge.DecisionRequireApproval, nil
	case policy.DecisionThrottle:
		return edge.DecisionThrottle, nil
	case policy.DecisionAllowWithConstraints, policy.DecisionRedact:
		return edge.DecisionConstrain, nil
	case policy.DecisionQuarantine:
		return edge.DecisionDeny, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDecisionType, d.Type)
	}
}

// decodeDecideEnvelope unmarshals Rule.Decide into the {decision, reason}
// envelope shared by all three job-side rule types. It returns a typed
// error wrapping the rule ID so failures point straight at the offending
// authoring record.
func decodeDecideEnvelope(rule policy.Rule) (decideEnvelope, error) {
	var dec decideEnvelope
	if len(rule.Decide) == 0 {
		return dec, nil
	}
	if err := json.Unmarshal(rule.Decide, &dec); err != nil {
		return decideEnvelope{}, fmt.Errorf("legacyshim: decode decide for %q: %w", rule.ID, err)
	}
	return dec, nil
}

// legacyStringFromDecisionType inverts decisionTypeFromLegacyString. The
// require_human → require_approval rename is the only non-identity mapping;
// every other unified value already names itself the legacy way.
func legacyStringFromDecisionType(d policy.DecisionType) (string, error) {
	switch d {
	case policy.DecisionAllow:
		return "allow", nil
	case policy.DecisionDeny:
		return "deny", nil
	case policy.DecisionRequireHuman:
		return "require_approval", nil
	case policy.DecisionThrottle:
		return "throttle", nil
	case policy.DecisionAllowWithConstraints:
		return "allow_with_constraints", nil
	case policy.DecisionQuarantine:
		return "quarantine", nil
	case policy.DecisionRedact:
		return "redact", nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownDecisionType, d)
	}
}

// traceHasMarker reports whether any TraceStep in the slice records the
// given reason marker. Used to distinguish RECORDED (allow with trace
// marker) from ALLOW (allow without marker) on the inverse edge mapping.
func traceHasMarker(trace []policy.TraceStep, marker string) bool {
	for _, step := range trace {
		if step.Reason == marker {
			return true
		}
	}
	return false
}
