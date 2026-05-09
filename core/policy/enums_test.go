package policy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRuleType_StringAndParse(t *testing.T) {
	cases := []struct {
		val  RuleType
		wire string
	}{
		{RuleTypeInput, "input"},
		{RuleTypeOutput, "output"},
		{RuleTypeVelocity, "velocity"},
		{RuleTypeEdge, "edge"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseRuleType(tc.wire)
		if err != nil {
			t.Fatalf("ParseRuleType(%q) err = %v, want nil", tc.wire, err)
		}
		if parsed != tc.val {
			t.Fatalf("ParseRuleType(%q) = %v, want %v", tc.wire, parsed, tc.val)
		}
	}
}

func TestRuleType_ParseRejects(t *testing.T) {
	rejects := []string{"", " ", "input ", " input", "INPUT", "Input", "iNpUt", "unknown", "input2"}
	for _, in := range rejects {
		_, err := ParseRuleType(in)
		if err == nil {
			t.Fatalf("ParseRuleType(%q) err = nil, want non-nil", in)
		}
		if !errors.Is(err, ErrInvalidRuleType) {
			t.Fatalf("ParseRuleType(%q) err %v does not wrap ErrInvalidRuleType", in, err)
		}
	}
}

func TestRuleType_MarshalJSON_RejectsZero(t *testing.T) {
	var zero RuleType
	if _, err := json.Marshal(zero); err == nil {
		t.Fatalf("json.Marshal(zero RuleType) err = nil, want non-nil — zero value must not serialize")
	}
}

func TestRuleType_UnmarshalJSON(t *testing.T) {
	var v RuleType
	if err := json.Unmarshal([]byte(`"input"`), &v); err != nil {
		t.Fatalf("Unmarshal valid err = %v, want nil", err)
	}
	if v != RuleTypeInput {
		t.Fatalf("Unmarshal valid = %v, want RuleTypeInput", v)
	}
	var bad RuleType
	if err := json.Unmarshal([]byte(`"INPUT"`), &bad); err == nil {
		t.Fatalf("Unmarshal of non-canonical case must fail")
	}
	if err := json.Unmarshal([]byte(`""`), &bad); err == nil {
		t.Fatalf("Unmarshal of empty string must fail")
	}
}

func TestRuleStatus_StringAndParse(t *testing.T) {
	cases := []struct {
		val  RuleStatus
		wire string
	}{
		{RuleStatusDraft, "draft"},
		{RuleStatusPublished, "published"},
		{RuleStatusDeprecated, "deprecated"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseRuleStatus(tc.wire)
		if err != nil || parsed != tc.val {
			t.Fatalf("ParseRuleStatus(%q) = (%v, %v), want (%v, nil)", tc.wire, parsed, err, tc.val)
		}
	}
	for _, in := range []string{"", " ", "DRAFT", "Published", "publish"} {
		if _, err := ParseRuleStatus(in); err == nil || !errors.Is(err, ErrInvalidRuleStatus) {
			t.Fatalf("ParseRuleStatus(%q) err = %v, want wrapped ErrInvalidRuleStatus", in, err)
		}
	}
}

func TestDecisionType_StringAndParse(t *testing.T) {
	cases := []struct {
		val  DecisionType
		wire string
	}{
		{DecisionAllow, "allow"},
		{DecisionDeny, "deny"},
		{DecisionRequireHuman, "require_human"},
		{DecisionThrottle, "throttle"},
		{DecisionAllowWithConstraints, "allow_with_constraints"},
		{DecisionQuarantine, "quarantine"},
		{DecisionRedact, "redact"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseDecisionType(tc.wire)
		if err != nil || parsed != tc.val {
			t.Fatalf("ParseDecisionType(%q) = (%v, %v), want (%v, nil)", tc.wire, parsed, err, tc.val)
		}
		// Verify wire-format parity with safety.proto enum (DecisionType
		// values 6 and 7 were appended by epic-d9a6c0a1).
		if tc.val == DecisionQuarantine && tc.wire != "quarantine" {
			t.Fatalf("quarantine wire form drifted")
		}
		if tc.val == DecisionRedact && tc.wire != "redact" {
			t.Fatalf("redact wire form drifted")
		}
	}
	for _, in := range []string{"", " ", "ALLOW", "Deny", "require-human", "approve", "block"} {
		if _, err := ParseDecisionType(in); err == nil || !errors.Is(err, ErrInvalidDecisionType) {
			t.Fatalf("ParseDecisionType(%q) err = %v, want wrapped ErrInvalidDecisionType", in, err)
		}
	}
}

func TestDecisionType_RoundTripIncludesNewWireValues(t *testing.T) {
	for _, val := range []DecisionType{DecisionAllowWithConstraints, DecisionQuarantine, DecisionRedact} {
		raw, err := json.Marshal(val)
		if err != nil {
			t.Fatalf("Marshal(%v) err = %v", val, err)
		}
		var got DecisionType
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal(%s) err = %v", raw, err)
		}
		if got != val {
			t.Fatalf("round-trip drift: %v -> %s -> %v", val, raw, got)
		}
	}
}

func TestDecisionSource_StringAndParse(t *testing.T) {
	cases := []struct {
		val  DecisionSource
		wire string
	}{
		{DecisionSourceJob, "job"},
		{DecisionSourceEdge, "edge"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseDecisionSource(tc.wire)
		if err != nil || parsed != tc.val {
			t.Fatalf("ParseDecisionSource(%q) = (%v, %v), want (%v, nil)", tc.wire, parsed, err, tc.val)
		}
	}
	for _, in := range []string{"", " ", "JOB", "Edge", "agent"} {
		if _, err := ParseDecisionSource(in); err == nil || !errors.Is(err, ErrInvalidDecisionSource) {
			t.Fatalf("ParseDecisionSource(%q) err = %v, want wrapped ErrInvalidDecisionSource", in, err)
		}
	}
}

func TestRuleScopeKind_StringAndParse(t *testing.T) {
	cases := []struct {
		val  RuleScopeKind
		wire string
	}{
		{RuleScopeGlobal, "global"},
		{RuleScopeTenant, "tenant"},
		{RuleScopeWorkflow, "workflow"},
		{RuleScopeEdgeFleet, "edge_fleet"},
		{RuleScopeEdgeUser, "edge_user"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseRuleScopeKind(tc.wire)
		if err != nil || parsed != tc.val {
			t.Fatalf("ParseRuleScopeKind(%q) = (%v, %v), want (%v, nil)", tc.wire, parsed, err, tc.val)
		}
	}
	for _, in := range []string{"", " ", "GLOBAL", "Tenant", "edge-fleet", "edgefleet"} {
		if _, err := ParseRuleScopeKind(in); err == nil || !errors.Is(err, ErrInvalidRuleScopeKind) {
			t.Fatalf("ParseRuleScopeKind(%q) err = %v, want wrapped ErrInvalidRuleScopeKind", in, err)
		}
	}
}

func TestEdgeMode_StringAndParse(t *testing.T) {
	cases := []struct {
		val  EdgeMode
		wire string
	}{
		{EdgeModeObserve, "observe"},
		{EdgeModeEnforce, "enforce"},
		{EdgeModeEnterpriseStrict, "enterprise-strict"},
	}
	for _, tc := range cases {
		if got := tc.val.String(); got != tc.wire {
			t.Fatalf("%v.String() = %q, want %q", tc.val, got, tc.wire)
		}
		parsed, err := ParseEdgeMode(tc.wire)
		if err != nil || parsed != tc.val {
			t.Fatalf("ParseEdgeMode(%q) = (%v, %v), want (%v, nil)", tc.wire, parsed, err, tc.val)
		}
	}
	// Reject unknown + case + the common typo "enterprise_strict" (underscore vs hyphen).
	for _, in := range []string{"", " ", "OBSERVE", "Enforce", "enterprise_strict", "strict"} {
		if _, err := ParseEdgeMode(in); err == nil || !errors.Is(err, ErrInvalidEdgeMode) {
			t.Fatalf("ParseEdgeMode(%q) err = %v, want wrapped ErrInvalidEdgeMode", in, err)
		}
	}
	// MarshalJSON round-trip.
	raw, err := json.Marshal(EdgeModeEnterpriseStrict)
	if err != nil {
		t.Fatalf("Marshal err = %v", err)
	}
	if string(raw) != `"enterprise-strict"` {
		t.Fatalf("Marshal = %s, want \"enterprise-strict\"", raw)
	}
	var back EdgeMode
	if err := json.Unmarshal(raw, &back); err != nil || back != EdgeModeEnterpriseStrict {
		t.Fatalf("Unmarshal round-trip failed: err=%v back=%v", err, back)
	}
}

func TestEnumErrorMessagesContainRejectedValue(t *testing.T) {
	// Every Parse* error message should include the rejected value verbatim
	// for log-emission purposes (architect rail: "wrapped string contains the
	// rejected value verbatim").
	_, err := ParseRuleType("bogus_kind")
	if err == nil || !strings.Contains(err.Error(), `"bogus_kind"`) {
		t.Fatalf("ParseRuleType error %v missing rejected value", err)
	}
	_, err = ParseEdgeMode("WRONG")
	if err == nil || !strings.Contains(err.Error(), `"WRONG"`) {
		t.Fatalf("ParseEdgeMode error %v missing rejected value", err)
	}
}
