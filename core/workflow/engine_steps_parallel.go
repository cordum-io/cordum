package workflow

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

func resolveParallelConfig(step *Step, scope map[string]any) ([]string, string, int, error) {
	if step == nil {
		return nil, "", 0, fmt.Errorf("parallel step required")
	}
	if step.Input == nil {
		return nil, "", 0, fmt.Errorf("parallel step input required")
	}
	rawChildren, ok := step.Input["steps"]
	if !ok {
		return nil, "", 0, fmt.Errorf("parallel step input.steps required")
	}
	evaledChildren, err := evalTemplates(rawChildren, scope)
	if err != nil {
		return nil, "", 0, fmt.Errorf("parallel steps eval failed: %w", err)
	}
	childStepIDs, err := parseParallelStepIDs(evaledChildren)
	if err != nil {
		return nil, "", 0, err
	}
	if len(childStepIDs) == 0 {
		return []string{}, "all", 0, nil
	}

	strategy := "all"
	if rawStrategy, ok := step.Input["strategy"]; ok {
		evaledStrategy, err := evalTemplates(rawStrategy, scope)
		if err != nil {
			return nil, "", 0, fmt.Errorf("parallel strategy eval failed: %w", err)
		}
		if strategy, err = normalizeParallelStrategy(evaledStrategy); err != nil {
			return nil, "", 0, err
		}
	}

	required := 0
	switch strategy {
	case "all":
		required = len(childStepIDs)
	case "any":
		required = 1
	case "n_of_m":
		rawRequired, ok := step.Input["required"]
		if !ok {
			return nil, "", 0, fmt.Errorf("parallel strategy n_of_m requires input.required")
		}
		evaledRequired, err := evalTemplates(rawRequired, scope)
		if err != nil {
			return nil, "", 0, fmt.Errorf("parallel required eval failed: %w", err)
		}
		parsedRequired, err := parsePositiveInt(evaledRequired)
		if err != nil {
			return nil, "", 0, fmt.Errorf("parallel required invalid: %w", err)
		}
		required = parsedRequired
	default:
		return nil, "", 0, fmt.Errorf("unsupported parallel strategy: %s", strategy)
	}
	if required <= 0 || required > len(childStepIDs) {
		return nil, "", 0, fmt.Errorf("parallel required must be between 1 and %d", len(childStepIDs))
	}
	return childStepIDs, strategy, required, nil
}

func parseParallelStepIDs(value any) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	appendStep := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	switch v := value.(type) {
	case nil:
		return nil, fmt.Errorf("parallel step input.steps required")
	case []string:
		for _, child := range v {
			appendStep(child)
		}
	case []any:
		for _, child := range v {
			if childStr, ok := child.(string); ok {
				appendStep(childStr)
				continue
			}
			return nil, fmt.Errorf("parallel step id must be string, got %T", child)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return []string{}, nil
		}
		for _, item := range strings.Split(trimmed, ",") {
			appendStep(item)
		}
	default:
		return nil, fmt.Errorf("parallel input.steps must be array/string, got %T", value)
	}
	if len(out) == 0 {
		return []string{}, nil
	}
	return out, nil
}

func normalizeParallelStrategy(value any) (string, error) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	switch strings.ToLower(raw) {
	case "", "all":
		return "all", nil
	case "any":
		return "any", nil
	case "n_of_m", "n-of-m", "nofm":
		return "n_of_m", nil
	default:
		return "", fmt.Errorf("parallel strategy must be all, any, or n_of_m")
	}
}

func summarizeParallelChildren(parent *StepRun, childStepIDs []string) (succeeded int, failed int, running int) {
	for _, childStepID := range childStepIDs {
		var child *StepRun
		if parent != nil {
			child = parent.Children[childStepID]
		}
		if child == nil {
			continue
		}
		switch child.Status {
		case StepStatusSucceeded:
			succeeded++
		case StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
			failed++
		case StepStatusRunning, StepStatusWaiting:
			running++
		}
	}
	return succeeded, failed, running
}

func evaluateParallelOutcome(strategy string, required, total, succeeded, failed int) (done bool, success bool, message string) {
	switch strategy {
	case "all":
		if failed > 0 {
			return true, false, "parallel strategy all failed because at least one child failed"
		}
		if succeeded == total {
			return true, true, ""
		}
		return false, false, ""
	case "any":
		if succeeded > 0 {
			return true, true, ""
		}
		if failed == total {
			return true, false, "parallel strategy any exhausted all children without success"
		}
		return false, false, ""
	case "n_of_m":
		if succeeded >= required {
			return true, true, ""
		}
		remainingPossible := total - failed
		if remainingPossible < required {
			return true, false, fmt.Sprintf("parallel strategy n_of_m cannot reach required successes (%d/%d)", required, total)
		}
		return false, false, ""
	default:
		return true, false, fmt.Sprintf("unsupported parallel strategy: %s", strategy)
	}
}

func (e *Engine) cancelParallelChildren(parent *StepRun, run *WorkflowRun, childStepIDs []string, now time.Time) int {
	cancelled := 0
	for _, childStepID := range childStepIDs {
		if parent == nil {
			break
		}
		child := parent.Children[childStepID]
		if child == nil && run != nil {
			child = run.Steps[childStepID]
		}
		if child == nil || isTerminalStepStatus(child.Status) {
			continue
		}
		if child.JobID != "" {
			if err := e.publishJobCancel(child.JobID, "parallel strategy satisfied"); err != nil {
				slog.Error("cancel parallel child publish failed",
					"job_id", child.JobID,
					"step_id", childStepID,
					"err", err,
				)
			}
		}
		child.Status = StepStatusCancelled
		child.CompletedAt = &now
		if child.Error == nil {
			child.Error = map[string]any{"message": "cancelled by parallel strategy"}
		}
		parent.Children[childStepID] = child
		if run != nil {
			run.Steps[childStepID] = child
		}
		cancelled++
	}
	return cancelled
}

func aggregateParallelOutputs(run *WorkflowRun, childStepIDs []string) map[string]any {
	outputs := make(map[string]any, len(childStepIDs))
	if run == nil {
		return outputs
	}
	stepsCtx, _ := run.Context["steps"].(map[string]any)
	for _, childStepID := range childStepIDs {
		if entry, ok := stepsCtx[childStepID]; ok {
			outputs[childStepID] = entry
			continue
		}
		sr := run.Steps[childStepID]
		if sr == nil {
			outputs[childStepID] = nil
			continue
		}
		if sr.Output != nil {
			outputs[childStepID] = sr.Output
			continue
		}
		if sr.Error != nil {
			outputs[childStepID] = map[string]any{"error": sr.Error}
			continue
		}
		outputs[childStepID] = nil
	}
	return outputs
}
