package workflow

import (
	"context"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestCancelRun_NoOpOnTerminalRun asserts that CancelRun on a run already in a
// terminal state (Succeeded/Failed/Denied/TimedOut/Cancelled) is a no-op and
// does NOT clobber the existing run status, CompletedAt, or cascade
// cancellation to terminal step states.
//
// Regression for audit finding #1 (engine_state.go:264-329).
func TestCancelRun_NoOpOnTerminalRun(t *testing.T) {
	for _, tc := range []RunStatus{
		RunStatusSucceeded,
		RunStatusFailed,
		RunStatusDenied,
		RunStatusTimedOut,
		RunStatusCancelled,
	} {
		t.Run(string(tc), func(t *testing.T) {
			ws := newWorkflowStore(t)
			defer func() { _ = ws.Close() }()
			engine := NewEngine(ws, &recordingBus{})
			ctx := context.Background()

			wf := &Workflow{
				ID:    "wf-cancel-terminal-" + string(tc),
				OrgID: "org",
				Steps: map[string]*Step{
					"s1": {ID: "s1", Type: StepTypeWorker, Topic: "job.test"},
				},
			}
			if err := ws.SaveWorkflow(ctx, wf); err != nil {
				t.Fatalf("save workflow: %v", err)
			}

			originalCompleted := time.Now().Add(-1 * time.Hour).UTC()
			// Step state mirrors a finished step.
			finishedStepStatus := StepStatusSucceeded
			switch tc {
			case RunStatusFailed:
				finishedStepStatus = StepStatusFailed
			case RunStatusDenied:
				finishedStepStatus = StepStatusDenied
			case RunStatusTimedOut:
				finishedStepStatus = StepStatusTimedOut
			case RunStatusCancelled:
				finishedStepStatus = StepStatusCancelled
			}
			run := &WorkflowRun{
				ID:          "run-cancel-terminal-" + string(tc),
				WorkflowID:  wf.ID,
				OrgID:       "org",
				Status:      tc,
				CompletedAt: &originalCompleted,
				Steps: map[string]*StepRun{
					"s1": {StepID: "s1", Status: finishedStepStatus, CompletedAt: &originalCompleted},
				},
				CreatedAt: originalCompleted,
				UpdatedAt: originalCompleted,
			}
			if err := ws.CreateRun(ctx, run); err != nil {
				t.Fatalf("create run: %v", err)
			}

			if err := engine.CancelRun(ctx, run.ID); err != nil {
				t.Fatalf("CancelRun on terminal run %s returned err: %v", tc, err)
			}

			got, err := ws.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if got.Status != tc {
				t.Fatalf("status changed from %s to %s — terminal status must be preserved", tc, got.Status)
			}
			if got.CompletedAt == nil || !got.CompletedAt.Equal(originalCompleted) {
				t.Fatalf("CompletedAt mutated: was %v got %v", originalCompleted, got.CompletedAt)
			}
			s1 := got.Steps["s1"]
			if s1 == nil || s1.Status != finishedStepStatus {
				t.Fatalf("step status mutated: want %s got %v", finishedStepStatus, s1)
			}
		})
	}
}

// TestEngineState_DeniedTerminalOnArrival asserts that a DENIED job result
// short-circuits to StepStatusDenied immediately and does NOT pass through
// shouldRetry — policy denials should not consume retry budget.
//
// Regression for audit finding #2 (engine_state.go:525-543).
func TestEngineState_DeniedTerminalOnArrival(t *testing.T) {
	step := &Step{
		ID:    "denied-step",
		Type:  StepTypeWorker,
		Topic: "job.test",
		Retry: &RetryConfig{MaxRetries: 3, InitialBackoffSec: 1, Multiplier: 2},
	}
	sr := &StepRun{StepID: step.ID, Status: StepStatusRunning, Attempts: 1}
	res := &pb.JobResult{
		JobId:        "job-denied",
		Status:       pb.JobStatus_JOB_STATUS_DENIED,
		ErrorMessage: "policy denial",
	}

	retry, _ := applyResult(sr, res, step)
	if retry {
		t.Fatalf("DENIED must not enter retry path; got retry=true (attempts=%d)", sr.Attempts)
	}
	if sr.Status != StepStatusDenied {
		t.Fatalf("expected StepStatusDenied terminal-on-arrival, got %s", sr.Status)
	}
	if sr.CompletedAt == nil {
		t.Fatalf("expected CompletedAt set on terminal denial")
	}
}

// TestApproveStep_RejectsOnTerminalRun asserts that ApproveStep is a no-op
// (returns error) when the parent run is already in a terminal state.
//
// Regression for audit finding #10 (engine_state.go:206-208).
func TestApproveStep_RejectsOnTerminalRun(t *testing.T) {
	for _, tc := range []RunStatus{
		RunStatusCancelled,
		RunStatusFailed,
		RunStatusDenied,
		RunStatusTimedOut,
		RunStatusSucceeded,
	} {
		t.Run(string(tc), func(t *testing.T) {
			ws := newWorkflowStore(t)
			defer func() { _ = ws.Close() }()
			engine := NewEngine(ws, &recordingBus{})
			ctx := context.Background()

			wf := &Workflow{
				ID:    "wf-approve-terminal-" + string(tc),
				OrgID: "org",
				Steps: map[string]*Step{
					"gate": {ID: "gate", Type: StepTypeApproval, Topic: "job.test"},
				},
			}
			if err := ws.SaveWorkflow(ctx, wf); err != nil {
				t.Fatalf("save workflow: %v", err)
			}
			now := time.Now().UTC()
			run := &WorkflowRun{
				ID:         "run-approve-terminal-" + string(tc),
				WorkflowID: wf.ID,
				OrgID:      "org",
				Status:     tc,
				Steps: map[string]*StepRun{
					"gate": {StepID: "gate", Status: StepStatusWaiting},
				},
				CreatedAt:   now,
				UpdatedAt:   now,
				CompletedAt: &now,
			}
			if err := ws.CreateRun(ctx, run); err != nil {
				t.Fatalf("create run: %v", err)
			}

			if err := engine.ApproveStep(ctx, run.ID, "gate", true); err == nil {
				t.Fatalf("expected ApproveStep on %s run to fail, got nil", tc)
			}

			got, err := ws.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if got.Status != tc {
				t.Fatalf("status mutated from %s to %s", tc, got.Status)
			}
			if got.Steps["gate"].Status != StepStatusWaiting {
				t.Fatalf("approval step mutated: want StepStatusWaiting got %s", got.Steps["gate"].Status)
			}
		})
	}
}

// TestShouldRetry_AttemptsSemanticsDocumented locks in the documented retry
// semantics: shouldRetry uses Attempts<=MaxRetries with pre-increment Attempts,
// which (per workflow caller pattern) yields exactly MaxRetries actual retries
// after the initial attempt — i.e. (MaxRetries+1) total attempts.
//
// Regression for audit finding #12 (engine_state.go:559-567). This test pins
// the convention so future refactors of either scheduler-side maxSchedulingRetries
// or this function update both call sites consistently.
func TestShouldRetry_AttemptsSemanticsDocumented(t *testing.T) {
	step := &Step{Retry: &RetryConfig{MaxRetries: 3}}

	cases := []struct {
		attempts int
		want     bool
	}{
		{0, true}, // first call (no attempt yet) — would retry
		{1, true}, // 1 attempt — would retry
		{2, true}, // 2 attempts — would retry
		{3, true}, // 3 attempts — boundary (Attempts<=max)
		{4, false},
		{5, false},
	}
	for _, c := range cases {
		sr := &StepRun{Attempts: c.attempts}
		got := shouldRetry(step, sr)
		if got != c.want {
			t.Fatalf("shouldRetry(MaxRetries=3, Attempts=%d) = %v, want %v", c.attempts, got, c.want)
		}
	}

	// MaxRetries=0 is "no retry configured", always false.
	if shouldRetry(&Step{Retry: &RetryConfig{MaxRetries: 0}}, &StepRun{Attempts: 0}) {
		t.Fatal("MaxRetries=0 must short-circuit to no retry")
	}
	// Nil Retry config: no retry.
	if shouldRetry(&Step{}, &StepRun{Attempts: 0}) {
		t.Fatal("nil Retry must short-circuit to no retry")
	}
}
