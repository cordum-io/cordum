package workflow

import (
	"context"
	"testing"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

func TestTemplateEvaluationInStepInput(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-input",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"step": {
				ID:    "step",
				Type:  StepTypeWorker,
				Topic: "job.default",
				Input: map[string]any{
					"foo": "${input.foo}",
					"msg": "hello ${input.foo}",
				},
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-input",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"foo": "bar"},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	stored, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	sr := stored.Steps["step"]
	if sr == nil || sr.Input == nil {
		t.Fatalf("missing step input in run state")
	}
	if sr.Input["foo"] != "bar" {
		t.Fatalf("expected foo=bar got %v", sr.Input["foo"])
	}
	if sr.Input["msg"] != "hello bar" {
		t.Fatalf("expected msg interpolated, got %v", sr.Input["msg"])
	}
}

func TestForEachMaxParallelLimitsDispatch(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	wf := &Workflow{
		ID:    "wf-par",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"fan": {
				ID:          "fan",
				Type:        StepTypeWorker,
				Topic:       "job.default",
				ForEach:     "input.items",
				MaxParallel: 1,
			},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-par",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		Input:      map[string]any{"items": []any{"a", "b", "c"}},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), wf.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 publish due to max_parallel, got %d", len(bus.published))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-par:fan[0]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if len(bus.published) != 2 {
		t.Fatalf("expected 2 publishes after first completion, got %d", len(bus.published))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  "run-par:fan[1]@1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if len(bus.published) != 3 {
		t.Fatalf("expected 3 publishes after second completion, got %d", len(bus.published))
	}
}
