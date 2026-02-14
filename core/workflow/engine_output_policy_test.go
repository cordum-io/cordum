package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type stubOutputSafety struct {
	metaRecord scheduler.OutputSafetyRecord
	metaErr    error
}

func (s *stubOutputSafety) EvaluateOutput(_ context.Context, _ *scheduler.OutputEvaluateRequest) (scheduler.OutputSafetyRecord, error) {
	return scheduler.OutputSafetyRecord{}, nil
}

func (s *stubOutputSafety) CheckOutputMeta(_ *pb.JobResult, _ *pb.JobRequest) (scheduler.OutputSafetyRecord, error) {
	if s.metaErr != nil {
		return scheduler.OutputSafetyRecord{}, s.metaErr
	}
	return s.metaRecord, nil
}

func (s *stubOutputSafety) CheckOutputContent(_ context.Context, _ *pb.JobResult, _ *pb.JobRequest) (scheduler.OutputSafetyRecord, error) {
	return scheduler.OutputSafetyRecord{}, nil
}

func TestStepOutputPolicyQuarantineBlocksPropagation(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}

	checker := &stubOutputSafety{
		metaRecord: scheduler.OutputSafetyRecord{
			Decision: scheduler.OutputQuarantine,
			Reason:   "secret detected in step output",
		},
	}

	engine := NewEngine(store, bus).WithOutputSafety(checker)

	workflowID := "wf-output-policy"
	wf := &Workflow{
		ID:   workflowID,
		Name: "output-policy-test",
		Steps: map[string]*Step{
			"step-a": {ID: "step-a", Type: StepTypeWorker, Topic: "job.test"},
			"step-b": {ID: "step-b", Type: StepTypeWorker, Topic: "job.test", DependsOn: []string{"step-a"}},
		},
	}
	ctx := context.Background()
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	runID := "run-output-q"
	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-a": {StepID: "step-a", Status: StepStatusRunning, JobID: runID + ":step-a"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:     runID + ":step-a",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:step-a-output",
	})

	updated, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	stepA := updated.Steps["step-a"]
	if stepA == nil {
		t.Fatal("step-a missing from run")
	}
	if stepA.Status != StepStatusFailed {
		t.Fatalf("expected step-a failed due to quarantine, got %s", stepA.Status)
	}
	if code, _ := stepA.Error["code"].(string); code != "output_quarantined" {
		t.Fatalf("expected error code output_quarantined, got %v", stepA.Error)
	}
}

func TestStepOutputPolicyAllowPassesThrough(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}

	checker := &stubOutputSafety{
		metaRecord: scheduler.OutputSafetyRecord{Decision: scheduler.OutputAllow, Reason: "ok"},
	}

	engine := NewEngine(store, bus).WithOutputSafety(checker)

	workflowID := "wf-allow-policy"
	wf := &Workflow{
		ID:   workflowID,
		Name: "allow-policy-test",
		Steps: map[string]*Step{
			"step-a": {ID: "step-a", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	ctx := context.Background()
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	runID := "run-allow-out"
	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-a": {StepID: "step-a", Status: StepStatusRunning, JobID: runID + ":step-a"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:     runID + ":step-a",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:step-a-clean",
	})

	updated, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	stepA := updated.Steps["step-a"]
	if stepA == nil {
		t.Fatal("step-a missing from run")
	}
	if stepA.Status != StepStatusSucceeded {
		t.Fatalf("expected step-a succeeded, got %s", stepA.Status)
	}
}

func TestStepOutputPolicyNilCheckerSkips(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}

	// No output safety checker — should skip check entirely
	engine := NewEngine(store, bus)

	workflowID := "wf-nil-checker"
	wf := &Workflow{
		ID:   workflowID,
		Name: "nil-checker-test",
		Steps: map[string]*Step{
			"step-a": {ID: "step-a", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	ctx := context.Background()
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	runID := "run-nil-check"
	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-a": {StepID: "step-a", Status: StepStatusRunning, JobID: runID + ":step-a"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:     runID + ":step-a",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:step-a-nil",
	})

	updated, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	stepA := updated.Steps["step-a"]
	if stepA == nil {
		t.Fatal("step-a missing from run")
	}
	if stepA.Status != StepStatusSucceeded {
		t.Fatalf("expected step-a succeeded with nil checker, got %s", stepA.Status)
	}
}

func TestStepOutputPolicyRedactSubstitutesPtr(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()
	bus := &recordingBus{}

	checker := &stubOutputSafety{
		metaRecord: scheduler.OutputSafetyRecord{
			Decision:    scheduler.OutputRedact,
			Reason:      "PII found, redacting",
			RedactedPtr: "redis://res:step-a-redacted",
		},
	}

	engine := NewEngine(store, bus).WithOutputSafety(checker)

	workflowID := "wf-redact-policy"
	wf := &Workflow{
		ID:   workflowID,
		Name: "redact-policy-test",
		Steps: map[string]*Step{
			"step-a": {ID: "step-a", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	ctx := context.Background()
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	runID := "run-redact-out"
	now := time.Now().UTC()
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step-a": {StepID: "step-a", Status: StepStatusRunning, JobID: runID + ":step-a"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	engine.HandleJobResult(ctx, &pb.JobResult{
		JobId:     runID + ":step-a",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:step-a-original",
	})

	updated, err := store.GetRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	stepA := updated.Steps["step-a"]
	if stepA == nil {
		t.Fatal("step-a missing from run")
	}
	// Step should still succeed — redact doesn't block, it substitutes
	if stepA.Status != StepStatusSucceeded {
		t.Fatalf("expected step-a succeeded after redact, got %s", stepA.Status)
	}
}
