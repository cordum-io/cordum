package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StartRun loads the workflow/run and dispatches any ready steps.
func (e *Engine) StartRun(ctx context.Context, workflowID, runID string) error {
	unlock, ok := e.lockRun(runID)
	if !ok {
		return nil // Another replica owns this run.
	}
	defer unlock()
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
	unlock, ok := e.lockRun(runID)
	if !ok {
		return "", fmt.Errorf("run %s is being processed by another replica", runID)
	}
	defer unlock()
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

func (e *Engine) ApproveStep(ctx context.Context, runID, stepID string, approved bool) error {
	unlock, ok := e.lockRun(runID)
	if !ok {
		return fmt.Errorf("run %s is being processed by another replica", runID)
	}
	defer unlock()

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

	// On denial, activate the on_error handler (matching HandleJobResult logic).
	if !approved {
		stepDef := wfDef.Steps[stepID]
		if stepDef != nil && stepDef.OnError != "" {
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
					errCtx["step_id"] = stepID
					errCtx["message"] = "approval denied"
					targetSR.Input["error"] = errCtx
					run.Steps[stepDef.OnError] = targetSR
					e.appendTimeline(ctx, run, "step_error_redirect", stepID, "", string(sr.Status), "", stepDef.OnError, nil)
				}
			}
		}
	}

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
	if run.Status == RunStatusRunning {
		return e.scheduleReady(ctx, wfDef, run)
	}
	return nil
}

// CancelRun marks a run and all non-terminal steps as cancelled to prevent further dispatch.
func (e *Engine) CancelRun(ctx context.Context, runID string) error {
	unlock, ok := e.lockRun(runID)
	if !ok {
		return fmt.Errorf("run %s is being processed by another replica", runID)
	}
	defer unlock()

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
	var failedJobIDs []string
	seen := make(map[string]struct{}, len(cancelJobIDs))
	for _, jobID := range cancelJobIDs {
		if jobID == "" {
			continue
		}
		if _, ok := seen[jobID]; ok {
			continue
		}
		seen[jobID] = struct{}{}
		if err := e.publishJobCancel(jobID, "workflow run cancelled"); err != nil {
			failedJobIDs = append(failedJobIDs, jobID)
		}
	}
	if len(failedJobIDs) > 0 {
		e.appendTimeline(ctx, run, "cancel_publish_failed", "", "", "error", "",
			fmt.Sprintf("failed to cancel %d job(s) — these jobs may still be running", len(failedJobIDs)),
			map[string]any{"orphaned_job_ids": failedJobIDs},
		)
		return fmt.Errorf("workflow cancel run %s: %d cancel publish failures (jobs: %v)", runID, len(failedJobIDs), failedJobIDs)
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
	var failedJobIDs []string
	seen := make(map[string]struct{}, len(cancelJobIDs))
	for _, jobID := range cancelJobIDs {
		if jobID == "" {
			continue
		}
		if _, ok := seen[jobID]; ok {
			continue
		}
		seen[jobID] = struct{}{}
		if err := e.publishJobCancel(jobID, "workflow run timed out"); err != nil {
			failedJobIDs = append(failedJobIDs, jobID)
		}
	}
	if len(failedJobIDs) > 0 {
		e.appendTimeline(ctx, run, "cancel_publish_failed", "", "", "error", "",
			fmt.Sprintf("failed to cancel %d job(s) after timeout — these jobs may still be running", len(failedJobIDs)),
			map[string]any{"orphaned_job_ids": failedJobIDs},
		)
		return fmt.Errorf("workflow timeout run %s: %d cancel publish failures (jobs: %v)", run.ID, len(failedJobIDs), failedJobIDs)
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

func (e *Engine) publishJobCancel(jobID, reason string) error {
	if e == nil || e.bus == nil || jobID == "" {
		return nil
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

	backoff := [3]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = e.bus.Publish(capsdk.SubjectCancel, packet)
		if lastErr == nil {
			return nil
		}
		slog.Error("publish job cancel failed",
			"job_id", jobID,
			"reason", reason,
			"attempt", attempt+1,
			"err", lastErr,
		)
		if attempt < 2 {
			time.Sleep(backoff[attempt])
		}
	}
	return fmt.Errorf("publish job cancel %s after 3 attempts: %w", jobID, lastErr)
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

// chainOutcome represents the result of walking an on_error handler chain.
type chainOutcome int

const (
	chainPending   chainOutcome = iota // a handler in the chain is still pending/running
	chainRecovered                     // a handler in the chain succeeded (recovery)
	chainExhausted                     // all handlers in the chain failed with no further on_error
)

// walkOnErrorChain iteratively follows the on_error chain starting from startStepID.
// It returns chainPending if any handler is still processing, chainRecovered if any
// handler succeeded, or chainExhausted if the chain is exhausted with no recovery.
// Uses a visited map for cycle detection.
func walkOnErrorChain(wfDef *Workflow, run *WorkflowRun, startStepID string) chainOutcome {
	visited := map[string]bool{}
	cur := startStepID
	for {
		if visited[cur] {
			return chainExhausted // cycle detected
		}
		visited[cur] = true
		stepDef := wfDef.Steps[cur]
		if stepDef == nil || stepDef.OnError == "" {
			return chainExhausted
		}
		handlerSR := run.Steps[stepDef.OnError]
		if handlerSR == nil || handlerSR.Status == "" || handlerSR.Status == StepStatusPending || handlerSR.Status == StepStatusRunning {
			return chainPending
		}
		if handlerSR.Status == StepStatusSucceeded {
			return chainRecovered
		}
		// Handler failed — walk to its own on_error
		cur = stepDef.OnError
	}
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
				switch walkOnErrorChain(wfDef, run, stepID) {
				case chainPending:
					allDone = false
				case chainRecovered:
					completed++
				case chainExhausted:
					hasFailed = true
				}
			} else {
				hasFailed = true
			}
		case StepStatusCancelled:
			if isSwitchBranchNotTaken(sr) {
				completed++
			} else {
				hasFailed = true
			}
		case StepStatusTimedOut:
			stepDef := wfDef.Steps[stepID]
			if stepDef != nil && stepDef.OnError != "" {
				switch walkOnErrorChain(wfDef, run, stepID) {
				case chainPending:
					allDone = false
				case chainRecovered:
					completed++
				case chainExhausted:
					hasTimedOut = true
				}
			} else {
				hasTimedOut = true
			}
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

func (e *Engine) markRunTerminal(runID string) {
	if e == nil || runID == "" {
		return
	}
	e.lockMgr.markTerminal(runID)
}
