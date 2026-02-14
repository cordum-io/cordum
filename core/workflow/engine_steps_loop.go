package workflow

import (
	"fmt"
	"strings"
)

func resolveLoopConfig(step *Step) (maxIterations int, conditionExpr, untilExpr, bodyStepID string, err error) {
	if step == nil {
		return 0, "", "", "", fmt.Errorf("loop step required")
	}
	maxIterations = defaultLoopMaxIter
	if step.Input == nil {
		return maxIterations, "", "", "", nil
	}
	if maxIterations, err = loopMaxIterations(step.Input); err != nil {
		return 0, "", "", "", err
	}
	if conditionExpr, _, err = loopInputString(step.Input, "condition", "while"); err != nil {
		return 0, "", "", "", err
	}
	if untilExpr, _, err = loopInputString(step.Input, "until"); err != nil {
		return 0, "", "", "", err
	}
	if bodyStepID, _, err = loopInputString(step.Input, "body_step", "body"); err != nil {
		return 0, "", "", "", err
	}
	return maxIterations, conditionExpr, untilExpr, bodyStepID, nil
}

func loopMaxIterations(input map[string]any) (int, error) {
	if input == nil {
		return defaultLoopMaxIter, nil
	}
	for _, key := range []string{"max_iterations", "maxIterations"} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		n, err := parsePositiveInt(raw)
		if err != nil {
			return 0, fmt.Errorf("loop input.%s invalid: %w", key, err)
		}
		return n, nil
	}
	return defaultLoopMaxIter, nil
}

func loopInputString(input map[string]any, keys ...string) (string, bool, error) {
	if input == nil {
		return "", false, nil
	}
	for _, key := range keys {
		raw, ok := input[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case nil:
			return "", true, nil
		case string:
			return strings.TrimSpace(v), true, nil
		default:
			return "", true, fmt.Errorf("loop input.%s must be string", key)
		}
	}
	return "", false, nil
}

func buildLoopEvalScope(run *WorkflowRun, index int, previousOutput any) map[string]any {
	scope := buildEvalScope(run, nil)
	scope["loop"] = map[string]any{
		"index":           index,
		"iteration":       index + 1,
		"previous_output": previousOutput,
	}
	return scope
}

func loopPreviousOutput(run *WorkflowRun, childID string) any {
	if run == nil || childID == "" {
		return nil
	}
	if run.Context != nil {
		if steps, ok := run.Context["steps"].(map[string]any); ok && steps != nil {
			if entry, ok := steps[childID].(map[string]any); ok {
				if output, ok := entry["output"]; ok {
					return output
				}
				if ptr, ok := entry["result_ptr"]; ok {
					return ptr
				}
			}
		}
	}
	if run.Steps != nil {
		if sr := run.Steps[childID]; sr != nil && sr.Output != nil {
			return sr.Output
		}
	}
	return nil
}

func (e *Engine) buildLoopPayload(run *WorkflowRun, step *Step, scope map[string]any) (map[string]any, error) {
	base := map[string]any{}
	if step != nil && len(step.Input) > 0 {
		evaluated, err := evalTemplates(step.Input, scope)
		if err != nil {
			return nil, err
		}
		if m, ok := evaluated.(map[string]any); ok {
			base = m
		} else {
			return nil, fmt.Errorf("step input must be object, got %T", evaluated)
		}
	} else if run != nil && len(run.Input) > 0 {
		base = run.Input
	}
	capHint := len(base)
	if capHint < int(^uint(0)>>1) {
		capHint++
	}
	out := make(map[string]any, capHint)
	for k, v := range base {
		out[k] = v
	}
	if loopScope, ok := scope["loop"]; ok {
		if _, exists := out["loop"]; !exists {
			out["loop"] = loopScope
		}
	}
	if err := e.validateStepInput(step, out); err != nil {
		return nil, err
	}
	return out, nil
}
