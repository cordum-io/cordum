package main

// diffForParity is a thin re-export of the harness's Diff function so
// cmd/diff-cli can live in its own package without duplicating the
// grading engine. This module doesn't import
// github.com/cordum/cordum-sdk-conformance-harness-go (the parent
// harness module) because that package's `main` is not importable;
// instead, the parity runner rebuilds the diff CLI from the harness
// source tree via a relative go-file include.
//
// To avoid code duplication, we inline the minimal diff entrypoint
// this binary needs. Full wildcard semantics are intentionally
// re-implemented here so a future parity run compiles standalone —
// divergence between this and harness/go/diff.go would be caught by
// the parity runner itself.

import (
	"fmt"
	"regexp"
	"time"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func diffForParity(actual, expected any) error {
	if s, ok := expected.(string); ok && isWildcardToken(s) {
		return checkWildcardToken(actual, s)
	}
	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("want object, got %T", actual)
		}
		for k, v := range exp {
			sub, ok := act[k]
			if !ok {
				return fmt.Errorf("expected key %q missing", k)
			}
			if err := diffForParity(sub, v); err != nil {
				return fmt.Errorf("%s: %v", k, err)
			}
		}
	case []any:
		act, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("want array, got %T", actual)
		}
		if len(act) != len(exp) {
			return fmt.Errorf("length mismatch (want %d, got %d)", len(exp), len(act))
		}
		for i, v := range exp {
			if err := diffForParity(act[i], v); err != nil {
				return fmt.Errorf("[%d]: %v", i, err)
			}
		}
	case nil:
		if actual != nil {
			return fmt.Errorf("want null, got %v", actual)
		}
	case bool, string, float64:
		if actual != exp {
			return fmt.Errorf("want %v, got %v", exp, actual)
		}
	default:
		if fmt.Sprintf("%v", actual) != fmt.Sprintf("%v", exp) {
			return fmt.Errorf("want %v, got %v", exp, actual)
		}
	}
	return nil
}

func isWildcardToken(s string) bool {
	switch s {
	case "$any$", "$timestamp$", "$uuid$", "$int$", "$request_id$":
		return true
	}
	return false
}

func checkWildcardToken(actual any, token string) error {
	switch token {
	case "$any$", "$request_id$":
		return nil
	case "$timestamp$":
		s, ok := actual.(string)
		if !ok {
			return fmt.Errorf("$timestamp$ expects string, got %T", actual)
		}
		if _, err := time.Parse(time.RFC3339, s); err == nil {
			return nil
		}
		if _, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return nil
		}
		return fmt.Errorf("%q is not an RFC3339 timestamp", s)
	case "$uuid$":
		s, ok := actual.(string)
		if !ok || !uuidRe.MatchString(s) {
			return fmt.Errorf("%v is not a UUID", actual)
		}
		return nil
	case "$int$":
		switch v := actual.(type) {
		case bool:
			return fmt.Errorf("$int$ rejects bool (got %v)", v)
		case float64:
			if v == float64(int64(v)) {
				return nil
			}
			return fmt.Errorf("$int$ expects integer, got %v", v)
		case int, int32, int64:
			return nil
		}
		return fmt.Errorf("$int$ expects integer, got %T", actual)
	}
	return fmt.Errorf("unknown wildcard %q", token)
}
