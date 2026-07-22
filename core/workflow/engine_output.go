package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	schemas "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
)

// validationTimeout bounds Redis calls during step input/output schema
// validation. Without this, a slow or unresponsive Redis would block the
// workflow engine indefinitely.
const validationTimeout = 5 * time.Second

func recordStepOutput(ctx context.Context, mem store.Store, run *WorkflowRun, stepID string, stepDef *Step, resultPtr string, applyOutputPath bool) {
	if run == nil || stepID == "" || resultPtr == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if run.Context == nil {
		run.Context = map[string]any{}
	}
	steps, ok := run.Context["steps"].(map[string]any)
	if !ok || steps == nil {
		steps = map[string]any{}
		run.Context["steps"] = steps
	}

	entry := map[string]any{"result_ptr": resultPtr}
	if inline, ok := inlineResult(ctx, mem, resultPtr); ok {
		effective := inline
		// If result_data_path is set, extract the sub-field as the effective output.
		if stepDef != nil {
			if rdp := strings.TrimSpace(stepDef.ResultDataPath); rdp != "" {
				if extracted, found := extractDataPath(inline, rdp); found {
					effective = extracted
				}
			}
		}
		entry["output"] = effective
		if applyOutputPath && stepDef != nil {
			if path := strings.TrimSpace(stepDef.OutputPath); path != "" {
				if pathErr := setContextPath(run.Context, path, effective); pathErr != nil {
					slog.Warn("workflow: setContextPath failed for output",
						"path", path, "error", pathErr)
				}
			}
		}
	} else if applyOutputPath && stepDef != nil {
		if path := strings.TrimSpace(stepDef.OutputPath); path != "" {
			if pathErr := setContextPath(run.Context, path, resultPtr); pathErr != nil {
				slog.Warn("workflow: setContextPath failed for result ptr",
					"path", path, "error", pathErr)
			}
		}
	}

	steps[stepID] = entry
}

func recordStepInlineOutput(run *WorkflowRun, stepID string, stepDef *Step, output any) {
	if run == nil || stepID == "" {
		return
	}
	if run.Context == nil {
		run.Context = map[string]any{}
	}
	steps, ok := run.Context["steps"].(map[string]any)
	if !ok || steps == nil {
		steps = map[string]any{}
		run.Context["steps"] = steps
	}
	entry := map[string]any{"output": output}
	if stepDef != nil {
		if path := strings.TrimSpace(stepDef.OutputPath); path != "" {
			if pathErr := setContextPath(run.Context, path, output); pathErr != nil {
				slog.Warn("workflow: setContextPath failed for inline output",
					"path", path, "error", pathErr)
			}
		}
	}
	steps[stepID] = entry
}

func inlineResult(ctx context.Context, mem store.Store, resultPtr string) (any, bool) {
	if mem == nil || resultPtr == "" {
		return nil, false
	}
	key, err := store.KeyFromPointer(resultPtr)
	if err != nil {
		return nil, false
	}
	data, err := mem.GetResult(ctx, key)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	if len(data) > maxInlineResultBytes {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(data, &out); err == nil {
		return out, true
	}
	return string(data), true
}

func (e *Engine) validateStepInput(step *Step, payload map[string]any) error {
	if step == nil {
		return nil
	}
	if len(step.InputSchema) > 0 {
		return schemas.ValidateMap(step.InputSchema, payload)
	}
	if id := strings.TrimSpace(step.InputSchemaID); id != "" {
		if e.schemaRegistry == nil {
			return fmt.Errorf("schema registry unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
		defer cancel()
		return e.schemaRegistry.ValidateID(ctx, id, payload)
	}
	return nil
}

func (e *Engine) validateStepOutput(step *Step, resultPtr string) error {
	if step == nil || resultPtr == "" {
		return nil
	}
	hasInline := len(step.OutputSchema) > 0
	id := strings.TrimSpace(step.OutputSchemaID)
	if !hasInline && id == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
	defer cancel()
	payload, ok := fetchResultPayload(ctx, e.mem, resultPtr)
	if !ok {
		// Fail-closed: if a schema is configured but the result payload cannot
		// be fetched, reject the step rather than silently skipping validation.
		return fmt.Errorf("output schema validation failed: unable to fetch result payload %q", resultPtr)
	}
	// If result_data_path is set, extract the sub-field for validation.
	if rdp := strings.TrimSpace(step.ResultDataPath); rdp != "" {
		extracted, found := extractDataPath(payload, rdp)
		if !found {
			return fmt.Errorf("output schema validation failed: result_data_path %q not found in job result", rdp)
		}
		payload = extracted
	}
	if hasInline {
		return schemas.ValidateMap(step.OutputSchema, payload)
	}
	if e.schemaRegistry == nil {
		return fmt.Errorf("schema registry unavailable")
	}
	return e.schemaRegistry.ValidateID(ctx, id, payload)
}

func fetchResultPayload(ctx context.Context, mem store.Store, resultPtr string) (any, bool) {
	if mem == nil || resultPtr == "" {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := store.KeyFromPointer(resultPtr)
	if err != nil {
		return nil, false
	}
	data, err := mem.GetResult(ctx, key)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(data, &out); err == nil {
		return out, true
	}
	return string(data), true
}

// extractDataPath navigates a dot-separated path into a value. Works on
// map[string]any at each level. Returns (value, true) if the path is found,
// or (nil, false) if any segment is missing or the value is not a map at
// an intermediate level.
func extractDataPath(value any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return value, true
	}
	parts := strings.Split(path, ".")
	cur := value
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func (e *Engine) validateInlineOutput(step *Step, value any) error {
	if step == nil {
		return nil
	}
	if len(step.OutputSchema) > 0 {
		return schemas.ValidateMap(step.OutputSchema, value)
	}
	if id := strings.TrimSpace(step.OutputSchemaID); id != "" {
		if e.schemaRegistry == nil {
			return fmt.Errorf("schema registry unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), validationTimeout)
		defer cancel()
		return e.schemaRegistry.ValidateID(ctx, id, value)
	}
	return nil
}
