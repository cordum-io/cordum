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

	"github.com/google/uuid"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/memory"
	schemas "github.com/cordum/cordum/core/infra/schema"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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
	schemaRegistry   *schemas.Registry
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

// WithSchemaRegistry sets an optional schema registry for validating inputs/outputs.
func (e *Engine) WithSchemaRegistry(registry *schemas.Registry) *Engine {
	e.schemaRegistry = registry
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

// RerunFrom creates a new run that reuses inputs and optionally resumes from a step.
func (e *Engine) RerunFrom(ctx context.Context, runID, stepID string, dryRun bool) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if runID == "" {
		return "", fmt.Errorf("run id required")
	}
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("get run: %w", err)
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("get workflow: %w", err)
	}
	deps := map[string]struct{}{}
	if stepID != "" {
		if _, ok := wfDef.Steps[stepID]; !ok {
			return "", fmt.Errorf("step not found")
		}
		collectDependencies(wfDef, stepID, deps)
	}
	newID := uuid.NewString()
	now := time.Now().UTC()
	newRun := &WorkflowRun{
		ID:          newID,
		WorkflowID:  run.WorkflowID,
		OrgID:       run.OrgID,
		TeamID:      run.TeamID,
		Input:       cloneMap(run.Input),
		Context:     cloneContextForDeps(run.Context, deps),
		Status:      RunStatusPending,
		Steps:       map[string]*StepRun{},
		TriggeredBy: run.TriggeredBy,
		CreatedAt:   now,
		UpdatedAt:   now,
		RerunOf:     run.ID,
		RerunStep:   stepID,
		DryRun:      dryRun,
	}
	newRun.Labels = cloneStringMap(run.Labels)
	newRun.Metadata = cloneStringMap(run.Metadata)
	if newRun.Metadata == nil {
		newRun.Metadata = map[string]string{}
	}
	newRun.Metadata["rerun_of"] = run.ID
	if stepID != "" {
		newRun.Metadata["rerun_step"] = stepID
	}
	if dryRun {
		newRun.Metadata["dry_run"] = "true"
		if newRun.Labels == nil {
			newRun.Labels = map[string]string{}
		}
		newRun.Labels["dry_run"] = "true"
	}
	for depID := range deps {
		prev := run.Steps[depID]
		if prev == nil || prev.Status != StepStatusSucceeded {
			return "", fmt.Errorf("dependency %s not succeeded", depID)
		}
		newRun.Steps[depID] = cloneStepRun(prev)
	}
	if err := e.store.CreateRun(ctx, newRun); err != nil {
		return "", err
	}
	return newID, nil
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

	prevStatus := run.Status
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
			if err := e.validateStepOutput(stepDef, res.ResultPtr); err != nil {
				child.Status = StepStatusFailed
				child.CompletedAt = &now
				child.Error = map[string]any{"message": err.Error()}
				e.appendTimeline(ctx, run, "step_output_invalid", stepID, res.JobId, string(child.Status), res.ResultPtr, err.Error(), nil)
			}
		}
		if !retry && (child.Status == StepStatusSucceeded || child.Status == StepStatusFailed || child.Status == StepStatusCancelled || child.Status == StepStatusTimedOut) {
			e.appendTimeline(ctx, run, "step_completed", stepID, res.JobId, string(child.Status), res.ResultPtr, res.ErrorMessage, nil)
		}
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
			if err := e.validateStepOutput(stepDef, res.ResultPtr); err != nil {
				stepRun.Status = StepStatusFailed
				stepRun.CompletedAt = &now
				stepRun.Error = map[string]any{"message": err.Error()}
				e.appendTimeline(ctx, run, "step_output_invalid", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, err.Error(), nil)
			}
		}
		if !retry && (stepRun.Status == StepStatusSucceeded || stepRun.Status == StepStatusFailed || stepRun.Status == StepStatusCancelled || stepRun.Status == StepStatusTimedOut) {
			e.appendTimeline(ctx, run, "step_completed", stepID, res.JobId, string(stepRun.Status), res.ResultPtr, res.ErrorMessage, nil)
		}
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
	if prevStatus != run.Status {
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
	}

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
	prevStatus := run.Status
	if approved {
		sr.Status = StepStatusSucceeded
	} else {
		sr.Status = StepStatusFailed
	}
	sr.CompletedAt = &now
	run.Steps[stepID] = sr
	updateRunStatus(run, wfDef, now)
	if approved {
		e.appendTimeline(ctx, run, "step_approved", stepID, "", string(sr.Status), "", "", nil)
	} else {
		e.appendTimeline(ctx, run, "step_rejected", stepID, "", string(sr.Status), "", "", nil)
	}
	if prevStatus != run.Status {
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
	}
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
	e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "run cancelled", nil)
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
	cancelReq := &pb.JobCancel{
		JobId:       jobID,
		Reason:      reason,
		RequestedBy: "workflow-engine",
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        "workflow-engine",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobCancel{JobCancel: cancelReq},
	}
	_ = e.bus.Publish(capsdk.SubjectCancel, packet)
}

func (e *Engine) scheduleReady(ctx context.Context, wfDef *Workflow, run *WorkflowRun) error {
	if wfDef == nil || run == nil {
		return fmt.Errorf("workflow/run required")
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed || run.Status == RunStatusSucceeded || run.Status == RunStatusTimedOut {
		return nil
	}
	now := time.Now().UTC()
	prevStatus := run.Status
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
			// For-each steps may remain RUNNING while new children need dispatching as capacity frees up.
			if step.ForEach == "" {
				if step.Type != StepTypeDelay {
					continue
				}
			} else if parentSR.Status != StepStatusRunning {
				continue
			}
		}
		if !depsSatisfied(step, run) {
			continue
		}
		// condition gate (non-condition steps only)
		if step.Condition != "" && step.Type != StepTypeCondition {
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

		// Condition steps evaluate an expression and store the boolean result.
		if step.Type == StepTypeCondition {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				parentSR.StartedAt = &now
				condExpr := strings.TrimSpace(step.Condition)
				if condExpr == "" {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": "condition expression required"}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", "condition expression required", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				ok, err := evalCondition(condExpr, buildEvalScope(run, nil))
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if err := e.validateInlineOutput(step, ok); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.Output = ok
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, ok)
				e.appendTimeline(ctx, run, "step_condition_evaluated", stepID, "", string(parentSR.Status), "", "", map[string]any{"value": ok})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Approval steps pause until explicitly approved/denied.
		if step.Type == StepTypeApproval {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusWaiting
				parentSR.StartedAt = &now
				run.Status = RunStatusWaiting
				e.appendTimeline(ctx, run, "step_waiting", stepID, "", string(parentSR.Status), "", "approval requested", nil)
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Delay steps wait for a timer before succeeding.
		if step.Type == StepTypeDelay {
			delay, err := delayForStep(step, now)
			if err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.Error = map[string]any{"message": err.Error()}
				parentSR.CompletedAt = &now
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_delay_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				continue
			}
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				parentSR.StartedAt = &now
				if delay <= 0 {
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.NextAttemptAt = nil
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_delay_completed", stepID, "", string(parentSR.Status), "", "delay completed", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				next := now.Add(delay)
				parentSR.NextAttemptAt = &next
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_delay_started", stepID, "", string(parentSR.Status), "", "delay started", map[string]any{"delay_ms": delay.Milliseconds()})
				e.scheduleAfter(delay, run.WorkflowID, run.ID)
				continue
			}
			if parentSR.Status == StepStatusRunning && parentSR.NextAttemptAt != nil && !parentSR.NextAttemptAt.After(now) {
				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.NextAttemptAt = nil
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_delay_completed", stepID, "", string(parentSR.Status), "", "delay completed", nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Emit event steps publish a system alert to the bus.
		if step.Type == StepTypeNotify {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				payload, err := evalTemplates(step.Input, buildEvalScope(run, nil))
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					continue
				}
				if e.bus == nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": "bus unavailable"}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", "bus unavailable", nil)
					continue
				}
				alert := buildEventAlert(step, payload)
				packet := &pb.BusPacket{
					TraceId:         run.ID,
					SenderId:        "workflow-engine",
					CreatedAt:       timestamppb.Now(),
					ProtocolVersion: capsdk.DefaultProtocolVersion,
					Payload:         &pb.BusPacket_Alert{Alert: alert},
				}
				if err := e.bus.Publish(capsdk.SubjectWorkflowEvent, packet); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_event_failed", stepID, "", string(parentSR.Status), "", err.Error(), map[string]any{"payload": payload})
					continue
				}
				parentSR.Status = StepStatusSucceeded
				parentSR.StartedAt = &now
				parentSR.CompletedAt = &now
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_event_emitted", stepID, "", string(parentSR.Status), "", alert.Message, map[string]any{"payload": payload})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
			}
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
			// Pre-create children so the parent doesn't incorrectly aggregate to SUCCEEDED
			// before all items have been processed (e.g. when max_parallel throttles dispatch).
			for idx := range items {
				childID := fmt.Sprintf("%s[%d]", stepID, idx)
				if parentSR.Children[childID] != nil {
					continue
				}
				child := &StepRun{StepID: childID, Status: StepStatusPending}
				parentSR.Children[childID] = child
				run.Steps[childID] = child
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
					child = &StepRun{StepID: childID, Status: StepStatusPending}
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
				if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
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
					data := map[string]any{"foreach_index": idx}
					if req.ContextPtr != "" {
						data["context_ptr"] = req.ContextPtr
					}
					e.appendTimeline(ctx, run, "step_dispatched", childID, jobID, string(child.Status), "", "", data)
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
		if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
			logging.Error("workflow-engine", "publish step", "run_id", run.ID, "step_id", stepID, "error", err)
			parentSR.Status = StepStatusFailed
			parentSR.Error = map[string]any{"message": err.Error()}
		} else {
			parentSR.Status = StepStatusRunning
			parentSR.StartedAt = &now
			parentSR.Attempts++
			parentSR.JobID = jobID
			parentSR.Input = payload
			var data map[string]any
			if req.ContextPtr != "" {
				data = map[string]any{"context_ptr": req.ContextPtr}
			}
			e.appendTimeline(ctx, run, "step_dispatched", stepID, jobID, string(parentSR.Status), "", "", data)
			if e.OnStepDispatched != nil {
				e.OnStepDispatched(run.ID, stepID, jobID)
			}
		}
		run.Steps[stepID] = parentSR
	}

	updateRunStatus(run, wfDef, now)
	if prevStatus != run.Status {
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
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

func fetchResultPayload(ctx context.Context, mem memory.Store, resultPtr string) (any, bool) {
	if mem == nil || resultPtr == "" {
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := memory.KeyFromPointer(resultPtr)
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

func collectDependencies(wfDef *Workflow, stepID string, deps map[string]struct{}) {
	if wfDef == nil || stepID == "" {
		return
	}
	step := wfDef.Steps[stepID]
	if step == nil {
		return
	}
	for _, dep := range step.DependsOn {
		if dep == "" {
			continue
		}
		if _, ok := deps[dep]; ok {
			continue
		}
		deps[dep] = struct{}{}
		collectDependencies(wfDef, dep, deps)
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func cloneContextForDeps(ctx map[string]any, deps map[string]struct{}) map[string]any {
	if ctx == nil {
		return nil
	}
	out := cloneMap(ctx)
	stepsRaw, ok := out["steps"]
	if !ok {
		return out
	}
	steps, ok := stepsRaw.(map[string]any)
	if !ok {
		return out
	}
	filtered := map[string]any{}
	for dep := range deps {
		if val, ok := steps[dep]; ok {
			filtered[dep] = val
		}
	}
	out["steps"] = filtered
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func cloneStepRun(sr *StepRun) *StepRun {
	if sr == nil {
		return nil
	}
	data, err := json.Marshal(sr)
	if err != nil {
		return &StepRun{StepID: sr.StepID, Status: sr.Status}
	}
	var out StepRun
	if err := json.Unmarshal(data, &out); err != nil {
		return &StepRun{StepID: sr.StepID, Status: sr.Status}
	}
	return &out
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
		ProtocolVersion: capsdk.DefaultProtocolVersion,
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
	if err := e.validateStepInput(step, out); err != nil {
		return nil, err
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
	if meta := buildStepMetadata(run, step); meta != nil {
		req.Meta = meta
		if req.PrincipalId == "" && meta.GetActorId() != "" {
			req.PrincipalId = meta.GetActorId()
		}
	}
	if run != nil {
		if run.DryRun || run.Metadata["dry_run"] == "true" {
			req.Env["dry_run"] = "true"
			if req.Labels == nil {
				req.Labels = map[string]string{}
			}
			req.Labels["dry_run"] = "true"
		}
	}
	if e.config != nil {
		if cfg, err := e.config.Effective(ctx, run.OrgID, run.TeamID, wfDef.ID, stepID); err == nil && cfg != nil {
			if data, err := json.Marshal(cfg); err == nil {
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["CORDUM_EFFECTIVE_CONFIG"] = string(data)
			}
		}
	}
	return req
}

func buildStepMetadata(run *WorkflowRun, step *Step) *pb.JobMetadata {
	if run == nil && (step == nil || step.Meta == nil) {
		return nil
	}
	meta := &pb.JobMetadata{}
	if run != nil {
		if tenant := strings.TrimSpace(run.OrgID); tenant != "" {
			meta.TenantId = tenant
		}
	}
	if step == nil || step.Meta == nil {
		if meta.TenantId == "" {
			return nil
		}
		return meta
	}
	sm := step.Meta
	meta.ActorId = strings.TrimSpace(sm.ActorId)
	meta.ActorType = actorTypeFromString(sm.ActorType)
	meta.IdempotencyKey = strings.TrimSpace(sm.IdempotencyKey)
	meta.PackId = strings.TrimSpace(sm.PackId)
	meta.Capability = strings.TrimSpace(sm.Capability)
	meta.RiskTags = cleanStrings(sm.RiskTags)
	meta.Requires = cleanStrings(sm.Requires)
	if len(sm.Labels) > 0 {
		meta.Labels = cloneStringMap(sm.Labels)
	}
	if metaEmpty(meta) {
		return nil
	}
	return meta
}

func actorTypeFromString(raw string) pb.ActorType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "human":
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case "service":
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func cleanStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func metaEmpty(meta *pb.JobMetadata) bool {
	if meta == nil {
		return true
	}
	return strings.TrimSpace(meta.TenantId) == "" &&
		strings.TrimSpace(meta.ActorId) == "" &&
		meta.ActorType == pb.ActorType_ACTOR_TYPE_UNSPECIFIED &&
		strings.TrimSpace(meta.IdempotencyKey) == "" &&
		strings.TrimSpace(meta.Capability) == "" &&
		len(meta.RiskTags) == 0 &&
		len(meta.Requires) == 0 &&
		strings.TrimSpace(meta.PackId) == "" &&
		len(meta.Labels) == 0
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

func delayForStep(step *Step, now time.Time) (time.Duration, error) {
	if step == nil {
		return 0, nil
	}
	if step.DelaySec < 0 {
		return 0, fmt.Errorf("delay_sec must be non-negative")
	}
	if step.DelaySec > 0 {
		return time.Duration(step.DelaySec) * time.Second, nil
	}
	if strings.TrimSpace(step.DelayUntil) != "" {
		ts := strings.TrimSpace(step.DelayUntil)
		target, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0, fmt.Errorf("invalid delay_until: %w", err)
		}
		if target.After(now) {
			return target.Sub(now), nil
		}
		return 0, nil
	}
	if step.TimeoutSec > 0 {
		return time.Duration(step.TimeoutSec) * time.Second, nil
	}
	return 0, nil
}

func buildEventAlert(step *Step, payload any) *pb.SystemAlert {
	level := "INFO"
	message := ""
	code := ""
	component := "workflow-engine"

	switch v := payload.(type) {
	case map[string]any:
		if val, ok := v["level"].(string); ok && strings.TrimSpace(val) != "" {
			level = strings.ToUpper(strings.TrimSpace(val))
		}
		if val, ok := v["message"].(string); ok && strings.TrimSpace(val) != "" {
			message = strings.TrimSpace(val)
		}
		if val, ok := v["code"].(string); ok && strings.TrimSpace(val) != "" {
			code = strings.TrimSpace(val)
		}
		if val, ok := v["component"].(string); ok && strings.TrimSpace(val) != "" {
			component = strings.TrimSpace(val)
		}
	case map[string]string:
		if val := strings.TrimSpace(v["level"]); val != "" {
			level = strings.ToUpper(val)
		}
		if val := strings.TrimSpace(v["message"]); val != "" {
			message = val
		}
		if val := strings.TrimSpace(v["code"]); val != "" {
			code = val
		}
		if val := strings.TrimSpace(v["component"]); val != "" {
			component = val
		}
	}

	if message == "" && step != nil {
		if step.Name != "" {
			message = step.Name
		} else {
			message = step.ID
		}
	}

	return &pb.SystemAlert{
		Level:     level,
		Message:   message,
		Component: component,
		Code:      code,
	}
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

func (e *Engine) appendTimeline(ctx context.Context, run *WorkflowRun, eventType, stepID, jobID, status, resultPtr, message string, data map[string]any) {
	if e == nil || e.store == nil || run == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	evt := &TimelineEvent{
		Time:       time.Now().UTC(),
		Type:       eventType,
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		StepID:     stepID,
		JobID:      jobID,
		Status:     status,
		ResultPtr:  resultPtr,
		Message:    message,
		Data:       data,
	}
	_ = e.store.AppendTimelineEvent(ctx, run.ID, evt)
}
