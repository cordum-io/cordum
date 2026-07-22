package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var (
	errResultResourceEmpty    = errors.New("workflow result resource is empty")
	errResultResourceTooLarge = errors.New("workflow result resource exceeds inline limit")
	errResultMediaType        = errors.New("workflow result resource does not match its media type")
)

type resolvedStepOutput struct {
	value     any
	content   []byte
	mediaType string
}

// WithResourceRegistry installs the operator-controlled result registry.
func (e *Engine) WithResourceRegistry(registry *resource.Registry) *Engine {
	e.resourceReader.Resolver = registry
	return e
}

// WithLegacyResourceCompatibility explicitly enables migration-only pointers.
func (e *Engine) WithLegacyResourceCompatibility(observe func(resourceio.LegacyUse)) *Engine {
	e.resourceReader.Compatibility = resourceio.LegacyCompatibility{Enabled: true, Observe: observe}
	return e
}

func hasJobResultResource(res *pb.JobResult) bool {
	return res != nil && (res.GetResultRef() != nil || res.GetResultPtr() != "")
}

func (e *Engine) resolveStepOutput(
	ctx context.Context,
	run *WorkflowRun,
	res *pb.JobResult,
) (resolvedStepOutput, error) {
	if e == nil || run == nil || !hasJobResultResource(res) {
		return resolvedStepOutput{}, errResultResourceEmpty
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()
	trusted := resource.TrustedContext{TenantID: run.OrgID, JobID: res.GetJobId()}
	resolved, err := e.resourceReader.Read(ctx, resourceio.ReadRequest{
		Reference: res.GetResultRef(), LegacyPointer: res.GetResultPtr(), Trusted: trusted,
		Component: "workflow.result",
		LoadLegacy: func(loadCtx context.Context, pointer string) ([]byte, error) {
			return e.loadLegacyResultResource(loadCtx, pointer, trusted)
		},
	})
	if err != nil {
		return resolvedStepOutput{}, err
	}
	if len(resolved.Content) == 0 {
		return resolvedStepOutput{}, errResultResourceEmpty
	}
	if len(resolved.Content) > maxInlineResultBytes {
		return resolvedStepOutput{}, errResultResourceTooLarge
	}
	content := append([]byte(nil), resolved.Content...)
	resolved.Content = content
	mediaType := workflowResultMediaType(resolved)
	value, err := decodeWorkflowResult(resolved)
	return resolvedStepOutput{value: value, content: content, mediaType: mediaType}, err
}

func (e *Engine) loadLegacyResultResource(
	ctx context.Context,
	pointer string,
	trusted resource.TrustedContext,
) ([]byte, error) {
	if e.mem == nil {
		return nil, resourceio.ErrLegacyLoaderMissing
	}
	key, err := resourceio.BoundLegacyKey(pointer, resourceio.LegacyResult, trusted)
	if err != nil {
		return nil, err
	}
	return e.mem.GetResult(ctx, key)
}

func decodeWorkflowResult(resolved resource.ResolvedResource) (any, error) {
	mediaType := workflowResultMediaType(resolved)
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		var value any
		if json.Unmarshal(resolved.Content, &value) == nil {
			return value, nil
		}
		return nil, errResultMediaType
	}
	if strings.HasPrefix(mediaType, "text/") {
		if !utf8.Valid(resolved.Content) {
			return nil, errResultMediaType
		}
		return string(resolved.Content), nil
	}
	return nil, errResultMediaType
}

func workflowResultMediaType(resolved resource.ResolvedResource) string {
	mediaType := strings.ToLower(strings.TrimSpace(resolved.MediaType))
	if mediaType != "" {
		return mediaType
	}
	var value any
	if json.Unmarshal(resolved.Content, &value) == nil {
		return "application/json"
	}
	return "text/plain"
}

func (e *Engine) validateResolvedStepOutput(ctx context.Context, step *Step, output any) error {
	if step == nil {
		return nil
	}
	if len(step.OutputSchema) > 0 {
		return schema.ValidateMap(step.OutputSchema, output)
	}
	id := strings.TrimSpace(step.OutputSchemaID)
	if id == "" {
		return nil
	}
	if e.schemaRegistry == nil {
		return fmt.Errorf("schema registry unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, validationTimeout)
	defer cancel()
	return e.schemaRegistry.ValidateID(ctx, id, output)
}

func (e *Engine) processStepOutput(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	step *Step,
	stepRun *StepRun,
	res *pb.JobResult,
	applyOutputPath bool,
) bool {
	if e == nil || run == nil || stepRun == nil || res == nil {
		return false
	}
	if e.canRecordBoundLegacyPointer(step, res) {
		if err := e.recordBoundLegacyPointer(ctx, run, stepID, step, res, applyOutputPath); err != nil {
			e.failStepOutput(ctx, run, stepID, stepRun, res, err)
			return false
		}
		stepRun.Output = res.GetResultPtr()
		return true
	}
	output, err := e.resolveAndValidateStepOutput(ctx, run, step, res)
	if err != nil {
		e.failStepOutput(ctx, run, stepID, stepRun, res, err)
		return false
	}
	originalPointer := res.GetResultPtr()
	if e.checkStepOutputPolicy(ctx, run, stepID, stepRun, res, output) {
		return false
	}
	if res.GetResultPtr() != originalPointer {
		output, err = e.resolveAndValidateStepOutput(ctx, run, step, res)
		if err != nil {
			e.failStepOutput(ctx, run, stepID, stepRun, res, err)
			return false
		}
	}
	e.recordResolvedStepOutput(run, stepID, step, res, output, applyOutputPath)
	stepRun.Output = output.value
	return true
}

func (e *Engine) canRecordBoundLegacyPointer(step *Step, res *pb.JobResult) bool {
	if e == nil || res == nil || res.GetResultRef() != nil || e.mem != nil || e.outputSafety != nil {
		return false
	}
	if !e.resourceReader.Compatibility.Enabled || res.GetResultPtr() == "" {
		return false
	}
	return step == nil || (len(step.OutputSchema) == 0 &&
		strings.TrimSpace(step.OutputSchemaID) == "" && strings.TrimSpace(step.ResultDataPath) == "")
}

func (e *Engine) recordBoundLegacyPointer(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	step *Step,
	res *pb.JobResult,
	applyOutputPath bool,
) error {
	trusted := resource.TrustedContext{TenantID: run.OrgID, JobID: res.GetJobId()}
	if _, err := resourceio.BoundLegacyKey(res.GetResultPtr(), resourceio.LegacyResult, trusted); err != nil {
		return err
	}
	if observe := e.resourceReader.Compatibility.Observe; observe != nil {
		observe(resourceio.LegacyUse{
			Component: "workflow.result", TenantID: trusted.TenantID, JobID: trusted.JobID,
		})
	}
	recordStepOutput(ctx, nil, run, stepID, step, res.GetResultPtr(), applyOutputPath)
	return nil
}

func (e *Engine) resolveAndValidateStepOutput(
	ctx context.Context,
	run *WorkflowRun,
	step *Step,
	res *pb.JobResult,
) (resolvedStepOutput, error) {
	output, err := e.resolveStepOutput(ctx, run, res)
	if err != nil {
		return resolvedStepOutput{}, err
	}
	value := output.value
	if step != nil && strings.TrimSpace(step.ResultDataPath) != "" {
		var found bool
		value, found = extractDataPath(value, step.ResultDataPath)
		if !found {
			return resolvedStepOutput{}, fmt.Errorf("output result_data_path not found")
		}
	}
	if err := e.validateResolvedStepOutput(ctx, step, value); err != nil {
		return resolvedStepOutput{}, err
	}
	output.value = value
	return output, nil
}

func (e *Engine) failStepOutput(
	ctx context.Context,
	run *WorkflowRun,
	stepID string,
	stepRun *StepRun,
	res *pb.JobResult,
	err error,
) {
	now := time.Now().UTC()
	message := "workflow result rejected"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		message = "workflow result processing cancelled"
	}
	stepRun.Status = StepStatusFailed
	stepRun.CompletedAt = &now
	stepRun.Output = nil
	stepRun.Error = map[string]any{"message": message}
	e.appendTimeline(ctx, run, "step_output_invalid", stepID, res.GetJobId(), string(stepRun.Status), "", message, nil)
}

func (e *Engine) recordResolvedStepOutput(
	run *WorkflowRun,
	stepID string,
	step *Step,
	res *pb.JobResult,
	output resolvedStepOutput,
	applyOutputPath bool,
) {
	if run.Context == nil {
		run.Context = map[string]any{}
	}
	steps, ok := run.Context["steps"].(map[string]any)
	if !ok || steps == nil {
		steps = map[string]any{}
		run.Context["steps"] = steps
	}
	entry := map[string]any{"output": output.value}
	if res.GetResultRef() == nil && e.resourceReader.Compatibility.Enabled {
		entry["result_ptr"] = res.GetResultPtr()
	}
	if applyOutputPath && step != nil && strings.TrimSpace(step.OutputPath) != "" {
		if err := setContextPath(run.Context, step.OutputPath, output.value); err != nil {
			entry["output_path_error"] = "invalid output path"
		}
	}
	steps[stepID] = entry
}
