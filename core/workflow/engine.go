package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Engine coordinates workflow runs, dispatching steps as jobs and updating run state.
type Engine struct {
	store *RedisStore
	bus   scheduler.Bus
	mu    sync.Mutex
	// optional callbacks for observability or hooks
	OnStepDispatched func(runID, stepID, jobID string)
	OnStepFinished   func(runID, stepID string, status StepStatus)
	config           ConfigProvider
}

// ConfigProvider supplies effective config given identity context.
type ConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// NewEngine creates a workflow engine bound to a Redis workflow store and bus.
func NewEngine(store *RedisStore, bus scheduler.Bus) *Engine {
	return &Engine{store: store, bus: bus}
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
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed {
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
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		logging.Error("workflow-engine", "get workflow failed", "workflow_id", run.WorkflowID, "error", err)
		return
	}

	baseStepID, childKey := splitForEachStep(stepID)
	stepDef := wfDef.Steps[baseStepID]
	now := time.Now().UTC()

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
			child = &StepRun{StepID: stepID}
		}
		retry, delay := applyResult(child, res, stepDef)
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
		if stepRun == nil {
			stepRun = &StepRun{StepID: stepID}
		}
		retry, delay := applyResult(stepRun, res, stepDef)
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
	for id, sr := range run.Steps {
		if sr == nil {
			sr = &StepRun{StepID: id}
		}
		switch sr.Status {
		case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
			// leave terminal states
		default:
			sr.Status = StepStatusCancelled
			sr.CompletedAt = &now
			run.Steps[id] = sr
		}
	}
	run.Status = RunStatusCancelled
	run.CompletedAt = &now
	run.UpdatedAt = now
	updateRunStatus(run, wfDef, now)
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	return nil
}

func (e *Engine) scheduleReady(ctx context.Context, wfDef *Workflow, run *WorkflowRun) error {
	if wfDef == nil || run == nil {
		return fmt.Errorf("workflow/run required")
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed {
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
			ok, err := evalCondition(step.Condition, run.Input, run.Context)
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
			items, err := evalForEach(step.ForEach, run.Input, run.Context)
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
			for idx, item := range items {
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
				jobID := fmt.Sprintf("%s:%s", run.ID, childID)
				req := e.buildJobRequest(ctx, wfDef, run, step, childID, jobID)
				// Attach for-each metadata
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["foreach_index"] = fmt.Sprintf("%d", idx)
				if data, err := json.Marshal(item); err == nil {
					req.Env["foreach_item"] = string(data)
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
					child.Item = item
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

		jobID := fmt.Sprintf("%s:%s", run.ID, stepID)
		req := e.buildJobRequest(ctx, wfDef, run, step, stepID, jobID)

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
			if e.OnStepDispatched != nil {
				e.OnStepDispatched(run.ID, stepID, jobID)
			}
		}
		run.Steps[stepID] = parentSR
	}

	run.UpdatedAt = now
	return e.store.UpdateRun(ctx, run)
}

func evalCondition(expr string, input map[string]any, ctx map[string]any) (bool, error) {
	scope := map[string]any{
		"input": input,
		"ctx":   ctx,
	}
	val, err := Eval(expr, scope)
	if err != nil {
		return false, err
	}
	return truthy(val), nil
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

func (e *Engine) buildJobRequest(ctx context.Context, wfDef *Workflow, run *WorkflowRun, step *Step, stepID, jobID string) *pb.JobRequest {
	subject := step.Topic
	if subject == "" {
		subject = "job.workflow." + wfDef.ID
	}
	req := &pb.JobRequest{
		JobId:     jobID,
		Topic:     subject,
		Priority:  pb.JobPriority_JOB_PRIORITY_BATCH,
		AdapterId: step.WorkerID,
		Env: map[string]string{
			"workflow_id": wfDef.ID,
			"run_id":      run.ID,
			"step_id":     stepID,
			"tenant_id":   run.OrgID,
			"team_id":     run.TeamID,
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

func evalForEach(expr string, input map[string]any, ctx map[string]any) ([]any, error) {
	scope := map[string]any{
		"input": input,
		"ctx":   ctx,
	}
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
		sr.Status = StepStatusFailed
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
	delay := float64(initial) * math.Pow(mult, float64(sr.Attempts-1))
	if cfg.MaxBackoffSec > 0 && delay > float64(cfg.MaxBackoffSec) {
		delay = float64(cfg.MaxBackoffSec)
	}
	return time.Duration(delay) * time.Second
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
	hasFailed := false
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
		case StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
			hasFailed = true
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
