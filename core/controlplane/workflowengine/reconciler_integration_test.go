package workflowengine

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/memory"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

type stubBus struct {
	mu        sync.Mutex
	published int
}

func (b *stubBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	b.published++
	b.mu.Unlock()
	return nil
}

func (b *stubBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func TestReconcilerReconcileRun(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	workflowStore, err := wf.NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer workflowStore.Close()

	jobStore, err := memory.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	bus := &stubBus{}
	engine := wf.NewEngine(workflowStore, bus)

	wfDef := &wf.Workflow{
		ID:    "wf-test",
		OrgID: "org",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &wf.WorkflowRun{
		ID:         "run-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step": {StepID: "step", Status: wf.StepStatusRunning, JobID: "run-1:step@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	jobID := "run-1:step@1"
	if err := jobStore.SetState(context.Background(), jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set job state pending: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("set job state scheduled: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, scheduler.JobStateSucceeded); err != nil {
		t.Fatalf("set job state succeeded: %v", err)
	}

	rec := newReconciler(workflowStore, engine, jobStore, time.Millisecond, 10)
	rec.reconcileRun(context.Background(), run.ID)

	updated, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step"].Status != wf.StepStatusSucceeded {
		t.Fatalf("expected step succeeded, got %s", updated.Steps["step"].Status)
	}
	if updated.Status != wf.RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", updated.Status)
	}
}
