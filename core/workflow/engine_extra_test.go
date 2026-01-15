package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/memory"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

func TestRerunFromCopiesDependencies(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	engine := NewEngine(store, &recordingBus{})
	wfDef := &Workflow{
		ID:    "wf-rerun",
		OrgID: "org",
		Steps: map[string]*Step{
			"step1": {ID: "step1", Type: StepTypeWorker, Topic: "job.default"},
			"step2": {ID: "step2", Type: StepTypeWorker, Topic: "job.default", DependsOn: []string{"step1"}},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-old",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Steps: map[string]*StepRun{
			"step1": {StepID: "step1", Status: StepStatusSucceeded, Output: "res"},
			"step2": {StepID: "step2", Status: StepStatusSucceeded},
		},
		Context: map[string]any{
			"steps": map[string]any{
				"step1": map[string]any{"output": "ok"},
				"step2": map[string]any{"output": "skip"},
			},
		},
		Status:    RunStatusSucceeded,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	newID, err := engine.RerunFrom(context.Background(), run.ID, "step2", true)
	if err != nil {
		t.Fatalf("rerun from: %v", err)
	}
	newRun, err := store.GetRun(context.Background(), newID)
	if err != nil {
		t.Fatalf("get new run: %v", err)
	}
	if newRun.Metadata["rerun_of"] != run.ID || newRun.Metadata["rerun_step"] != "step2" {
		t.Fatalf("expected rerun metadata")
	}
	if newRun.Labels["dry_run"] != "true" || newRun.Metadata["dry_run"] != "true" {
		t.Fatalf("expected dry run flags")
	}
	steps, _ := newRun.Context["steps"].(map[string]any)
	if _, ok := steps["step1"]; !ok || len(steps) != 1 {
		t.Fatalf("expected context limited to deps")
	}
}

func TestCancelRunPublishesCancels(t *testing.T) {
	store := newWorkflowStore(t)
	defer store.Close()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	wfDef := &Workflow{ID: "wf-cancel", OrgID: "org", Steps: map[string]*Step{
		"step": {ID: "step", Type: StepTypeWorker, Topic: "job.default"},
	}}
	if err := store.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-cancel",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: "job-1", Children: map[string]*StepRun{
				"step[0]": {StepID: "step[0]", Status: StepStatusRunning, JobID: "job-2"},
			}},
		},
		Status:    RunStatusRunning,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := engine.CancelRun(context.Background(), run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	updated, _ := store.GetRun(context.Background(), run.ID)
	if updated.Status != RunStatusCancelled {
		t.Fatalf("expected run cancelled, got %s", updated.Status)
	}

	count := 0
	for _, msg := range bus.Snapshot() {
		if msg.subject == capsdk.SubjectCancel {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 cancel publishes, got %d", count)
	}
}

func TestEvalForEachVariants(t *testing.T) {
	scope := map[string]any{"input": map[string]any{"items": []any{"a", "b"}}}
	items, err := evalForEach("input.items", scope)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected items array")
	}
	_, err = evalForEach("input.missing", scope)
	if err != nil {
		t.Fatalf("missing should return empty slice: %v", err)
	}
	_, err = evalForEach("input", scope)
	if err == nil {
		t.Fatalf("expected error for non-array")
	}
}

func TestPutJobContextAndDelay(t *testing.T) {
	memStore, srv := newMemoryStore(t)
	defer srv.Close()
	defer memStore.Close()

	engine := (&Engine{}).WithMemory(memStore)
	ptr, err := engine.putJobContext(context.Background(), "job-ctx", map[string]any{"k": "v"})
	if err != nil || ptr == "" {
		t.Fatalf("expected context pointer")
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		t.Fatalf("parse pointer: %v", err)
	}
	val, err := memStore.GetContext(context.Background(), key)
	if err != nil || len(val) == 0 {
		t.Fatalf("expected stored context")
	}

	if _, err := delayForStep(&Step{DelaySec: -1}, time.Now()); err == nil {
		t.Fatalf("expected error for negative delay")
	}
	delay, err := delayForStep(&Step{DelaySec: 2}, time.Now())
	if err != nil || delay != 2*time.Second {
		t.Fatalf("expected delay from delay_sec")
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	delay, err = delayForStep(&Step{DelayUntil: future}, time.Now().UTC())
	if err != nil || delay <= 0 {
		t.Fatalf("expected delay from delay_until")
	}
}

func TestBuildEventAlert(t *testing.T) {
	payload := map[string]any{"level": "warn", "message": "hi", "code": "c1", "component": "cmp"}
	alert := buildEventAlert(&Step{ID: "step"}, payload)
	if alert.Level != "WARN" || alert.Message != "hi" || alert.Code != "c1" || alert.Component != "cmp" {
		t.Fatalf("unexpected alert: %#v", alert)
	}
	alert = buildEventAlert(&Step{ID: "step", Name: "Named"}, map[string]any{})
	if alert.Message != "Named" {
		t.Fatalf("expected step name fallback")
	}
}

func TestCloneStepRun(t *testing.T) {
	sr := &StepRun{StepID: "step", Status: StepStatusSucceeded, Output: "ptr", Children: map[string]*StepRun{
		"child": {StepID: "child", Status: StepStatusRunning, JobID: "job"},
	}}
	clone := cloneStepRun(sr)
	if clone == nil || clone.StepID != sr.StepID || clone.Children["child"].JobID != "job" {
		t.Fatalf("expected clone of step run")
	}
}

func TestInlineResultValidation(t *testing.T) {
	engine := &Engine{}
	step := &Step{OutputSchema: map[string]any{"type": "object", "required": []any{"result"}}}
	payload := map[string]any{"result": "ok"}
	if err := engine.validateInlineOutput(step, payload); err != nil {
		t.Fatalf("expected inline output valid")
	}
}
