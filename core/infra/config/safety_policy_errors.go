package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// Match-clause field sets, mirrored from
// core/infra/config/schema/safety_policy.schema.json (definitions.policyMatch
// and definitions.inputMatch). The schema is the source of truth; if you
// add or remove a field there, update the corresponding set below and the
// TestEnrichSafetyPolicyValidationError_FieldSetsMatchSchema test that
// guards against drift.
var (
	policyMatchOnlyFields = map[string]struct{}{
		"pack_ids":                   {},
		"actor_ids":                  {},
		"actor_types":                {},
		"agent_risk_tiers":           {},
		"agent_data_classifications": {},
		"label_allowlist":            {},
		"label_threshold":            {},
		"secrets_present":            {},
		"predicate":                  {},
		"delegation":                 {},
		"mcp":                        {},
		"requires":                   {},
	}
	inputMatchOnlyFields = map[string]struct{}{
		"scanners":         {},
		"content_patterns": {},
		"keywords":         {},
		"content_types":    {},
		"detectors":        {},
		"input_size_gt":    {},
		"max_input_bytes":  {},
		"scope":            {},
	}
)

// Compiled once; matches messages emitted by jsonschema/v5 of the form
// `additionalProperties 'X' not allowed`. We capture the field name to
// look up which rule type it actually belongs on.
var additionalPropsRegex = regexp.MustCompile(`additionalProperties '([^']+)' not allowed`)

// enrichSafetyPolicyValidationError wraps a schema validation failure with a
// "did you mean..." hint when the offending property is valid on the SIBLING
// rule type (rules[]/input_rules[]).
//
// Background — see GitHub issue #312. The bare error from jsonschema/v5
// says nothing about WHY the property was rejected; operators copying a
// rule with `keywords:` from config/safety.yaml into rules[].match get a
// pure red wall and no hint that `keywords` is valid on input_rules[].
//
// The function is intentionally narrow: it preserves the original error
// (via %w) and only appends a single suggestion line. If we can't extract
// a property name, or the property isn't on either rule's exclusive set,
// the original error passes through unchanged.
func enrichSafetyPolicyValidationError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	matches := additionalPropsRegex.FindAllStringSubmatch(msg, -1)
	if len(matches) == 0 {
		return err
	}

	// Collect unique suggestions across all rejections in this error (the
	// jsonschema library aggregates multiple causes into one error tree).
	seenSuggestions := map[string]struct{}{}
	suggestions := make([]string, 0, len(matches))
	for _, m := range matches {
		field := m[1]
		path := pathContextFor(msg, field)
		// Field used in rules[].match but is input-only → suggest input_rules.
		if path == "rules" {
			if _, ok := inputMatchOnlyFields[field]; ok {
				s := fmt.Sprintf("'%s' is valid under input_rules[].match (content inspection); see docs/policy/global-authority.md", field)
				if _, dup := seenSuggestions[s]; !dup {
					seenSuggestions[s] = struct{}{}
					suggestions = append(suggestions, s)
				}
			}
		}
		// Field used in input_rules[].match but is policy-only → suggest rules.
		if path == "input_rules" {
			if _, ok := policyMatchOnlyFields[field]; ok {
				s := fmt.Sprintf("'%s' is valid under rules[].match (dispatch); see docs/policy/global-authority.md", field)
				if _, dup := seenSuggestions[s]; !dup {
					seenSuggestions[s] = struct{}{}
					suggestions = append(suggestions, s)
				}
			}
		}
	}

	if len(suggestions) == 0 {
		return err
	}
	sort.Strings(suggestions)
	hint := "did you mean: " + suggestions[0]
	for _, s := range suggestions[1:] {
		hint += "; " + s
	}
	return fmt.Errorf("%w (%s)", err, hint)
}

// pathContextFor returns "rules" or "input_rules" if the substring around
// the rejected field name in the jsonschema error indicates one of those
// sections. Returns "" for any other context (so we don't fire incorrect
// hints on unrelated additionalProperties rejections elsewhere in the schema).
//
// The jsonschema/v5 error embeds the schema location like:
//
//	/properties/rules/items/$ref/properties/match/$ref/additionalProperties
//	/properties/input_rules/items/$ref/properties/match/$ref/additionalProperties
//
// We probe for `/properties/rules/items` and `/properties/input_rules/items`
// against the WHOLE message rather than trying to align with the specific
// instance location for the matched field — multiple causes in a single
// error often share one schema location, so a global probe is robust.
func pathContextFor(msg, _ string) string {
	const ruleHint = "/properties/rules/items"
	const inputHint = "/properties/input_rules/items"
	hasRules := containsLiteral(msg, ruleHint)
	hasInput := containsLiteral(msg, inputHint)
	switch {
	case hasInput && !hasRules:
		return "input_rules"
	case hasRules && !hasInput:
		return "rules"
	default:
		// Ambiguous (both present, or neither). Don't risk a wrong hint.
		return ""
	}
}

// containsLiteral is a tiny strings.Contains shim so this file does not pull
// in the strings package just for one call (avoids an import collision in
// tests). Inlined for clarity.
func containsLiteral(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Sentinel so callers can errors.Is the inner ValidationError if they want
// to distinguish enrichment from the underlying error. Not used today but
// keeps the API forward-compatible.
var ErrSafetyPolicyValidation = errors.New("safety policy schema validation")
