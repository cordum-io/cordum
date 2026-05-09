package policy

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel errors returned by the Parse* functions in this package. Use
// errors.Is to distinguish between unknown-value and empty-value cases when
// callers need branchy error handling; otherwise the wrapped string contains
// the rejected value verbatim for log emission.
var (
	ErrInvalidRuleType       = errors.New("policy: invalid rule type")
	ErrInvalidRuleStatus     = errors.New("policy: invalid rule status")
	ErrInvalidDecisionType   = errors.New("policy: invalid decision type")
	ErrInvalidDecisionSource = errors.New("policy: invalid decision source")
	ErrInvalidRuleScopeKind  = errors.New("policy: invalid rule scope kind")
	ErrInvalidEdgeMode       = errors.New("policy: invalid edge mode")
)

// RuleType discriminates a rule's match/decide payload schema. The four
// values correspond to the existing job (input/output/velocity) and edge
// authoring surfaces being unified by this package.
type RuleType string

const (
	RuleTypeInput    RuleType = "input"
	RuleTypeOutput   RuleType = "output"
	RuleTypeVelocity RuleType = "velocity"
	RuleTypeEdge     RuleType = "edge"
)

func (t RuleType) String() string { return string(t) }

// ParseRuleType parses a wire-format rule type. It rejects the empty string
// and any non-canonical value (including case variants and whitespace-padded
// forms) with an error wrapping ErrInvalidRuleType.
func ParseRuleType(s string) (RuleType, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidRuleType)
	}
	switch RuleType(s) {
	case RuleTypeInput, RuleTypeOutput, RuleTypeVelocity, RuleTypeEdge:
		return RuleType(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRuleType, s)
	}
}

func (t RuleType) MarshalJSON() ([]byte, error) {
	if _, err := ParseRuleType(string(t)); err != nil {
		return nil, err
	}
	return json.Marshal(string(t))
}

func (t *RuleType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseRuleType(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// RuleStatus tracks a rule's lifecycle state. Authors edit `draft` rules,
// `published` rules participate in evaluation, and `deprecated` rules remain
// queryable for audit and rollback but are skipped by evaluators.
type RuleStatus string

const (
	RuleStatusDraft      RuleStatus = "draft"
	RuleStatusPublished  RuleStatus = "published"
	RuleStatusDeprecated RuleStatus = "deprecated"
)

func (s RuleStatus) String() string { return string(s) }

func ParseRuleStatus(s string) (RuleStatus, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidRuleStatus)
	}
	switch RuleStatus(s) {
	case RuleStatusDraft, RuleStatusPublished, RuleStatusDeprecated:
		return RuleStatus(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRuleStatus, s)
	}
}

func (s RuleStatus) MarshalJSON() ([]byte, error) {
	if _, err := ParseRuleStatus(string(s)); err != nil {
		return nil, err
	}
	return json.Marshal(string(s))
}

func (s *RuleStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseRuleStatus(raw)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// DecisionType is the unified decision outcome emitted by either evaluator.
// It is the union of the existing safetykernel SafetyDecision, output-rule
// decision, and edge EdgeDecision values, normalised to a single canonical
// set. The seven values mirror the wire-format DecisionType enum in
// cap/proto/cordum/agent/v1/safety.proto: ALLOW, DENY, REQUIRE_HUMAN,
// THROTTLE, ALLOW_WITH_CONSTRAINTS, QUARANTINE, REDACT.
type DecisionType string

const (
	DecisionAllow                DecisionType = "allow"
	DecisionDeny                 DecisionType = "deny"
	DecisionRequireHuman         DecisionType = "require_human"
	DecisionThrottle             DecisionType = "throttle"
	DecisionAllowWithConstraints DecisionType = "allow_with_constraints"
	DecisionQuarantine           DecisionType = "quarantine"
	DecisionRedact               DecisionType = "redact"
)

func (d DecisionType) String() string { return string(d) }

func ParseDecisionType(s string) (DecisionType, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidDecisionType)
	}
	switch DecisionType(s) {
	case DecisionAllow, DecisionDeny, DecisionRequireHuman, DecisionThrottle,
		DecisionAllowWithConstraints, DecisionQuarantine, DecisionRedact:
		return DecisionType(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidDecisionType, s)
	}
}

func (d DecisionType) MarshalJSON() ([]byte, error) {
	if _, err := ParseDecisionType(string(d)); err != nil {
		return nil, err
	}
	return json.Marshal(string(d))
}

func (d *DecisionType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseDecisionType(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// DecisionSource identifies which evaluator produced a Decision: the
// safetykernel job pipeline (`job`) or the edge classifier path (`edge`).
type DecisionSource string

const (
	DecisionSourceJob  DecisionSource = "job"
	DecisionSourceEdge DecisionSource = "edge"
)

func (s DecisionSource) String() string { return string(s) }

func ParseDecisionSource(s string) (DecisionSource, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidDecisionSource)
	}
	switch DecisionSource(s) {
	case DecisionSourceJob, DecisionSourceEdge:
		return DecisionSource(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidDecisionSource, s)
	}
}

func (s DecisionSource) MarshalJSON() ([]byte, error) {
	if _, err := ParseDecisionSource(string(s)); err != nil {
		return nil, err
	}
	return json.Marshal(string(s))
}

func (s *DecisionSource) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseDecisionSource(raw)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// RuleScopeKind narrows where a rule applies. `global` covers an entire
// deployment; `tenant` and `workflow` scope to job-side bindings; `edge_fleet`
// and `edge_user` scope to edge-side bindings (whole fleet vs single user).
type RuleScopeKind string

const (
	RuleScopeGlobal    RuleScopeKind = "global"
	RuleScopeTenant    RuleScopeKind = "tenant"
	RuleScopeWorkflow  RuleScopeKind = "workflow"
	RuleScopeEdgeFleet RuleScopeKind = "edge_fleet"
	RuleScopeEdgeUser  RuleScopeKind = "edge_user"
)

func (k RuleScopeKind) String() string { return string(k) }

func ParseRuleScopeKind(s string) (RuleScopeKind, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidRuleScopeKind)
	}
	switch RuleScopeKind(s) {
	case RuleScopeGlobal, RuleScopeTenant, RuleScopeWorkflow, RuleScopeEdgeFleet, RuleScopeEdgeUser:
		return RuleScopeKind(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRuleScopeKind, s)
	}
}

func (k RuleScopeKind) MarshalJSON() ([]byte, error) {
	if _, err := ParseRuleScopeKind(string(k)); err != nil {
		return nil, err
	}
	return json.Marshal(string(k))
}

func (k *RuleScopeKind) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseRuleScopeKind(s)
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// EdgeMode is the per-bundle enforcement posture for the edge evaluator. It
// migrates today's global EdgePolicyMode switch onto bundle metadata so
// different fleets and users can run at different modes simultaneously. See
// spec § "Edge bundle metadata" (cordum/docs/specs/policy-studio-rewrite.md).
type EdgeMode string

const (
	EdgeModeObserve          EdgeMode = "observe"
	EdgeModeEnforce          EdgeMode = "enforce"
	EdgeModeEnterpriseStrict EdgeMode = "enterprise-strict"
)

func (m EdgeMode) String() string { return string(m) }

func ParseEdgeMode(s string) (EdgeMode, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidEdgeMode)
	}
	switch EdgeMode(s) {
	case EdgeModeObserve, EdgeModeEnforce, EdgeModeEnterpriseStrict:
		return EdgeMode(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidEdgeMode, s)
	}
}

func (m EdgeMode) MarshalJSON() ([]byte, error) {
	if _, err := ParseEdgeMode(string(m)); err != nil {
		return nil, err
	}
	return json.Marshal(string(m))
}

func (m *EdgeMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseEdgeMode(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
