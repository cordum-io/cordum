package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/memory"
	schemas "github.com/cordum/cordum/core/infra/schema"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Engine coordinates workflow runs, dispatching steps as jobs and updating run state.
type Engine struct {
	store           *RedisStore
	bus             model.Bus
	mem             memory.Store
	runLocks        sync.Map // per-run locks to avoid global serialization
	maxForEachItems int
	// optional callbacks for observability or hooks
	OnStepDispatched func(runID, stepID, jobID string)
	OnStepFinished   func(runID, stepID string, status StepStatus)
	config           ConfigProvider
	schemaRegistry   *schemas.Registry
	outputSafety     model.OutputSafetyChecker // optional output policy enforcement on step results

	// timerMu guards pendingTimers. pendingTimers tracks cancellable delay
	// timers so they can be stopped on engine shutdown without leaking goroutines.
	timerMu       sync.Mutex
	pendingTimers []*time.Timer
	stopped       chan struct{} // closed by Stop(); nil until first use
}

type runLock struct {
	mu       sync.Mutex
	refs     atomic.Int32
	terminal atomic.Bool
}

const (
	maxInlineResultBytes       = 256 << 10
	defaultMaxForEachItems     = 1000
	defaultLoopMaxIter         = 100
	switchBranchNotTakenReason = "switch_branch_not_taken"
)

// ConfigProvider supplies effective config given identity context.
type ConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// NewEngine creates a workflow engine bound to a Redis workflow store and bus.
func NewEngine(store *RedisStore, bus model.Bus) *Engine {
	return &Engine{store: store, bus: bus, maxForEachItems: defaultMaxForEachItems}
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

// WithOutputSafety sets an optional output safety checker for inter-step policy enforcement.
func (e *Engine) WithOutputSafety(c model.OutputSafetyChecker) *Engine {
	e.outputSafety = c
	return e
}

// WithMaxForEachItems sets the maximum number of items allowed in for_each fan-out.
// Values <= 0 are ignored and the default remains in effect.
func (e *Engine) WithMaxForEachItems(limit int) *Engine {
	if limit > 0 {
		e.maxForEachItems = limit
	}
	return e
}

// lockRun acquires a per-run mutex and returns an unlock function.
// This replaces the global engine mutex so different runs don't block each other.
func (e *Engine) lockRun(runID string) func() {
	val, _ := e.runLocks.LoadOrStore(runID, &runLock{})
	lock := val.(*runLock)
	lock.refs.Add(1)
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		if lock.refs.Add(-1) == 0 && lock.terminal.Load() {
			e.runLocks.Delete(runID)
		}
	}
}

// StartRun loads the workflow/run and dispatches any ready steps.
func (e *Engine) StartRun(ctx context.Context, workflowID, runID string) error {
	defer e.lockRun(runID)()
	wfDef, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if run.Status == RunStatusCancelled || run.Status == RunStatusFailed || run.Status == RunStatusSucceeded || run.Status == RunStatusTimedOut {
		e.markRunTerminal(run.ID)
		return nil
	}
	return e.scheduleReady(ctx, wfDef, run)
}

// RerunFrom creates a new run that reuses inputs and optionally resumes from a step.
func (e *Engine) RerunFrom(ctx context.Context, runID, stepID string, dryRun bool) (string, error) {
	defer e.lockRun(runID)()
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

	defer e.lockRun(runID)()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		logging.Error("workflow-engine", "get run failed", "run_id", runID, "error", err)
		return
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		e.markRunTerminal(run.ID)
		return
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		logging.Error("workflow-engine", "get workflow failed", "workflow_id", run.WorkflowID, "error", err)
		return
	}

	now := time.Now().UTC()
	if timedOut, err := e.enforceWorkflowTimeout(ctx, wfDef, run, now); err != nil {
		logging.Error("workflow-engine", "enforce workflow timeout", "run_id", run.ID, "error", err)
		return
	} else if timedOut {
		return
	}

	prevStatus := run.Status
	baseStepID, childKey := splitForEachStep(stepID)
	stepDef := wfDef.Steps[baseStepID]
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
			if !e.checkStepOutputPolicy(ctx, run, stepID, child, res) {
				recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, false)
			}
		}
		parent.Children[stepID] = child
		run.Steps[stepID] = child
		if stepDef != nil && stepDef.Type == StepTypeLoop {
			// Loop parent status is finalized by the loop scheduler logic, not raw child aggregation.
			parent.Status = StepStatusRunning
			parent.CompletedAt = nil
		} else {
			parent.Status = aggregateChildren(parent)
			if parent.Status == StepStatusSucceeded || parent.Status == StepStatusFailed {
				parent.CompletedAt = &now
			}
		}
		run.Steps[baseStepID] = parent
		// on_error handler: activate error handler step when parent step fails
		if parent.Status == StepStatusFailed && stepDef != nil && stepDef.OnError != "" {
			if _, ok := wfDef.Steps[stepDef.OnError]; ok {
				targetSR := run.Steps[stepDef.OnError]
				if targetSR == nil {
					targetSR = &StepRun{StepID: stepDef.OnError}
				}
				if targetSR.Status == "" || targetSR.Status == StepStatusPending {
					targetSR.Status = StepStatusPending
					if targetSR.Input == nil {
						targetSR.Input = make(map[string]any)
					}
					errCtx := make(map[string]any)
					if parent.Error != nil {
						for k, v := range parent.Error {
							errCtx[k] = v
						}
					}
					errCtx["step_id"] = baseStepID
					targetSR.Input["error"] = errCtx
					run.Steps[stepDef.OnError] = targetSR
					e.appendTimeline(ctx, run, "step_error_redirect", baseStepID, "", string(parent.Status), "", stepDef.OnError, nil)
				}
			}
		}
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
			if !e.checkStepOutputPolicy(ctx, run, stepID, stepRun, res) {
				recordStepOutput(ctx, e.mem, run, stepID, stepDef, res.ResultPtr, true)
			}
		}
		run.Steps[stepID] = stepRun
		// on_error handler: activate error handler step when step fails
		if !retry && stepRun.Status == StepStatusFailed && stepDef != nil && stepDef.OnError != "" {
			if _, ok := wfDef.Steps[stepDef.OnError]; ok {
				targetSR := run.Steps[stepDef.OnError]
				if targetSR == nil {
					targetSR = &StepRun{StepID: stepDef.OnError}
				}
				if targetSR.Status == "" || targetSR.Status == StepStatusPending {
					targetSR.Status = StepStatusPending
					if targetSR.Input == nil {
						targetSR.Input = make(map[string]any)
					}
					errCtx := make(map[string]any)
					if stepRun.Error != nil {
						for k, v := range stepRun.Error {
							errCtx[k] = v
						}
					}
					errCtx["step_id"] = stepID
					targetSR.Input["error"] = errCtx
					run.Steps[stepDef.OnError] = targetSR
					e.appendTimeline(ctx, run, "step_error_redirect", stepID, "", string(stepRun.Status), "", stepDef.OnError, nil)
				}
			}
		}
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
	if isTerminalRunStatus(run.Status) {
		e.markRunTerminal(run.ID)
	}

	if run.Status == RunStatusRunning {
		if err := e.scheduleReady(ctx, wfDef, run); err != nil {
			logging.Error("workflow-engine", "schedule ready", "run_id", run.ID, "error", err)
		}
	}
}

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

// ApproveStep resumes a waiting approval step.
func (e *Engine) ApproveStep(ctx context.Context, runID, stepID string, approved bool) error {
	defer e.lockRun(runID)()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	now := time.Now().UTC()
	if timedOut, err := e.enforceWorkflowTimeout(ctx, wfDef, run, now); err != nil {
		return fmt.Errorf("workflow approve step enforce timeout for run %s: %w", runID, err)
	} else if timedOut {
		return fmt.Errorf("run timed out")
	}
	sr := run.Steps[stepID]
	if sr == nil {
		return fmt.Errorf("step not found")
	}
	if sr.Status != StepStatusWaiting {
		return fmt.Errorf("step not waiting")
	}
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
		return fmt.Errorf("workflow approve step update run %s: %w", runID, err)
	}
	if isTerminalRunStatus(run.Status) {
		e.markRunTerminal(run.ID)
	}
	if approved && run.Status == RunStatusRunning {
		return e.scheduleReady(ctx, wfDef, run)
	}
	return nil
}

// CancelRun marks a run and all non-terminal steps as cancelled to prevent further dispatch.
func (e *Engine) CancelRun(ctx context.Context, runID string) error {
	defer e.lockRun(runID)()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	wfDef, err := e.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return fmt.Errorf("get workflow: %w", err)
	}
	now := time.Now().UTC()
	// Ensure all workflow-defined steps exist in the run map.
	for stepID := range wfDef.Steps {
		if _, exists := run.Steps[stepID]; !exists {
			run.Steps[stepID] = &StepRun{StepID: stepID}
		}
	}
	var cancelJobIDs []string
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
		return fmt.Errorf("workflow cancel run update run %s: %w", runID, err)
	}
	e.markRunTerminal(run.ID)
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

func (e *Engine) enforceWorkflowTimeout(ctx context.Context, wfDef *Workflow, run *WorkflowRun, now time.Time) (bool, error) {
	if wfDef == nil || run == nil || wfDef.TimeoutSec <= 0 {
		return false, nil
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return false, nil
	}
	startedAt := run.StartedAt
	if startedAt == nil {
		if run.Status == RunStatusPending {
			return false, nil
		}
		if !run.CreatedAt.IsZero() {
			startedAt = &run.CreatedAt
		}
	}
	if startedAt == nil {
		return false, nil
	}
	deadline := startedAt.Add(time.Duration(wfDef.TimeoutSec) * time.Second)
	if now.Before(deadline) {
		return false, nil
	}
	if err := e.timeoutRun(ctx, wfDef, run, now); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) timeoutRun(ctx context.Context, wfDef *Workflow, run *WorkflowRun, now time.Time) error {
	if e == nil || run == nil || wfDef == nil {
		return nil
	}
	// Ensure all workflow-defined steps exist in the run map.
	if run.Steps == nil {
		run.Steps = map[string]*StepRun{}
	}
	for stepID := range wfDef.Steps {
		if _, exists := run.Steps[stepID]; !exists {
			run.Steps[stepID] = &StepRun{StepID: stepID}
		}
	}
	var cancelJobIDs []string
	for id, sr := range run.Steps {
		if sr == nil {
			continue
		}
		cancelJobIDs = append(cancelJobIDs, collectCancelableJobs(sr)...)
		timeoutStepRun(sr, now)
		run.Steps[id] = sr
	}
	run.Status = RunStatusTimedOut
	run.CompletedAt = &now
	run.UpdatedAt = now
	if run.Error == nil {
		run.Error = map[string]any{}
	}
	run.Error["message"] = "workflow run timed out"
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("workflow enforce timeout update run %s: %w", run.ID, err)
	}
	e.markRunTerminal(run.ID)
	e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "run timed out", map[string]any{"timeout_sec": wfDef.TimeoutSec})
	seen := make(map[string]struct{}, len(cancelJobIDs))
	for _, jobID := range cancelJobIDs {
		if jobID == "" {
			continue
		}
		if _, ok := seen[jobID]; ok {
			continue
		}
		seen[jobID] = struct{}{}
		e.publishJobCancel(jobID, "workflow run timed out")
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

func timeoutStepRun(sr *StepRun, now time.Time) {
	if sr == nil {
		return
	}
	switch sr.Status {
	case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
		// leave terminal states
	default:
		sr.Status = StepStatusTimedOut
		sr.CompletedAt = &now
		if sr.Error == nil {
			sr.Error = map[string]any{"message": "workflow run timed out"}
		}
	}
	for _, child := range sr.Children {
		if child == nil {
			continue
		}
		timeoutStepRun(child, now)
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
		e.markRunTerminal(run.ID)
		return nil
	}
	now := time.Now().UTC()
	prevStatus := run.Status
	if run.Status == RunStatusPending {
		run.Status = RunStatusRunning
		run.StartedAt = &now
	}
	if timedOut, err := e.enforceWorkflowTimeout(ctx, wfDef, run, now); err != nil {
		return fmt.Errorf("workflow schedule ready enforce timeout for run %s: %w", run.ID, err)
	} else if timedOut {
		return nil
	}
	parallelChildOwners := collectParallelChildOwners(wfDef)
	loopBodyOwners := collectLoopBodyOwners(wfDef)

	for stepID, step := range wfDef.Steps {
		if ownerID, managed := parallelChildOwners[stepID]; managed && ownerID != stepID {
			// Child definitions listed by a parallel step are orchestrated by the parent handler.
			continue
		}
		if ownerID, managed := loopBodyOwners[stepID]; managed && ownerID != stepID {
			// Body definitions listed by a loop step are orchestrated by the parent loop handler.
			continue
		}
		parentSR := run.Steps[stepID]
		if parentSR == nil {
			parentSR = &StepRun{StepID: stepID}
		}
		if parentSR.Status != "" && parentSR.Status != StepStatusPending && parentSR.Status != StepStatusWaiting {
			// For-each steps may remain RUNNING while new children need dispatching as capacity frees up.
			if step.ForEach == "" && step.Type != StepTypeParallel && step.Type != StepTypeLoop && step.Type != StepTypeSubWorkflow {
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
		// on_error target steps are only dispatched when explicitly activated.
		if isOnErrorTarget(wfDef, stepID) {
			if parentSR.Status == "" || (parentSR.Input == nil || parentSR.Input["error"] == nil) {
				continue
			}
		}
		// condition gate (non-condition/non-switch steps only)
		if step.Condition != "" && step.Type != StepTypeCondition && step.Type != StepTypeSwitch {
			ok, err := evalCondition(step.Condition, buildEvalScope(run, nil))
			if err != nil {
				logging.Error("workflow-engine", "condition eval failed", "step_id", stepID, "error", err)
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_condition_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
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

		// Transform steps evaluate input expressions inline and store the result.
		if step.Type == StepTypeTransform {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				parentSR.StartedAt = &now
				if step.Input == nil {
					// No input → succeed with empty output.
					result := map[string]any{}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Output = result
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, result)
					e.appendTimeline(ctx, run, "step_transform_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"keys": 0})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				result, err := evalTemplates(step.Input, buildEvalScope(run, nil))
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": fmt.Sprintf("transform expression error: %s", err.Error())}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_transform_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if err := e.validateInlineOutput(step, result); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_transform_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.Output = result
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, result)
				e.appendTimeline(ctx, run, "step_transform_completed", stepID, "", string(parentSR.Status), "", "", nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Switch steps evaluate expression and choose a single branch (inline, no dispatch).
		if step.Type == StepTypeSwitch {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				parentSR.StartedAt = &now
			}
			condExpr := strings.TrimSpace(step.Condition)
			if condExpr == "" {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": "switch expression required"}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "switch expression required", nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			scope := buildEvalScope(run, nil)
			switchValue, err := Eval(condExpr, scope)
			if err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			cases, err := parseSwitchCases(step, scope)
			if err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			defaultStepID, err := parseSwitchDefault(step, scope)
			if err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			invalidTarget := ""
			for _, candidate := range cases {
				if wfDef.Steps[candidate.StepID] == nil {
					invalidTarget = fmt.Sprintf("switch case target step %q not found", candidate.StepID)
					break
				}
			}
			if invalidTarget == "" && defaultStepID != "" && wfDef.Steps[defaultStepID] == nil {
				invalidTarget = fmt.Sprintf("switch default target step %q not found", defaultStepID)
			}
			if invalidTarget != "" {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": invalidTarget}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", invalidTarget, nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			targetStepID := ""
			matchedCase := "default"
			for _, candidate := range cases {
				if switchValueEquals(switchValue, candidate.MatchValue) {
					targetStepID = candidate.StepID
					matchedCase = fmt.Sprint(candidate.MatchValue)
					break
				}
			}
			if targetStepID == "" {
				targetStepID = defaultStepID
			}
			if strings.TrimSpace(targetStepID) == "" {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": "no matching case"}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "no matching case", map[string]any{"value": switchValue})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			unmatchedTargets := map[string]struct{}{}
			for _, candidate := range cases {
				if candidate.StepID == "" || candidate.StepID == targetStepID {
					continue
				}
				unmatchedTargets[candidate.StepID] = struct{}{}
			}
			if defaultStepID != "" && defaultStepID != targetStepID {
				unmatchedTargets[defaultStepID] = struct{}{}
			}
			for branchStepID := range unmatchedTargets {
				branchRun := run.Steps[branchStepID]
				if branchRun == nil {
					branchRun = &StepRun{StepID: branchStepID}
				}
				if isTerminalStepStatus(branchRun.Status) {
					continue
				}
				branchRun.Status = StepStatusCancelled
				if branchRun.StartedAt == nil {
					branchRun.StartedAt = &now
				}
				branchRun.CompletedAt = &now
				branchRun.Error = map[string]any{
					"message": "branch not taken",
					"reason":  switchBranchNotTakenReason,
				}
				run.Steps[branchStepID] = branchRun
			}

			output := map[string]any{
				"matched_case":  matchedCase,
				"matched_value": switchValue,
				"target_step":   targetStepID,
			}
			if err := e.validateInlineOutput(step, output); err != nil {
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			parentSR.Status = StepStatusSucceeded
			parentSR.CompletedAt = &now
			parentSR.Error = nil
			parentSR.Output = output
			run.Steps[stepID] = parentSR
			recordStepInlineOutput(run, stepID, step, output)

			// Mark selected branch step as pending so scheduleReady handles it on
			// the next iteration, respecting the target step's type and deps.
			targetSR := run.Steps[targetStepID]
			if targetSR == nil {
				targetSR = &StepRun{StepID: targetStepID, Status: StepStatusPending}
			}
			if targetSR.Status == "" {
				targetSR.Status = StepStatusPending
			}
			run.Steps[targetStepID] = targetSR

			e.appendTimeline(ctx, run, "step_switch_evaluated", stepID, "", string(parentSR.Status), "", "", output)
			if e.OnStepFinished != nil {
				e.OnStepFinished(run.ID, stepID, parentSR.Status)
			}
			continue
		}

		// Sub-workflow steps orchestrate a child workflow run and wait for completion.
		if step.Type == StepTypeSubWorkflow {
			scope := buildEvalScope(run, nil)
			childWorkflowID, inputMapping, outputMapping, err := resolveSubWorkflowConfig(step, scope)
			if err != nil {
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			childRunID := strings.TrimSpace(parentSR.JobID)
			if childRunID == "" {
				callStack := normalizeCallStackForRun(run)
				if containsString(callStack, childWorkflowID) {
					msg := fmt.Sprintf("circular workflow reference detected: %s", childWorkflowID)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				childInput, err := normalizeSubWorkflowInput(run, inputMapping)
				if err != nil {
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				if _, err := e.store.GetWorkflow(ctx, childWorkflowID); err != nil {
					msg := fmt.Sprintf("get child workflow: %v", err)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				childRunID = uuid.NewString()
				childLabels := cloneStringMap(run.Labels)
				childMetadata := cloneStringMap(run.Metadata)
				if childMetadata == nil {
					childMetadata = map[string]string{}
				}
				childStack := append(append([]string{}, callStack...), childWorkflowID)
				childMetadata["parent_run_id"] = run.ID
				childMetadata["parent_step_id"] = stepID
				childMetadata["call_stack"] = encodeCallStack(childStack)
				if run.DryRun || run.Metadata["dry_run"] == "true" {
					childMetadata["dry_run"] = "true"
					if childLabels == nil {
						childLabels = map[string]string{}
					}
					childLabels["dry_run"] = "true"
				}

				childRun := &WorkflowRun{
					ID:          childRunID,
					WorkflowID:  childWorkflowID,
					OrgID:       run.OrgID,
					TeamID:      run.TeamID,
					Input:       childInput,
					Context:     map[string]any{},
					Status:      RunStatusPending,
					Steps:       map[string]*StepRun{},
					TriggeredBy: run.TriggeredBy,
					CreatedAt:   now,
					UpdatedAt:   now,
					Labels:      childLabels,
					Metadata:    childMetadata,
					DryRun:      run.DryRun || run.Metadata["dry_run"] == "true",
				}
				if err := e.store.CreateRun(ctx, childRun); err != nil {
					msg := fmt.Sprintf("create child run: %v", err)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, "", string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if err := e.StartRun(ctx, childWorkflowID, childRunID); err != nil {
					msg := fmt.Sprintf("start child run: %v", err)
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": msg}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				parentSR.Status = StepStatusRunning
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = nil
				parentSR.JobID = childRunID
				parentSR.Error = nil
				parentSR.Output = map[string]any{
					"workflow_id": childWorkflowID,
					"run_id":      childRunID,
					"status":      string(RunStatusRunning),
				}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_subworkflow_started", stepID, childRunID, string(parentSR.Status), "", "", map[string]any{
					"workflow_id": childWorkflowID,
					"run_id":      childRunID,
				})
				continue
			}

			childRun, err := e.store.GetRun(ctx, childRunID)
			if err != nil {
				msg := fmt.Sprintf("get child run: %v", err)
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": msg}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			parentSR.Output = map[string]any{
				"workflow_id": childWorkflowID,
				"run_id":      childRunID,
				"status":      string(childRun.Status),
			}
			switch childRun.Status {
			case RunStatusPending, RunStatusRunning, RunStatusWaiting:
				parentSR.Status = StepStatusRunning
				run.Steps[stepID] = parentSR
				continue
			case RunStatusSucceeded:
				mappedOutput, err := buildSubWorkflowOutput(run, childRun, outputMapping)
				if err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if err := e.validateInlineOutput(step, mappedOutput); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.Error = nil
				parentSR.Output = mappedOutput
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, mappedOutput)
				e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", "", map[string]any{
					"workflow_id":  childWorkflowID,
					"run_id":       childRunID,
					"child_status": string(childRun.Status),
				})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			case RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
				msg := fmt.Sprintf("child run %s ended with status %s", childRunID, childRun.Status)
				if childRun.Error != nil {
					if childMsg, ok := childRun.Error["message"].(string); ok && strings.TrimSpace(childMsg) != "" {
						msg = childMsg
					}
				}
				if childStepMsg := subWorkflowChildErrorMessage(childRun); childStepMsg != "" {
					msg = childStepMsg
				}
				parentSR.Status = subWorkflowTerminalStatus(childRun.Status)
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{
					"message":      msg,
					"child_run_id": childRunID,
					"child_status": string(childRun.Status),
				}
				parentSR.Output = map[string]any{
					"workflow_id": childWorkflowID,
					"run_id":      childRunID,
					"status":      string(childRun.Status),
				}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_subworkflow_completed", stepID, childRunID, string(parentSR.Status), "", msg, map[string]any{
					"workflow_id":  childWorkflowID,
					"run_id":       childRunID,
					"child_status": string(childRun.Status),
				})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			default:
				parentSR.Status = StepStatusRunning
				run.Steps[stepID] = parentSR
				continue
			}
		}

		if step.Type == StepTypeParallel || step.Type == StepTypeLoop {
			var (
				childStepIDs []string
				strategy     string
				required     int
				err          error
			)
			if step.Type == StepTypeParallel {
				childStepIDs, strategy, required, err = resolveParallelConfig(step, buildEvalScope(run, nil))
				if err != nil {
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
			}

			// Loop repeats a body step until condition/until stops or cap is reached.
			if step.Type == StepTypeLoop {
				maxIterations, conditionExpr, untilExpr, bodyStepID, err := resolveLoopConfig(step)
				if err != nil {
					parentSR.Status = StepStatusFailed
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
					parentSR.CompletedAt = &now
					parentSR.Error = map[string]any{"message": err.Error()}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				bodyStep := step
				if bodyStepID != "" {
					bodyStep = wfDef.Steps[bodyStepID]
					if bodyStep == nil {
						msg := fmt.Sprintf("loop body_step %q not found", bodyStepID)
						parentSR.Status = StepStatusFailed
						if parentSR.StartedAt == nil {
							parentSR.StartedAt = &now
						}
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
				}
				if parentSR.Children == nil {
					parentSR.Children = make(map[string]*StepRun)
				}
				if parentSR.Status == "" || parentSR.Status == StepStatusPending {
					parentSR.Status = StepStatusRunning
					if parentSR.StartedAt == nil {
						parentSR.StartedAt = &now
					}
				}

				hasActive := false
				hasFailed := false
				for _, child := range parentSR.Children {
					if child == nil {
						continue
					}
					switch child.Status {
					case StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
						hasFailed = true
					case StepStatusRunning, StepStatusWaiting, StepStatusPending:
						hasActive = true
					}
				}
				if hasFailed {
					parentSR.Status = StepStatusFailed
					parentSR.CompletedAt = &now
					if parentSR.Error == nil {
						parentSR.Error = map[string]any{"message": "loop child iteration failed"}
					}
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "loop child iteration failed", nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				if hasActive {
					run.Steps[stepID] = parentSR
					continue
				}

				nextIndex := len(parentSR.Children)
				var previousOutput any
				if nextIndex > 0 {
					previousOutput = loopPreviousOutput(run, fmt.Sprintf("%s[%d]", stepID, nextIndex-1))
				}
				scope := buildLoopEvalScope(run, nextIndex, previousOutput)

				if nextIndex >= maxIterations {
					if conditionExpr != "" || untilExpr != "" {
						msg := fmt.Sprintf("loop max_iterations exceeded (%d)", maxIterations)
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": msg}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", msg, map[string]any{"iterations": nextIndex, "max_iterations": maxIterations})
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Error = nil
					parentSR.Output = map[string]any{
						"iterations":  nextIndex,
						"last_output": previousOutput,
					}
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, parentSR.Output)
					e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"iterations": nextIndex, "max_iterations": maxIterations, "reason": "max_iterations_reached"})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				shouldContinue := true
				if conditionExpr != "" {
					ok, err := evalCondition(conditionExpr, scope)
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					shouldContinue = shouldContinue && ok
				}
				if untilExpr != "" {
					ok, err := evalCondition(untilExpr, scope)
					if err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					if ok {
						shouldContinue = false
					}
				}
				if !shouldContinue {
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Error = nil
					parentSR.Output = map[string]any{
						"iterations":  nextIndex,
						"last_output": previousOutput,
					}
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, parentSR.Output)
					e.appendTimeline(ctx, run, "step_loop_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{"iterations": nextIndex, "max_iterations": maxIterations, "reason": "condition_stopped"})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				childID := fmt.Sprintf("%s[%d]", stepID, nextIndex)
				child := parentSR.Children[childID]
				if child == nil {
					child = &StepRun{StepID: childID, Status: StepStatusPending}
				}
				if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					run.Steps[stepID] = parentSR
					continue
				}
				jobID := fmt.Sprintf("%s:%s@%d", run.ID, childID, child.Attempts+1)
				req := e.buildJobRequest(ctx, wfDef, run, bodyStep, childID, jobID)
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env["loop_index"] = fmt.Sprintf("%d", nextIndex)
				req.Env["loop_iteration"] = fmt.Sprintf("%d", nextIndex+1)
				if encoded, err := json.Marshal(previousOutput); err == nil {
					req.Env["loop_previous_output"] = string(encoded)
				}
				payload, err := e.buildLoopPayload(run, bodyStep, scope)
				if err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					run.Steps[stepID] = parentSR
					continue
				}
				if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					child.Input = payload
					parentSR.Children[childID] = child
					run.Steps[childID] = child
					run.Steps[stepID] = parentSR
					continue
				} else if ptr != "" {
					req.ContextPtr = ptr
				}
				packet := makeJobPacket(run.ID, req)
				if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
					logging.Error("workflow-engine", "publish loop step", "run_id", run.ID, "step_id", childID, "error", err)
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
				} else {
					child.Status = StepStatusRunning
					child.StartedAt = &now
					child.Attempts++
					child.JobID = jobID
					child.Input = payload
					e.appendTimeline(ctx, run, "step_loop_iteration", stepID, child.JobID, string(child.Status), "", "", map[string]any{
						"index":       nextIndex,
						"iteration":   nextIndex + 1,
						"body_step":   bodyStep.ID,
						"context_ptr": req.ContextPtr,
					})
					e.appendTimeline(ctx, run, "step_dispatched", childID, jobID, string(child.Status), "", "", map[string]any{
						"loop_index": nextIndex,
					})
					if e.OnStepDispatched != nil {
						e.OnStepDispatched(run.ID, childID, jobID)
					}
				}
				parentSR.Children[childID] = child
				run.Steps[childID] = child
				run.Steps[stepID] = parentSR
				continue
			}
			if ownerID, exists := parallelChildOwners[stepID]; exists && ownerID != stepID {
				msg := fmt.Sprintf("parallel step id %q is reserved as child of %q", stepID, ownerID)
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": msg}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", msg, nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			if parentSR.Children == nil {
				parentSR.Children = make(map[string]*StepRun)
			}
			if len(childStepIDs) == 0 {
				parentSR.Status = StepStatusSucceeded
				parentSR.StartedAt = &now
				parentSR.CompletedAt = &now
				parentSR.Output = map[string]any{}
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, map[string]any{})
				e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{
					"strategy": strategy,
					"required": required,
					"total":    0,
				})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				e.appendTimeline(ctx, run, "step_parallel_started", stepID, "", string(parentSR.Status), "", "", map[string]any{
					"strategy": strategy,
					"required": required,
					"total":    len(childStepIDs),
				})
			}

			activeChildren := make(map[string]struct{}, len(childStepIDs))
			initErr := ""
			for _, childStepID := range childStepIDs {
				activeChildren[childStepID] = struct{}{}
				childDef := wfDef.Steps[childStepID]
				if childDef == nil {
					initErr = fmt.Sprintf("parallel child step %q not found", childStepID)
					break
				}
				if ownerID, managed := parallelChildOwners[childStepID]; managed && ownerID != stepID {
					initErr = fmt.Sprintf("parallel child step %q is reserved by %q", childStepID, ownerID)
					break
				}
				child := run.Steps[childStepID]
				if child == nil {
					child = &StepRun{StepID: childStepID, Status: StepStatusPending}
				} else if child.Status == "" {
					child.Status = StepStatusPending
				}
				parentSR.Children[childStepID] = child
				run.Steps[childStepID] = child
			}
			for childID := range parentSR.Children {
				if _, ok := activeChildren[childID]; !ok {
					delete(parentSR.Children, childID)
				}
			}
			if initErr != "" {
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": initErr}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", initErr, nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			// Terminal evaluation before dispatching new work.
			succeededCount, failedCount, runningCount := summarizeParallelChildren(parentSR, childStepIDs)
			if done, success, message := evaluateParallelOutcome(strategy, required, len(childStepIDs), succeededCount, failedCount); done {
				if success {
					cancelled := e.cancelParallelChildren(parentSR, run, childStepIDs, now)
					aggregated := aggregateParallelOutputs(run, childStepIDs)
					if err := e.validateInlineOutput(step, aggregated); err != nil {
						parentSR.Status = StepStatusFailed
						parentSR.CompletedAt = &now
						parentSR.Error = map[string]any{"message": err.Error()}
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					parentSR.Status = StepStatusSucceeded
					parentSR.CompletedAt = &now
					parentSR.Error = nil
					parentSR.Output = aggregated
					run.Steps[stepID] = parentSR
					recordStepInlineOutput(run, stepID, step, aggregated)
					e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", "", map[string]any{
						"strategy":        strategy,
						"required":        required,
						"total":           len(childStepIDs),
						"succeeded_count": succeededCount,
						"failed_count":    failedCount,
						"cancelled_count": cancelled,
					})
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				parentSR.Status = StepStatusFailed
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": message}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_parallel_completed", stepID, "", string(parentSR.Status), "", message, map[string]any{
					"strategy":        strategy,
					"required":        required,
					"total":           len(childStepIDs),
					"succeeded_count": succeededCount,
					"failed_count":    failedCount,
				})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}

			// Dispatch ready children with optional max_parallel throttling.
			for _, childStepID := range childStepIDs {
				if step.MaxParallel > 0 && runningCount >= step.MaxParallel {
					break
				}
				childDef := wfDef.Steps[childStepID]
				if childDef == nil {
					continue
				}
				child := run.Steps[childStepID]
				if child == nil {
					child = &StepRun{StepID: childStepID, Status: StepStatusPending}
				}
				if child.Status != "" && child.Status != StepStatusPending {
					continue
				}
				if child.NextAttemptAt != nil && child.NextAttemptAt.After(now) {
					continue
				}
				jobID := fmt.Sprintf("%s:%s@%d", run.ID, childStepID, child.Attempts+1)
				req := e.buildJobRequest(ctx, wfDef, run, childDef, childStepID, jobID)
				payload, err := e.buildJobPayload(run, childDef, nil)
				if err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					parentSR.Children[childStepID] = child
					run.Steps[childStepID] = child
					continue
				}
				if ptr, err := e.putJobContext(ctx, jobID, payload); err != nil {
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
					child.Input = payload
					parentSR.Children[childStepID] = child
					run.Steps[childStepID] = child
					continue
				} else if ptr != "" {
					req.ContextPtr = ptr
				}
				packet := makeJobPacket(run.ID, req)
				if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
					logging.Error("workflow-engine", "publish parallel child step", "run_id", run.ID, "step_id", childStepID, "error", err)
					child.Status = StepStatusFailed
					child.Error = map[string]any{"message": err.Error()}
				} else {
					child.Status = StepStatusRunning
					child.StartedAt = &now
					child.Attempts++
					child.JobID = jobID
					child.Input = payload
					runningCount++
					var data map[string]any
					if req.ContextPtr != "" {
						data = map[string]any{"context_ptr": req.ContextPtr}
					}
					e.appendTimeline(ctx, run, "step_dispatched", childStepID, jobID, string(child.Status), "", "", data)
					if e.OnStepDispatched != nil {
						e.OnStepDispatched(run.ID, childStepID, jobID)
					}
				}
				parentSR.Children[childStepID] = child
				run.Steps[childStepID] = child
			}
			parentSR.Status = StepStatusRunning
			run.Steps[stepID] = parentSR
			continue
		}

		// For-each fan-out.
		if step.ForEach != "" {
			items, err := evalForEach(step.ForEach, buildEvalScope(run, nil))
			if err != nil {
				logging.Error("workflow-engine", "for_each eval failed", "step_id", stepID, "error", err)
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": err.Error()}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_foreach_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
				continue
			}
			if e.maxForEachItems > 0 && len(items) > e.maxForEachItems {
				msg := fmt.Sprintf("for_each fan-out exceeds limit (%d > %d)", len(items), e.maxForEachItems)
				parentSR.Status = StepStatusFailed
				if parentSR.StartedAt == nil {
					parentSR.StartedAt = &now
				}
				parentSR.CompletedAt = &now
				parentSR.Error = map[string]any{"message": msg}
				run.Steps[stepID] = parentSR
				e.appendTimeline(ctx, run, "step_foreach_failed", stepID, "", string(parentSR.Status), "", msg, map[string]any{
					"count": len(items),
					"limit": e.maxForEachItems,
				})
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
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

		// Storage steps read/write/delete run context or artifact store inline (no job dispatch).
		if step.Type == StepTypeStorage {
			if parentSR.Status == "" || parentSR.Status == StepStatusPending {
				parentSR.Status = StepStatusRunning
				parentSR.StartedAt = &now

				scope := buildEvalScope(run, nil)
				evalInput, evalErr := evalTemplates(step.Input, scope)
				if evalErr != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": fmt.Sprintf("storage input expression error: %s", evalErr.Error())}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", evalErr.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}
				inputMap, _ := evalInput.(map[string]any)
				if inputMap == nil {
					inputMap = map[string]any{}
				}

				operation, _ := inputMap["operation"].(string)
				key, _ := inputMap["key"].(string)

				if run.Context == nil {
					run.Context = map[string]any{}
				}

				var output map[string]any
				switch operation {
				case "read":
					if key == "" {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": "storage read: key is required"}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					val, found := getContextPath(run.Context, key)
					if !found {
						parentSR.Status = StepStatusFailed
						msg := fmt.Sprintf("storage read: key %q not found", key)
						parentSR.Error = map[string]any{"message": msg}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", msg, nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					output = map[string]any{"operation": "read", "key": key, "value": val}

				case "write":
					if key == "" {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": "storage write: key is required"}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					val := inputMap["value"]
					_ = setContextPath(run.Context, key, val)
					output = map[string]any{"operation": "write", "key": key, "value": val}

				case "delete":
					if key == "" {
						parentSR.Status = StepStatusFailed
						parentSR.Error = map[string]any{"message": "storage delete: key is required"}
						parentSR.CompletedAt = &now
						run.Steps[stepID] = parentSR
						e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", "key is required", nil)
						if e.OnStepFinished != nil {
							e.OnStepFinished(run.ID, stepID, parentSR.Status)
						}
						continue
					}
					deleteContextPath(run.Context, key)
					output = map[string]any{"operation": "delete", "key": key}

				default:
					parentSR.Status = StepStatusFailed
					msg := fmt.Sprintf("unknown storage operation: %q", operation)
					parentSR.Error = map[string]any{"message": msg}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", msg, nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				if err := e.validateInlineOutput(step, output); err != nil {
					parentSR.Status = StepStatusFailed
					parentSR.Error = map[string]any{"message": err.Error()}
					parentSR.CompletedAt = &now
					run.Steps[stepID] = parentSR
					e.appendTimeline(ctx, run, "step_storage_failed", stepID, "", string(parentSR.Status), "", err.Error(), nil)
					if e.OnStepFinished != nil {
						e.OnStepFinished(run.ID, stepID, parentSR.Status)
					}
					continue
				}

				parentSR.Status = StepStatusSucceeded
				parentSR.CompletedAt = &now
				parentSR.Output = output
				run.Steps[stepID] = parentSR
				recordStepInlineOutput(run, stepID, step, output)
				e.appendTimeline(ctx, run, "step_storage_completed", stepID, "", string(parentSR.Status), "", "", output)
				if e.OnStepFinished != nil {
					e.OnStepFinished(run.ID, stepID, parentSR.Status)
				}
			}
			run.Steps[stepID] = parentSR
			continue
		}

		// Warn when a step type has no dedicated handler and falls through to generic dispatch.
		switch step.Type {
		case StepTypeWorker, StepTypeCondition, StepTypeApproval, StepTypeDelay, StepTypeNotify, StepTypeTransform, StepTypeStorage, StepTypeSwitch, StepTypeParallel, StepTypeLoop, StepTypeSubWorkflow:
			// Handled above.
		default:
			logging.Warn("workflow-engine", "step has no dedicated handler, dispatching as generic job", "run_id", run.ID, "step_id", stepID, "step_type", string(step.Type))
		}

		// Respect backoff windows for retrying steps.
		if parentSR.NextAttemptAt != nil && parentSR.NextAttemptAt.After(now) {
			run.Steps[stepID] = parentSR
			continue
		}

		jobID := fmt.Sprintf("%s:%s@%d", run.ID, stepID, parentSR.Attempts+1)
		req := e.buildJobRequest(ctx, wfDef, run, step, stepID, jobID)
		// Set idempotency key for crash safety if not already set by workflow author.
		if req.Meta == nil {
			req.Meta = &pb.JobMetadata{}
		}
		if strings.TrimSpace(req.Meta.IdempotencyKey) == "" {
			req.Meta.IdempotencyKey = fmt.Sprintf("wf:%s:%s:%d", run.ID, stepID, parentSR.Attempts+1)
		}
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

		// Persist state BEFORE dispatch (crash safety: if engine crashes after dispatch
		// but before persist, on recovery the step is already RUNNING with a JobID, and
		// the idempotency key prevents the scheduler from processing a duplicate).
		parentSR.Status = StepStatusRunning
		parentSR.StartedAt = &now
		parentSR.Attempts++
		parentSR.JobID = jobID
		parentSR.Input = payload
		run.Steps[stepID] = parentSR
		if err := e.store.UpdateRun(ctx, run); err != nil {
			logging.Error("workflow-engine", "pre-dispatch persist failed", "run_id", run.ID, "step_id", stepID, "error", err)
			parentSR.Status = StepStatusPending
			parentSR.Attempts--
			parentSR.JobID = ""
			parentSR.StartedAt = nil
			run.Steps[stepID] = parentSR
			continue
		}

		// Dispatch to NATS — state is already persisted so a crash here is safe.
		packet := makeJobPacket(run.ID, req)
		if err := e.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
			logging.Error("workflow-engine", "publish step", "run_id", run.ID, "step_id", stepID, "error", err)
			// Revert to pending for retry on next scheduleReady; idempotency key
			// prevents duplicate execution if the message was actually delivered.
			parentSR.Status = StepStatusPending
			parentSR.Attempts--
			parentSR.JobID = ""
			parentSR.StartedAt = nil
			run.Steps[stepID] = parentSR
			_ = e.store.UpdateRun(ctx, run)
		} else {
			var data map[string]any
			if req.ContextPtr != "" {
				data = map[string]any{"context_ptr": req.ContextPtr}
			}
			e.appendTimeline(ctx, run, "step_dispatched", stepID, jobID, string(parentSR.Status), "", "", data)
			if e.OnStepDispatched != nil {
				e.OnStepDispatched(run.ID, stepID, jobID)
			}
		}
	}

	updateRunStatus(run, wfDef, now)
	if prevStatus != run.Status {
		e.appendTimeline(ctx, run, "run_status", "", "", string(run.Status), "", "", nil)
	}
	run.UpdatedAt = now
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return fmt.Errorf("workflow schedule ready update run %s: %w", run.ID, err)
	}
	if isTerminalRunStatus(run.Status) {
		e.markRunTerminal(run.ID)
	}
	return nil
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

func getContextPath(ctx map[string]any, path string) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = ctx
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

func deleteContextPath(ctx map[string]any, path string) {
	if ctx == nil {
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	parts := strings.Split(path, ".")
	cur := ctx
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		if i == len(parts)-1 {
			delete(cur, part)
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
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
	// Steps explicitly activated by on_error (have error context in input)
	// bypass normal dependency checks so the error handler can run immediately.
	if sr := run.Steps[step.ID]; sr != nil && sr.Status == StepStatusPending && sr.Input != nil {
		if _, hasErr := sr.Input["error"]; hasErr {
			return true
		}
	}
	for _, dep := range step.DependsOn {
		sr, ok := run.Steps[dep]
		if !ok || sr.Status != StepStatusSucceeded {
			return false
		}
	}
	return true
}

// isOnErrorTarget returns true if stepID is referenced as an OnError target by any step.
func isOnErrorTarget(wfDef *Workflow, stepID string) bool {
	if wfDef == nil {
		return false
	}
	for _, s := range wfDef.Steps {
		if s != nil && s.OnError == stepID {
			return true
		}
	}
	return false
}

func splitJobID(jobID string) (runID, stepID string) {
	parts := strings.SplitN(jobID, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	runID = parts[0]
	stepID = parts[1]
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
	// Avoid overflow in capacity arithmetic on extreme map sizes.
	capHint := len(base)
	if capHint < int(^uint(0)>>1) {
		capHint++
	}
	out := make(map[string]any, capHint)
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
	if run == nil {
		run = &WorkflowRun{}
	}
	subject := step.Topic
	if subject == "" {
		subject = "job.workflow." + wfDef.ID
	}

	priority := pb.JobPriority_JOB_PRIORITY_BATCH
	if run.Input != nil {
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
	if run.Input != nil {
		if raw, ok := run.Input["memory_id"]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := memory.NormalizeMemoryID(s); trimmed != "" {
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
	if run.DryRun || run.Metadata["dry_run"] == "true" {
		req.Env["dry_run"] = "true"
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		req.Labels["dry_run"] = "true"
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

func collectParallelChildOwners(wfDef *Workflow) map[string]string {
	owners := map[string]string{}
	if wfDef == nil {
		return owners
	}
	for parentID, step := range wfDef.Steps {
		if step == nil || step.Type != StepTypeParallel || step.Input == nil {
			continue
		}
		rawChildren, ok := step.Input["steps"]
		if !ok {
			continue
		}
		childIDs, err := parseParallelStepIDs(rawChildren)
		if err != nil {
			continue
		}
		for _, childID := range childIDs {
			if childID == "" || childID == parentID {
				continue
			}
			if _, exists := owners[childID]; !exists {
				owners[childID] = parentID
			}
		}
	}
	return owners
}

func collectLoopBodyOwners(wfDef *Workflow) map[string]string {
	owners := map[string]string{}
	if wfDef == nil {
		return owners
	}
	for parentID, step := range wfDef.Steps {
		if step == nil || step.Type != StepTypeLoop || step.Input == nil {
			continue
		}
		bodyStepID, _, err := loopInputString(step.Input, "body_step", "body")
		if err != nil {
			continue
		}
		if bodyStepID == "" || bodyStepID == parentID {
			continue
		}
		if _, exists := owners[bodyStepID]; !exists {
			owners[bodyStepID] = parentID
		}
	}
	return owners
}

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

func parsePositiveInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v, nil
		}
	case int32:
		if v > 0 {
			return int(v), nil
		}
	case int64:
		if v > 0 {
			return int(v), nil
		}
	case float64:
		if v > 0 && math.Mod(v, 1) == 0 {
			return int(v), nil
		}
	case float32:
		fv := float64(v)
		if fv > 0 && math.Mod(fv, 1) == 0 {
			return int(fv), nil
		}
	case json.Number:
		if i64, err := v.Int64(); err == nil && i64 > 0 {
			return int(i64), nil
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("expected positive integer, got %v", value)
}

type switchCase struct {
	MatchValue any
	StepID     string
}

func parseSwitchCases(step *Step, scope map[string]any) ([]switchCase, error) {
	if step == nil || step.Input == nil {
		return nil, fmt.Errorf("switch input.cases required")
	}
	rawCases, ok := step.Input["cases"]
	if !ok {
		return nil, fmt.Errorf("switch input.cases required")
	}
	evaledCases, err := evalTemplates(rawCases, scope)
	if err != nil {
		return nil, fmt.Errorf("switch cases eval failed: %w", err)
	}
	switch typed := evaledCases.(type) {
	case []any:
		out := make([]switchCase, 0, len(typed))
		for idx, entry := range typed {
			parsed, err := parseSwitchCaseEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("switch case %d invalid: %w", idx, err)
			}
			out = append(out, parsed)
		}
		return out, nil
	case map[string]any:
		out := make([]switchCase, 0, len(typed))
		for matchVal, targetRaw := range typed {
			stepID := strings.TrimSpace(fmt.Sprint(targetRaw))
			if stepID == "" {
				return nil, fmt.Errorf("switch case %q has empty target step", matchVal)
			}
			out = append(out, switchCase{MatchValue: matchVal, StepID: stepID})
		}
		return out, nil
	case nil:
		return []switchCase{}, nil
	default:
		return nil, fmt.Errorf("switch input.cases must be array or map, got %T", evaledCases)
	}
}

func parseSwitchCaseEntry(value any) (switchCase, error) {
	var raw map[string]any
	switch typed := value.(type) {
	case map[string]any:
		raw = typed
	case map[string]string:
		raw = make(map[string]any, len(typed))
		for k, v := range typed {
			raw[k] = v
		}
	default:
		return switchCase{}, fmt.Errorf("case must be object, got %T", value)
	}
	matchValue, matchKey, hasMatch := firstSwitchValue(raw, "when", "match", "value")
	if !hasMatch {
		return switchCase{}, fmt.Errorf("case requires one of when/match/value")
	}
	if matchKey == "when" {
		// "when" is treated as a match literal, not an expression.
		matchValue = strings.TrimSpace(fmt.Sprint(matchValue))
	}
	targetRaw, _, hasTarget := firstSwitchValue(raw, "next", "step", "target")
	if !hasTarget {
		return switchCase{}, fmt.Errorf("case requires one of next/step/target")
	}
	stepID := strings.TrimSpace(fmt.Sprint(targetRaw))
	if stepID == "" {
		return switchCase{}, fmt.Errorf("case target step id required")
	}
	return switchCase{MatchValue: matchValue, StepID: stepID}, nil
}

func firstSwitchValue(values map[string]any, keys ...string) (any, string, bool) {
	for _, key := range keys {
		if val, ok := values[key]; ok {
			return val, key, true
		}
	}
	return nil, "", false
}

func parseSwitchDefault(step *Step, scope map[string]any) (string, error) {
	if step == nil || step.Input == nil {
		return "", nil
	}
	rawDefault, ok := step.Input["default"]
	if !ok {
		rawDefault, ok = step.Input["default_step"]
		if !ok {
			return "", nil
		}
	}
	evaledDefault, err := evalTemplates(rawDefault, scope)
	if err != nil {
		return "", fmt.Errorf("switch default eval failed: %w", err)
	}
	if evaledDefault == nil {
		return "", nil
	}
	defaultStepID := strings.TrimSpace(fmt.Sprint(evaledDefault))
	return defaultStepID, nil
}

func switchValueEquals(actual, expected any) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	if actualNum, ok := switchComparableNumber(actual); ok {
		if expectedNum, ok := switchComparableNumber(expected); ok {
			return actualNum == expectedNum
		}
	}
	switch actualTyped := actual.(type) {
	case string:
		if expectedTyped, ok := expected.(string); ok {
			return strings.TrimSpace(actualTyped) == strings.TrimSpace(expectedTyped)
		}
	case bool:
		if expectedTyped, ok := expected.(bool); ok {
			return actualTyped == expectedTyped
		}
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected)
}

func switchComparableNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return parsed, true
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
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
			e.publishJobCancel(child.JobID, "parallel strategy satisfied")
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

func isTerminalStepStatus(status StepStatus) bool {
	switch status {
	case StepStatusSucceeded, StepStatusFailed, StepStatusCancelled, StepStatusTimedOut:
		return true
	default:
		return false
	}
}

func isSwitchBranchNotTaken(sr *StepRun) bool {
	if sr == nil || sr.Status != StepStatusCancelled || sr.Error == nil {
		return false
	}
	reason, _ := sr.Error["reason"].(string)
	return strings.TrimSpace(reason) == switchBranchNotTakenReason
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
	case pb.JobStatus_JOB_STATUS_FAILED, pb.JobStatus_JOB_STATUS_DENIED, pb.JobStatus_JOB_STATUS_TIMEOUT, pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE:
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
	case pb.JobStatus_JOB_STATUS_FAILED_FATAL:
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
	managedParallelChildren := collectParallelChildOwners(wfDef)
	managedLoopBodyChildren := collectLoopBodyOwners(wfDef)
	expectedSteps := len(wfDef.Steps)
	for stepID := range wfDef.Steps {
		if ownerID, managed := managedParallelChildren[stepID]; managed && ownerID != stepID {
			expectedSteps--
			continue
		}
		if ownerID, managed := managedLoopBodyChildren[stepID]; managed && ownerID != stepID {
			expectedSteps--
		}
	}
	if expectedSteps < 0 {
		expectedSteps = 0
	}
	for stepID := range wfDef.Steps {
		if ownerID, managed := managedParallelChildren[stepID]; managed && ownerID != stepID {
			// Parallel child templates are orchestrated by the parent parallel step.
			continue
		}
		if ownerID, managed := managedLoopBodyChildren[stepID]; managed && ownerID != stepID {
			// Loop body templates are orchestrated by the parent loop step.
			continue
		}
		sr := run.Steps[stepID]
		if sr == nil {
			// Unactivated on_error targets don't block run completion.
			if isOnErrorTarget(wfDef, stepID) {
				expectedSteps--
			} else {
				allDone = false
			}
			continue
		}
		switch sr.Status {
		case StepStatusFailed:
			stepDef := wfDef.Steps[stepID]
			if stepDef != nil && stepDef.OnError != "" {
				targetSR := run.Steps[stepDef.OnError]
				if targetSR == nil || targetSR.Status == "" || targetSR.Status == StepStatusPending || targetSR.Status == StepStatusRunning {
					// on_error handler is still pending/running — don't mark run as failed yet
					allDone = false
					break
				}
				if targetSR.Status == StepStatusSucceeded {
					// on_error handler succeeded — treat failure as handled
					completed++
					break
				}
			}
			hasFailed = true
		case StepStatusCancelled:
			if isSwitchBranchNotTaken(sr) {
				completed++
				break
			}
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
	if allDone && completed == expectedSteps {
		run.Status = RunStatusSucceeded
		run.CompletedAt = &now
		return
	}
	run.Status = RunStatusRunning
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return true
	default:
		return false
	}
}

func (e *Engine) markRunTerminal(runID string) {
	if e == nil || runID == "" {
		return
	}
	val, ok := e.runLocks.Load(runID)
	if !ok {
		return
	}
	lock := val.(*runLock)
	lock.terminal.Store(true)
	if lock.refs.Load() == 0 {
		e.runLocks.Delete(runID)
	}
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

// Stop cancels all pending delay timers and prevents new ones from firing.
// It is safe to call multiple times.
func (e *Engine) Stop() {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.stopped != nil {
		select {
		case <-e.stopped:
			return // already stopped
		default:
		}
		close(e.stopped)
	} else {
		e.stopped = make(chan struct{})
		close(e.stopped)
	}
	for _, t := range e.pendingTimers {
		t.Stop()
	}
	e.pendingTimers = nil
}

// PendingTimers returns the number of active delay timers (for testing).
func (e *Engine) PendingTimers() int {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	return len(e.pendingTimers)
}

func (e *Engine) scheduleAfter(delay time.Duration, workflowID, runID string) {
	if delay <= 0 {
		return
	}
	e.timerMu.Lock()
	if e.stopped != nil {
		select {
		case <-e.stopped:
			e.timerMu.Unlock()
			return // engine stopped, discard
		default:
		}
	}
	if e.stopped == nil {
		e.stopped = make(chan struct{})
	}
	stopped := e.stopped

	var t *time.Timer
	t = time.AfterFunc(delay, func() {
		// Atomically check stopped under timerMu to eliminate TOCTOU race.
		// Stop() also holds timerMu when closing the channel, so this is safe.
		e.timerMu.Lock()
		select {
		case <-stopped:
			e.timerMu.Unlock()
			return
		default:
		}
		// Remove ourselves from the pending list while holding the lock.
		for i, pt := range e.pendingTimers {
			if pt == t {
				e.pendingTimers[i] = e.pendingTimers[len(e.pendingTimers)-1]
				e.pendingTimers = e.pendingTimers[:len(e.pendingTimers)-1]
				break
			}
		}
		e.timerMu.Unlock()
		_ = e.StartRun(context.Background(), workflowID, runID)
	})
	e.pendingTimers = append(e.pendingTimers, t)
	e.timerMu.Unlock()
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
