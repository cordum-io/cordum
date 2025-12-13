package workflow

import (
	"fmt"
	"testing"
)

func TestEvalLiteralsAndPaths(t *testing.T) {
	ctx := map[string]any{"foo": map[string]any{"bar": 10}, "str": "hello", "arr": []any{1, 2, 3}}
	cases := []struct {
		expr string
		want any
	}{
		{"true", true},
		{"false", false},
		{"42", float64(42)},
		{"'hi'", "hi"},
		{"foo.bar", float64(10)},
		{"str", "hello"},
		{"length(arr)", 3},
		{"first(arr)", float64(1)},
		{"foo.bar == 10", true},
		{"foo.bar > 5", true},
		{"foo.bar < 5", false},
		{"!false", true},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Fatalf("expr %q: want %v got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalCompareStrings(t *testing.T) {
	ctx := map[string]any{"env": map[string]any{"tier": "prod"}}
	got, err := Eval("env.tier == 'prod'", ctx)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != true {
		t.Fatalf("expected true, got %v", got)
	}
}
