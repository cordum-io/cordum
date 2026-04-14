package workflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestJobStatusFromState(t *testing.T) {
	cases := map[model.JobState]pb.JobStatus{
		model.JobStateSucceeded: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		model.JobStateFailed:    pb.JobStatus_JOB_STATUS_FAILED,
		model.JobStateTimeout:   pb.JobStatus_JOB_STATUS_TIMEOUT,
		model.JobStateDenied:    pb.JobStatus_JOB_STATUS_DENIED,
		model.JobStateCancelled: pb.JobStatus_JOB_STATUS_CANCELLED,
		"unknown":                   pb.JobStatus_JOB_STATUS_UNSPECIFIED,
	}
	for state, expect := range cases {
		if got := jobStatusFromState(state); got != expect {
			t.Fatalf("state %s expected %v got %v", state, expect, got)
		}
	}
}

func TestRunLockKeyAndSplitJobID(t *testing.T) {
	if runLockKey("abc") != "cordum:wf:run:lock:abc" {
		t.Fatalf("unexpected lock key")
	}
	if run, step := splitJobID("run:step"); run != "run" || step != "step" {
		t.Fatalf("unexpected split: %s %s", run, step)
	}
	if run, step := splitJobID("bad"); run != "" || step != "" {
		t.Fatalf("expected empty split")
	}
}

func TestNewReconcilerDefaults(t *testing.T) {
	r := newReconciler(nil, nil, nil, 0, 0)
	if r.pollInterval <= 0 {
		t.Fatalf("expected default poll interval")
	}
	if r.runScanLimit != 200 {
		t.Fatalf("expected default run scan limit")
	}
}

func TestStartHealthServer(t *testing.T) {
	srv := startHealthServer("127.0.0.1:0", nil)
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestReconcilerHandleJobResultNoop(t *testing.T) {
	r := newReconciler(nil, nil, nil, 10*time.Millisecond, 1)
	if err := r.HandleJobResult(context.Background(), &pb.JobResult{JobId: ""}); err != nil {
		t.Fatalf("expected nil error")
	}
	if err := r.HandleJobResult(context.Background(), &pb.JobResult{JobId: "run:step"}); err != nil {
		t.Fatalf("expected nil error when engine nil")
	}
}

func TestReconcilerReconcileRunEarlyReturn(t *testing.T) {
	r := newReconciler(nil, nil, nil, 10*time.Millisecond, 1)
	r.reconcileRun(context.Background(), "")
	r.reconcileRun(context.Background(), "run")
}

func TestReconcilerFailureReasonPropagation(t *testing.T) {
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
	defer func() { _ = workflowStore.Close() }()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	engine := NewEngine(workflowStore, &stubBus{})
	wfDef := &Workflow{
		ID:    "wf-err",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	jobID := "run-err:step@1"
	run := &WorkflowRun{
		ID:         "run-err",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, model.JobStateFailed); err != nil {
		t.Fatalf("set job state: %v", err)
	}
	if err := jobStore.SetFailureReason(context.Background(), jobID, "boom"); err != nil {
		t.Fatalf("set failure reason: %v", err)
	}

	rec := newReconciler(workflowStore, engine, jobStore, 10*time.Millisecond, 1)
	rec.reconcileRun(context.Background(), run.ID)

	final, _ := workflowStore.GetRun(context.Background(), run.ID)
	if final.Steps["step"] == nil {
		t.Fatalf("expected step run")
	}
	if msg, ok := final.Steps["step"].Error["message"].(string); !ok || msg != "boom" {
		t.Fatalf("expected failure reason 'boom', got %#v", final.Steps["step"].Error)
	}
}

func TestReconcilerFallbackErrorMessage(t *testing.T) {
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
	defer func() { _ = workflowStore.Close() }()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	engine := NewEngine(workflowStore, &stubBus{})
	wfDef := &Workflow{
		ID:    "wf-fallback",
		OrgID: "org",
		Steps: map[string]*Step{
			"step": {ID: "step", Type: StepTypeWorker, Topic: "job.test"},
		},
	}
	if err := workflowStore.SaveWorkflow(context.Background(), wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	jobID := "run-fallback:step@1"
	run := &WorkflowRun{
		ID:         "run-fallback",
		WorkflowID: wfDef.ID,
		OrgID:      "org",
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"step": {StepID: "step", Status: StepStatusRunning, JobID: jobID},
		},
	}
	if err := workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := jobStore.SetState(context.Background(), jobID, model.JobStateFailed); err != nil {
		t.Fatalf("set job state: %v", err)
	}

	rec := newReconciler(workflowStore, engine, jobStore, 10*time.Millisecond, 1)
	rec.reconcileRun(context.Background(), run.ID)

	final, _ := workflowStore.GetRun(context.Background(), run.ID)
	if final.Steps["step"] == nil {
		t.Fatalf("expected step run")
	}
	msg, ok := final.Steps["step"].Error["message"].(string)
	if !ok || !strings.Contains(msg, jobID) || !strings.Contains(msg, "no error details available") {
		t.Fatalf("unexpected fallback message: %q", msg)
	}
}

func TestSplitJobIDMulti(t *testing.T) {
	run, step := splitJobID("run:with:step")
	if run != "run" || step != "with:step" {
		t.Fatalf("unexpected split for multi: %s %s", run, step)
	}
	run, step = splitJobID("run:with:step@2")
	if run != "run" || step != "with:step" {
		t.Fatalf("unexpected split for multi with attempt: %s %s", run, step)
	}
}

func TestReconcilerHandleJobResultLockBusy(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	ctx := context.Background()
	lockKey := runLockKey("run-1")
	token, err := jobStore.TryAcquireLock(ctx, lockKey, time.Second)
	if err != nil || token == "" {
		t.Fatalf("expected lock acquired: token=%q err=%v", token, err)
	}

	rec := newReconciler(nil, nil, jobStore, 10*time.Millisecond, 1)
	err = rec.HandleJobResult(ctx, &pb.JobResult{JobId: "run-1:step@1"})
	if err == nil {
		t.Fatalf("expected retryable error when lock busy")
	}
	if delay, ok := bus.RetryDelay(err); !ok || delay != 500*time.Millisecond {
		t.Fatalf("expected retry delay 500ms, got %v ok=%v", delay, ok)
	}
}

func TestReconcilerStartStopsOnContext(t *testing.T) {
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
	defer func() { _ = workflowStore.Close() }()

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	engine := NewEngine(workflowStore, &stubBus{})
	rec := newReconciler(workflowStore, engine, jobStore, 5*time.Millisecond, 10)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.Start(ctx)
		close(done)
	}()
	time.Sleep(15 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("reconciler did not stop after context cancel")
	}
}

func TestHandleJobResult_CancelledContext(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer func() { _ = jobStore.Close() }()

	workflowStore, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	defer func() { _ = workflowStore.Close() }()

	engine := NewEngine(workflowStore, &stubBus{})
	rec := newReconciler(workflowStore, engine, jobStore, time.Minute, 10)

	// Pre-cancel the context to simulate shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// HandleJobResult should return promptly with a cancelled context,
	// not block on Redis operations.
	done := make(chan error, 1)
	go func() {
		done <- rec.HandleJobResult(ctx, &pb.JobResult{JobId: "run-1:step-1"})
	}()
	select {
	case <-done:
		// Returned promptly — no goroutine leak.
	case <-time.After(2 * time.Second):
		t.Fatal("HandleJobResult did not return after context cancellation (goroutine leak)")
	}
}
