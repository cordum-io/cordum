package workflow

import (
	"fmt"
	"strings"
)

func evalCondition(expr string, scope map[string]any) (bool, error) {
	val, err := Eval(expr, scope)
	if err != nil {
		return false, err
	}
	return truthy(val), nil
}

func buildEvalScope(run *WorkflowRun, item any) map[string]any {
	scope := map[string]any{
		"input": runInput(run),
		"ctx":   runContext(run),
		"steps": runSteps(run),
	}
	if item != nil {
		scope["item"] = item
	}
	return scope
}

func runInput(run *WorkflowRun) map[string]any {
	if run == nil {
		return nil
	}
	return run.Input
}

func runContext(run *WorkflowRun) map[string]any {
	if run == nil {
		return nil
	}
	return run.Context
}

func runSteps(run *WorkflowRun) map[string]any {
	if run == nil || run.Context == nil {
		return map[string]any{}
	}
	if steps, ok := run.Context["steps"].(map[string]any); ok && steps != nil {
		return steps
	}
	return map[string]any{}
}

func evalTemplates(value any, scope map[string]any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		return evalTemplateString(v, scope)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			evaled, err := evalTemplates(child, scope)
			if err != nil {
				return nil, err
			}
			out[k] = evaled
		}
		return out, nil
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, child := range v {
			evaled, err := evalTemplateString(child, scope)
			if err != nil {
				return nil, err
			}
			out[k] = evaled
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			evaled, err := evalTemplates(child, scope)
			if err != nil {
				return nil, err
			}
			out[i] = evaled
		}
		return out, nil
	case []string:
		out := make([]any, len(v))
		for i, child := range v {
			evaled, err := evalTemplateString(child, scope)
			if err != nil {
				return nil, err
			}
			out[i] = evaled
		}
		return out, nil
	case []int:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = child
		}
		return out, nil
	default:
		return value, nil
	}
}

func evalTemplateString(s string, scope map[string]any) (any, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") && strings.Count(trimmed, "${") == 1 && strings.Count(trimmed, "}") == 1 {
		expr := strings.TrimSuffix(strings.TrimPrefix(trimmed, "${"), "}")
		return Eval(strings.TrimSpace(expr), scope)
	}
	var b strings.Builder
	rest := s
	for {
		start := strings.Index(rest, "${")
		if start == -1 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:start])
		rest = rest[start+2:]
		end := strings.Index(rest, "}")
		if end == -1 {
			return nil, fmt.Errorf("unterminated template expression")
		}
		expr := strings.TrimSpace(rest[:end])
		rest = rest[end+1:]
		val, err := Eval(expr, scope)
		if err != nil {
			return nil, err
		}
		if val != nil {
			b.WriteString(fmt.Sprint(val))
		}
	}
	return b.String(), nil
}

func evalForEach(expr string, scope map[string]any) ([]any, error) {
	val, err := Eval(expr, scope)
	if err != nil {
		return nil, err
	}
	switch v := val.(type) {
	case []any:
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, nil
	case []int:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, nil
	case nil:
		return []any{}, nil
	default:
		return nil, fmt.Errorf("for_each expression must return array, got %T", val)
	}
}
