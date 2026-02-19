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
		{"42", 42},
		{"'hi'", "hi"},
		{"foo.bar", 10},
		{"str", "hello"},
		{"length(arr)", 3},
		{"first(arr)", 1},
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
			t.Fatalf("expr %q: want %v (%T) got %v (%T)", c.expr, c.want, c.want, got, got)
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

func TestEvalLogicalOperators(t *testing.T) {
	ctx := map[string]any{"foo": map[string]any{"bar": 10}, "str": "hello"}
	cases := []struct {
		expr string
		want bool
	}{
		{"foo.bar > 5 && str == 'hello'", true},
		{"foo.bar > 100 && str == 'hello'", false},
		{"foo.bar > 100 || str == 'hello'", true},
		{"foo.bar > 100 || str == 'bye'", false},
		{"(foo.bar > 5) && (foo.bar < 20)", true},
		{"(foo.bar > 5) && (foo.bar < 8)", false},
		{"!(foo.bar > 100)", true},
		{"!(foo.bar > 5)", false},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("expr %q: want %v, got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalArithmetic(t *testing.T) {
	ctx := map[string]any{"x": 10, "y": 3}
	cases := []struct {
		expr string
		want string
	}{
		{"x + 5", "15"},
		{"x - y", "7"},
		{"x * y", "30"},
		{"x / y", "3.3333333333333335"},
		{"x % y", "1"},
		{"x + y * 2", "16"},
		{"(x + y) * 2", "26"},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if fmt.Sprint(got) != c.want {
			t.Fatalf("expr %q: want %s, got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalTernary(t *testing.T) {
	ctx := map[string]any{"val": 15}
	got, err := Eval("val > 10 ? 'big' : 'small'", ctx)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != "big" {
		t.Fatalf("expected 'big', got %v", got)
	}

	got, err = Eval("val > 100 ? 'big' : 'small'", ctx)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != "small" {
		t.Fatalf("expected 'small', got %v", got)
	}
}

func TestEvalStringOperators(t *testing.T) {
	ctx := map[string]any{"str": "hello world"}
	cases := []struct {
		expr string
		want bool
	}{
		{"str contains 'world'", true},
		{"str contains 'xyz'", false},
		{"str startsWith 'hello'", true},
		{"str startsWith 'world'", false},
		{"str endsWith 'world'", true},
		{"str endsWith 'hello'", false},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if got != c.want {
			t.Fatalf("expr %q: want %v, got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalArrayAccess(t *testing.T) {
	ctx := map[string]any{"arr": []any{"a", "b", "c"}, "nested": map[string]any{"items": []any{10, 20, 30}}}
	cases := []struct {
		expr string
		want string
	}{
		{"arr[0]", "a"},
		{"arr[2]", "c"},
		{"nested.items[1]", "20"},
		{"length(arr)", "3"},
		{"first(arr)", "a"},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if fmt.Sprint(got) != c.want {
			t.Fatalf("expr %q: want %s, got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalInOperator(t *testing.T) {
	ctx := map[string]any{"role": "admin", "roles": []any{"admin", "editor", "viewer"}}
	got, err := Eval("role in roles", ctx)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != true {
		t.Fatalf("expected true, got %v", got)
	}

	ctx["role"] = "guest"
	got, err = Eval("role in roles", ctx)
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != false {
		t.Fatalf("expected false, got %v", got)
	}
}

func TestEvalEmptyExpression(t *testing.T) {
	_, err := Eval("", nil)
	if err == nil {
		t.Fatal("expected error for empty expression")
	}
}

func TestEvalInvalidExpression(t *testing.T) {
	_, err := Eval("!", map[string]any{})
	if err == nil {
		t.Fatal("expected error for invalid expression '!'")
	}
}

func TestEvalUndefinedVariable(t *testing.T) {
	got, err := Eval("missing", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error for undefined var: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for undefined var, got %v", got)
	}
}

func TestEvalCustomFunctions(t *testing.T) {
	ctx := map[string]any{
		"arr": []any{10, 20, 30},
		"str": "hello",
		"m":   map[string]any{"a": 1, "b": 2},
	}
	cases := []struct {
		expr string
		want string
	}{
		{"length(arr)", "3"},
		{"length(str)", "5"},
		{"length(m)", "2"},
		{"first(arr)", "10"},
	}
	for _, c := range cases {
		got, err := Eval(c.expr, ctx)
		if err != nil {
			t.Fatalf("expr %q: %v", c.expr, err)
		}
		if fmt.Sprint(got) != c.want {
			t.Fatalf("expr %q: want %s, got %v", c.expr, c.want, got)
		}
	}
}

func TestEvalFirstEmpty(t *testing.T) {
	got, err := Eval("first(arr)", map[string]any{"arr": []any{}})
	if err != nil {
		t.Fatalf("eval err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for first of empty array, got %v", got)
	}
}

func TestTruthy(t *testing.T) {
	cases := []struct {
		val  any
		want bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{"hello", true},
		{"", false},
		{float64(1), true},
		{float64(0), false},
		{int(1), true},
		{int(0), false},
		{int64(1), true},
		{int64(0), false},
		{uint(1), true},
		{uint(0), false},
		{[]any{1}, true},
	}
	for _, c := range cases {
		got := truthy(c.val)
		if got != c.want {
			t.Fatalf("truthy(%v) = %v, want %v", c.val, got, c.want)
		}
	}
}
