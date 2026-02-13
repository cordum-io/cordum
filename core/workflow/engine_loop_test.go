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

func TestLoopFixedCountNoCondition(t *testing.T) {
	store, engine, bus, runID := setupLoopRun(
		t,
		"wf-loop-fixed",
		"run-loop-fixed",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 3,
		},
		nil,
	)
	defer store.Close()

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected first loop iteration dispatch, got %d", got)
	}

	for idx := 0; idx < 3; idx++ {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:loop[%d]@1", runID, idx),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: fmt.Sprintf("redis://res:loop[%d]", idx),
		})
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["loop"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected loop parent succeeded, got %#v", parent)
	}
	if got := len(parent.Children); got != 3 {
		t.Fatalf("expected 3 loop children, got %d", got)
	}
	if got := loopIterations(t, parent.Output); got != 3 {
		t.Fatalf("expected iterations=3, got %d", got)
	}
	if got := loopLastOutput(parent.Output); got != "redis://res:loop[2]" {
		t.Fatalf("expected last_output from final child, got %#v", loopLastOutput(parent.Output))
	}
	if !hasTimelineEventForRun(t, store, runID, "step_loop_iteration") {
		t.Fatalf("expected step_loop_iteration timeline event")
	}
	if !hasTimelineEventForRun(t, store, runID, "step_loop_completed") {
		t.Fatalf("expected step_loop_completed timeline event")
	}
}

func TestLoopUntilStopsAtFiveIterations(t *testing.T) {
	store, engine, _, runID := setupLoopRun(
		t,
		"wf-loop-until",
		"run-loop-until",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 1000,
			"until":          "loop.index >= 5",
		},
		nil,
	)
	defer store.Close()

	for idx := 0; idx < 10; idx++ {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:loop[%d]@1", runID, idx),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: fmt.Sprintf("redis://res:loop[%d]", idx),
		})
		if run := mustGetRun(t, store, runID); run.Status == RunStatusSucceeded {
			break
		}
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["loop"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected loop parent succeeded, got %#v", parent)
	}
	if got := len(parent.Children); got != 5 {
		t.Fatalf("expected 5 loop children, got %d", got)
	}
	if got := loopIterations(t, parent.Output); got != 5 {
		t.Fatalf("expected iterations=5, got %d", got)
	}
}

func TestLoopMaxExceededFails(t *testing.T) {
	store, engine, _, runID := setupLoopRun(
		t,
		"wf-loop-max",
		"run-loop-max",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 3,
			"condition":      "loop.index >= 0",
		},
		nil,
	)
	defer store.Close()

	for idx := 0; idx < 3; idx++ {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:loop[%d]@1", runID, idx),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: fmt.Sprintf("redis://res:loop[%d]", idx),
		})
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	parent := final.Steps["loop"]
	if parent == nil || parent.Status != StepStatusFailed {
		t.Fatalf("expected loop parent failed, got %#v", parent)
	}
	if parent.Error == nil {
		t.Fatalf("expected loop parent error")
	}
	msg, _ := parent.Error["message"].(string)
	if !strings.Contains(msg, "max_iterations exceeded") {
		t.Fatalf("expected max_iterations exceeded error, got %q", msg)
	}
}

func TestLoopWhileConditionRunsFiveIterations(t *testing.T) {
	store, engine, _, runID := setupLoopRun(
		t,
		"wf-loop-while",
		"run-loop-while",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 100,
			"condition":      "loop.index < 5",
		},
		nil,
	)
	defer store.Close()

	for idx := 0; idx < 10; idx++ {
		engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:     fmt.Sprintf("%s:loop[%d]@1", runID, idx),
			Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: fmt.Sprintf("redis://res:loop[%d]", idx),
		})
		if run := mustGetRun(t, store, runID); run.Status == RunStatusSucceeded {
			break
		}
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["loop"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected loop parent succeeded, got %#v", parent)
	}
	if got := len(parent.Children); got != 5 {
		t.Fatalf("expected 5 loop children, got %d", got)
	}
	if got := loopIterations(t, parent.Output); got != 5 {
		t.Fatalf("expected iterations=5, got %d", got)
	}
}

func TestLoopZeroIterationsWhenConditionFalseInitially(t *testing.T) {
	store, _, bus, runID := setupLoopRun(
		t,
		"wf-loop-zero",
		"run-loop-zero",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 10,
			"condition":      "loop.index < 0",
		},
		nil,
	)
	defer store.Close()

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 0 {
		t.Fatalf("expected no loop dispatches when initial condition is false, got %d", got)
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
	parent := final.Steps["loop"]
	if parent == nil || parent.Status != StepStatusSucceeded {
		t.Fatalf("expected loop parent succeeded, got %#v", parent)
	}
	if got := len(parent.Children); got != 0 {
		t.Fatalf("expected 0 loop children, got %d", got)
	}
	if got := loopIterations(t, parent.Output); got != 0 {
		t.Fatalf("expected iterations=0, got %d", got)
	}
}

func TestLoopScopeVariablesInChildPayload(t *testing.T) {
	store, engine, bus, runID := setupLoopRun(
		t,
		"wf-loop-scope",
		"run-loop-scope",
		map[string]any{
			"body_step":      "body",
			"max_iterations": 2,
		},
		map[string]any{
			"index":     "${loop.index}",
			"iteration": "${loop.iteration}",
			"prev":      "${loop.previous_output}",
		},
	)
	defer store.Close()

	initial := mustGetRun(t, store, runID)
	firstPayload := childPayload(t, initial, "loop[0]")
	if got := asInt(t, firstPayload["index"]); got != 0 {
		t.Fatalf("expected first payload loop.index=0, got %d", got)
	}
	if got := asInt(t, firstPayload["iteration"]); got != 1 {
		t.Fatalf("expected first payload loop.iteration=1, got %d", got)
	}
	if firstPayload["prev"] != nil {
		t.Fatalf("expected first payload loop.previous_output=nil, got %#v", firstPayload["prev"])
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":loop[0]@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:loop[0]",
	})

	second := mustGetRun(t, store, runID)
	secondPayload := childPayload(t, second, "loop[1]")
	if got := asInt(t, secondPayload["index"]); got != 1 {
		t.Fatalf("expected second payload loop.index=1, got %d", got)
	}
	if got := asInt(t, secondPayload["iteration"]); got != 2 {
		t.Fatalf("expected second payload loop.iteration=2, got %d", got)
	}
	if got := secondPayload["prev"]; got != "redis://res:loop[0]" {
		t.Fatalf("expected second payload loop.previous_output from prior result ptr, got %#v", got)
	}

	req := findPublishedJobRequest(bus, runID+":loop[1]@1")
	if req == nil {
		t.Fatalf("expected published request for loop[1]")
	}
	if req.Env["loop_index"] != "1" {
		t.Fatalf("expected loop_index env=1, got %q", req.Env["loop_index"])
	}
	if req.Env["loop_iteration"] != "2" {
		t.Fatalf("expected loop_iteration env=2, got %q", req.Env["loop_iteration"])
	}
	if req.Env["loop_previous_output"] != "\"redis://res:loop[0]\"" {
		t.Fatalf("expected loop_previous_output env to include previous ptr, got %q", req.Env["loop_previous_output"])
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":loop[1]@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:loop[1]",
	})
	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func setupLoopRun(
	t *testing.T,
	workflowID string,
	runID string,
	loopInput map[string]any,
	bodyInput map[string]any,
) (*RedisStore, *Engine, *recordingBus, string) {
	t.Helper()

	store := newWorkflowStore(t)
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	steps := map[string]*Step{
		"loop": {
			ID:    "loop",
			Type:  StepTypeLoop,
			Input: map[string]any{},
		},
		"body": {
			ID:    "body",
			Type:  StepTypeWorker,
			Topic: "job.loop.body",
			Input: map[string]any{},
		},
	}
	for key, value := range loopInput {
		steps["loop"].Input[key] = value
	}
	if _, ok := steps["loop"].Input["body_step"]; !ok {
		steps["loop"].Input["body_step"] = "body"
	}
	for key, value := range bodyInput {
		steps["body"].Input[key] = value
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

func mustGetRun(t *testing.T, store *RedisStore, runID string) *WorkflowRun {
	t.Helper()
	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return run
}

func loopIterations(t *testing.T, output any) int {
	t.Helper()
	m, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("expected loop output map, got %T", output)
	}
	return asInt(t, m["iterations"])
}

func loopLastOutput(output any) any {
	m, ok := output.(map[string]any)
	if !ok {
		return nil
	}
	return m["last_output"]
}

func asInt(t *testing.T, value any) int {
	t.Helper()
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		t.Fatalf("expected numeric value, got %T (%#v)", value, value)
	}
	return 0
}

func childPayload(t *testing.T, run *WorkflowRun, childID string) map[string]any {
	t.Helper()
	if run == nil {
		t.Fatalf("run required")
	}
	child := run.Steps[childID]
	if child == nil {
		t.Fatalf("missing child %s", childID)
	}
	if child.Input == nil {
		t.Fatalf("expected child input map for %s, got nil", childID)
	}
	return child.Input
}

func findPublishedJobRequest(bus *recordingBus, jobID string) *pb.JobRequest {
	if bus == nil {
		return nil
	}
	for _, msg := range bus.Snapshot() {
		if msg.subject != capsdk.SubjectSubmit || msg.packet == nil {
			continue
		}
		req := msg.packet.GetJobRequest()
		if req == nil {
			continue
		}
		if req.JobId == jobID {
			return req
		}
	}
	return nil
}
