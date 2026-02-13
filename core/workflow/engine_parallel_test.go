package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestParallelAllSucceed(t *testing.T) {
	childIDs := []string{"check_a", "check_b", "check_c"}
	store, engine, bus, runID := setupParallelRun(t, "wf-parallel-all", "run-parallel-all", childIDs, "all", 0, 0)
	defer store.Close()

	if countPublishedSubject(bus, capsdk.SubjectSubmit) != 3 {
		t.Fatalf("expected 3 child dispatches, got %d", countPublishedSubject(bus, capsdk.SubjectSubmit))
	}

	for _, childID := range childIDs {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:%s@1", runID, childID),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: "redis://res:" + childID,
		})
	}

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["parallel"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected parent step succeeded, got %#v", parent)
	}
	if parent.Output == nil {
		t.Fatalf("expected aggregated parent output")
	}
	if !hasTimelineEventForRun(t, store, runID, "step_parallel_completed") {
		t.Fatalf("expected step_parallel_completed timeline event")
	}
}

func TestParallelOneFailsStrategyAll(t *testing.T) {
	childIDs := []string{"check_a", "check_b", "check_c"}
	store, engine, _, runID := setupParallelRun(t, "wf-parallel-fail", "run-parallel-fail", childIDs, "all", 0, 0)
	defer store.Close()

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "check_a"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:check_a",
	})
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:  fmt.Sprintf("%s:%s@1", runID, "check_b"),
		Status: pb.JobStatus_JOB_STATUS_FAILED,
	})

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	parent := final.Steps["parallel"]
	if parent == nil || parent.Status != StepStatusFailed {
		t.Fatalf("expected parent step failed, got %#v", parent)
	}
	if parent.Error == nil {
		t.Fatalf("expected parent step error")
	}
}

func TestParallelAnyStrategy(t *testing.T) {
	childIDs := []string{"check_a", "check_b", "check_c"}
	store, engine, _, runID := setupParallelRun(t, "wf-parallel-any", "run-parallel-any", childIDs, "any", 0, 0)
	defer store.Close()

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "check_a"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:check_a",
	})

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["parallel"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected parent step succeeded, got %#v", parent)
	}
	if final.Steps["check_b"] == nil || final.Steps["check_b"].Status != StepStatusCancelled {
		t.Fatalf("expected check_b cancelled, got %#v", final.Steps["check_b"])
	}
	if final.Steps["check_c"] == nil || final.Steps["check_c"].Status != StepStatusCancelled {
		t.Fatalf("expected check_c cancelled, got %#v", final.Steps["check_c"])
	}
}

func TestParallelNOfM(t *testing.T) {
	childIDs := []string{"check_1", "check_2", "check_3", "check_4", "check_5"}
	store, engine, _, runID := setupParallelRun(t, "wf-parallel-nofm", "run-parallel-nofm", childIDs, "n_of_m", 3, 0)
	defer store.Close()

	for _, childID := range []string{"check_1", "check_2", "check_3"} {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:%s@1", runID, childID),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: "redis://res:" + childID,
		})
	}

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["parallel"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected parent step succeeded, got %#v", parent)
	}
	if final.Steps["check_4"] == nil || final.Steps["check_4"].Status != StepStatusCancelled {
		t.Fatalf("expected check_4 cancelled, got %#v", final.Steps["check_4"])
	}
	if final.Steps["check_5"] == nil || final.Steps["check_5"].Status != StepStatusCancelled {
		t.Fatalf("expected check_5 cancelled, got %#v", final.Steps["check_5"])
	}
}

func TestParallelMaxParallel(t *testing.T) {
	childIDs := []string{"check_a", "check_b", "check_c"}
	store, engine, bus, runID := setupParallelRun(t, "wf-parallel-throttle", "run-parallel-throttle", childIDs, "all", 0, 1)
	defer store.Close()

	if countPublishedSubject(bus, capsdk.SubjectSubmit) != 1 {
		t.Fatalf("expected 1 initial dispatch with max_parallel=1, got %d", countPublishedSubject(bus, capsdk.SubjectSubmit))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "check_a"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:check_a",
	})
	if countPublishedSubject(bus, capsdk.SubjectSubmit) != 2 {
		t.Fatalf("expected second child dispatch after first completion, got %d", countPublishedSubject(bus, capsdk.SubjectSubmit))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "check_b"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:check_b",
	})
	if countPublishedSubject(bus, capsdk.SubjectSubmit) != 3 {
		t.Fatalf("expected third child dispatch after second completion, got %d", countPublishedSubject(bus, capsdk.SubjectSubmit))
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "check_c"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:check_c",
	})

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func TestParallelOutputAggregation(t *testing.T) {
	childIDs := []string{"alpha", "beta"}
	store, engine, _, runID := setupParallelRun(t, "wf-parallel-output", "run-parallel-output", childIDs, "all", 0, 0)
	defer store.Close()

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "alpha"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:alpha",
	})
	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     fmt.Sprintf("%s:%s@1", runID, "beta"),
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:beta",
	})

	final, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	parent := final.Steps["parallel"]
	if parent == nil {
		t.Fatalf("expected parent step run")
	}
	aggregated, ok := parent.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected parent output map, got %T", parent.Output)
	}
	alphaEntry, ok := aggregated["alpha"].(map[string]any)
	if !ok {
		t.Fatalf("expected alpha aggregation entry, got %#v", aggregated["alpha"])
	}
	betaEntry, ok := aggregated["beta"].(map[string]any)
	if !ok {
		t.Fatalf("expected beta aggregation entry, got %#v", aggregated["beta"])
	}
	if alphaEntry["result_ptr"] != "redis://res:alpha" {
		t.Fatalf("expected alpha result_ptr, got %#v", alphaEntry["result_ptr"])
	}
	if betaEntry["result_ptr"] != "redis://res:beta" {
		t.Fatalf("expected beta result_ptr, got %#v", betaEntry["result_ptr"])
	}
}

func setupParallelRun(
	t *testing.T,
	workflowID string,
	runID string,
	childIDs []string,
	strategy string,
	required int,
	maxParallel int,
) (*RedisStore, *Engine, *recordingBus, string) {
	t.Helper()

	store := newWorkflowStore(t)
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	steps := map[string]*Step{
		"parallel": {
			ID:          "parallel",
			Type:        StepTypeParallel,
			MaxParallel: maxParallel,
			Input: map[string]any{
				"steps": childIDs,
			},
		},
	}
	if strategy != "" {
		steps["parallel"].Input["strategy"] = strategy
	}
	if strategy == "n_of_m" && required > 0 {
		steps["parallel"].Input["required"] = required
	}
	for _, childID := range childIDs {
		steps[childID] = &Step{
			ID:    childID,
			Type:  StepTypeWorker,
			Topic: "job.parallel." + childID,
		}
	}

	wf := &Workflow{
		ID:    workflowID,
		OrgID: "org-1",
		Steps: steps,
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: workflowID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.StartRun(context.Background(), workflowID, runID); err != nil {
		t.Fatalf("start run: %v", err)
	}
	return store, engine, bus, runID
}

func countPublishedSubject(bus *recordingBus, subject string) int {
	total := 0
	for _, msg := range bus.Snapshot() {
		if msg.subject == subject {
			total++
		}
	}
	return total
}

func hasTimelineEventForRun(t *testing.T, store *RedisStore, runID, eventType string) bool {
	t.Helper()
	events, err := store.ListTimelineEvents(context.Background(), runID, 50)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	for _, evt := range events {
		if evt.Type == eventType {
			return true
		}
	}
	return false
}
