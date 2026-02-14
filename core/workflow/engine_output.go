package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/logging"
	schemas "github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// checkStepOutputPolicy runs output policy on a step result before propagating it.
// Returns true if the output was quarantined/denied and should NOT be recorded.
func (e *Engine) checkStepOutputPolicy(ctx context.Context, run *WorkflowRun, stepID string, stepRun *StepRun, res *pb.JobResult) bool {
	if e.outputSafety == nil || res == nil || res.ResultPtr == "" {
		return false
	}
	record, err := e.outputSafety.CheckOutputMeta(res, nil)
	if err != nil {
		logging.Error("workflow-engine", "step output policy check failed", "run_id", run.ID, "step_id", stepID, "error", err)
		return false // fail-open on error to preserve backward compat
	}
	now := time.Now().UTC()
	switch record.Decision {
	case model.OutputQuarantine, model.OutputDeny:
		stepRun.Status = StepStatusFailed
		stepRun.CompletedAt = &now
		stepRun.Error = map[string]any{
			"code":    "output_quarantined",
			"message": record.Reason,
		}
		e.appendTimeline(ctx, run, "step_output_quarantined", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, record.Reason, nil)
		return true
	case model.OutputRedact:
		if record.RedactedPtr != "" {
			res.ResultPtr = record.RedactedPtr
		}
		e.appendTimeline(ctx, run, "step_output_redacted", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, record.Reason, nil)
		return false
	default:
		return false
	}
}

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
		entry["output"] = inline
		if applyOutputPath && stepDef != nil {
			if path := strings.TrimSpace(stepDef.OutputPath); path != "" {
				_ = setContextPath(run.Context, path, inline)
			}
		}
	} else if applyOutputPath && stepDef != nil {
		if path := strings.TrimSpace(stepDef.OutputPath); path != "" {
			_ = setContextPath(run.Context, path, resultPtr)
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
			_ = setContextPath(run.Context, path, output)
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
		return e.schemaRegistry.ValidateID(context.Background(), id, payload)
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
	payload, ok := fetchResultPayload(context.Background(), e.mem, resultPtr)
	if !ok {
		return nil
	}
	if hasInline {
		return schemas.ValidateMap(step.OutputSchema, payload)
	}
	if e.schemaRegistry == nil {
		return fmt.Errorf("schema registry unavailable")
	}
	return e.schemaRegistry.ValidateID(context.Background(), id, payload)
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
		return e.schemaRegistry.ValidateID(context.Background(), id, value)
	}
	return nil
}
