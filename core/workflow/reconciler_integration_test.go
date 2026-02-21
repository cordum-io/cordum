package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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
	workflowStore, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer workflowStore.Close()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	bus := &stubBus{}
	engine := NewEngine(workflowStore, bus)

	wfDef := &Workflow{
		ID:    "wf-test",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: "run-1:step@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	jobID := "run-1:step@1"
	if err := jobStore.SetState(context.Background(), jobID, model.JobStatePending); err != nil {
		t.Fatalf("set job state pending: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, model.JobStateScheduled); err != nil {
		t.Fatalf("set job state scheduled: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, model.JobStateSucceeded); err != nil {
		t.Fatalf("set job state succeeded: %v", err)
	}

	rec := newReconciler(workflowStore, engine, jobStore, time.Millisecond, 10)
	rec.reconcileRun(context.Background(), run.ID)

	updated, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step"].Status != StepStatusSucceeded {
		t.Fatalf("expected step succeeded, got %s", updated.Steps["step"].Status)
	}
	if updated.Status != RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", updated.Status)
	}
}

// TestWorkflowReconcilerSingleTickPerTTLWindow verifies that when two workflow
// reconciler goroutines race to acquire the distributed lock via real Redis
// (miniredis), only one executes tick() per TTL window.
func TestWorkflowReconcilerSingleTickPerTTLWindow(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	lockTTL := 50 * time.Millisecond
	var tickCount atomic.Int32
	var wg sync.WaitGroup

	// Two goroutines race to acquire the reconciler lock.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := jobStore.TryAcquireLock(context.Background(), reconcilerLockKey, lockTTL)
			if err != nil || token == "" {
				return
			}
			tickCount.Add(1)
			// Simulate tick work.
			time.Sleep(2 * time.Millisecond)
			// No ReleaseLock — TTL-based hold for horizontal scaling.
		}()
	}
	wg.Wait()

	if got := tickCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 tick in TTL window, got %d", got)
	}

	// Fast-forward miniredis time to expire the lock TTL.
	srv.FastForward(lockTTL + 10*time.Millisecond)

	token, err := jobStore.TryAcquireLock(context.Background(), reconcilerLockKey, lockTTL)
	if err != nil {
		t.Fatalf("lock acquisition after TTL: %v", err)
	}
	if token == "" {
		t.Fatal("expected lock to be available after TTL expiry")
	}
}
