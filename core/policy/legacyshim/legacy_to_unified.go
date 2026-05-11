package legacyshim

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
)

// ErrUnknownLegacyDecision is returned when a legacy decision string falls
// outside the documented set the unified evaluator accepts. Callers use
// errors.Is to detect this case without sniffing for the wrapped value.
var ErrUnknownLegacyDecision = errors.New("legacyshim: unknown legacy decision")

// ErrUnknownEdgeDecision is returned when an edge.EdgeDecision value cannot
// be mapped onto a unified DecisionType. The mapping is defined by the table
// in EdgeDecisionToDecisionType; values outside that table fail loudly here.
var ErrUnknownEdgeDecision = errors.New("legacyshim: unknown edge decision")

// inputRuleEnvelope packages the legacy InputPolicyRule envelope fields that
// do not have a first-class home on the unified Rule struct. Stored verbatim
// inside Rule.Match (json.RawMessage) so the inverse RuleToInputPolicyRule
// can reconstruct the legacy struct without information loss.
type inputRuleEnvelope struct {
	Tier     string                  `json:"tier,omitempty"`
	Selector config.PolicySelector   `json:"selector,omitzero"`
	Enabled  *bool                   `json:"enabled,omitempty"`
	Severity string                  `json:"severity,omitempty"`
	Match    config.InputPolicyMatch `json:"match"`
}

// outputRuleEnvelope is the OutputPolicyRule analogue of inputRuleEnvelope.
type outputRuleEnvelope struct {
	Enabled  *bool                    `json:"enabled,omitempty"`
	Severity string                   `json:"severity,omitempty"`
	Match    config.OutputPolicyMatch `json:"match"`
}

// velocityRuleEnvelope wraps the composite legacy PolicyRule (the velocity
// authoring surface). Match, Velocity, Constraints, Remediations and the
// tier+selector envelope live here so the unified Rule can carry the full
// legacy authoring intent under Type=velocity without dropping fields.
type velocityRuleEnvelope struct {
	Tier         string                     `json:"tier,omitempty"`
	Selector     config.PolicySelector      `json:"selector,omitzero"`
	Match        config.PolicyMatch         `json:"match"`
	Velocity     *config.VelocityConfig     `json:"velocity,omitempty"`
	Constraints  config.PolicyConstraints   `json:"constraints,omitzero"`
	Remediations []config.PolicyRemediation `json:"remediations,omitempty"`
}

// decideEnvelope is the common JSON shape stored under Rule.Decide for the
// three job-side rule types. It carries the legacy decision string verbatim
// so RuleTo* round-trips do not need to re-encode the canonical values.
type decideEnvelope struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// InputPolicyRuleToRule packs a legacy InputPolicyRule into the unified
// policy.Rule shape. The legacy envelope (tier, selector, severity, enabled)
// and the InputPolicyMatch payload are JSON-encoded into Rule.Match;
// {decision, reason} live under Rule.Decide. Rule.Status defaults to
// published — legacy authoring did not track lifecycle, and the legacy
// evaluator only fires on enabled-by-default rules.
func InputPolicyRuleToRule(old config.InputPolicyRule) (policy.Rule, error) {
	scope, err := scopeFromLegacyTierSelector(old.Tier, old.Selector)
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: input rule %q: %w", old.ID, err)
	}
	matchBytes, err := json.Marshal(inputRuleEnvelope{
		Tier:     old.Tier,
		Selector: old.Selector,
		Enabled:  old.Enabled,
		Severity: old.Severity,
		Match:    old.Match,
	})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode input match for %q: %w", old.ID, err)
	}
	decideBytes, err := json.Marshal(decideEnvelope{Decision: old.Decision, Reason: old.Reason})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode input decide for %q: %w", old.ID, err)
	}
	return policy.Rule{
		ID:          old.ID,
		Name:        old.ID,
		Type:        policy.RuleTypeInput,
		Scope:       scope,
		Status:      policy.RuleStatusPublished,
		Match:       matchBytes,
		Decide:      decideBytes,
		Description: old.Desc,
	}, nil
}

// OutputPolicyRuleToRule mirrors InputPolicyRuleToRule for OutputPolicyRule.
// The output authoring surface has no Tier/Selector — output rules are
// global by construction in the legacy evaluator — so Rule.Scope is set to
// Kind=global with no value.
func OutputPolicyRuleToRule(old config.OutputPolicyRule) (policy.Rule, error) {
	matchBytes, err := json.Marshal(outputRuleEnvelope{
		Enabled:  old.Enabled,
		Severity: old.Severity,
		Match:    old.Match,
	})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode output match for %q: %w", old.ID, err)
	}
	decideBytes, err := json.Marshal(decideEnvelope{Decision: old.Decision, Reason: old.Reason})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode output decide for %q: %w", old.ID, err)
	}
	return policy.Rule{
		ID:          old.ID,
		Name:        old.ID,
		Type:        policy.RuleTypeOutput,
		Scope:       policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Status:      policy.RuleStatusPublished,
		Match:       matchBytes,
		Decide:      decideBytes,
		Description: old.Desc,
	}, nil
}

// PolicyRuleToVelocityRule packs the composite legacy PolicyRule (the
// Velocity authoring surface) into the unified shape. The function returns
// an error when Velocity is nil — the legacy PolicyRule struct doubles as
// both a generic "matcher rule" and a velocity-specific rule, but only the
// latter is in scope for this shim path. Generic-matcher rules must be
// converted via the dedicated bundle-CRUD paths instead.
func PolicyRuleToVelocityRule(old config.PolicyRule) (policy.Rule, error) {
	if old.Velocity == nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: policy rule %q is not a velocity rule", old.ID)
	}
	scope, err := scopeFromLegacyTierSelector(old.Tier, old.Selector)
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: velocity rule %q: %w", old.ID, err)
	}
	matchBytes, err := json.Marshal(velocityRuleEnvelope{
		Tier:         old.Tier,
		Selector:     old.Selector,
		Match:        old.Match,
		Velocity:     old.Velocity,
		Constraints:  old.Constraints,
		Remediations: old.Remediations,
	})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode velocity match for %q: %w", old.ID, err)
	}
	decideBytes, err := json.Marshal(decideEnvelope{Decision: old.Decision, Reason: old.Reason})
	if err != nil {
		return policy.Rule{}, fmt.Errorf("legacyshim: encode velocity decide for %q: %w", old.ID, err)
	}
	return policy.Rule{
		ID:     old.ID,
		Name:   old.ID,
		Type:   policy.RuleTypeVelocity,
		Scope:  scope,
		Status: policy.RuleStatusPublished,
		Match:  matchBytes,
		Decide: decideBytes,
	}, nil
}

// EdgePolicyModeToBundleMetadata maps the legacy global edge enforcement
// mode string ("observe" / "enforce" / "enterprise-strict") onto the
// per-bundle BundleMetadata.EdgeMode. An empty / unknown input yields a
// zero BundleMetadata so callers can still populate a bundle without
// inheriting a phantom mode.
func EdgePolicyModeToBundleMetadata(mode string) policy.BundleMetadata {
	parsed, err := policy.ParseEdgeMode(strings.TrimSpace(mode))
	if err != nil {
		return policy.BundleMetadata{}
	}
	return policy.BundleMetadata{EdgeMode: parsed}
}

// EdgeDecisionToDecisionType is the canonical edge → unified decision-type
// mapping. CONSTRAIN maps onto allow_with_constraints (the unified analogue
// for "allowed with policy adjustments"); RECORDED maps onto allow because
// the unified evaluator records the decision via Decision.Trace rather than
// a dedicated decision value.
func EdgeDecisionToDecisionType(d edge.EdgeDecision) (policy.DecisionType, error) {
	switch d {
	case edge.DecisionAllow:
		return policy.DecisionAllow, nil
	case edge.DecisionDeny:
		return policy.DecisionDeny, nil
	case edge.DecisionRequireApproval:
		return policy.DecisionRequireHuman, nil
	case edge.DecisionThrottle:
		return policy.DecisionThrottle, nil
	case edge.DecisionConstrain:
		return policy.DecisionAllowWithConstraints, nil
	case edge.DecisionRecorded:
		return policy.DecisionAllow, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownEdgeDecision, d)
	}
}

// LegacyPolicyDecisionToDecision packs a legacy config.PolicyDecision into
// a unified policy.Decision so callers that have only the legacy result
// shape can still emit on the unified decision stream. The mapping is
// lossless for the documented values; constraints, remediations, rule_tier
// and approval_required fields ride along inside Trace[0].Constraints as a
// JSON envelope keyed by "constraints", "remediations", "rule_tier" and
// "approval_required" so the inverse helper can reconstruct them.
func LegacyPolicyDecisionToDecision(pd config.PolicyDecision) (policy.Decision, error) {
	dt, err := decisionTypeFromLegacyString(pd.Decision)
	if err != nil {
		return policy.Decision{}, fmt.Errorf("legacyshim: legacy decision %q: %w", pd.Decision, err)
	}
	traceEnv := legacyDecisionTraceEnvelope{
		Constraints:      pd.Constraints,
		Remediations:     pd.Remediations,
		RuleTier:         pd.RuleTier,
		ApprovalRequired: pd.ApprovalRequired,
	}
	traceBytes, err := json.Marshal(traceEnv)
	if err != nil {
		return policy.Decision{}, fmt.Errorf("legacyshim: encode trace constraints: %w", err)
	}
	return policy.Decision{
		Source: policy.DecisionSourceJob,
		RuleID: pd.RuleID,
		Type:   dt,
		Trace: []policy.TraceStep{{
			RuleID:       pd.RuleID,
			DecisionType: dt,
			Reason:       pd.Reason,
			Constraints:  traceBytes,
		}},
	}, nil
}

// scopeFromLegacyTierSelector projects the legacy tier+selector pair onto
// the unified RuleScope. tier=global → kind=global; tier=workflow with a
// workflow_id → kind=workflow; tier=job with a workflow_id → kind=workflow
// (legacy job-tier rules implicitly inherit a workflow scope via the
// authoring UI). An empty / unknown tier yields a global scope, matching
// config.NormalizePolicyTier.
func scopeFromLegacyTierSelector(tier string, sel config.PolicySelector) (policy.RuleScope, error) {
	switch config.NormalizePolicyTier(tier) {
	case config.PolicyTierGlobal:
		return policy.RuleScope{Kind: policy.RuleScopeGlobal}, nil
	case config.PolicyTierWorkflow:
		if strings.TrimSpace(sel.WorkflowID) == "" {
			return policy.RuleScope{}, errors.New("workflow tier requires selector.workflow_id")
		}
		return policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: sel.WorkflowID}, nil
	case config.PolicyTierJob:
		if strings.TrimSpace(sel.WorkflowID) == "" {
			return policy.RuleScope{}, errors.New("job tier requires selector.workflow_id for unified-scope projection")
		}
		return policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: sel.WorkflowID}, nil
	default:
		return policy.RuleScope{Kind: policy.RuleScopeGlobal}, nil
	}
}

// decisionTypeFromLegacyString maps the legacy decision string used by both
// InputPolicyRule.Decision (deny|require_approval) and the composite
// PolicyRule.Decision (allow|deny|require_approval|allow_with_constraints|
// throttle), plus OutputPolicyRule additions (quarantine|redact), onto the
// unified policy.DecisionType.
func decisionTypeFromLegacyString(s string) (policy.DecisionType, error) {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "allow":
		return policy.DecisionAllow, nil
	case "deny":
		return policy.DecisionDeny, nil
	case "require_approval":
		return policy.DecisionRequireHuman, nil
	case "throttle":
		return policy.DecisionThrottle, nil
	case "allow_with_constraints":
		return policy.DecisionAllowWithConstraints, nil
	case "quarantine":
		return policy.DecisionQuarantine, nil
	case "redact":
		return policy.DecisionRedact, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownLegacyDecision, s)
	}
}
