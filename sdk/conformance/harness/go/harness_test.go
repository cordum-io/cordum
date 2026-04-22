package main

import (
	"encoding/json"
	"testing"
)

// TestDiffPrimitives pins the per-token wildcard semantics. The
// Python and TypeScript harnesses run structurally-identical test
// cases; step 9's parity runner asserts byte-equal verdicts.
func TestDiffPrimitives(t *testing.T) {
	cases := []struct {
		name     string
		actual   string
		expected string
		wantPass bool
	}{
		{"exact-string", `"hello"`, `"hello"`, true},
		{"exact-string-mismatch", `"hello"`, `"world"`, false},
		{"any-wildcard-on-string", `"whatever"`, `"$any$"`, true},
		{"any-wildcard-on-int", `42`, `"$any$"`, true},
		{"any-wildcard-on-object", `{"k":"v"}`, `"$any$"`, true},
		{"timestamp-valid", `"2026-01-01T00:00:00Z"`, `"$timestamp$"`, true},
		{"timestamp-invalid", `"not-a-date"`, `"$timestamp$"`, false},
		{"int-wildcard-valid", `42`, `"$int$"`, true},
		{"int-wildcard-float-fails", `3.14`, `"$int$"`, false},
		{"uuid-valid", `"550e8400-e29b-41d4-a716-446655440000"`, `"$uuid$"`, true},
		{"uuid-invalid", `"not-a-uuid"`, `"$uuid$"`, false},
		{"unknown-wildcard", `"x"`, `"$zzz$"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var a, e any
			if err := json.Unmarshal([]byte(c.actual), &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.expected), &e); err != nil {
				t.Fatal(err)
			}
			err := Diff(a, e, "$")
			if c.wantPass && err != nil {
				t.Errorf("want pass, got error: %v", err)
			}
			if !c.wantPass && err == nil {
				t.Errorf("want fail, got pass")
			}
		})
	}
}

// TestDiffNestedObject covers the per-key descent and the "actual may
// carry additional keys" rule.
func TestDiffNestedObject(t *testing.T) {
	actualJSON := `{"id":"abc","name":"alpha","status":"active","extra":"ignored"}`
	expectedJSON := `{"name":"alpha","status":"active","id":"$any$"}`
	var a, e any
	_ = json.Unmarshal([]byte(actualJSON), &a)
	_ = json.Unmarshal([]byte(expectedJSON), &e)
	if err := Diff(a, e, "$"); err != nil {
		t.Errorf("expected nested object diff to pass, got %v", err)
	}
}

// TestDiffArrayLenMismatch covers the ordered-array semantics.
func TestDiffArrayLenMismatch(t *testing.T) {
	a := []any{"a", "b", "c"}
	e := []any{"a", "b"}
	if err := Diff(a, e, "$"); err == nil {
		t.Error("want length-mismatch error")
	}
}

// TestDiffObjectMissingKey asserts a required key's absence fails.
func TestDiffObjectMissingKey(t *testing.T) {
	a := map[string]any{"a": 1}
	e := map[string]any{"a": float64(1), "b": "$any$"}
	if err := Diff(a, e, "$"); err == nil {
		t.Error("want missing-key error")
	}
}

// TestSelectJSONPath covers the minimal-JSONPath implementation the
// bodyMatches expressions rely on.
func TestSelectJSONPath(t *testing.T) {
	root := map[string]any{
		"id": "xyz",
		"items": []any{
			map[string]any{"id": "item-0"},
			map[string]any{"id": "item-1"},
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"$.id", "xyz"},
		{"$.items[0].id", "item-0"},
		{"$.items[1].id", "item-1"},
	}
	for _, c := range cases {
		got, err := selectJSONPath(root, c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: want %v, got %v", c.expr, c.want, got)
		}
	}
}

// TestResolveVarsSubstitution covers the $vars.* placeholder layer.
func TestResolveVarsSubstitution(t *testing.T) {
	vars := map[string]any{"agentId": "agent-0001", "tenant": "default"}
	in := map[string]any{
		"id":     "$vars.agentId",
		"tenant": "$vars.tenant",
		"nested": map[string]any{"copy": "$vars.agentId"},
		"list":   []any{"$vars.agentId", "literal"},
	}
	out := resolveVars(in, vars).(map[string]any)
	if out["id"] != "agent-0001" {
		t.Errorf("id not substituted: %v", out["id"])
	}
	nested := out["nested"].(map[string]any)
	if nested["copy"] != "agent-0001" {
		t.Errorf("nested var not substituted: %v", nested["copy"])
	}
	list := out["list"].([]any)
	if list[0] != "agent-0001" || list[1] != "literal" {
		t.Errorf("list substitution broken: %v", list)
	}
}

// TestInferErrorStatus pins the class → status mapping shared with
// python/_diff.py and typescript/diff.ts.
func TestInferErrorStatus(t *testing.T) {
	cases := map[string]int{
		"AuthenticationError": 401,
		"AuthorizationError":  403,
		"NotFoundError":       404,
		"ValidationError":     400,
		"ConflictError":       409,
		"RateLimitError":      429,
		"ServerError":         500,
		"NetworkError":        0,
		"TimeoutError":        0,
	}
	for class, want := range cases {
		if got := inferErrorStatus(class); got != want {
			t.Errorf("%s: want %d, got %d", class, want, got)
		}
	}
}
