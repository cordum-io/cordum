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

// Drift guard: every field in policyMatchOnlyFields / inputMatchOnlyFields
// must still be (a) defined in the schema and (b) NOT in the OTHER side's
// definition. AND — bidirectionally — every field that's actually exclusive
// to one match definition in the schema must appear in the corresponding
// hint set. The CodeRabbit major finding on #316 pointed out the one-way
// check above silently passes if someone adds a NEW exclusive field to the
// schema and forgets the hint set; the bidirectional check below catches
// that case too. Either drift direction fires this test with a precise
// pointer at the field that diverged.
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

	// Direction 1 — every entry in our hint sets must be a real schema
	// field AND must not also appear in the OTHER definition (otherwise
	// it isn't exclusive, and the hint would mislead).
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

	// Direction 2 — walk the SCHEMA. Every field that's exclusive to one
	// definition must appear in the matching hint set; otherwise the hint
	// won't fire when a user puts it in the wrong section. This catches
	// the case where the schema gains a new exclusive field but the hint
	// sets are left behind.
	for field := range policyMatch {
		if _, alsoInInput := inputMatch[field]; alsoInInput {
			continue // shared field; no exclusive hint applies
		}
		if _, listed := policyMatchOnlyFields[field]; !listed {
			t.Errorf(
				"schema field %q is exclusive to policyMatch but missing from policyMatchOnlyFields — "+
					"hint won't fire if a user puts it in input_rules[].match", field)
		}
	}
	for field := range inputMatch {
		if _, alsoInPolicy := policyMatch[field]; alsoInPolicy {
			continue
		}
		if _, listed := inputMatchOnlyFields[field]; !listed {
			t.Errorf(
				"schema field %q is exclusive to inputMatch but missing from inputMatchOnlyFields — "+
					"hint won't fire if a user puts it in rules[].match", field)
		}
	}
}

// Regression for the second CodeRabbit major finding on #316. The previous
// global "is /properties/rules/items AND /properties/input_rules/items in
// the message?" check returned "" (ambiguous) when one validation error
// contained rejections in BOTH sections — dropping every otherwise-valid
// hint. The per-cause regex now emits one hint per rejection independently.
func TestEnrichSafetyPolicyValidationError_BothSectionsInOneError(t *testing.T) {
	// Build a single multi-cause error string by hand. The shape mirrors
	// what jsonschema/v5 actually produces — each cause gets its own
	// schema location prefix and an `additionalProperties 'X' not allowed`
	// trailer.
	combined := errors.New(
		"validate safety policy config: schema validation failed: jsonschema: '/' does not validate with " +
			"inmemory://safety-policy: " +
			// cause 1 — content-inspection field in rules[].match
			"jsonschema: '/rules/0/match' does not validate with inmemory://safety-policy#/properties/rules/items/$ref/properties/match/$ref/additionalProperties: additionalProperties 'keywords' not allowed; " +
			// cause 2 — dispatch-only field in input_rules[].match
			"jsonschema: '/input_rules/0/match' does not validate with inmemory://safety-policy#/properties/input_rules/items/$ref/properties/match/$ref/additionalProperties: additionalProperties 'delegation' not allowed",
	)
	enriched := enrichSafetyPolicyValidationError(combined)
	msg := enriched.Error()
	// Both hints must appear — previously the global path check returned
	// "ambiguous" and the user got zero suggestions.
	if !contains(msg, "'keywords' is valid under input_rules[].match") {
		t.Errorf("expected input_rules hint for keywords; got: %s", msg)
	}
	if !contains(msg, "'delegation' is valid under rules[].match") {
		t.Errorf("expected rules hint for delegation; got: %s", msg)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
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
