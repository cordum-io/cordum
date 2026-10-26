package config

import (
	"reflect"
	"testing"
)

// TestSafetyPolicyAcceptsDeprecatedConstraintFields pins the backward
// compatibility of max_output_bytes, allowed_destinations and redact_patterns
// under rule.constraints. task-e4e9489c added them to the schema and the docs,
// so policies that use them must keep loading. They are deprecated and
// ignored: PolicyConstraints (and the CAP PolicyConstraints proto) has no such
// fields, so the parser drops them and the Safety Kernel never enforces them.
func TestSafetyPolicyAcceptsDeprecatedConstraintFields(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-result-gating
    decision: allow_with_constraints
    reason: enforce output gating
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: 65536
      allowed_destinations:
        - "file://workspace/*"
        - "s3://artifacts/*"
      redact_patterns:
        - "secret_[a-z0-9]+"
        - "(?i)password=\\S+"
`
	policy, err := ParseSafetyPolicy([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseSafetyPolicy(deprecated constraints) error = %v; want nil", err)
	}
	if got := policy.Rules[0].Constraints; !reflect.ValueOf(got).IsZero() {
		t.Errorf("Constraints = %+v; want zero value (deprecated keys are ignored)", got)
	}
}

// TestSafetyPolicyDeprecatedMaxOutputBytesBounds keeps the original 0..16 MiB
// schema bounds, so existing policies validate exactly as they did before.
func TestSafetyPolicyDeprecatedMaxOutputBytesBounds(t *testing.T) {
	for _, tc := range []struct {
		value  string
		wantOK bool
	}{
		{"0", true},
		{"16777216", true},
		{"16777217", false},
		{"-1", false},
	} {
		yaml := `
version: "1"
rules:
  - id: rule-bound
    decision: allow_with_constraints
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: ` + tc.value + `
`
		if _, err := ParseSafetyPolicy([]byte(yaml)); (err == nil) != tc.wantOK {
			t.Errorf("ParseSafetyPolicy(max_output_bytes: %s) error = %v; want accepted = %v", tc.value, err, tc.wantOK)
		}
	}
}

// TestSafetyPolicyAcceptsSupportedConstraints verifies the constraint keys
// PolicyConstraints actually parses keep validating and reach the struct.
func TestSafetyPolicyAcceptsSupportedConstraints(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-legacy
    decision: allow_with_constraints
    match:
      topics: ["job.pack.action"]
    constraints:
      redaction_level: "moderate"
      budgets:
        max_runtime_ms: 30000
`
	policy, err := ParseSafetyPolicy([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseSafetyPolicy(supported constraints) error = %v; want nil", err)
	}
	if got := policy.Rules[0].Constraints.Budgets.MaxRuntimeMs; got != 30000 {
		t.Errorf("Budgets.MaxRuntimeMs = %d; want 30000", got)
	}
}

// TestSafetyPolicyRejectsUnknownConstraintFields confirms the strict
// additionalProperties:false is preserved — typos like max_output_byte
// (missing 's') still fail.
func TestSafetyPolicyRejectsUnknownConstraintFields(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-typo
    decision: allow_with_constraints
    match:
      topics: ["job.pack.action"]
    constraints:
      max_output_byte: 1024
`
	if _, err := ParseSafetyPolicy([]byte(yaml)); err == nil {
		t.Error("ParseSafetyPolicy(typo'd field) returned nil; want unknown-field error")
	}
}
