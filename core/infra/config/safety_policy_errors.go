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
// guards against drift (it walks the schema bidirectionally so a NEW
// exclusive field added to the schema without a hint-set update will
// also fire the test, not just a removed one).
var (
	policyMatchOnlyFields = map[string]struct{}{
		"pack_ids":                   {},
		"actor_ids":                  {},
		"actor_types":                {},
		"agent_risk_tiers":           {},
		"agent_data_classifications": {},
		"labels":                     {},
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

// causeRegex extracts (section, field) pairs PER REJECTION from a
// jsonschema/v5 validation error. The library emits each cause with its own
// schema location prefix like:
//
//	... inmemory://safety-policy#/properties/rules/items/$ref/properties/match/...:
//	    additionalProperties 'keywords' not allowed
//	... inmemory://safety-policy#/properties/input_rules/items/$ref/properties/match/...:
//	    additionalProperties 'delegation' not allowed
//
// Matching the (section, field) tuple greedily-but-locally lets us emit
// distinct hints per cause — previously a single error containing both
// sections fell back to "ambiguous" and dropped every hint (CodeRabbit
// finding on #316).
//
// The gap `[^']*?` is lazy, so each match pairs a rule-match path with the
// FIRST `additionalProperties 'X'` that follows it — that rejection's own
// offending field. `/` is intentionally allowed in the gap because the
// rejection path itself contains `/additionalProperties` before the message;
// it's the laziness (not a `/` exclusion) that scopes each match to one
// cause. `([^']+)` then captures the field name up to its closing quote.
var causeRegex = regexp.MustCompile(
	`/properties/(rules|input_rules)/items/\$ref/properties/match[^']*?additionalProperties '([^']+)' not allowed`,
)

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

	// Per-cause (section, field) extraction. Each match is one rejection,
	// scoped to a single rules[] or input_rules[] match-clause path. This
	// is the right granularity — when one error has rejections in BOTH
	// sections, we now suggest both, instead of dropping all hints because
	// the global path was ambiguous (CodeRabbit major finding on #316).
	pairs := causeRegex.FindAllStringSubmatch(msg, -1)
	if len(pairs) == 0 {
		return err
	}

	seenSuggestions := map[string]struct{}{}
	suggestions := make([]string, 0, len(pairs))
	for _, p := range pairs {
		section, field := p[1], p[2]
		var hint string
		switch section {
		case "rules":
			if _, ok := inputMatchOnlyFields[field]; ok {
				hint = fmt.Sprintf("'%s' is valid under input_rules[].match (content inspection); see docs/policy/global-authority.md", field)
			}
		case "input_rules":
			if _, ok := policyMatchOnlyFields[field]; ok {
				hint = fmt.Sprintf("'%s' is valid under rules[].match (dispatch); see docs/policy/global-authority.md", field)
			}
		}
		if hint == "" {
			continue
		}
		if _, dup := seenSuggestions[hint]; dup {
			continue
		}
		seenSuggestions[hint] = struct{}{}
		suggestions = append(suggestions, hint)
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

// Sentinel so callers can errors.Is the inner ValidationError if they want
// to distinguish enrichment from the underlying error. Not used today but
// keeps the API forward-compatible.
var ErrSafetyPolicyValidation = errors.New("safety policy schema validation")
