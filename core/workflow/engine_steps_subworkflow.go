package workflow

import (
	"fmt"
	"strings"
)

func resolveSubWorkflowConfig(step *Step, scope map[string]any) (workflowID string, inputMapping any, outputMapping any, err error) {
	if step == nil {
		return "", nil, nil, fmt.Errorf("subworkflow step required")
	}
	if step.Input == nil {
		return "", nil, nil, fmt.Errorf("subworkflow input required")
	}
	rawWorkflowID, ok := step.Input["workflow_id"]
	if !ok {
		return "", nil, nil, fmt.Errorf("subworkflow input.workflow_id required")
	}
	evaledWorkflowID, err := evalTemplates(rawWorkflowID, scope)
	if err != nil {
		return "", nil, nil, fmt.Errorf("subworkflow workflow_id eval failed: %w", err)
	}
	workflowID = strings.TrimSpace(fmt.Sprint(evaledWorkflowID))
	if workflowID == "" {
		return "", nil, nil, fmt.Errorf("subworkflow workflow_id required")
	}
	if rawInputMapping, ok := step.Input["input_mapping"]; ok {
		inputMapping, err = evalTemplates(rawInputMapping, scope)
		if err != nil {
			return "", nil, nil, fmt.Errorf("subworkflow input_mapping eval failed: %w", err)
		}
	}
	if rawOutputMapping, ok := step.Input["output_mapping"]; ok {
		outputMapping = rawOutputMapping
	}
	return workflowID, inputMapping, outputMapping, nil
}

func normalizeSubWorkflowInput(parentRun *WorkflowRun, inputMapping any) (map[string]any, error) {
	if inputMapping == nil {
		if parentRun == nil || parentRun.Input == nil {
			return map[string]any{}, nil
		}
		return cloneMap(parentRun.Input), nil
	}
	return normalizeSubWorkflowMap(inputMapping, "input_mapping")
}

func normalizeSubWorkflowMap(value any, fieldName string) (map[string]any, error) {
	switch typed := value.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return cloneMap(typed), nil
	case map[string]string:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("subworkflow %s must evaluate to object, got %T", fieldName, value)
	}
}

func buildSubWorkflowOutput(parentRun, childRun *WorkflowRun, outputMapping any) (map[string]any, error) {
	if childRun == nil {
		return nil, fmt.Errorf("child run required")
	}
	if outputMapping == nil {
		return map[string]any{
			"child_run_id":      childRun.ID,
			"child_workflow_id": childRun.WorkflowID,
			"child_status":      string(childRun.Status),
			"output":            childRun.Output,
			"steps":             runSteps(childRun),
		}, nil
	}
	scope := buildSubWorkflowScope(parentRun, childRun)
	evaledOutput, err := evalTemplates(outputMapping, scope)
	if err != nil {
		return nil, fmt.Errorf("subworkflow output_mapping eval failed: %w", err)
	}
	mappedOutput, err := normalizeSubWorkflowMap(evaledOutput, "output_mapping")
	if err != nil {
		return nil, err
	}
	if _, ok := mappedOutput["child_run_id"]; !ok {
		mappedOutput["child_run_id"] = childRun.ID
	}
	if _, ok := mappedOutput["child_workflow_id"]; !ok {
		mappedOutput["child_workflow_id"] = childRun.WorkflowID
	}
	if _, ok := mappedOutput["child_status"]; !ok {
		mappedOutput["child_status"] = string(childRun.Status)
	}
	return mappedOutput, nil
}

func buildSubWorkflowScope(parentRun, childRun *WorkflowRun) map[string]any {
	scope := buildEvalScope(parentRun, nil)
	child := map[string]any{}
	if childRun != nil {
		child = map[string]any{
			"id":          childRun.ID,
			"workflow_id": childRun.WorkflowID,
			"status":      string(childRun.Status),
			"input":       childRun.Input,
			"output":      childRun.Output,
			"ctx":         childRun.Context,
			"steps":       runSteps(childRun),
			"error":       childRun.Error,
		}
	}
	scope["child"] = child
	scope["subworkflow"] = child
	return scope
}

func parseCallStack(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	normalized := strings.NewReplacer(",", ">", ";", ">").Replace(raw)
	parts := strings.Split(normalized, ">")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func encodeCallStack(stack []string) string {
	cleaned := make([]string, 0, len(stack))
	for _, entry := range stack {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, ">")
}

func normalizeCallStackForRun(run *WorkflowRun) []string {
	if run == nil {
		return nil
	}
	stack := parseCallStack(run.Metadata["call_stack"])
	current := strings.TrimSpace(run.WorkflowID)
	if current == "" {
		return stack
	}
	if len(stack) == 0 || stack[len(stack)-1] != current {
		stack = append(stack, current)
	}
	return stack
}

func containsString(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func subWorkflowTerminalStatus(status RunStatus) StepStatus {
	switch status {
	case RunStatusCancelled:
		return StepStatusCancelled
	case RunStatusTimedOut:
		return StepStatusTimedOut
	default:
		return StepStatusFailed
	}
}

func subWorkflowChildErrorMessage(childRun *WorkflowRun) string {
	if childRun == nil || childRun.Steps == nil {
		return ""
	}
	for _, sr := range childRun.Steps {
		if sr == nil {
			continue
		}
		switch sr.Status {
		case StepStatusFailed, StepStatusTimedOut, StepStatusCancelled:
			if sr.Error != nil {
				if msg, ok := sr.Error["message"].(string); ok && strings.TrimSpace(msg) != "" {
					return msg
				}
			}
		}
	}
	return ""
}
