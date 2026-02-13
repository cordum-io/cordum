package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSubWorkflowSucceeds(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	saveWorkflowDef(t, store, buildChildWorkflow("wf-child-success"))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-parent-success", "wf-child-success", nil))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-parent-success",
		WorkflowID: "wf-parent-success",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-parent-success", "run-parent-success"); err != nil {
		t.Fatalf("start parent run: %v", err)
	}
	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected 1 child dispatch, got %d", got)
	}

	parent := mustGetRun(t, store, "run-parent-success")
	invoke := parent.Steps["invoke"]
	if invoke == nil || invoke.Status != StepStatusRunning {
		t.Fatalf("expected invoke step running, got %#v", invoke)
	}
	childRunID := strings.TrimSpace(invoke.JobID)
	if childRunID == "" {
		t.Fatalf("expected child run id on invoke step")
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:child_step@1", childRunID),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:child-success",
	})
	if err := engine.StartRun(context.Background(), "wf-parent-success", "run-parent-success"); err != nil {
		t.Fatalf("poll parent run: %v", err)
	}

	final := mustGetRun(t, store, "run-parent-success")
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected parent run succeeded, got %s", final.Status)
	}
	invoke = final.Steps["invoke"]
	if invoke == nil || invoke.Status != StepStatusSucceeded {
		t.Fatalf("expected invoke step succeeded, got %#v", invoke)
	}
	output, ok := invoke.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected invoke output map, got %T", invoke.Output)
	}
	if output["child_run_id"] != childRunID {
		t.Fatalf("expected child_run_id=%s, got %#v", childRunID, output["child_run_id"])
	}
	if output["child_workflow_id"] != "wf-child-success" {
		t.Fatalf("expected child_workflow_id wf-child-success, got %#v", output["child_workflow_id"])
	}
}

func TestSubWorkflowFailsWhenChildFails(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})

	saveWorkflowDef(t, store, buildChildWorkflow("wf-child-fail"))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-parent-fail", "wf-child-fail", nil))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-parent-fail",
		WorkflowID: "wf-parent-fail",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-parent-fail", "run-parent-fail"); err != nil {
		t.Fatalf("start parent run: %v", err)
	}
	parent := mustGetRun(t, store, "run-parent-fail")
	childRunID := parent.Steps["invoke"].JobID

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:        fmt.Sprintf("%s:child_step@1", childRunID),
		Status:       pb.JobStatus_JOB_STATUS_FAILED,
		ErrorMessage: "child exploded",
	})
	if err := engine.StartRun(context.Background(), "wf-parent-fail", "run-parent-fail"); err != nil {
		t.Fatalf("poll parent run: %v", err)
	}

	final := mustGetRun(t, store, "run-parent-fail")
	if final.Status != RunStatusFailed {
		t.Fatalf("expected parent run failed, got %s", final.Status)
	}
	invoke := final.Steps["invoke"]
	if invoke == nil || invoke.Status != StepStatusFailed {
		t.Fatalf("expected invoke failed, got %#v", invoke)
	}
	if invoke.Error == nil {
		t.Fatalf("expected invoke error")
	}
	msg, _ := invoke.Error["message"].(string)
	if !strings.Contains(msg, "child exploded") {
		t.Fatalf("expected propagated child error, got %q", msg)
	}
}

func TestSubWorkflowInputMapping(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})

	saveWorkflowDef(t, store, buildChildWorkflow("wf-child-input-map"))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-parent-input-map", "wf-child-input-map", map[string]any{
		"input_mapping": map[string]any{
			"case_id": "${input.ticket_id}",
			"urgent":  "${input.priority == 'high'}",
		},
	}))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-parent-input-map",
		WorkflowID: "wf-parent-input-map",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input: map[string]any{
			"ticket_id": "TCK-42",
			"priority":  "high",
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-parent-input-map", "run-parent-input-map"); err != nil {
		t.Fatalf("start parent run: %v", err)
	}
	parent := mustGetRun(t, store, "run-parent-input-map")
	childRun := mustGetRun(t, store, parent.Steps["invoke"].JobID)

	if childRun.Input["case_id"] != "TCK-42" {
		t.Fatalf("expected mapped case_id, got %#v", childRun.Input["case_id"])
	}
	if urgent, ok := childRun.Input["urgent"].(bool); !ok || !urgent {
		t.Fatalf("expected mapped urgent=true, got %#v", childRun.Input["urgent"])
	}
	if len(childRun.Input) != 2 {
		t.Fatalf("expected only mapped keys in child input, got %#v", childRun.Input)
	}
}

func TestSubWorkflowOutputMapping(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})

	saveWorkflowDef(t, store, buildChildWorkflow("wf-child-output-map"))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-parent-output-map", "wf-child-output-map", map[string]any{
		"output_mapping": map[string]any{
			"ticket":       "${input.ticket_id}",
			"result_ptr":   "${child.steps.child_step.result_ptr}",
			"child_status": "${child.status}",
		},
	}))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-parent-output-map",
		WorkflowID: "wf-parent-output-map",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input: map[string]any{
			"ticket_id": "TCK-007",
		},
		Status:    RunStatusPending,
		Steps:     map[string]*StepRun{},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-parent-output-map", "run-parent-output-map"); err != nil {
		t.Fatalf("start parent run: %v", err)
	}
	parent := mustGetRun(t, store, "run-parent-output-map")
	childRunID := parent.Steps["invoke"].JobID

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:child_step@1", childRunID),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:child-output-map",
	})
	if err := engine.StartRun(context.Background(), "wf-parent-output-map", "run-parent-output-map"); err != nil {
		t.Fatalf("poll parent run: %v", err)
	}

	final := mustGetRun(t, store, "run-parent-output-map")
	invoke := final.Steps["invoke"]
	if invoke == nil || invoke.Status != StepStatusSucceeded {
		t.Fatalf("expected invoke succeeded, got %#v", invoke)
	}
	output, ok := invoke.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected invoke output map, got %T", invoke.Output)
	}
	if output["ticket"] != "TCK-007" {
		t.Fatalf("expected mapped ticket, got %#v", output["ticket"])
	}
	if output["result_ptr"] != "redis://res:child-output-map" {
		t.Fatalf("expected mapped result pointer, got %#v", output["result_ptr"])
	}
	if output["child_status"] != "succeeded" {
		t.Fatalf("expected mapped child status, got %#v", output["child_status"])
	}
}

func TestSubWorkflowCircularDetected(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})

	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-a", "wf-b", nil))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-b", "wf-a", nil))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-a",
		WorkflowID: "wf-a",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-a", "run-a"); err != nil {
		t.Fatalf("start run a: %v", err)
	}
	aRun := mustGetRun(t, store, "run-a")
	bRunID := aRun.Steps["invoke"].JobID
	if strings.TrimSpace(bRunID) == "" {
		t.Fatalf("expected child run id for wf-b")
	}

	bRun := mustGetRun(t, store, bRunID)
	if bRun.Status != RunStatusFailed {
		t.Fatalf("expected wf-b child run failed due cycle, got %s", bRun.Status)
	}
	bInvoke := bRun.Steps["invoke"]
	if bInvoke == nil || bInvoke.Status != StepStatusFailed {
		t.Fatalf("expected wf-b invoke failed, got %#v", bInvoke)
	}
	msg, _ := bInvoke.Error["message"].(string)
	if !strings.Contains(msg, "circular workflow reference detected") {
		t.Fatalf("expected circular reference message, got %q", msg)
	}

	if err := engine.StartRun(context.Background(), "wf-a", "run-a"); err != nil {
		t.Fatalf("poll run a: %v", err)
	}
	aFinal := mustGetRun(t, store, "run-a")
	if aFinal.Status != RunStatusFailed {
		t.Fatalf("expected wf-a run failed after child cycle failure, got %s", aFinal.Status)
	}
}

func TestSubWorkflowContextInheritanceAndCallStack(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})

	saveWorkflowDef(t, store, buildChildWorkflow("wf-child-context"))
	saveWorkflowDef(t, store, buildParentSubWorkflow("wf-parent-context", "wf-child-context", nil))
	createRunDef(t, store, &WorkflowRun{
		ID:         "run-parent-context",
		WorkflowID: "wf-parent-context",
		OrgID:      "org-acme",
		TeamID:     "team-risk",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		Metadata: map[string]string{
			"call_stack": "bootstrap",
		},
		DryRun:    true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	if err := engine.StartRun(context.Background(), "wf-parent-context", "run-parent-context"); err != nil {
		t.Fatalf("start parent run: %v", err)
	}
	parent := mustGetRun(t, store, "run-parent-context")
	childRun := mustGetRun(t, store, parent.Steps["invoke"].JobID)

	if childRun.OrgID != "org-acme" {
		t.Fatalf("expected org inheritance, got %s", childRun.OrgID)
	}
	if childRun.TeamID != "team-risk" {
		t.Fatalf("expected team inheritance, got %s", childRun.TeamID)
	}
	if !childRun.DryRun {
		t.Fatalf("expected child dry_run inherited")
	}
	if childRun.Metadata["parent_run_id"] != "run-parent-context" {
		t.Fatalf("expected parent_run_id metadata, got %#v", childRun.Metadata["parent_run_id"])
	}
	if childRun.Metadata["parent_step_id"] != "invoke" {
		t.Fatalf("expected parent_step_id metadata, got %#v", childRun.Metadata["parent_step_id"])
	}
	if childRun.Metadata["dry_run"] != "true" {
		t.Fatalf("expected dry_run metadata, got %#v", childRun.Metadata["dry_run"])
	}
	if childRun.Metadata["call_stack"] != "bootstrap>wf-parent-context>wf-child-context" {
		t.Fatalf("unexpected call_stack metadata: %#v", childRun.Metadata["call_stack"])
	}
}

func buildChildWorkflow(id string) *Workflow {
	return &Workflow{
		ID:    id,
		OrgID: "org-1",
		Steps: map[string]*Step{
			"child_step": {
				ID:    "child_step",
				Type:  StepTypeWorker,
				Topic: "job.child.step",
			},
		},
	}
}

func buildParentSubWorkflow(id, childWorkflowID string, overrides map[string]any) *Workflow {
	input := map[string]any{
		"workflow_id": childWorkflowID,
	}
	for key, value := range overrides {
		input[key] = value
	}
	return &Workflow{
		ID:    id,
		OrgID: "org-1",
		Steps: map[string]*Step{
			"invoke": {
				ID:    "invoke",
				Type:  StepTypeSubWorkflow,
				Input: input,
			},
		},
	}
}

func saveWorkflowDef(t *testing.T, store *RedisStore, wf *Workflow) {
	t.Helper()
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow %s: %v", wf.ID, err)
	}
}

func createRunDef(t *testing.T, store *RedisStore, run *WorkflowRun) {
	t.Helper()
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run %s: %v", run.ID, err)
	}
}
