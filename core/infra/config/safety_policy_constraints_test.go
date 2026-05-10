package config

import (
	"strings"
	"testing"
)

// TestSafetyPolicyAcceptsNewConstraintFields verifies the schema extension
// permits max_output_bytes, allowed_destinations, and redact_patterns
// under rule.constraints. These are the CONSTRAIN-extension primitives
// the unified Decision.Type = allow_with_constraints path emits.
func TestSafetyPolicyAcceptsNewConstraintFields(t *testing.T) {
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
		t.Fatalf("ParseSafetyPolicy() error = %v; want nil", err)
	}
	if policy == nil {
		t.Fatal("ParseSafetyPolicy() returned nil policy")
	}
}

// TestSafetyPolicyAcceptsZeroMaxOutputBytes verifies the minimum bound
// (0) is honored — zero is a valid policy meaning "no output allowed".
func TestSafetyPolicyAcceptsZeroMaxOutputBytes(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-zero-budget
    decision: allow_with_constraints
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: 0
`
	if _, err := ParseSafetyPolicy([]byte(yaml)); err != nil {
		t.Errorf("ParseSafetyPolicy(zero max_output_bytes) error = %v; want nil", err)
	}
}

// TestSafetyPolicyRejectsOversizedMaxOutputBytes enforces the 16MiB cap.
// >16777216 must be rejected by the schema.
func TestSafetyPolicyRejectsOversizedMaxOutputBytes(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-oversized
    decision: allow_with_constraints
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: 16777217
`
	_, err := ParseSafetyPolicy([]byte(yaml))
	if err == nil {
		t.Fatal("ParseSafetyPolicy(>16MiB max_output_bytes) returned nil error; want schema error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "max_output_bytes") &&
		!strings.Contains(strings.ToLower(err.Error()), "maximum") {
		t.Errorf("ParseSafetyPolicy(>16MiB) error = %v; want max-bound error", err)
	}
}

// TestSafetyPolicyRejectsNegativeMaxOutputBytes enforces the minimum bound.
func TestSafetyPolicyRejectsNegativeMaxOutputBytes(t *testing.T) {
	yaml := `
version: "1"
default_tenant: default
rules:
  - id: rule-negative
    decision: allow_with_constraints
    match:
      topics: ["job.openclaw.result_gating"]
    constraints:
      max_output_bytes: -1
`
	if _, err := ParseSafetyPolicy([]byte(yaml)); err == nil {
		t.Error("ParseSafetyPolicy(negative max_output_bytes) returned nil error; want minimum-bound error")
	}
}

// TestSafetyPolicyBackcompatNoNewFields verifies that policies without
// the new constraint fields keep validating. Crucial for the additive-
// only contract — existing safety policies in the wild must not break.
func TestSafetyPolicyBackcompatNoNewFields(t *testing.T) {
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
	if _, err := ParseSafetyPolicy([]byte(yaml)); err != nil {
		t.Errorf("ParseSafetyPolicy(legacy constraints) error = %v; want nil", err)
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
