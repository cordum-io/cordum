package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

const (
	reconcilerLockKey = "cordum:workflow-engine:reconciler:default"
)

// Reconciler polls for stuck workflow runs and advances them.
type Reconciler = reconciler

type reconciler struct {
	workflowStore *RedisStore
	engine        *Engine
	jobStore      model.JobStore
	pollInterval  time.Duration
	lockTTL       time.Duration
	runScanLimit  int64
}

// NewReconciler creates a workflow reconciler that polls for stuck runs.
func NewReconciler(workflowStore *RedisStore, engine *Engine, jobStore model.JobStore, pollInterval time.Duration, runScanLimit int64) *reconciler {
	return newReconciler(workflowStore, engine, jobStore, pollInterval, runScanLimit)
}

func newReconciler(workflowStore *RedisStore, engine *Engine, jobStore model.JobStore, pollInterval time.Duration, runScanLimit int64) *reconciler {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if runScanLimit <= 0 {
		runScanLimit = 200
	}
	return &reconciler{
		workflowStore: workflowStore,
		engine:        engine,
		jobStore:      jobStore,
		pollInterval:  pollInterval,
		lockTTL:       pollInterval * 2,
		runScanLimit:  runScanLimit,
	}
}

func (r *reconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.jobStore == nil || r.workflowStore == nil || r.engine == nil {
				continue
			}
			token, err := r.jobStore.TryAcquireLock(ctx, reconcilerLockKey, r.lockTTL)
			if err != nil {
				slog.Error("reconciler lock acquisition failed", "error", err)
				continue
			}
			if token == "" {
				continue
			}
			r.tick(ctx)
			// Lock held until TTL expiry (pollInterval * 2) — intentional for
			// horizontal scaling. Only one replica may run tick() per TTL window.
		}
	}
}

func (r *reconciler) HandleJobResult(ctx context.Context, jr *pb.JobResult) error {
	if jr == nil || jr.JobId == "" {
		return nil
	}
	runID, _ := splitJobID(jr.JobId)
	if runID == "" {
		return nil
	}
	if r.jobStore != nil {
		lockKey := runLockKey(runID)
		token, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
		if err != nil {
			return bus.RetryAfter(err, 1*time.Second)
		}
		if token == "" {
			return bus.RetryAfter(fmt.Errorf("run lock busy"), 500*time.Millisecond)
		}
		defer func() {
			if rlErr := r.jobStore.ReleaseLock(context.Background(), lockKey, token); rlErr != nil {
				slog.Warn("lock release failed", "lock_key", lockKey, "error", rlErr)
			}
		}()
	}
	if r.engine != nil {
		if err := r.engine.HandleJobResult(ctx, jr); err != nil {
			if errors.Is(err, ErrRunNotFound) {
				slog.Info("reconciler: discarding result for deleted run",
					"run_id", runID, "job_id", jr.JobId)
				return nil
			}
			return bus.RetryAfter(err, 1*time.Second)
		}
	}
	return nil
}

func (r *reconciler) tick(ctx context.Context) {
	statuses := []RunStatus{RunStatusPending, RunStatusRunning, RunStatusWaiting}
	for _, status := range statuses {
		ids, err := r.workflowStore.ListRunIDsByStatus(ctx, status, r.runScanLimit)
		if err != nil {
			slog.Error("list runs by status", "status", status, "error", err)
			continue
		}
		for _, runID := range ids {
			r.reconcileRun(ctx, runID)
		}
	}
	// Scan cancelled/timed-out runs for orphaned jobs that failed to cancel.
	terminalStatuses := []RunStatus{RunStatusCancelled, RunStatusTimedOut, RunStatusFailed, RunStatusDenied}
	for _, status := range terminalStatuses {
		ids, err := r.workflowStore.ListRunIDsByStatus(ctx, status, r.runScanLimit)
		if err != nil {
			slog.Error("list terminal runs by status", "status", status, "error", err)
			continue
		}
		for _, runID := range ids {
			r.reconcileOrphanedJobs(ctx, runID)
		}
	}
}

func (r *reconciler) reconcileRun(ctx context.Context, runID string) {
	if runID == "" || r.workflowStore == nil || r.engine == nil || r.jobStore == nil {
		return
	}
	lockKey := runLockKey(runID)
	token, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil {
		slog.Error("reconciler run lock failed", "run_id", runID, "error", err)
		return
	}
	if token == "" {
		return
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		if rlErr := r.jobStore.ReleaseLock(releaseCtx, lockKey, token); rlErr != nil {
			slog.Warn("lock release failed, TTL will expire", "lock_key", lockKey, "error", rlErr)
		}
	}()

	run, err := r.workflowStore.GetRun(ctx, runID)
	if err != nil || run == nil {
		return
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusDenied, RunStatusCancelled, RunStatusTimedOut:
		return
	}

	for _, sr := range run.Steps {
		if sr == nil || sr.Status != StepStatusRunning || sr.JobID == "" {
			continue
		}
		state, err := r.jobStore.GetState(ctx, sr.JobID)
		if err != nil {
			slog.Error("reconciler: GetState failed, skipping step",
				"run_id", runID, "job_id", sr.JobID, "error", err)
			continue
		}
		status := jobStatusFromState(state)
		if status == pb.JobStatus_JOB_STATUS_UNSPECIFIED {
			continue
		}
		// For succeeded jobs, the result pointer is critical — without it the
		// step would be permanently marked Succeeded with no output (data loss).
		// Defer completion until the store is reachable.
		resultPtr := ""
		if status == pb.JobStatus_JOB_STATUS_SUCCEEDED {
			resultPtr, err = r.jobStore.GetResultPtr(ctx, sr.JobID)
			if err != nil {
				slog.Error("reconciler: GetResultPtr failed, deferring step completion",
					"run_id", runID, "job_id", sr.JobID, "error", err)
				continue
			}
		}
		failureReason := ""
		if status != pb.JobStatus_JOB_STATUS_SUCCEEDED {
			failureReason, err = r.jobStore.GetFailureReason(ctx, sr.JobID)
			if err != nil {
				slog.Warn("reconciler: GetFailureReason failed, using generic message",
					"run_id", runID, "job_id", sr.JobID, "error", err)
			}
		}
		jr := &pb.JobResult{
			JobId:        sr.JobID,
			Status:       status,
			ResultPtr:    resultPtr,
			ErrorMessage: failureReason,
			WorkerId:     "",
			ExecutionMs:  0,
		}
		if status != pb.JobStatus_JOB_STATUS_SUCCEEDED && jr.ErrorMessage == "" {
			jr.ErrorMessage = fmt.Sprintf("job %s terminated with state %s (no error details available)", sr.JobID, state)
		}
		// Ignore ErrRunNotFound — run may have been deleted between scan and processing.
		_ = r.engine.HandleJobResult(ctx, jr)
	}

	// Detect approval steps stuck in Waiting with no corresponding job in the
	// scheduler. This handles the crash gap between UpdateRun (step=Waiting)
	// and Publish (approval job dispatch) in scheduleReady.
	r.reconcileStuckWaitingSteps(ctx, run)

	if err := r.engine.StartRun(ctx, run.WorkflowID, run.ID); err != nil {
		slog.Error("reconciler: StartRun failed, will retry next tick",
			"run_id", run.ID, "workflow_id", run.WorkflowID, "error", err)
	}
}

// reconcileOrphanedJobs scans cancelled/timed-out runs for step runs whose
// underlying jobs are still running or dispatched. This catches cases where
// the initial cancel publish failed despite retries.
func (r *reconciler) reconcileOrphanedJobs(ctx context.Context, runID string) {
	if runID == "" || r.workflowStore == nil || r.engine == nil || r.jobStore == nil {
		return
	}
	run, err := r.workflowStore.GetRun(ctx, runID) // #nosec G706 -- runID is internal run identifier, structured slog logging
	if err != nil || run == nil {
		return
	}
	if run.Status != RunStatusCancelled && run.Status != RunStatusTimedOut {
		return
	}

	var orphaned []string // #nosec G706 -- runID used in structured slog key-value pairs below
	for _, sr := range run.Steps {
		orphaned = append(orphaned, collectOrphanedJobIDs(ctx, sr, r.jobStore)...)
	}
	if len(orphaned) == 0 {
		return
	}

	lockKey := runLockKey(runID)
	token, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil {
		slog.Error("reconciler orphan lock failed", "run_id", runID, "error", err)
		return
	}
	if token == "" {
		return
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer releaseCancel()
		if rlErr := r.jobStore.ReleaseLock(releaseCtx, lockKey, token); rlErr != nil {
			slog.Warn("lock release failed, TTL will expire", "lock_key", lockKey, "error", rlErr)
		}
	}()

	reason := "orphaned after workflow " + string(run.Status)
	for _, jobID := range orphaned {
		slog.Warn("re-cancelling orphaned workflow job",
			"run_id", runID,
			"job_id", jobID,
			"run_status", run.Status,
		)
		if err := r.engine.publishJobCancel(jobID, reason); err != nil {
			slog.Error("reconciler: orphan cancel still failing",
				"run_id", runID,
				"job_id", jobID,
				"err", err,
			)
		}
	}
}

// collectOrphanedJobIDs recursively finds job IDs in a step run tree whose
// underlying job state is still Running or Dispatched (not yet terminated).
func collectOrphanedJobIDs(ctx context.Context, sr *StepRun, jobStore model.JobStore) []string {
	if sr == nil || sr.JobID == "" {
		if sr != nil {
			var out []string
			for _, child := range sr.Children {
				out = append(out, collectOrphanedJobIDs(ctx, child, jobStore)...)
			}
			return out
		}
		return nil
	}
	var out []string
	state, err := jobStore.GetState(ctx, sr.JobID)
	if err == nil && isActiveJobState(state) {
		out = append(out, sr.JobID)
	}
	for _, child := range sr.Children {
		out = append(out, collectOrphanedJobIDs(ctx, child, jobStore)...)
	}
	return out
}

func isActiveJobState(state model.JobState) bool {
	switch state {
	case model.JobStateRunning, model.JobStateDispatched:
		return true
	default:
		return false
	}
}

func jobStatusFromState(state model.JobState) pb.JobStatus {
	switch state {
	case model.JobStateSucceeded:
		return pb.JobStatus_JOB_STATUS_SUCCEEDED
	case model.JobStateFailed:
		return pb.JobStatus_JOB_STATUS_FAILED
	case model.JobStateTimeout:
		return pb.JobStatus_JOB_STATUS_TIMEOUT
	case model.JobStateDenied:
		return pb.JobStatus_JOB_STATUS_DENIED
	case model.JobStateCancelled:
		return pb.JobStatus_JOB_STATUS_CANCELLED
	default:
		return pb.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

// reconcileStuckWaitingSteps detects approval steps stuck in Waiting state
// because a crash occurred after UpdateRun persisted the Waiting status but
// before the approval job was published to the bus. Resets stuck steps to
// Pending so scheduleReady re-dispatches the approval job on the next tick.
func (r *reconciler) reconcileStuckWaitingSteps(ctx context.Context, run *WorkflowRun) {
	if run == nil || r.jobStore == nil {
		return
	}
	const (
		gracePeriod = 30 * time.Second
		maxRetries  = 3
	)

	now := time.Now().UTC()
	modified := false

	for stepID, sr := range run.Steps {
		if sr == nil || sr.Status != StepStatusWaiting || sr.JobID == "" {
			continue
		}
		// Grace period: skip steps that just entered Waiting — the publish
		// may still be in flight on another goroutine.
		if sr.StartedAt != nil && now.Sub(*sr.StartedAt) < gracePeriod {
			continue
		}
		// Check if the approval job exists in the scheduler.
		state, err := r.jobStore.GetState(ctx, sr.JobID)
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("reconciler: GetState for waiting step failed",
				"run_id", run.ID, "job_id", sr.JobID, "step_id", stepID, "error", err)
			continue
		}
		// Job exists (any state including PENDING, APPROVAL_REQUIRED, etc.) — not stuck.
		if err == nil && state != "" {
			continue
		}
		// Job not found — approval job was never dispatched (crash gap).
		retries := reconcileRetryCount(sr)
		if retries >= maxRetries {
			slog.Error("reconciler: stuck approval step exceeded max retries, marking failed",
				"run_id", run.ID, "step_id", stepID, "job_id", sr.JobID, "retries", retries)
			sr.Status = StepStatusFailed
			sr.CompletedAt = &now
			sr.Error = map[string]any{
				"message":             "approval dispatch failed after reconciler recovery attempts",
				"_reconcile_retries": retries,
			}
			run.Steps[stepID] = sr
			modified = true
			continue
		}
		// Reset to Pending for re-dispatch by scheduleReady.
		slog.Warn("reconciler: resetting stuck approval step to Pending for re-dispatch",
			"run_id", run.ID, "step_id", stepID, "job_id", sr.JobID, "retry", retries+1)
		sr.Status = StepStatusPending
		sr.JobID = ""
		sr.Attempts = 0
		sr.StartedAt = nil
		if sr.Error == nil {
			sr.Error = make(map[string]any)
		}
		sr.Error["_reconcile_retries"] = float64(retries + 1)
		run.Steps[stepID] = sr
		modified = true
	}

	if modified {
		if err := r.workflowStore.UpdateRun(ctx, run); err != nil {
			slog.Error("reconciler: failed to persist stuck approval step recovery",
				"run_id", run.ID, "error", err)
		}
	}
}

// reconcileRetryCount extracts the reconciler retry count from a step's Error map.
func reconcileRetryCount(sr *StepRun) int {
	if sr == nil || sr.Error == nil {
		return 0
	}
	rc, ok := sr.Error["_reconcile_retries"]
	if !ok {
		return 0
	}
	// JSON round-trips numbers as float64.
	switch v := rc.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func runLockKey(runID string) string {
	return "cordum:wf:run:lock:" + runID
}
