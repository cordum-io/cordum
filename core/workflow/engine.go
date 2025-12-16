package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Engine coordinates workflow runs, dispatching steps as jobs and updating run state.
type Engine struct {
	store *RedisStore
	bus   scheduler.Bus
	mem   memory.Store
	mu    sync.Mutex
	// optional callbacks for observability or hooks
	OnStepDispatched func(runID, stepID, jobID string)
	OnStepFinished   func(runID, stepID string, status StepStatus)
	config           ConfigProvider
}

const maxInlineResultBytes = 256 << 10

// ConfigProvider supplies effective config given identity context.
type ConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// NewEngine creates a workflow engine bound to a Redis workflow store and bus.
func NewEngine(store *RedisStore, bus scheduler.Bus) *Engine {
	return &Engine{store: store, bus: bus}
}

// WithMemory sets an optional memory store used to persist per-step job context payloads.
func (e *Engine) WithMemory(store memory.Store) *Engine {
	e.mem = store
	return e
}

// WithConfig sets an optional config provider.
func (e *Engine) WithConfig(cfg ConfigProvider) *Engine {
	e.config = cfg
	return e
}

// StartRun loads the workflow/run and dispatches any ready steps.
func (e *Engine) StartRun(ctx context.Context, workflowID, runID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	wfDef, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed || run.Status == RunStatusSucceeded || run.Status == RunStatusTimedOut {
		return nil
	}
	return e.scheduleReady(ctx, wfDef, run)
}

// HandleJobResult updates step/run state and dispatches next steps if ready.
func (e *Engine) HandleJobResult(ctx context.Context, res *pb.JobResult) {
	if res == nil || res.JobId == "" {
		return
	}
	runID, stepID := splitJobID(res.JobId)
	if runID == "" || stepID == "" {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		logging.Error("workflow-engine", "get run failed", "run_id", runID, "error", err)
		return
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		logging.Error("workflow-engine", "get workflow failed", "workflow_id", run.WorkflowID, "error", err)
		return
	}

	baseStepID, childKey := splitForEachStep(stepID)
	stepDef := wfDef.Steps[baseStepID]
	now := time.Now().UTC()
	attempt := parseAttempt(res.JobId)

	if childKey != "" {
		parent := run.Steps[baseStepID]
		if parent == nil {
			parent = &StepRun{StepID: baseStepID}
		}
		if parent.Children == nil {
			parent.Children = make(map[string]*StepRun)
		}
		child := parent.Children[stepID]
		if child == nil {
			child = run.Steps[stepID]
			if child == nil {
				child = &StepRun{StepID: stepID}
			}
		}
		if child.JobID != "" && child.JobID != res.JobId {
			return
		}
		if child.JobID == "" {
			child.JobID = res.JobId
		}
		if attempt > child.Attempts {
			child.Attempts = attempt
		}
		if child.JobID == res.JobId && shouldIgnoreProcessedResult(child) {
			return
		}
		retry, delay := applyResult(child, res, stepDef)
		if !retry && child.Status == StepStatusSucceeded && res.ResultPtr != "" {
			recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, false)
		}
		parent.Children[stepID] = child
		run.Steps[stepID] = child
		parent.Status = aggregateChildren(parent)
		if parent.Status == StepStatusSucceeded || parent.Status == StepStatusFailed {
			parent.CompletedAt = &now
		}
		run.Steps[baseStepID] = parent
		if retry && delay > 0 {
			e.scheduleAfter(delay, run.WorkflowID, run.ID)
		}
		if e.OnStepFinished != nil && !retry && (child.Status == StepStatusSucceeded || child.Status == StepStatusFailed || child.Status == StepStatusCancelled || child.Status == StepStatusTimedOut) {
			e.OnStepFinished(run.ID, stepID, child.Status)
		}
	} else {
		stepRun := run.Steps[stepID]
		if stepRun != nil && stepRun.JobID != "" && stepRun.JobID != res.JobId {
			return
		}
		if stepRun == nil {
			stepRun = &StepRun{StepID: stepID}
		}
		if stepRun.JobID == "" {
			stepRun.JobID = res.JobId
		}
		if attempt > stepRun.Attempts {
			stepRun.Attempts = attempt
		}
		if stepRun.JobID == res.JobId && shouldIgnoreProcessedResult(stepRun) {
			return
		}
		retry, delay := applyResult(stepRun, res, stepDef)
		if !retry && stepRun.Status == StepStatusSucceeded && res.ResultPtr != "" {
			recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, true)
		}
		run.Steps[stepID] = stepRun
		if retry && delay > 0 {
			e.scheduleAfter(delay, run.WorkflowID, run.ID)
		}
		if e.OnStepFinished != nil && !retry && (stepRun.Status == StepStatusSucceeded || stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusCancelled || stepRun.Status == StepStatusTimedOut) {
			e.OnStepFinished(run.ID, stepID, stepRun.Status)
		}
	}

	run.UpdatedAt = now
	updateRunStatus(run, wfDef, now)

	if err := e.store.UpdateRun(ctx, run); err != nil {
		logging.Error("workflow-engine", "update run", "run_id", run.ID, "error", err)
		return
	}

	if run.Status == RunStatusRunning {
		if err := e.scheduleReady(ctx, wfDef, run); err != nil {
			logging.Error("workflow-engine", "schedule ready", "run_id", run.ID, "error", err)
		}
	}
}

// ApproveStep resumes a waiting approval step.
func (e *Engine) ApproveStep(ctx context.Context, runID, stepID string, approved bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	sr := run.Steps[stepID]
	if sr == nil {
		return fmt.Errorf("step not found")
	}
	if sr.Status != StepStatusWaiting {
		return fmt.Errorf("step not waiting")
	}
	now := time.Now().UTC()
	if approved {
		sr.Status = StepStatusSucceeded
	} else {
		sr.Status = StepStatusFailed
	}
	sr.CompletedAt = &now
	run.Steps[stepID] = sr
	updateRunStatus(run, wfDef, now)
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	if approved && run.Status == RunStatusRunning {
		return e.scheduleReady(ctx, wfDef, run)
	}
	return nil
}

// CancelRun marks a run and all non-terminal steps as cancelled to prevent further dispatch.
func (e *Engine) CancelRun(ctx context.Context, runID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	now := time.Now().UTC()
	var cancelJobIDs []string
	for stepID := range wfDef.Steps {
		sr := run.Steps[stepID]
		if sr == nil {
			sr = &StepRun{StepID: stepID}
		}
		cancelJobIDs = append(cancelJobIDs, collectCancelableJobs(sr)...)
		cancelStepRun(sr, now)
		run.Steps[stepID] = sr
	}
	for id, sr := range run.Steps {
		if sr == nil {
			continue
		}
		cancelJobIDs = append(cancelJobIDs, collectCancelableJobs(sr)...)
		cancelStepRun(sr, now)
		run.Steps[id] = sr
	}
	run.Status = RunStatusCancelled
	run.CompletedAt = &now
	run.UpdatedAt = now
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(cancelJobIDs))
	for _, jobID := range cancelJobIDs {
		if jobID == "" {
			continue
		}
		if _, ok := seen[jobID]; ok {
			continue
		}
		seen[jobID] = struct{}{}
		e.publishJobCancel(jobID, "workflow run cancelled")
	}
	return nil
}

func cancelStepRun(sr *StepRun, now time.Time) {
	if sr == nil {
		return
	}
	switch sr.Status {
	case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
		// leave terminal states
	default:
		sr.Status = StepStatusCancelled
		sr.CompletedAt = &now
	}
	for _, child := range sr.Children {
		if child == nil {
			continue
		}
		cancelStepRun(child, now)
	}
}

func collectCancelableJobs(sr *StepRun) []string {
	if sr == nil {
		return nil
	}
	var out []string
	if sr.Status == StepStatusRunning && sr.JobID != "" {
		out = append(out, sr.JobID)
	}
	for _, child := range sr.Children {
		out = append(out, collectCancelableJobs(child)...)
	}
	return out
}

func (e *Engine) publishJobCancel(jobID, reason string) {
	if e == nil || e.bus == nil || jobID == "" {
		return
	}
	cancelReq := &pb.JobRequest{
		JobId: jobID,
		Topic: "sys.job.cancel",
		Env: map[string]string{
			"cancel_reason": reason,
		},
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        "workflow-engine",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: cancelReq},
	}
	_ = e.bus.Publish("sys.job.cancel", packet)
}

func (e *Engine) scheduleReady(ctx context.Context, wfDef *Workflow, run *WorkflowRun) error {
	if wfDef == nil || run == nil {
		return fmt.Errorf("workflow/run required")
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed || run.Status == RunStatusSucceeded || run.Status == RunStatusTimedOut {
		return nil
	}
	now := time.Now().UTC()
	if run.Status == RunStatusPending {
		run.Status = RunStatusRunning
		run.StartedAt = &now
	}

	for stepID, step := range wfDef.Steps {
		parentSR := run.Steps[stepID]
		if parentSR == nil {
			parentSR = &StepRun{StepID: stepID}
		}
		if parentSR.Status != "" && parentSR.Status != StepStatusPending && parentSR.Status != StepStatusWaiting {
			continue
		}
		if !depsSatisfied(step, run) {
			continue
		}
		// condition gate
		if step.Condition != "" {
			ok, err := evalCondition(step.Condition, buildEvalScope(run, nil))
			if err != nil {
				logging.Error("workflow-engine", "condition eval failed", "step_id", stepID, "error", err)
				continue
			}
			if !ok {
				parentSR.Status = StepStatusSucceeded
				t := now
				parentSR.StartedAt = &t
				parentSR.CompletedAt = &t
				run.Steps[stepID] = parentSR
				continue
			}
		}

		// Approval steps pause until explicitly approved/denied.
		if step.Type == StepTypeApproval {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusWaiting
				parentSR.StartedAt = &now
				run.Status = RunStatusWaiting
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// For-each fan-out.
		if step.ForEach != "" {
			items, err := evalForEach(step.ForEach, buildEvalScope(run, nil))
			if err != nil {
				logging.Error("workflow-engine", "for_each eval failed", "step_id", stepID, "error", err)
				continue
			}
			if parentSR.Children == nil {
				parentSR.Children = make(map[string]*StepRun)
			}
			if len(items) == 0 {
				parentSR.Status = StepStatusSucceeded
				parentSR.StartedAt = &now
				parentSR.CompletedAt = &now
				run.Steps[stepID] = parentSR
				continue
			}
			parentSR.Status = StepStatusRunning
			if parentSR.StartedAt == nil {
				parentSR.StartedAt = &now
			}
			runningChildren := 0
			for _, child := range parentSR.Children {
				if child != nil && child.Status == StepStatusRunning {
					runningChildren++
				}
			}
			for idx, item := range items {
				if step.MaxParallel > 0 && runningChildren >= step.MaxParallel {
					break
				}
				childID := fmt.Sprintf("%s[%d]", stepID, idx)
				child := parentSR.Children[childID]
				if child == nil {
					child = &StepRun{StepID: childID}
				}
				if child.Status != "" && child.Status != StepStatusPending {
					continue
				}
				if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
					continue
				}
				jobID := fmt.Sprintf("%s:%s@%d", run.ID, childID, child.Attempts+1)
				req := e.buildJobRequest(ctx, wfDef, run, step, childID, jobID)
				// Attach for-each metadata
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["foreach_index"] = fmt.Sprintf("%d", idx)
				if data, err := json.Marshal(item); err == nil {
					req.Env["foreach_item"] = string(data)
				}
				payload, err := e.buildJobPayload(run, step, item)
				if err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					continue
				}
				if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					child.Input = payload
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					continue
				} else if ptr != "" {
					req.ContextPtr = ptr
				}
				packet := makeJobPacket(run.ID, req)
				if err := e.bus.Publish("sys.job.submit", packet); err != nil {
					logging.Error("workflow-engine", "publish foreach step", "run_id", run.ID, "step_id", childID, "error", err)
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
				} else {
					child.Status = StepStatusRunning
					child.StartedAt = &now
					child.Attempts++
					child.JobID = jobID
					child.Input = payload
					child.Item = item
					runningChildren++
					if e.OnStepDispatched != nil {
						e.OnStepDispatched(run.ID, childID, jobID)
					}
				}
				parentSR.Children[childID] = child
				run.Steps[childID] = child
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Respect backoff windows for retrying steps.
		if parentSR.NextAttemptAt != nil && parentSR.NextAttemptAt.After(now) {
			run.Steps[stepID] = parentSR
			continue
		}

		jobID := fmt.Sprintf("%s:%s@%d", run.ID, stepID, parentSR.Attempts+1)
		req := e.buildJobRequest(ctx, wfDef, run, step, stepID, jobID)
		payload, err := e.buildJobPayload(run, step, nil)
		if err != nil {
			parentSR.Status = StepStatusFailed
			parentSR.Error = map[string]any{"message": err.Error()}
			run.Steps[stepID] = parentSR
			continue
		}
		if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
			parentSR.Status = StepStatusFailed
			parentSR.Error = map[string]any{"message": err.Error()}
			parentSR.Input = payload
			run.Steps[stepID] = parentSR
			continue
		} else if ptr != "" {
			req.ContextPtr = ptr
		}

		packet := makeJobPacket(run.ID, req)
		if err := e.bus.Publish("sys.job.submit", packet); err != nil {
			logging.Error("workflow-engine", "publish step", "run_id", run.ID, "step_id", stepID, "error", err)
			parentSR.Status = StepStatusFailed
			parentSR.Error = map[string]any{"message": err.Error()}
		} else {
			parentSR.Status = StepStatusRunning
			parentSR.StartedAt = &now
			parentSR.Attempts++
			parentSR.JobID = jobID
			parentSR.Input = payload
			if e.OnStepDispatched != nil {
				e.OnStepDispatched(run.ID, stepID, jobID)
			}
		}
		run.Steps[stepID] = parentSR
	}

	run.UpdatedAt = now
	return e.store.UpdateRun(ctx, run)
}

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

func recordStepOutput(ctx context.Context, mem memory.Store, run *WorkflowRun, stepID string, stepDef *Step, resultPtr string, applyOutputPath bool) {
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

func inlineResult(ctx context.Context, mem memory.Store, resultPtr string) (any, bool) {
	if mem == nil || resultPtr == "" {
		return nil, false
	}
	key, err := memory.KeyFromPointer(resultPtr)
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

func setContextPath(ctx map[string]any, path string, value any) error {
	if ctx == nil {
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	cur := ctx
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("invalid context path")
		}
		if i == len(parts)-1 {
			cur[part] = value
			return nil
		}
		next, ok := cur[part].(map[string]any)
		if !ok || next == nil {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
	return nil
}

func depsSatisfied(step *Step, run *WorkflowRun) bool {
	if step == nil || len(step.DependsOn) == 0 {
		return true
	}
	for _, dep := range step.DependsOn {
		sr, ok := run.Steps[dep]
		if !ok || sr.Status != StepStatusSucceeded {
			return false
		}
	}
	return true
}

func splitJobID(jobID string) (runID, stepID string) {
	parts := strings.Split(jobID, ":")
	if len(parts) < 2 {
		return "", ""
	}
	runID = strings.Join(parts[:len(parts)-1], ":")
	stepID = parts[len(parts)-1]
	if at := strings.LastIndex(stepID, "@"); at > 0 {
		stepID = stepID[:at]
	}
	return
}

func makeJobPacket(traceID string, req *pb.JobRequest) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "workflow-engine",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: req},
	}
}

func (e *Engine) buildJobPayload(run *WorkflowRun, step *Step, item any) (map[string]any, error) {
	base := map[string]any{}
	if step != nil && len(step.Input) > 0 {
		scope := buildEvalScope(run, item)
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
	out := make(map[string]any, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	if item != nil {
		if _, ok := out["item"]; !ok {
			out["item"] = item
		}
	}
	return out, nil
}

func (e *Engine) putJobContext(ctx context.Context, jobID string, payload map[string]any) (string, error) {
	if e.mem == nil {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal step input: %w", err)
	}
	key := memory.MakeContextKey(jobID)
	if err := e.mem.PutContext(ctx, key, data); err != nil {
		return "", fmt.Errorf("store step context: %w", err)
	}
	return memory.PointerForKey(key), nil
}

func defaultContextModeForTopic(topic string) string {
	if strings.HasPrefix(topic, "job.chat") {
		return "chat"
	}
	if strings.HasPrefix(topic, "job.code") || strings.HasPrefix(topic, "job.workflow.repo") {
		return "rag"
	}
	return "raw"
}

func (e *Engine) buildJobRequest(ctx context.Context, wfDef *Workflow, run *WorkflowRun, step *Step, stepID, jobID string) *pb.JobRequest {
	subject := step.Topic
	if subject == "" {
		subject = "job.workflow." + wfDef.ID
	}

	priority := pb.JobPriority_JOB_PRIORITY_BATCH
	if run != nil && run.Input != nil {
		if raw, ok := run.Input["priority"]; ok {
			if s, ok := raw.(string); ok {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "critical":
					priority = pb.JobPriority_JOB_PRIORITY_CRITICAL
				case "interactive":
					priority = pb.JobPriority_JOB_PRIORITY_INTERACTIVE
				case "batch":
					priority = pb.JobPriority_JOB_PRIORITY_BATCH
				}
			}
		}
	}
	memoryID := "run:" + run.ID
	if run != nil && run.Input != nil {
		if raw, ok := run.Input["memory_id"]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					memoryID = trimmed
				}
			}
		}
	}
	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      subject,
		Priority:   priority,
		AdapterId:  step.WorkerID,
		WorkflowId: wfDef.ID,
		MemoryId:   memoryID,
		Env: map[string]string{
			"workflow_id":  wfDef.ID,
			"run_id":       run.ID,
			"step_id":      stepID,
			"tenant_id":    run.OrgID,
			"team_id":      run.TeamID,
			"memory_id":    memoryID,
			"context_mode": defaultContextModeForTopic(subject),
		},
		Labels: map[string]string{
			"workflow_id": wfDef.ID,
			"run_id":      run.ID,
			"step_id":     stepID,
		},
		TenantId: run.OrgID,
	}
	if step.WorkerID != "" {
		req.Labels["worker_id"] = step.WorkerID
	}
	for k, v := range step.RouteLabels {
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels[k] = v
	}
	if step.TimeoutSec > 0 {
		req.Budget = &pb.Budget{
			DeadlineMs: step.TimeoutSec * 1000,
		}
	}
	if e.config != nil {
		if cfg, err := e.config.Effective(ctx, run.OrgID, run.TeamID, wfDef.ID, stepID); err == nil && cfg != nil {
			if data, err := json.Marshal(cfg); err == nil {
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["CORETEX_EFFECTIVE_CONFIG"] = string(data)
			}
		}
	}
	return req
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

func splitForEachStep(stepID string) (base string, child string) {
	idx := strings.Index(stepID, "[")
	if idx == -1 {
		return stepID, ""
	}
	return stepID[:idx], stepID
}

func applyResult(sr *StepRun, res *pb.JobResult, step *Step) (retry bool, delay time.Duration) {
	now := time.Now().UTC()
	switch res.Status {
	case pb.JobStatus_JOB_STATUS_SUCCEEDED:
		sr.Status = StepStatusSucceeded
		sr.CompletedAt = &now
		sr.NextAttemptAt = nil
		if res.ResultPtr != "" {
			sr.Output = res.ResultPtr
		}
		sr.Error = nil
	case pb.JobStatus_JOB_STATUS_FAILED, pb.JobStatus_JOB_STATUS_DENIED, pb.JobStatus_JOB_STATUS_TIMEOUT:
		if shouldRetry(step, sr) {
			delay = computeBackoff(step, sr)
			next := now.Add(delay)
			sr.NextAttemptAt = &next
			sr.Status = StepStatusPending
			sr.Error = map[string]any{"message": res.ErrorMessage}
			return true, delay
		}
		if res.Status == pb.JobStatus_JOB_STATUS_TIMEOUT {
			sr.Status = StepStatusTimedOut
		} else {
			sr.Status = StepStatusFailed
		}
		sr.CompletedAt = &now
		sr.Error = map[string]any{"message": res.ErrorMessage}
	case pb.JobStatus_JOB_STATUS_CANCELLED:
		sr.Status = StepStatusCancelled
		sr.CompletedAt = &now
	default:
		sr.Status = StepStatusFailed
		sr.CompletedAt = &now
		sr.Error = map[string]any{"message": fmt.Sprintf("unexpected status: %s", res.Status.String())}
	}
	return false, 0
}

func shouldRetry(step *Step, sr *StepRun) bool {
	if step == nil || step.Retry == nil {
		return false
	}
	max := step.Retry.MaxRetries
	if max <= 0 {
		return false
	}
	return sr.Attempts <= max
}

func computeBackoff(step *Step, sr *StepRun) time.Duration {
	if step == nil || step.Retry == nil {
		return time.Second
	}
	cfg := step.Retry
	initial := cfg.InitialBackoffSec
	if initial <= 0 {
		initial = 1
	}
	mult := cfg.Multiplier
	if mult <= 1 {
		mult = 2
	}
	attempt := sr.Attempts
	if attempt < 1 {
		attempt = 1
	}
	delay := float64(initial) * math.Pow(mult, float64(attempt-1))
	if cfg.MaxBackoffSec > 0 && delay > float64(cfg.MaxBackoffSec) {
		delay = float64(cfg.MaxBackoffSec)
	}
	return time.Duration(delay) * time.Second
}

func shouldIgnoreProcessedResult(sr *StepRun) bool {
	if sr == nil {
		return false
	}
	switch sr.Status {
	case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
		return true
	case StepStatusPending:
		return sr.NextAttemptAt != nil
	default:
		return false
	}
}

func parseAttempt(jobID string) int {
	at := strings.LastIndex(jobID, "@")
	if at == -1 || at == len(jobID)-1 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(jobID[at+1:]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func aggregateChildren(parent *StepRun) StepStatus {
	if len(parent.Children) == 0 {
		return parent.Status
	}
	allDone := true
	hasFailed := false
	for _, child := range parent.Children {
		switch child.Status {
		case StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
			hasFailed = true
		case StepStatusSucceeded:
		default:
			allDone = false
		}
	}
	if hasFailed {
		return StepStatusFailed
	}
	if allDone {
		return StepStatusSucceeded
	}
	return StepStatusRunning
}

func updateRunStatus(run *WorkflowRun, wfDef *Workflow, now time.Time) {
	if run == nil || wfDef == nil {
		return
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusTimedOut {
		return
	}
	hasFailed := false
	hasTimedOut := false
	waiting := false
	allDone := true
	completed := 0
	for stepID := range wfDef.Steps {
		sr := run.Steps[stepID]
		if sr == nil {
			allDone = false
			continue
		}
		switch sr.Status {
		case StepStatusFailed, StepStatusCancelled:
			hasFailed = true
		case StepStatusTimedOut:
			hasTimedOut = true
		case StepStatusSucceeded:
			completed++
		case StepStatusWaiting:
			waiting = true
			allDone = false
		default:
			allDone = false
		}
	}
	if hasFailed {
		run.Status = RunStatusFailed
		run.CompletedAt = &now
		return
	}
	if hasTimedOut {
		run.Status = RunStatusTimedOut
		run.CompletedAt = &now
		return
	}
	if waiting {
		run.Status = RunStatusWaiting
		return
	}
	if allDone && completed == len(wfDef.Steps) {
		run.Status = RunStatusSucceeded
		run.CompletedAt = &now
		return
	}
	run.Status = RunStatusRunning
}

func (e *Engine) scheduleAfter(delay time.Duration, workflowID, runID string) {
	if delay <= 0 {
		return
	}
	go func() {
		time.Sleep(delay)
		_ = e.StartRun(context.Background(), workflowID, runID)
	}()
}
