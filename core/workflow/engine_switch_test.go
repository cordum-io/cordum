package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSwitchMatchesCase(t *testing.T) {
	store, engine, bus, runID := setupSwitchRun(
		t,
		"wf-switch-match",
		"run-switch-match",
		map[string]any{"route": "beta"},
		[]map[string]any{
			{"match": "alpha", "next": "alpha"},
			{"match": "beta", "next": "beta"},
		},
		"",
		"",
	)
	defer store.Close()

	ensureSwitchBranchDispatched(t, store, engine, bus, runID)

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected 1 switch branch dispatch, got %d", got)
	}
	if req := findPublishedJobRequest(bus, runID+":beta@1"); req == nil {
		t.Fatalf("expected matched branch beta dispatch")
	}

	current := mustGetRun(t, store, runID)
	if current.Status != RunStatusRunning {
		t.Fatalf("expected run running before matched branch result, got %s", current.Status)
	}
	alpha := current.Steps["alpha"]
	if alpha == nil || alpha.Status != StepStatusCancelled {
		t.Fatalf("expected non-matching alpha branch cancelled, got %#v", alpha)
	}
	if reason, _ := alpha.Error["reason"].(string); reason != switchBranchNotTakenReason {
		t.Fatalf("expected alpha cancellation reason %q, got %#v", switchBranchNotTakenReason, alpha.Error)
	}
	route := current.Steps["route"]
	if route == nil || route.Status != StepStatusSucceeded {
		t.Fatalf("expected switch step succeeded, got %#v", route)
	}
	output, ok := route.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected switch output map, got %T", route.Output)
	}
	if got := output["target_step"]; got != "beta" {
		t.Fatalf("expected target_step beta, got %#v", got)
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":beta@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:beta",
	})

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func TestSwitchDefaultBranch(t *testing.T) {
	store, engine, bus, runID := setupSwitchRun(
		t,
		"wf-switch-default",
		"run-switch-default",
		map[string]any{"route": "unknown"},
		[]map[string]any{
			{"match": "alpha", "next": "alpha"},
		},
		"fallback",
		"",
	)
	defer store.Close()

	ensureSwitchBranchDispatched(t, store, engine, bus, runID)

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected 1 default branch dispatch, got %d", got)
	}
	if req := findPublishedJobRequest(bus, runID+":fallback@1"); req == nil {
		t.Fatalf("expected default fallback dispatch")
	}

	current := mustGetRun(t, store, runID)
	alpha := current.Steps["alpha"]
	if alpha == nil || alpha.Status != StepStatusCancelled {
		t.Fatalf("expected non-matching alpha branch cancelled, got %#v", alpha)
	}
	if reason, _ := alpha.Error["reason"].(string); reason != switchBranchNotTakenReason {
		t.Fatalf("expected alpha cancellation reason %q, got %#v", switchBranchNotTakenReason, alpha.Error)
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":fallback@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:fallback",
	})

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func TestSwitchNoMatchNoDefaultFails(t *testing.T) {
	store, _, bus, runID := setupSwitchRun(
		t,
		"wf-switch-fail",
		"run-switch-fail",
		map[string]any{"route": "unknown"},
		[]map[string]any{
			{"match": "alpha", "next": "alpha"},
		},
		"",
		"",
	)
	defer store.Close()

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 0 {
		t.Fatalf("expected no dispatch when no case matches and no default, got %d", got)
	}

	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusFailed {
		t.Fatalf("expected run failed, got %s", final.Status)
	}
	route := final.Steps["route"]
	if route == nil || route.Status != StepStatusFailed {
		t.Fatalf("expected route step failed, got %#v", route)
	}
	msg, _ := route.Error["message"].(string)
	if msg != "no matching case" {
		t.Fatalf("expected no matching case error, got %q", msg)
	}
}

func TestSwitchOutputPath(t *testing.T) {
	store, engine, bus, runID := setupSwitchRun(
		t,
		"wf-switch-output",
		"run-switch-output",
		map[string]any{"route": "alpha"},
		[]map[string]any{
			{"match": "alpha", "next": "alpha"},
		},
		"fallback",
		"ctx.route_decision",
	)
	defer store.Close()

	ensureSwitchBranchDispatched(t, store, engine, bus, runID)

	current := mustGetRun(t, store, runID)
	ctxMap, ok := current.Context["ctx"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx map in run context, got %#v", current.Context["ctx"])
	}
	decision, ok := ctxMap["route_decision"].(map[string]any)
	if !ok {
		t.Fatalf("expected route_decision map, got %#v", ctxMap["route_decision"])
	}
	if got := decision["target_step"]; got != "alpha" {
		t.Fatalf("expected output_path target_step alpha, got %#v", got)
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":alpha@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:alpha",
	})
	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}

func TestSwitchUnmatchedBranchesSkipped(t *testing.T) {
	store, engine, bus, runID := setupSwitchRun(
		t,
		"wf-switch-skipped",
		"run-switch-skipped",
		map[string]any{"route": "alpha"},
		[]map[string]any{
			{"match": "alpha", "next": "alpha"},
			{"match": "beta", "next": "beta"},
		},
		"fallback",
		"",
	)
	defer store.Close()

	ensureSwitchBranchDispatched(t, store, engine, bus, runID)

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected only matched branch dispatch, got %d", got)
	}

	current := mustGetRun(t, store, runID)
	for _, branchID := range []string{"beta", "fallback"} {
		branch := current.Steps[branchID]
		if branch == nil || branch.Status != StepStatusCancelled {
			t.Fatalf("expected branch %s cancelled, got %#v", branchID, branch)
		}
		if reason, _ := branch.Error["reason"].(string); reason != switchBranchNotTakenReason {
			t.Fatalf("expected branch %s reason %q, got %#v", branchID, switchBranchNotTakenReason, branch.Error)
		}
	}
	if current.Status != RunStatusRunning {
		t.Fatalf("expected run still running until matched branch completes, got %s", current.Status)
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     runID + ":alpha@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:alpha",
	})
	final := mustGetRun(t, store, runID)
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded after matched branch completion, got %s", final.Status)
	}
}

func setupSwitchRun(
	t *testing.T,
	workflowID string,
	runID string,
	runInput map[string]any,
	cases []map[string]any,
	defaultStepID string,
	outputPath string,
) (*RedisStore, *Engine, *recordingBus, string) {
	t.Helper()

	store := newWorkflowStore(t)
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	switchInput := map[string]any{
		"cases": toAnySlice(cases),
	}
	if defaultStepID != "" {
		switchInput["default"] = defaultStepID
	}

	steps := map[string]*Step{
		"route": {
			ID:         "route",
			Type:       StepTypeSwitch,
			Condition:  "input.route",
			Input:      switchInput,
			OutputPath: outputPath,
		},
	}

	seenBranches := map[string]struct{}{}
	for _, entry := range cases {
		branchID, _ := entry["next"].(string)
		branchID = strings.TrimSpace(branchID)
		if branchID == "" {
			continue
		}
		if _, exists := seenBranches[branchID]; exists {
			continue
		}
		seenBranches[branchID] = struct{}{}
		steps[branchID] = &Step{
			ID:        branchID,
			Type:      StepTypeWorker,
			Topic:     "job.switch." + branchID,
			DependsOn: []string{"route"},
		}
	}
	if defaultStepID != "" {
		if _, exists := seenBranches[defaultStepID]; !exists {
			seenBranches[defaultStepID] = struct{}{}
			steps[defaultStepID] = &Step{
				ID:        defaultStepID,
				Type:      StepTypeWorker,
				Topic:     "job.switch." + defaultStepID,
				DependsOn: []string{"route"},
			}
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
		Input:      runInput,
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

func toAnySlice(values []map[string]any) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func ensureSwitchBranchDispatched(t *testing.T, store *RedisStore, engine *Engine, bus *recordingBus, runID string) {
	t.Helper()
	if countPublishedSubject(bus, capsdk.SubjectSubmit) > 0 {
		return
	}
	run := mustGetRun(t, store, runID)
	if run.Status != RunStatusRunning {
		return
	}
	if err := engine.StartRun(context.Background(), run.WorkflowID, runID); err != nil {
		t.Fatalf("re-run start for switch dispatch: %v", err)
	}
}

// TestSwitchMapFormatCases verifies that cases can be provided as a map
// (value → step_id) in addition to the array format.
func TestSwitchMapFormatCases(t *testing.T) {
	store := newWorkflowStore(t)
	bus := &recordingBus{}
	engine := NewEngine(store, bus)

	// Cases as map[string]any: match value → target step ID
	switchInput := map[string]any{
		"cases": map[string]any{
			"alpha": "step_alpha",
			"beta":  "step_beta",
		},
	}

	steps := map[string]*Step{
		"route": {
			ID:        "route",
			Type:      StepTypeSwitch,
			Condition: "input.route",
			Input:     switchInput,
		},
		"step_alpha": {
			ID:        "step_alpha",
			Type:      StepTypeWorker,
			Topic:     "job.switch.alpha",
			DependsOn: []string{"route"},
		},
		"step_beta": {
			ID:        "step_beta",
			Type:      StepTypeWorker,
			Topic:     "job.switch.beta",
			DependsOn: []string{"route"},
		},
	}

	wf := &Workflow{ID: "wf-map-cases", OrgID: "org-1", Steps: steps}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-map-cases",
		WorkflowID: "wf-map-cases",
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"route": "beta"},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(context.Background(), "wf-map-cases", "run-map-cases"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer store.Close()

	ensureSwitchBranchDispatched(t, store, engine, bus, "run-map-cases")

	current := mustGetRun(t, store, "run-map-cases")
	routeStep := current.Steps["route"]
	if routeStep == nil || routeStep.Status != StepStatusSucceeded {
		t.Fatalf("expected switch step succeeded, got %#v", routeStep)
	}
	output, ok := routeStep.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected switch output map, got %T", routeStep.Output)
	}
	if got := output["target_step"]; got != "step_beta" {
		t.Fatalf("expected target_step step_beta, got %#v", got)
	}

	alpha := current.Steps["step_alpha"]
	if alpha == nil || alpha.Status != StepStatusCancelled {
		t.Fatalf("expected step_alpha cancelled, got %#v", alpha)
	}

	if got := countPublishedSubject(bus, capsdk.SubjectSubmit); got != 1 {
		t.Fatalf("expected 1 dispatch for matched beta branch, got %d", got)
	}

	engine.HandleJobResult(context.Background(), &pb.JobResult{
		JobId:     "run-map-cases:step_beta@1",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:beta",
	})
	final := mustGetRun(t, store, "run-map-cases")
	if final.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", final.Status)
	}
}
