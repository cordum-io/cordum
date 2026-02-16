package workflow

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// failNBus fails the first N Publish calls, then succeeds.
type failNBus struct {
	mu        sync.Mutex
	failCount int
	calls     int
	published []pubMsg
}

func (b *failNBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.calls <= b.failCount {
		return fmt.Errorf("bus unavailable (attempt %d)", b.calls)
	}
	b.published = append(b.published, pubMsg{subject: subject, packet: packet})
	return nil
}

func (b *failNBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func (b *failNBus) totalCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *failNBus) publishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

// alwaysFailBus always returns an error from Publish.
type alwaysFailBus struct {
	mu    sync.Mutex
	calls int
}

func (b *alwaysFailBus) Publish(string, *pb.BusPacket) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return fmt.Errorf("permanent bus failure")
}

func (b *alwaysFailBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

func (b *alwaysFailBus) totalCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// ---------------------------------------------------------------------------
// publishJobCancel retry tests
// ---------------------------------------------------------------------------

func TestPublishJobCancel_RetriesAndSucceeds(t *testing.T) {
	ws := newWorkflowStore(t)
	defer ws.Close()

	bus := &failNBus{failCount: 1} // fail first, succeed second
	engine := NewEngine(ws, bus)

	err := engine.publishJobCancel("job-123", "test cancel")
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if bus.totalCalls() != 2 {
		t.Fatalf("expected 2 publish attempts, got %d", bus.totalCalls())
	}
	if bus.publishedCount() != 1 {
		t.Fatalf("expected 1 successful publish, got %d", bus.publishedCount())
	}
}

func TestPublishJobCancel_ExhaustsRetries(t *testing.T) {
	ws := newWorkflowStore(t)
	defer ws.Close()

	bus := &alwaysFailBus{}
	engine := NewEngine(ws, bus)

	err := engine.publishJobCancel("job-456", "test cancel")
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if bus.totalCalls() != 3 {
		t.Fatalf("expected 3 publish attempts, got %d", bus.totalCalls())
	}
}

func TestPublishJobCancel_NilGuards(t *testing.T) {
	ws := newWorkflowStore(t)
	defer ws.Close()

	engine := NewEngine(ws, &recordingBus{})

	// Empty job ID should be a no-op.
	if err := engine.publishJobCancel("", "reason"); err != nil {
		t.Fatalf("expected nil for empty jobID, got: %v", err)
	}

	// Nil bus should be a no-op.
	engine2 := NewEngine(ws, nil)
	if err := engine2.publishJobCancel("job-1", "reason"); err != nil {
		t.Fatalf("expected nil for nil bus, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CancelRun / timeoutRun timeline tests
// ---------------------------------------------------------------------------

func TestCancelRun_RecordsOrphanedJobsInTimeline(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	ws, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer ws.Close()

	bus := &alwaysFailBus{}
	engine := NewEngine(ws, bus)

	ctx := context.Background()
	wfDef := &Workflow{
		ID:    "wf-cancel-orphan",
		OrgID: "org",
		Steps: map[string]*Step{
			"s1": {ID: "s1", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := ws.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-cancel-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"s1": {StepID: "s1", Status: StepStatusRunning, JobID: "run-cancel-1:s1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := ws.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	cancelErr := engine.CancelRun(ctx, run.ID)
	if cancelErr == nil {
		t.Fatal("expected error from CancelRun when bus fails")
	}

	// Verify the run was still marked cancelled (state update happens before publish).
	updated, err := ws.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != RunStatusCancelled {
		t.Fatalf("expected run status Cancelled, got %s", updated.Status)
	}

	// Verify timeline has the cancel_publish_failed event.
	events, err := ws.ListTimelineEvents(ctx, run.ID, 100)
	if err != nil {
		t.Fatalf("get timeline: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "cancel_publish_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cancel_publish_failed timeline event")
	}
}

func TestTimeoutRun_RecordsOrphanedJobsInTimeline(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	ws, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer ws.Close()

	bus := &alwaysFailBus{}
	engine := NewEngine(ws, bus)

	ctx := context.Background()
	wfDef := &Workflow{
		ID:         "wf-timeout-orphan",
		OrgID:      "org",
		TimeoutSec: 10,
		Steps: map[string]*Step{
			"s1": {ID: "s1", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := ws.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	run := &WorkflowRun{
		ID:         "run-timeout-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"s1": {StepID: "s1", Status: StepStatusRunning, JobID: "run-timeout-1:s1@1"},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := ws.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	now := time.Now().UTC()
	timeoutErr := engine.timeoutRun(ctx, wfDef, run, now)
	if timeoutErr == nil {
		t.Fatal("expected error from timeoutRun when bus fails")
	}

	updated, err := ws.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != RunStatusTimedOut {
		t.Fatalf("expected run status TimedOut, got %s", updated.Status)
	}

	events, err := ws.ListTimelineEvents(ctx, run.ID, 100)
	if err != nil {
		t.Fatalf("get timeline: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "cancel_publish_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cancel_publish_failed timeline event")
	}
}

// ---------------------------------------------------------------------------
// Reconciler orphan detection test
// ---------------------------------------------------------------------------

func TestReconciler_DetectsAndReCancelsOrphanedJobs(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	ws, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer ws.Close()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	// Use a bus that tracks cancel publishes.
	var cancelCount atomic.Int32
	cbus := &countingCancelBus{cancelCount: &cancelCount}
	engine := NewEngine(ws, cbus)

	ctx := context.Background()

	wfDef := &Workflow{
		ID:    "wf-orphan",
		OrgID: "org",
		Steps: map[string]*Step{
			"s1": {ID: "s1", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := ws.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	jobID := "run-orphan-1:s1@1"

	// Create a cancelled run with a step that has a JobID.
	run := &WorkflowRun{
		ID:         "run-orphan-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusCancelled,
		Steps: map[string]*StepRun{
			"s1": {StepID: "s1", Status: StepStatusCancelled, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	now := time.Now().UTC()
	run.CompletedAt = &now
	if err := ws.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Set the job to Running in the job store — simulating an orphan.
	for _, s := range []model.JobState{
		model.JobStatePending,
		model.JobStateScheduled,
		model.JobStateDispatched,
		model.JobStateRunning,
	} {
		if err := jobStore.SetState(ctx, jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}

	rec := newReconciler(ws, engine, jobStore, time.Millisecond, 10)
	rec.reconcileOrphanedJobs(ctx, run.ID)

	if cancelCount.Load() != 1 {
		t.Fatalf("expected 1 cancel publish for orphaned job, got %d", cancelCount.Load())
	}
}

func TestReconciler_NoOrphansWhenJobAlreadyCancelled(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	redisURL := "redis://" + srv.Addr()
	ws, err := NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer ws.Close()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	var cancelCount atomic.Int32
	cbus := &countingCancelBus{cancelCount: &cancelCount}
	engine := NewEngine(ws, cbus)

	ctx := context.Background()
	wfDef := &Workflow{
		ID:    "wf-no-orphan",
		OrgID: "org",
		Steps: map[string]*Step{
			"s1": {ID: "s1", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := ws.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	jobID := "run-no-orphan-1:s1@1"
	run := &WorkflowRun{
		ID:         "run-no-orphan-1",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusCancelled,
		Steps: map[string]*StepRun{
			"s1": {StepID: "s1", Status: StepStatusCancelled, JobID: jobID},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	now := time.Now().UTC()
	run.CompletedAt = &now
	if err := ws.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	// Job already cancelled — no orphan.
	// Must follow valid state transitions: Pending → Scheduled → Dispatched → Running → Cancelled.
	for _, s := range []model.JobState{
		model.JobStatePending,
		model.JobStateScheduled,
		model.JobStateDispatched,
		model.JobStateRunning,
		model.JobStateCancelled,
	} {
		if err := jobStore.SetState(ctx, jobID, s); err != nil {
			t.Fatalf("set state %s: %v", s, err)
		}
	}

	rec := newReconciler(ws, engine, jobStore, time.Millisecond, 10)
	rec.reconcileOrphanedJobs(ctx, run.ID)

	if cancelCount.Load() != 0 {
		t.Fatalf("expected 0 cancel publishes for already-cancelled job, got %d", cancelCount.Load())
	}
}

// countingCancelBus counts publishes to the cancel subject.
type countingCancelBus struct {
	cancelCount *atomic.Int32
}

func (b *countingCancelBus) Publish(subject string, packet *pb.BusPacket) error {
	if subject == capsdk.SubjectCancel {
		b.cancelCount.Add(1)
	}
	return nil
}

func (b *countingCancelBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }
