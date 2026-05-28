package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The end-to-end behavior #312 cares about: someone authors a policy
// fragment putting `keywords` in rules[].match (where it doesn't belong)
// and ParseSafetyPolicy returns an error that names the right section.
func TestParseSafetyPolicy_KeywordsInRulesSuggestsInputRules(t *testing.T) {
	bad := []byte(`
rules:
  - id: bad-keywords-in-rules
    match:
      topics: [job.x]
      keywords: ["refund"]
    decision: require_approval
    reason: ""
`)
	_, err := ParseSafetyPolicy(bad)
	if err == nil {
		t.Fatal("expected schema validation error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "additionalProperties") {
		t.Fatalf("expected underlying schema error preserved, got: %s", msg)
	}
	if !strings.Contains(msg, "input_rules[].match") {
		t.Errorf("expected hint pointing to input_rules[].match, got: %s", msg)
	}
	if !strings.Contains(msg, "'keywords'") {
		t.Errorf("expected hint to name the offending field 'keywords', got: %s", msg)
	}
	if !strings.Contains(msg, "docs/policy/global-authority.md") {
		t.Errorf("expected hint to link the canonical docs page, got: %s", msg)
	}
}

func TestParseSafetyPolicy_ContentPatternsInRulesSuggestsInputRules(t *testing.T) {
	bad := []byte(`
rules:
  - id: bad-patterns-in-rules
    match:
      topics: ["job.x"]
      content_patterns: ["ignore previous"]
    decision: deny
    reason: ""
`)
	_, err := ParseSafetyPolicy(bad)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "'content_patterns'") || !strings.Contains(err.Error(), "input_rules[].match") {
		t.Errorf("expected content_patterns→input_rules hint, got: %s", err)
	}
}

// Symmetric direction: a policy-only field in input_rules[] should suggest
// the dispatch (rules) section. delegation is policyMatch-only and a clean
// example because it isn't likely to appear in any input_rule by accident.
func TestParseSafetyPolicy_DelegationInInputRulesSuggestsRules(t *testing.T) {
	bad := []byte(`
input_rules:
  - id: bad-delegation-in-input-rules
    severity: high
    match:
      topics: ["job.x"]
      delegation:
        max_depth: 2
    decision: deny
    reason: ""
`)
	_, err := ParseSafetyPolicy(bad)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "'delegation'") || !strings.Contains(err.Error(), "rules[].match") {
		t.Errorf("expected delegation→rules hint, got: %s", err)
	}
}

// Valid policy should still parse fine.
func TestParseSafetyPolicy_ValidPolicyAcceptsBothSections(t *testing.T) {
	good := []byte(`
rules:
  - id: classify-allow
    match: { topics: [job.support.classify] }
    decision: allow
    reason: ""
input_rules:
  - id: send-money-approve
    severity: high
    match:
      topics: [job.support.send]
      keywords: ["refund"]
    decision: require_approval
    reason: ""
`)
	if _, err := ParseSafetyPolicy(good); err != nil {
		t.Fatalf("unexpected error on valid policy: %v", err)
	}
}

// Schema rejections unrelated to match-clause (e.g. an unknown top-level
// field) should pass through unchanged — we must not append a spurious
// "did you mean…" hint when neither rules[] nor input_rules[] is implicated.
func TestEnrichSafetyPolicyValidationError_PassesThroughUnrelated(t *testing.T) {
	original := errors.New(
		"validate safety policy config: schema validation failed: " +
			"jsonschema: '/totally_unknown' does not validate with " +
			"inmemory://safety-policy#/additionalProperties: " +
			"additionalProperties 'totally_unknown' not allowed",
	)
	enriched := enrichSafetyPolicyValidationError(original)
	if enriched.Error() != original.Error() {
		t.Errorf("unrelated error must pass through unchanged.\nbefore: %s\nafter:  %s",
			original, enriched)
	}
}

func TestEnrichSafetyPolicyValidationError_NilIsNil(t *testing.T) {
	if got := enrichSafetyPolicyValidationError(nil); got != nil {
		t.Errorf("nil in → nil out; got %v", got)
	}
}

// Drift guard: every field in policyMatchOnlyFields and inputMatchOnlyFields
// must still be (a) defined in the schema, and (b) NOT in the OTHER side's
// allowlist. If anyone adds a field to the schema and forgets to update the
// sets here, this test fires and points at exactly which one drifted.
func TestEnrichSafetyPolicyValidationError_FieldSetsMatchSchema(t *testing.T) {
	schemaBytes, err := configSchemaFS.ReadFile(safetyPolicySchemaFile)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(schemaBytes, &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	defs, _ := doc["definitions"].(map[string]any)
	if defs == nil {
		t.Fatal("schema missing definitions")
	}
	policyMatch := matchProperties(t, defs, "policyMatch")
	inputMatch := matchProperties(t, defs, "inputMatch")

	for field := range policyMatchOnlyFields {
		if _, ok := policyMatch[field]; !ok {
			t.Errorf("policyMatchOnlyFields lists %q but the schema's policyMatch does not", field)
		}
		if _, ok := inputMatch[field]; ok {
			t.Errorf("policyMatchOnlyFields lists %q but it's ALSO in inputMatch — it isn't exclusive", field)
		}
	}
	for field := range inputMatchOnlyFields {
		if _, ok := inputMatch[field]; !ok {
			t.Errorf("inputMatchOnlyFields lists %q but the schema's inputMatch does not", field)
		}
		if _, ok := policyMatch[field]; ok {
			t.Errorf("inputMatchOnlyFields lists %q but it's ALSO in policyMatch — it isn't exclusive", field)
		}
	}
}

func matchProperties(t *testing.T, defs map[string]any, name string) map[string]any {
	t.Helper()
	d, _ := defs[name].(map[string]any)
	if d == nil {
		t.Fatalf("schema missing definition %q", name)
	}
	props, _ := d["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("schema definition %q has no properties", name)
	}
	return props
}
