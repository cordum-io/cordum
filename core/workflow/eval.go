package workflow

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// evalOptions holds expr-lang options including custom function registrations.
var evalOptions = []expr.Option{
	expr.AllowUndefinedVariables(),

	expr.Function("length", func(params ...any) (any, error) {
		if len(params) == 0 {
			return 0, nil
		}
		v := params[0]
		if v == nil {
			return 0, nil
		}
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
			return rv.Len(), nil
		default:
			return 0, nil
		}
	}, new(func(any) int)),

	expr.Function("first", func(params ...any) (any, error) {
		if len(params) == 0 {
			return nil, nil
		}
		v := params[0]
		if v == nil {
			return nil, nil
		}
		rv := reflect.ValueOf(v)
		if (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() > 0 {
			return rv.Index(0).Interface(), nil
		}
		return nil, nil
	}, new(func(any) any)),
}

// exprCache caches compiled programs keyed by expression string.
// Expressions come from finite workflow YAML so the cache is bounded in practice.
var exprCache sync.Map // map[string]*vm.Program

func compileExpr(exprStr string) (*vm.Program, error) {
	if cached, ok := exprCache.Load(exprStr); ok {
		return cached.(*vm.Program), nil
	}
	program, err := expr.Compile(exprStr, evalOptions...)
	if err != nil {
		return nil, err
	}
	exprCache.Store(exprStr, program)
	return program, nil
}

// Eval evaluates an expression against a context map using expr-lang/expr.
// Compiled programs are cached for repeated evaluation (e.g. loop conditions).
// Supported: all expr-lang operators (==, !=, >, <, >=, <=, &&, ||, !,
// arithmetic, ternary, in, contains, startsWith, endsWith), dot paths,
// array indexing, and custom functions: length(), first().
func Eval(exprStr string, ctx map[string]any) (any, error) {
	exprStr = strings.TrimSpace(exprStr)
	if exprStr == "" {
		return nil, errors.New("empty expression")
	}

	program, err := compileExpr(exprStr)
	if err != nil {
		return nil, fmt.Errorf("eval %q: %w", exprStr, err)
	}
	result, err := expr.Run(program, ctx)
	if err != nil {
		return nil, fmt.Errorf("eval %q: %w", exprStr, err)
	}
	return result, nil
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
	case float32:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case uint:
		return t != 0
	case uint64:
		return t != 0
	default:
		return true
	}
}
