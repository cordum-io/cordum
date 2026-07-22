package store

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/model"
)

func TestRuntimeStateCannotRegressAfterFastTerminalResult(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	dispatchID, attempt := beginRunningDispatch(t, store, "job-fast-result")
	apply := durableResultApply("job-fast-result", dispatchID, attempt)
	if _, err := store.ApplyJobResult(ctx, apply); err != nil {
		t.Fatalf("ApplyJobResult() error = %v", err)
	}
	if err := store.SetState(ctx, apply.JobID, model.JobStateRunning); err == nil {
		t.Fatal("SetState(running) regressed a terminal runtime result")
	}
	state, err := store.GetState(ctx, apply.JobID)
	if err != nil || state != model.JobStateSucceeded {
		t.Fatalf("state after late running transition = (%q, %v), want succeeded", state, err)
	}
}

func TestCancelJobUpdatesAuthoritativeRuntimeState(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	_, _ = beginRunningDispatch(t, store, "job-runtime-cancel")
	state, err := store.CancelJob(ctx, "job-runtime-cancel")
	if err != nil || state != model.JobStateCancelled {
		t.Fatalf("CancelJob() = (%q, %v), want cancelled", state, err)
	}
	state, err = store.GetState(ctx, "job-runtime-cancel")
	if err != nil || state != model.JobStateCancelled {
		t.Fatalf("GetState() after cancel = (%q, %v), want cancelled", state, err)
	}
}

func TestApplyJobResultRejectsNonDispatchedState(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-not-dispatched"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState(pending) error = %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch() error = %v", err)
	}
	apply := durableResultApply(jobID, dispatchID, attempt)
	if got, err := store.ApplyJobResult(ctx, apply); err != nil || got != model.JobEventRejected {
		t.Fatalf("ApplyJobResult(pending) = (%v, %v), want rejected", got, err)
	}
}

func TestSignedProgressRejectsTerminalAttempt(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	dispatchID, attempt := beginRunningDispatch(t, store, "job-terminal-progress")
	apply := durableResultApply("job-terminal-progress", dispatchID, attempt)
	if _, err := store.ApplyJobResult(ctx, apply); err != nil {
		t.Fatalf("ApplyJobResult() error = %v", err)
	}
	disposition, err := store.AcceptSignedJobEvent(
		ctx, apply.JobID, dispatchID, attempt, apply.WorkerID, apply.Tenant,
		[]byte("progress-terminal"), []byte("digest"),
	)
	if err != nil || disposition != model.JobEventRejected {
		t.Fatalf("terminal progress = (%v, %v), want rejected", disposition, err)
	}
}
