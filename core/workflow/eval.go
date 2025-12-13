package workflow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Eval evaluates a simple expression against a context map.
// Supported:
//   - literals: numbers, booleans, quoted strings
//   - dot paths: foo.bar (walks nested maps)
//   - functions: length(x), first(x)
//   - comparisons: a == b, a != b, a > b, a < b, a >= b, a <= b
//   - unary ! for booleans
func Eval(expr string, ctx map[string]any) (any, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errors.New("empty expression")
	}

	// Unary not
	if strings.HasPrefix(expr, "!") {
		val, err := Eval(expr[1:], ctx)
		if err != nil {
			return nil, err
		}
		return !truthy(val), nil
	}

	// comparisons
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if parts := splitOnce(expr, op); len(parts) == 2 {
			left, err := Eval(parts[0], ctx)
			if err != nil {
				return nil, err
			}
			right, err := Eval(parts[1], ctx)
			if err != nil {
				return nil, err
			}
			return compare(left, right, op), nil
		}
	}

	// function calls
	if strings.HasPrefix(expr, "length(") && strings.HasSuffix(expr, ")") {
		argExpr := strings.TrimSuffix(strings.TrimPrefix(expr, "length("), ")")
		val, err := Eval(argExpr, ctx)
		if err != nil {
			return nil, err
		}
		switch v := val.(type) {
		case []any:
			return len(v), nil
		case string:
			return len(v), nil
		case map[string]any:
			return len(v), nil
		default:
			return 0, nil
		}
	}
	if strings.HasPrefix(expr, "first(") && strings.HasSuffix(expr, ")") {
		argExpr := strings.TrimSuffix(strings.TrimPrefix(expr, "first("), ")")
		val, err := Eval(argExpr, ctx)
		if err != nil {
			return nil, err
		}
		if arr, ok := val.([]any); ok && len(arr) > 0 {
			return arr[0], nil
		}
		return nil, nil
	}

	// literal string
	if strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'") && len(expr) >= 2 {
		return strings.Trim(expr, "'"), nil
	}
	if strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"") && len(expr) >= 2 {
		return strings.Trim(expr, "\""), nil
	}

	// literal bool
	if expr == "true" {
		return true, nil
	}
	if expr == "false" {
		return false, nil
	}

	// literal number
	if n, err := strconv.ParseFloat(expr, 64); err == nil {
		return n, nil
	}

	// path
	return resolvePath(expr, ctx), nil
}

func resolvePath(path string, ctx map[string]any) any {
	parts := strings.Split(path, ".")
	var cur any = ctx
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func splitOnce(expr, op string) []string {
	idx := strings.Index(expr, op)
	if idx < 0 {
		return nil
	}
	return []string{strings.TrimSpace(expr[:idx]), strings.TrimSpace(expr[idx+len(op):])}
}

func compare(a, b any, op string) bool {
	switch av := a.(type) {
	case float64:
		bv := toFloat(b)
		return cmpFloat(av, bv, op)
	case int:
		bv := toFloat(b)
		return cmpFloat(float64(av), bv, op)
	case string:
		if bs, ok := b.(string); ok {
			return cmpString(av, bs, op)
		}
	}
	// fallback equality
	switch op {
	case "==":
		return fmt.Sprint(a) == fmt.Sprint(b)
	case "!=":
		return fmt.Sprint(a) != fmt.Sprint(b)
	default:
		return false
	}
}

func cmpFloat(a, b float64, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	default:
		return false
	}
}

func cmpString(a, b, op string) bool {
	switch op {
	case "==":
		return a == b
	case "!=":
		return a != b
	case ">":
		return a > b
	case "<":
		return a < b
	case ">=":
		return a >= b
	case "<=":
		return a <= b
	default:
		return false
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return 0
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return true
	}
}
