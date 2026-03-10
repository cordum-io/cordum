package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ---- isOnErrorTarget unit tests ----

func TestIsOnErrorTarget(t *testing.T) {
	wf := &Workflow{
		Steps: map[string]*Step{
			"main":    {ID: "main", OnError: "handler"},
			"handler": {ID: "handler"},
			"other":   {ID: "other"},
		},
	}
	if !isOnErrorTarget(wf, "handler") {
		t.Fatal("handler should be on_error target")
	}
	if isOnErrorTarget(wf, "main") {
		t.Fatal("main should not be on_error target")
	}
	if isOnErrorTarget(wf, "other") {
		t.Fatal("other should not be on_error target")
	}
	if isOnErrorTarget(nil, "any") {
		t.Fatal("nil workflow should return false")
	}
}

// ---- depsSatisfied with on_error bypass ----

func TestDepsSatisfied_OnErrorBypassesDeps(t *testing.T) {
	step := &Step{ID: "handler", DependsOn: []string{"unreachable"}}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"handler": {
				StepID: "handler",
				Status: StepStatusPending,
				Input:  map[string]any{"error": map[string]any{"step_id": "main"}},
			},
			"unreachable": {StepID: "unreachable", Status: StepStatusFailed},
		},
	}

	if !depsSatisfied(step, run, nil) {
		t.Fatal("on_error step with error input should bypass dependency check")
	}
}

func TestDepsSatisfied_NormalDepsEnforced(t *testing.T) {
	step := &Step{ID: "after", DependsOn: []string{"before"}}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"after":  {StepID: "after", Status: StepStatusPending},
			"before": {StepID: "before", Status: StepStatusRunning},
		},
	}

	if depsSatisfied(step, run, nil) {
		t.Fatal("deps should not be satisfied when dependency is still running")
	}

	// Succeed the dependency
	run.Steps["before"].Status = StepStatusSucceeded
	if !depsSatisfied(step, run, nil) {
		t.Fatal("deps should be satisfied when dependency succeeded")
	}
}

// ---- Crash-recovery: idempotency key format ----

func TestIdempotencyKeyFormat(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-idemp",
		OrgID: "org",
		Steps: map[string]*Step{
			"step1": {ID: "step1", Type: StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         "run-idemp",
		WorkflowID: wf.ID,
		OrgID:      "org",
		TeamID:     "team",
		Input:      map[string]any{},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	msgs := bus.Snapshot()
	var req *pb.JobRequest
	for _, m := range msgs {
		if m.packet.GetJobRequest() != nil {
			req = m.packet.GetJobRequest()
			break
		}
	}
	if req == nil {
		t.Fatal("expected job request")
	}

	// Idempotency key should match pattern wf:{runID}:{stepID}:{attempt}
	expected := fmt.Sprintf("wf:%s:step1:1", run.ID)
	if req.Meta == nil || req.Meta.IdempotencyKey != expected {
		got := ""
		if req.Meta != nil {
			got = req.Meta.IdempotencyKey
		}
		t.Fatalf("expected idempotency key %q, got %q", expected, got)
	}
}

// ---- updateRunStatus on_error awareness ----

func TestUpdateRunStatus_OnErrorAware(t *testing.T) {
	now := time.Now()
	wf := &Workflow{
		Steps: map[string]*Step{
			"main":    {ID: "main", OnError: "handler"},
			"handler": {ID: "handler"},
		},
	}

	// Case 1: main failed, handler pending → run should NOT be failed
	run := &WorkflowRun{
		ID:     "run-1",
		Status: RunStatusRunning,
		Steps: map[string]*StepRun{
			"main":    {StepID: "main", Status: StepStatusFailed},
			"handler": {StepID: "handler", Status: StepStatusPending},
		},
	}
	updateRunStatus(run, wf, now)
	if run.Status == RunStatusFailed {
		t.Fatal("run should not fail while on_error handler is pending")
	}

	// Case 2: main failed, handler succeeded → run should succeed
	run.Steps["handler"] = &StepRun{StepID: "handler", Status: StepStatusSucceeded}
	updateRunStatus(run, wf, now)
	if run.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded when on_error handler succeeds, got: %s", run.Status)
	}
}

// ---- depsSatisfied with failed + on_error succeeded ----

func TestDepsSatisfied_FailedWithOnErrorSucceeded(t *testing.T) {
	// Step C depends on step A. A failed but A's on_error handler B succeeded.
	// depsSatisfied(C) should return true.
	wf := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A", OnError: "B"},
			"B": {ID: "B"},
			"C": {ID: "C", DependsOn: []string{"A"}},
		},
	}
	run := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusFailed},
			"B": {StepID: "B", Status: StepStatusSucceeded},
			"C": {StepID: "C", Status: StepStatusPending},
		},
	}

	stepC := wf.Steps["C"]
	if !depsSatisfied(stepC, run, wf) {
		t.Fatal("C should be satisfiable when dependency A failed but on_error handler B succeeded")
	}

	// If handler B is still running, deps should NOT be satisfied.
	run.Steps["B"].Status = StepStatusRunning
	if depsSatisfied(stepC, run, wf) {
		t.Fatal("C should NOT be satisfiable when on_error handler B is still running")
	}

	// If handler B failed, deps should NOT be satisfied.
	run.Steps["B"].Status = StepStatusFailed
	if depsSatisfied(stepC, run, wf) {
		t.Fatal("C should NOT be satisfiable when on_error handler B also failed")
	}

	// If A failed with no on_error handler, deps should NOT be satisfied.
	wfNoHandler := &Workflow{
		Steps: map[string]*Step{
			"A": {ID: "A"},
			"C": {ID: "C", DependsOn: []string{"A"}},
		},
	}
	run2 := &WorkflowRun{
		Steps: map[string]*StepRun{
			"A": {StepID: "A", Status: StepStatusFailed},
			"C": {StepID: "C", Status: StepStatusPending},
		},
	}
	if depsSatisfied(wfNoHandler.Steps["C"], run2, wfNoHandler) {
		t.Fatal("C should NOT be satisfiable when A failed and has no on_error handler")
	}
}
