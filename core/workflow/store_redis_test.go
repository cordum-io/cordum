package workflow

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestStore(t *testing.T) *RedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	return store
}

func TestWorkflowSaveGetList(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	wf := &Workflow{
		ID:          "wf-1",
		OrgID:       "org-1",
		Name:        "Sample",
		Description: "desc",
		Version:     "v1",
		Steps: map[string]*Step{
			"start": {ID: "start", Name: "Start", Type: StepTypeWorker, Topic: "job.echo"},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.GetWorkflow(ctx, "wf-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != wf.Name || got.OrgID != wf.OrgID {
		t.Fatalf("mismatch: %+v", got)
	}

	list, err := store.ListWorkflows(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "wf-1" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestWorkflowRunsCRUD(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-1",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Input:      map[string]any{"foo": "bar"},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		Labels:     map[string]string{"tenant": "org-1"},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != RunStatusPending {
		t.Fatalf("expected pending, got %s", got.Status)
	}

	now := time.Now().UTC()
	run.Status = RunStatusRunning
	run.StartedAt = &now
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	got, err = store.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get run 2: %v", err)
	}
	if got.Status != RunStatusRunning {
		t.Fatalf("expected running, got %s", got.Status)
	}

	list, err := store.ListRunsByWorkflow(ctx, "wf-1", 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "run-1" {
		t.Fatalf("unexpected runs: %+v", list)
	}
}
