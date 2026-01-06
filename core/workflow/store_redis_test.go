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
			"start": {ID: "start", Name: "Start", Type: StepTypeWorker, Topic: "job.default"},
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

func TestWorkflowListRunsAll(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run1 := &WorkflowRun{
		ID:         "run-a",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	run2 := &WorkflowRun{
		ID:         "run-b",
		WorkflowID: "wf-2",
		OrgID:      "org-1",
		Status:     RunStatusRunning,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run1); err != nil {
		t.Fatalf("create run1: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.CreateRun(ctx, run2); err != nil {
		t.Fatalf("create run2: %v", err)
	}

	list, err := store.ListRuns(ctx, time.Now().UTC().Unix(), 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(list))
	}
	if list[0].ID != "run-b" {
		t.Fatalf("expected newest run-b first, got %s", list[0].ID)
	}
}

func TestWorkflowDeleteRemovesIndexes(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	wf := &Workflow{
		ID:    "wf-del",
		OrgID: "org-1",
		Name:  "Delete me",
		Steps: map[string]*Step{"start": {ID: "start", Type: StepTypeApproval}},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := store.DeleteWorkflow(ctx, wf.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetWorkflow(ctx, wf.ID); err == nil {
		t.Fatalf("expected workflow to be deleted")
	}

	listOrg, err := store.ListWorkflows(ctx, "org-1", 10)
	if err != nil {
		t.Fatalf("list org: %v", err)
	}
	if len(listOrg) != 0 {
		t.Fatalf("expected empty org list, got %+v", listOrg)
	}
	listAll, err := store.ListWorkflows(ctx, "", 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(listAll) != 0 {
		t.Fatalf("expected empty list, got %+v", listAll)
	}
}

func TestRunDeleteRemovesIndexes(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-del",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := store.DeleteRun(ctx, run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if _, err := store.GetRun(ctx, run.ID); err == nil {
		t.Fatalf("expected run to be deleted")
	}

	list, err := store.ListRunsByWorkflow(ctx, run.WorkflowID, 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty runs list, got %+v", list)
	}
}

func TestRunStatusIndexing(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-idx-1",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	ids, err := store.ListRunIDsByStatus(ctx, RunStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("unexpected pending ids: %+v", ids)
	}

	run.Status = RunStatusRunning
	if err := store.UpdateRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	ids, err = store.ListRunIDsByStatus(ctx, RunStatusPending, 10)
	if err != nil {
		t.Fatalf("list pending after update: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no pending ids, got %+v", ids)
	}

	ids, err = store.ListRunIDsByStatus(ctx, RunStatusRunning, 10)
	if err != nil {
		t.Fatalf("list running: %v", err)
	}
	if len(ids) != 1 || ids[0] != run.ID {
		t.Fatalf("unexpected running ids: %+v", ids)
	}
}

func TestRunIdempotencyKeyMapping(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:             "run-idem-1",
		WorkflowID:     "wf-1",
		OrgID:          "org-1",
		Status:         RunStatusPending,
		Steps:          map[string]*StepRun{},
		IdempotencyKey: "idem-key-1",
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	got, err := store.GetRunByIdempotencyKey(ctx, "idem-key-1")
	if err != nil {
		t.Fatalf("get idempotency: %v", err)
	}
	if got != run.ID {
		t.Fatalf("expected run id %s, got %s", run.ID, got)
	}
	ok, err := store.TrySetRunIdempotencyKey(ctx, "idem-key-1", "run-idem-2")
	if err != nil {
		t.Fatalf("try set: %v", err)
	}
	if ok {
		t.Fatalf("expected idempotency key to be taken")
	}
}

func TestRunTimelineAppendAndList(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	run := &WorkflowRun{
		ID:         "run-timeline",
		WorkflowID: "wf-1",
		OrgID:      "org-1",
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
	}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := store.AppendTimelineEvent(ctx, run.ID, &TimelineEvent{Type: "run_created"}); err != nil {
		t.Fatalf("append timeline: %v", err)
	}
	if err := store.AppendTimelineEvent(ctx, run.ID, &TimelineEvent{Type: "run_status", Status: string(RunStatusRunning)}); err != nil {
		t.Fatalf("append timeline: %v", err)
	}

	events, err := store.ListTimelineEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatalf("list timeline: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "run_created" || events[1].Type != "run_status" {
		t.Fatalf("unexpected timeline events: %+v", events)
	}
}
