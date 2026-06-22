package workflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// standaloneTopology mirrors the deployed cordum-workflow-engine wiring in
// core/workflow/runner.go: the engine is built WithRunLocker(jobStore) (L130)
// AND the reconciler is handed the SAME jobStore (L152), so both lock
// runLockKey(runID) against the same Redis. This is the wiring that every other
// reconciler test omits — which is exactly why the reconciler->engine run-lock
// self-contention stayed invisible (the gateway in-process engine has no locker).
type standaloneTopology struct {
	srv           *miniredis.Miniredis
	workflowStore *RedisStore
	jobStore      *store.RedisJobStore
	engine        *Engine
	rec           *reconciler
}

// newReconcilerTopology builds the shared miniredis-backed stores and reconciler.
// When withLocker is true the engine is wired WithRunLocker(jobStore) (standalone
// runner.go topology); when false the engine has no locker (gateway topology).
func newReconcilerTopology(t *testing.T, withLocker bool) *standaloneTopology {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	redisURL := "redis://" + srv.Addr()
	workflowStore, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	engine := NewEngine(workflowStore, &stubBus{})
	if withLocker {
		// Mirror runner.go:125-130 + 152 exactly: engine has the distributed
		// locker and the reconciler shares the identical jobStore key namespace.
		engine = engine.WithRunLocker(jobStore)
	}
	rec := newReconciler(workflowStore, engine, jobStore, time.Millisecond, 10)
	t.Cleanup(func() {
		_ = workflowStore.Close()
		_ = jobStore.Close()
		srv.Close()
	})
	return &standaloneTopology{srv: srv, workflowStore: workflowStore, jobStore: jobStore, engine: engine, rec: rec}
}

func newStandaloneTopology(t *testing.T) *standaloneTopology {
	t.Helper()
	return newReconcilerTopology(t, true)
}

// seedSingleStepRun creates a one-worker-step workflow + a Running run whose
// single step is dispatched (Running, with jobID). Returns the jobID.
func (tp *standaloneTopology) seedSingleStepRun(t *testing.T, wfID, runID string) string {
	t.Helper()
	ctx := context.Background()
	wfDef := &Workflow{
		ID:    wfID,
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := tp.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	jobID := runID + ":step@1"
	run := &WorkflowRun{
		ID:         runID,
		WorkflowID: wfID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := tp.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return jobID
}

// TestReconciler_StandaloneTopology_HandleJobResultAdvancesRun reproduces the
// CRITICAL fail-silent no-op: with the run-locker wired (standalone topology),
// the reconciler holds runLockKey(runID), then engine.HandleJobResult re-acquires
// the SAME non-reentrant key, gets contention, and returns nil doing NO work.
// The delivered JobResult must actually advance the run.
func TestReconciler_StandaloneTopology_HandleJobResultAdvancesRun(t *testing.T) {
	tp := newStandaloneTopology(t)
	ctx := context.Background()
	jobID := tp.seedSingleStepRun(t, "wf-rl", "run-rl")

	// Deliver the step's terminal result through the reconciler's bus handler,
	// exactly as runner.go's SubjectResult subscription does.
	jr := &pb.JobResult{
		JobId:     jobID,
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "mem://result-rl",
	}
	if err := tp.rec.HandleJobResult(ctx, jr); err != nil {
		t.Fatalf("HandleJobResult returned error: %v", err)
	}

	updated, err := tp.workflowStore.GetRun(ctx, "run-rl")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got := updated.Steps["step"].Status; got != StepStatusSucceeded {
		t.Fatalf("expected step Succeeded, got %s — run did not advance (engine self-contended on runLockKey held by reconciler)", got)
	}
	if updated.Status != RunStatusSucceeded {
		t.Fatalf("expected run Succeeded, got %s", updated.Status)
	}
}

// TestReconciler_StandaloneTopology_ReconcileRunRecoversStuckRun reproduces the
// other half of the bug: a stuck run whose job finished in the store but whose
// JobResult was lost. The reconciler scan must recover it. With the run-locker
// wired, reconcileRun holds runLockKey then calls engine.HandleJobResult /
// StartRun, both of which self-contend and no-op, so the run never recovers.
func TestReconciler_StandaloneTopology_ReconcileRunRecoversStuckRun(t *testing.T) {
	tp := newStandaloneTopology(t)
	ctx := context.Background()
	jobID := tp.seedSingleStepRun(t, "wf-rl-stuck", "run-rl-stuck")

	// Job finished in the store, but the JobResult bus message never arrived —
	// the exact "stuck run" the reconciler scan exists to recover.
	for _, s := range []model.JobState{model.JobStatePending, model.JobStateScheduled, model.JobStateSucceeded} {
		if err := tp.jobStore.SetState(ctx, jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}
	if err := tp.jobStore.SetResultPtr(ctx, jobID, "mem://result-stuck"); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	tp.rec.reconcileRun(ctx, "run-rl-stuck")

	updated, err := tp.workflowStore.GetRun(ctx, "run-rl-stuck")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got := updated.Steps["step"].Status; got != StepStatusSucceeded {
		t.Fatalf("expected step Succeeded after reconcile, got %s — stuck run not recovered (engine self-contended on run lock)", got)
	}
	if updated.Status != RunStatusSucceeded {
		t.Fatalf("expected run Succeeded after reconcile, got %s", updated.Status)
	}
}

// TestReconciler_StandaloneTopology_ConcurrentDeliveryNoDoubleAdvance pins
// mutual exclusion: many concurrent deliveries of the SAME result (standalone
// topology) must advance the step exactly once. The fix must serialize via the
// run lock + idempotency, never double-advance.
func TestReconciler_StandaloneTopology_ConcurrentDeliveryNoDoubleAdvance(t *testing.T) {
	tp := newStandaloneTopology(t)
	ctx := context.Background()

	var succeededFinishes atomic.Int32
	tp.engine.OnStepFinished = func(_, _ string, status StepStatus) {
		if status == StepStatusSucceeded {
			succeededFinishes.Add(1)
		}
	}
	jobID := tp.seedSingleStepRun(t, "wf-rl-conc", "run-rl-conc")

	jr := &pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "mem://result-conc"}
	const deliveries = 8
	var wg sync.WaitGroup
	wg.Add(deliveries)
	for i := 0; i < deliveries; i++ {
		go func() {
			defer wg.Done()
			// Errors (e.g. "run lock busy" retry) are expected for losers; ignore.
			_ = tp.rec.HandleJobResult(ctx, jr)
		}()
	}
	wg.Wait()

	updated, err := tp.workflowStore.GetRun(ctx, "run-rl-conc")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != RunStatusSucceeded {
		t.Fatalf("expected run Succeeded, got %s", updated.Status)
	}
	if got := succeededFinishes.Load(); got != 1 {
		t.Fatalf("expected step to finish exactly once (no double-advance), got %d", got)
	}
}

// TestEngine_handleJobResultLockHeld_SurfacesError proves the lock-held engine
// entrypoint returns real errors instead of swallowing them. Before the fix the
// reconciler->engine call self-contended and returned nil (ACK-and-drop); now a
// genuine failure (run not found) propagates so the caller can NACK/retry.
func TestEngine_handleJobResultLockHeld_SurfacesError(t *testing.T) {
	tp := newStandaloneTopology(t)
	ctx := context.Background()

	// No run created → handleJobResultLocked's GetRun returns redis.Nil →
	// ErrRunNotFound must surface from the lock-held path.
	jr := &pb.JobResult{JobId: "ghost-run:step@1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}
	err := tp.engine.handleJobResultLockHeld(ctx, jr)
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound surfaced from lock-held path, got %v", err)
	}
}

// TestReconciler_GatewayTopology_NoLocker_StillAdvances guards against a
// regression to the gateway in-process engine path (gateway.go:600 builds the
// engine WITHOUT WithRunLocker). The reconciler still holds the distributed lock
// via jobStore; the engine takes only its local mutex. The run must still advance.
func TestReconciler_GatewayTopology_NoLocker_StillAdvances(t *testing.T) {
	tp := newReconcilerTopology(t, false) // no WithRunLocker — gateway topology
	ctx := context.Background()
	jobID := tp.seedSingleStepRun(t, "wf-gw", "run-gw")

	jr := &pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "mem://result-gw"}
	if err := tp.rec.HandleJobResult(ctx, jr); err != nil {
		t.Fatalf("HandleJobResult returned error: %v", err)
	}

	updated, err := tp.workflowStore.GetRun(ctx, "run-gw")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got := updated.Steps["step"].Status; got != StepStatusSucceeded {
		t.Fatalf("expected step Succeeded (no-locker gateway topology), got %s", got)
	}
	if updated.Status != RunStatusSucceeded {
		t.Fatalf("expected run Succeeded (no-locker gateway topology), got %s", updated.Status)
	}
}

// TestLockManager_AcquireLocal_SkipsDistributedLock pins the core mechanism:
// acquireLocal must NOT acquire the distributed runLockKey, even when a locker is
// configured — that is what lets a caller already holding the distributed lock
// re-enter without self-contending on the non-reentrant SetNX.
func TestLockManager_AcquireLocal_SkipsDistributedLock(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer func() { _ = jobStore.Close() }()

	lm := lockManager{locks: make(map[string]*runLock), locker: jobStore, ctx: context.Background()}
	const runID = "run-local-skip"

	release := lm.acquireLocal(runID)
	defer release()

	if srv.Exists(runLockKey(runID)) {
		t.Fatal("acquireLocal must NOT create the distributed lock key (would self-contend with a caller-held lock)")
	}
}

// TestLockManager_AcquireLocal_MutualExclusionWithAcquire verifies the critical
// invariant: acquireLocal and acquire share the SAME per-run local mutex, so a
// reconciler (acquireLocal) and any engine path (acquire) can never both be in
// the run's critical section at once — no double-advance. Uses no distributed
// locker (mirrors the gateway, where the local mutex is the only serialization).
func TestLockManager_AcquireLocal_MutualExclusionWithAcquire(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)} // no distributed locker
	const runID = "run-mixed"
	const goroutines = 40

	var concurrent, maxConcurrent atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		useLocal := i%2 == 0
		go func(local bool) {
			defer wg.Done()
			var release func()
			if local {
				release = lm.acquireLocal(runID)
			} else {
				r, ok := lm.acquire(runID)
				if !ok {
					return
				}
				release = r
			}
			defer release()

			cur := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			if cur > 1 {
				t.Errorf("mutual exclusion violated: %d holders in critical section", cur)
			}
			concurrent.Add(-1)
		}(useLocal)
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > 1 {
		t.Errorf("max concurrent was %d, expected 1 (acquire/acquireLocal must share the local mutex)", mc)
	}
}
