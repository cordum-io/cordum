package workflow

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
)

func TestReconcilerProductionAdvancesWithCanonicalRunIdentity(t *testing.T) {
	srv := miniredis.RunT(t)
	redisURL := "redis://" + srv.Addr()
	workflowStore, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("NewRedisWorkflowStore() error = %v", err)
	}
	defer func() { _ = workflowStore.Close() }()
	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	ctx := context.Background()
	wf := &Workflow{ID: "wf-reconcile-identity", OrgID: "tenant-a", Steps: map[string]*Step{
		"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
	}}
	if err := workflowStore.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("SaveWorkflow() error = %v", err)
	}
	now := time.Now().UTC()
	run := productionIdentityRun()
	run.ID, run.WorkflowID, run.Status = "run-reconcile-identity", wf.ID, RunStatusRunning
	run.CreatedAt, run.UpdatedAt = now, now
	run.Steps = map[string]*StepRun{"step": {
		StepID: "step", JobID: "run-reconcile-identity:step@1", Status: StepStatusRunning,
	}}
	if err := workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	for _, state := range []model.JobState{
		model.JobStatePending, model.JobStateScheduled, model.JobStateFailed,
	} {
		if err := jobStore.SetState(ctx, run.Steps["step"].JobID, state); err != nil {
			t.Fatalf("SetState(%s) error = %v", state, err)
		}
	}
	engine := NewEngine(workflowStore, &stubBus{}).WithProductionIdentityEnforcement(true)
	newReconciler(workflowStore, engine, jobStore, time.Millisecond, 10).reconcileRun(ctx, run.ID)

	updated, err := workflowStore.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got := updated.Steps["step"].Status; got != StepStatusFailed {
		t.Fatalf("step status = %q, want %q", got, StepStatusFailed)
	}
	if updated.Status != RunStatusFailed {
		t.Fatalf("run status = %q, want %q", updated.Status, RunStatusFailed)
	}
	if updated.Identity.GetTenantId() != run.Identity.GetTenantId() {
		t.Fatalf("run identity changed during reconciliation: %v", updated.Identity)
	}
}
