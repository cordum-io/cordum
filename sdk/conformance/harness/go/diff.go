// Package harness implements the Go-language conformance harness.
// The diff engine below is the per-harness grader that compares an
// actual gateway response against the fixture's `expect` block using
// the shared wildcard semantics documented in SPEC.md (+ future
// docs/wildcards.md and docs/grading.md).
//
// Grading rules (must match python _diff.py and typescript diff.ts
// byte-for-byte — parity enforced in step 9):
//
//	$any$           any value passes
//	$timestamp$     string parsable as RFC3339 (ISO-8601)
//	$uuid$          string matching UUID v4 shape
//	$int$           integer value (or JSON number round-tripping to int)
//	$request_id$    opaque request id — behaves like $any$ for now
//
// Ordered vs unordered arrays: arrays compare element-by-element by
// default; a fixture that needs order-insensitive comparison sets
// `unordered: true` at the array's wildcard anchor (future v2
// extension — v1 is order-sensitive only).
package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Diff compares actual vs expected, applying the documented wildcard
// tokens. It returns nil on pass, or a non-nil error with a cumulative
// path-qualified description of the first divergence it finds.
//
// Both trees are decoded-JSON (map[string]any / []any / string / float64 / bool / nil).
func Diff(actual, expected any, path string) error {
	if s, ok := expected.(string); ok && isWildcard(s) {
		return checkWildcard(actual, s, path)
	}
	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object, got %T", path, actual)
		}
		// Every key the fixture declares must be present + match.
		// The actual MAY carry additional keys the fixture doesn't
		// assert on — that keeps fixtures robust to response evolution.
		keys := sortedKeys(exp)
		for _, k := range keys {
			v := exp[k]
			actVal, ok := act[k]
			if !ok {
				return fmt.Errorf("%s.%s: expected key missing from response", path, k)
			}
			if err := Diff(actVal, v, path+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		act, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("%s: want array, got %T", path, actual)
		}
		if len(act) != len(exp) {
			return fmt.Errorf("%s: length mismatch (want %d, got %d)", path, len(exp), len(act))
		}
		for i, v := range exp {
			if err := Diff(act[i], v, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case nil:
		if actual != nil {
			return fmt.Errorf("%s: want null, got %v", path, actual)
		}
		return nil
	case bool, string, float64:
		if actual != exp {
			return fmt.Errorf("%s: want %v (%T), got %v (%T)", path, exp, exp, actual, actual)
		}
		return nil
	default:
		if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", exp) {
			return fmt.Errorf("%s: want %v, got %v", path, exp, actual)
		}
		return nil
	}
}

// isWildcard returns true for any of the supported grading tokens.
func isWildcard(s string) bool {
	switch s {
	case "$any$", "$timestamp$", "$uuid$", "$int$", "$request_id$":
		return true
	}
	return false
}

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// checkWildcard enforces the per-token semantics.
func checkWildcard(actual any, token, path string) error {
	switch token {
	case "$any$", "$request_id$":
		return nil
	case "$timestamp$":
		s, ok := actual.(string)
		if !ok {
			return fmt.Errorf("%s: $timestamp$ expects string, got %T", path, actual)
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return fmt.Errorf("%s: %q is not an RFC3339 timestamp", path, s)
			}
		}
		return nil
	case "$uuid$":
		s, ok := actual.(string)
		if !ok {
			return fmt.Errorf("%s: $uuid$ expects string, got %T", path, actual)
		}
		if !uuidRegex.MatchString(s) {
			return fmt.Errorf("%s: %q is not a UUID", path, s)
		}
		return nil
	case "$int$":
		switch v := actual.(type) {
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("%s: $int$ expects integer, got %v", path, v)
			}
			return nil
		case int, int32, int64:
			return nil
		}
		return fmt.Errorf("%s: $int$ expects integer, got %T", path, actual)
	}
	return fmt.Errorf("%s: unknown wildcard %q", path, token)
}

// sortedKeys returns map keys in deterministic order for grading —
// otherwise the first-divergence message would be non-deterministic
// across runs.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resolveVars substitutes `$vars.key` placeholders in fixture inputs
// with values accumulated by prior extract steps.
func resolveVars(in any, vars map[string]any) any {
	switch v := in.(type) {
	case string:
		if strings.HasPrefix(v, "$vars.") {
			key := strings.TrimPrefix(v, "$vars.")
			if out, ok := vars[key]; ok {
				return out
			}
			return "" // missing vars resolve to empty string so fixtures surface auth-failure as 401 not template errors
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = resolveVars(val, vars)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = resolveVars(item, vars)
		}
		return out
	default:
		return v
	}
}
