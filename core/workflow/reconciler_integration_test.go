package workflow

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// faultyJobStore wraps a real JobStore and allows injecting errors for specific methods.
type faultyJobStore struct {
	model.JobStore
	getStateErr         error
	getResultPtrErr     error
	getFailureReasonErr error
}

func (f *faultyJobStore) GetState(ctx context.Context, jobID string) (model.JobState, error) {
	if f.getStateErr != nil {
		return "", f.getStateErr
	}
	return f.JobStore.GetState(ctx, jobID)
}

func (f *faultyJobStore) GetResultPtr(ctx context.Context, jobID string) (string, error) {
	if f.getResultPtrErr != nil {
		return "", f.getResultPtrErr
	}
	return f.JobStore.GetResultPtr(ctx, jobID)
}

func (f *faultyJobStore) GetFailureReason(ctx context.Context, jobID string) (string, error) {
	if f.getFailureReasonErr != nil {
		return "", f.getFailureReasonErr
	}
	return f.JobStore.GetFailureReason(ctx, jobID)
}

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
	if err := jobStore.SetResultPtr(context.Background(), jobID, "mem://result-1"); err != nil {
		t.Fatalf("set result ptr: %v", err)
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

// TestReconcilerDefersStepOnGetResultPtrError verifies the critical fix: when
// GetResultPtr fails for a succeeded job, the reconciler must NOT synthesize an
// incomplete JobResult (which would permanently mark the step Succeeded with no
// output). Instead it defers the step so the next tick can retry.
func TestReconcilerDefersStepOnGetResultPtrError(t *testing.T) {
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

	realJobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer realJobStore.Close()

	bus := &stubBus{}
	engine := NewEngine(workflowStore, bus)

	wfDef := &Workflow{
		ID:    "wf-resultptr",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	jobID := "run-rp:step@1"
	run := &WorkflowRun{
		ID:         "run-rp",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Mark job as succeeded in the store.
	for _, s := range []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateSucceeded} {
		if err := realJobStore.SetState(context.Background(), jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}
	if err := realJobStore.SetResultPtr(context.Background(), jobID, "mem://result-key"); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	// Inject GetResultPtr error.
	faulty := &faultyJobStore{
		JobStore:        realJobStore,
		getResultPtrErr: fmt.Errorf("redis timeout"),
	}

	rec := newReconciler(workflowStore, engine, faulty, time.Millisecond, 10)
	rec.reconcileRun(context.Background(), run.ID)

	// Step must remain Running — not permanently marked Succeeded with no output.
	updated, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step"].Status != StepStatusRunning {
		t.Fatalf("expected step to remain Running (deferred), got %s", updated.Steps["step"].Status)
	}
	if updated.Status != RunStatusRunning {
		t.Fatalf("expected run to remain Running, got %s", updated.Status)
	}

	// Now clear the fault and reconcile again — should complete normally.
	faulty.getResultPtrErr = nil
	rec2 := newReconciler(workflowStore, engine, faulty, time.Millisecond, 10)
	rec2.reconcileRun(context.Background(), run.ID)

	recovered, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if recovered.Steps["step"].Status != StepStatusSucceeded {
		t.Fatalf("expected step Succeeded after fault cleared, got %s", recovered.Steps["step"].Status)
	}
}

// TestReconcilerSkipsStepOnGetStateError verifies that when GetState fails,
// the step is skipped (not processed) rather than silently ignored.
func TestReconcilerSkipsStepOnGetStateError(t *testing.T) {
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

	realJobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer realJobStore.Close()

	bus := &stubBus{}
	engine := NewEngine(workflowStore, bus)

	wfDef := &Workflow{
		ID:    "wf-getstate",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	jobID := "run-gs:step@1"
	run := &WorkflowRun{
		ID:         "run-gs",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Job is succeeded but GetState will fail.
	for _, s := range []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateSucceeded} {
		if err := realJobStore.SetState(context.Background(), jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}

	faulty := &faultyJobStore{
		JobStore:    realJobStore,
		getStateErr: fmt.Errorf("connection refused"),
	}

	rec := newReconciler(workflowStore, engine, faulty, time.Millisecond, 10)
	rec.reconcileRun(context.Background(), run.ID)

	// Step must remain Running — GetState error should not advance the step.
	updated, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step"].Status != StepStatusRunning {
		t.Fatalf("expected step to remain Running on GetState error, got %s", updated.Steps["step"].Status)
	}
}

// TestReconcilerProceedsOnGetFailureReasonError verifies that when
// GetFailureReason fails, the reconciler still processes the step with
// a generic error message rather than dropping it entirely.
func TestReconcilerProceedsOnGetFailureReasonError(t *testing.T) {
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

	realJobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer realJobStore.Close()

	bus := &stubBus{}
	engine := NewEngine(workflowStore, bus)

	wfDef := &Workflow{
		ID:    "wf-failreason",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	jobID := "run-fr:step@1"
	run := &WorkflowRun{
		ID:         "run-fr",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Mark job as failed.
	for _, s := range []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateFailed} {
		if err := realJobStore.SetState(context.Background(), jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}

	faulty := &faultyJobStore{
		JobStore:            realJobStore,
		getFailureReasonErr: fmt.Errorf("redis timeout"),
	}

	rec := newReconciler(workflowStore, engine, faulty, time.Millisecond, 10)
	rec.reconcileRun(context.Background(), run.ID)

	// Step should still be marked as Failed with a generic message.
	updated, err := workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step"].Status != StepStatusFailed {
		t.Fatalf("expected step Failed despite GetFailureReason error, got %s", updated.Steps["step"].Status)
	}
}
