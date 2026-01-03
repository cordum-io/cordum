package workflowengine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	wf "github.com/yaront1111/coretex-os/core/workflow"
)

const (
	reconcilerLockKey = "coretex:workflow-engine:reconciler:default"
)

type reconciler struct {
	workflowStore *wf.RedisStore
	engine        *wf.Engine
	jobStore      scheduler.JobStore
	pollInterval  time.Duration
	lockTTL       time.Duration
	runScanLimit  int64
}

func newReconciler(workflowStore *wf.RedisStore, engine *wf.Engine, jobStore scheduler.JobStore, pollInterval time.Duration, runScanLimit int64) *reconciler {
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
			ok, err := r.jobStore.TryAcquireLock(ctx, reconcilerLockKey, r.lockTTL)
			if err != nil {
				logging.Error("workflow-engine", "reconciler lock acquisition failed", "error", err)
				continue
			}
			if !ok {
				continue
			}
			r.tick(ctx)
			_ = r.jobStore.ReleaseLock(ctx, reconcilerLockKey)
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
		ok, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
		if err != nil {
			return bus.RetryAfter(err, 1*time.Second)
		}
		if !ok {
			return bus.RetryAfter(fmt.Errorf("run lock busy"), 500*time.Millisecond)
		}
		defer func() { _ = r.jobStore.ReleaseLock(context.Background(), lockKey) }()
	}
	if r.engine != nil {
		r.engine.HandleJobResult(ctx, jr)
	}
	return nil
}

func (r *reconciler) tick(ctx context.Context) {
	statuses := []wf.RunStatus{wf.RunStatusPending, wf.RunStatusRunning, wf.RunStatusWaiting}
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
}

func (r *reconciler) reconcileRun(ctx context.Context, runID string) {
	if runID == "" || r.workflowStore == nil || r.engine == nil || r.jobStore == nil {
		return
	}
	lockKey := runLockKey(runID)
	ok, err := r.jobStore.TryAcquireLock(ctx, lockKey, 30*time.Second)
	if err != nil || !ok {
		return
	}
	defer func() { _ = r.jobStore.ReleaseLock(context.Background(), lockKey) }()

	run, err := r.workflowStore.GetRun(ctx, runID)
	if err != nil || run == nil {
		return
	}
	switch run.Status {
	case wf.RunStatusSucceeded, wf.RunStatusFailed, wf.RunStatusCancelled, wf.RunStatusTimedOut:
		return
	}

	for _, sr := range run.Steps {
		if sr == nil || sr.Status != wf.StepStatusRunning || sr.JobID == "" {
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
		jr := &pb.JobResult{
			JobId:       sr.JobID,
			Status:      status,
			ResultPtr:   resultPtr,
			WorkerId:    "",
			ExecutionMs: 0,
		}
		if status != pb.JobStatus_JOB_STATUS_SUCCEEDED && jr.ErrorMessage == "" {
			jr.ErrorMessage = "job state: " + string(state)
		}
		r.engine.HandleJobResult(ctx, jr)
	}

	_ = r.engine.StartRun(ctx, run.WorkflowID, run.ID)
}

func jobStatusFromState(state scheduler.JobState) pb.JobStatus {
	switch state {
	case scheduler.JobStateSucceeded:
		return pb.JobStatus_JOB_STATUS_SUCCEEDED
	case scheduler.JobStateFailed:
		return pb.JobStatus_JOB_STATUS_FAILED
	case scheduler.JobStateTimeout:
		return pb.JobStatus_JOB_STATUS_TIMEOUT
	case scheduler.JobStateDenied:
		return pb.JobStatus_JOB_STATUS_DENIED
	case scheduler.JobStateCancelled:
		return pb.JobStatus_JOB_STATUS_CANCELLED
	default:
		return pb.JobStatus_JOB_STATUS_UNSPECIFIED
	}
}

func runLockKey(runID string) string {
	return "coretex:wf:run:lock:" + runID
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
