package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/logging"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	reconcilerLockKey = "cordum:workflow-engine:reconciler:default"
)

type reconciler struct {
	workflowStore *RedisStore
	engine        *Engine
	jobStore      model.JobStore
	pollInterval  time.Duration
	lockTTL       time.Duration
	runScanLimit  int64
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
				logging.Error("workflow-engine", "reconciler lock acquisition failed", "error", err)
				continue
			}
			if token == "" {
				continue
			}
			r.tick(ctx)
			_ = r.jobStore.ReleaseLock(ctx, reconcilerLockKey, token)
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
		defer func() { _ = r.jobStore.ReleaseLock(context.Background(), lockKey, token) }()
	}
	if r.engine != nil {
		r.engine.HandleJobResult(ctx, jr)
	}
	return nil
}

func (r *reconciler) tick(ctx context.Context) {
	statuses := []RunStatus{RunStatusPending, RunStatusRunning, RunStatusWaiting}
	for _, status := range statuses {
		ids, err := r.workflowStore.ListRunIDsByStatus(ctx, status, r.runScanLimit)
		if err != nil {
			logging.Error("workflow-engine", "list runs by status", "status", status, "error", err)
			continue
		}
		for _, runID := range ids {
			r.reconcileRun(ctx, runID)
		}
	}
	// Scan cancelled/timed-out runs for orphaned jobs that failed to cancel.
	terminalStatuses := []RunStatus{RunStatusCancelled, RunStatusTimedOut}
	for _, status := range terminalStatuses {
		ids, err := r.workflowStore.ListRunIDsByStatus(ctx, status, r.runScanLimit)
		if err != nil {
			logging.Error("workflow-engine", "list terminal runs by status", "status", status, "error", err)
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
	if err != nil || token == "" {
		return
	}
	defer func() { _ = r.jobStore.ReleaseLock(context.Background(), lockKey, token) }()

	run, err := r.workflowStore.GetRun(ctx, runID)
	if err != nil || run == nil {
		return
	}
	switch run.Status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled, RunStatusTimedOut:
		return
	}

	for _, sr := range run.Steps {
		if sr == nil || sr.Status != StepStatusRunning || sr.JobID == "" {
			continue
		}
		state, err := r.jobStore.GetState(ctx, sr.JobID)
		if err != nil {
			continue
		}
		status := jobStatusFromState(state)
		if status == pb.JobStatus_JOB_STATUS_UNSPECIFIED {
			continue
		}
		resultPtr, _ := r.jobStore.GetResultPtr(ctx, sr.JobID)
		failureReason := ""
		if status != pb.JobStatus_JOB_STATUS_SUCCEEDED {
			failureReason, _ = r.jobStore.GetFailureReason(ctx, sr.JobID)
		}
		jr := &pb.JobResult{
			JobId:       sr.JobID,
			Status:      status,
			ResultPtr:   resultPtr,
			ErrorMessage: failureReason,
			WorkerId:    "",
			ExecutionMs: 0,
		}
		if status != pb.JobStatus_JOB_STATUS_SUCCEEDED && jr.ErrorMessage == "" {
			jr.ErrorMessage = fmt.Sprintf("job %s terminated with state %s (no error details available)", sr.JobID, state)
		}
		r.engine.HandleJobResult(ctx, jr)
	}

	_ = r.engine.StartRun(ctx, run.WorkflowID, run.ID)
}

// reconcileOrphanedJobs scans cancelled/timed-out runs for step runs whose
// underlying jobs are still running or dispatched. This catches cases where
// the initial cancel publish failed despite retries.
func (r *reconciler) reconcileOrphanedJobs(ctx context.Context, runID string) {
	if runID == "" || r.workflowStore == nil || r.engine == nil || r.jobStore == nil {
		return
	}
	run, err := r.workflowStore.GetRun(ctx, runID)
	if err != nil || run == nil {
		return
	}
	if run.Status != RunStatusCancelled && run.Status != RunStatusTimedOut {
		return
	}

	var orphaned []string
	for _, sr := range run.Steps {
		orphaned = append(orphaned, collectOrphanedJobIDs(ctx, sr, r.jobStore)...)
	}
	if len(orphaned) == 0 {
		return
	}

	lockKey := runLockKey(runID)
	token, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || token == "" {
		return
	}
	defer func() { _ = r.jobStore.ReleaseLock(context.Background(), lockKey, token) }()

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

func runLockKey(runID string) string {
	return "cordum:wf:run:lock:" + runID
}

