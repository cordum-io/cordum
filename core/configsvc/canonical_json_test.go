package configsvc

import (
	"strings"
	"testing"
)

func TestCanonicalJSONOrdering(t *testing.T) {
	inputA := map[string]any{"b": 2, "a": 1, "list": []any{"x", "y"}}
	inputB := map[string]any{"a": 1, "list": []any{"x", "y"}, "b": 2}

	outA, err := canonicalJSON(inputA)
	if err != nil {
		t.Fatalf("canonical json A: %v", err)
	}
	outB, err := canonicalJSON(inputB)
	if err != nil {
		t.Fatalf("canonical json B: %v", err)
	}
	if string(outA) != string(outB) {
		t.Fatalf("expected stable json output")
	}
	if hashA, err := snapshotHash(inputA); err != nil || hashA == "" {
		t.Fatalf("expected hash for inputA")
	}
}

func TestCanonicalJSONSliceTypes(t *testing.T) {
	out, err := canonicalJSON(map[string]any{
		"ints":   []int{1, 2},
		"floats": []float64{1.5, 2.5},
		"bools":  []bool{true, false},
		"strings": []string{
			"a",
			"b",
		},
	})
	if err != nil {
		t.Fatalf("canonical json: %v", err)
	}
	if !strings.Contains(string(out), "ints") || !strings.Contains(string(out), "floats") {
		t.Fatalf("expected slice fields in output: %s", string(out))
	}
}

func TestCanonicalJSONUnsupportedType(t *testing.T) {
	_, err := canonicalJSON(map[string]any{"bad": func() {}})
	if err == nil {
		t.Fatalf("expected error for unsupported type")
	}
}

func TestSnapshotVersionOrdering(t *testing.T) {
	revs := map[Scope]int64{
		ScopeTeam:     3,
		ScopeSystem:   1,
		ScopeWorkflow: 4,
		ScopeOrg:      2,
		ScopeStep:     5,
	}
	got := snapshotVersion(revs)
	want := "system:1|org:2|team:3|workflow:4|step:5"
	if got != want {
		t.Fatalf("expected %s got %s", want, got)
	}
}
