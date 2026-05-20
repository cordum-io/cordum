package store

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
)

// TestJobStore_ApprovalToCancelledTransition asserts that a job in
// JobStateApproval can be transitioned to JobStateCancelled. Workflow engine
// routinely cancels approval-gate jobs (via CancelRun → publishJobCancel →
// worker returns CANCELLED → scheduler setJobState), so this transition must
// be in allowedTransitions.
//
// Regression for audit finding #6 (job_store.go:93-113).
func TestJobStore_ApprovalToCancelledTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()
	s, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	jobID := "job-approval-cancel"

	if err := s.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	if err := s.SetState(ctx, jobID, model.JobStateCancelled); err != nil {
		t.Fatalf("Approval→Cancelled must be an allowed transition, got: %v", err)
	}
	gotState, err := s.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if gotState != model.JobStateCancelled {
		t.Fatalf("expected JobStateCancelled, got %s", gotState)
	}
}

// TestJobStore_InitialToCancelledTransition asserts that a fresh job (no prior
// state) can be transitioned to JobStateCancelled, which is needed when a
// workflow is cancelled before any state is written (e.g. cancellation arrives
// before SetState(Pending)).
//
// Regression for audit finding #6 ("" entry in allowedTransitions).
func TestJobStore_InitialToCancelledTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()
	s, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	if err := s.SetState(ctx, "job-fresh-cancel", model.JobStateCancelled); err != nil {
		t.Fatalf(`""→Cancelled must be an allowed transition for early-cancel race, got: %v`, err)
	}
}

// TestJobStore_RunningSelfTransition asserts that JobStateRunning →
// JobStateRunning is a permitted no-op self-transition. Worker heartbeats /
// progress messages that re-assert Running must not trip
// invalidTransitionError.
//
// Regression for audit finding #7 (scheduler/engine.go:1747-1795).
func TestJobStore_RunningSelfTransition(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()
	s, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	jobID := "job-running-self"
	for _, st := range []model.JobState{model.JobStatePending, model.JobStateDispatched, model.JobStateRunning} {
		if err := s.SetState(ctx, jobID, st); err != nil {
			t.Fatalf("set %s: %v", st, err)
		}
	}
	// Second Running write must be a no-op, not an invalid-transition error.
	if err := s.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("Running→Running self-transition must succeed (no-op), got: %v", err)
	}
}

// TestJobStore_RetryingTransitionExists asserts the new non-terminal
// JobStateRetrying is defined and has valid transitions in both directions —
// from active states INTO Retrying, and from Retrying back to active or
// terminal states.
//
// Regression for audit finding #3 (FAILED_RETRYABLE must not map to terminal
// JobStateFailed; introduce JobStateRetrying instead).
func TestJobStore_RetryingTransitionExists(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()
	s, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// Dispatched/Running → Retrying.
	for _, from := range []model.JobState{model.JobStatePending, model.JobStateDispatched, model.JobStateRunning} {
		jobID := "job-retrying-" + string(from)
		if err := s.SetState(ctx, jobID, from); err != nil {
			t.Fatalf("set %s: %v", from, err)
		}
		if err := s.SetState(ctx, jobID, model.JobStateRetrying); err != nil {
			t.Fatalf("%s→Retrying must be allowed, got: %v", from, err)
		}
	}

	// Retrying → Failed (workflow exhausts retry budget) and Retrying →
	// Cancelled (workflow cancelled mid-retry).
	for _, to := range []model.JobState{model.JobStateFailed, model.JobStateCancelled, model.JobStateSucceeded} {
		jobID := "job-retrying-to-" + string(to)
		if err := s.SetState(ctx, jobID, model.JobStateRunning); err != nil {
			t.Fatalf("set Running: %v", err)
		}
		if err := s.SetState(ctx, jobID, model.JobStateRetrying); err != nil {
			t.Fatalf("Running→Retrying: %v", err)
		}
		if err := s.SetState(ctx, jobID, to); err != nil {
			t.Fatalf("Retrying→%s must be allowed, got: %v", to, err)
		}
	}

	// Retrying is NOT terminal.
	if terminalStates[model.JobStateRetrying] {
		t.Fatal("JobStateRetrying must not be in terminalStates — that is what makes it retryable")
	}
}
