package scheduler

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/redisutil"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type stubOutputChecker struct {
	metaRecord    OutputSafetyRecord
	metaErr       error
	contentRecord OutputSafetyRecord
	contentErr    error
	metaCalls     atomic.Int32
	contentCalls  atomic.Int32
}

func (s *stubOutputChecker) EvaluateOutput(_ context.Context, _ *OutputEvaluateRequest) (OutputSafetyRecord, error) {
	return OutputSafetyRecord{}, errors.New("not implemented in stub")
}

func (s *stubOutputChecker) CheckOutputMeta(_ *pb.JobResult, _ *pb.JobRequest) (OutputSafetyRecord, error) {
	s.metaCalls.Add(1)
	if s.metaErr != nil {
		return OutputSafetyRecord{}, s.metaErr
	}
	return s.metaRecord, nil
}

func (s *stubOutputChecker) CheckOutputContent(_ context.Context, _ *pb.JobResult, _ *pb.JobRequest) (OutputSafetyRecord, error) {
	s.contentCalls.Add(1)
	if s.contentErr != nil {
		return OutputSafetyRecord{}, s.contentErr
	}
	return s.contentRecord, nil
}

func TestHandleJobResultOutputAllowed(t *testing.T) {
	checker := &stubOutputChecker{
		metaRecord: OutputSafetyRecord{Decision: OutputAllow, Reason: "ok"},
		contentErr: errors.New("skip async in this test"),
	}
	bus := &fakeBus{}
	store := newSagaJobStore()
	store.states["job-out-allow"] = JobStateRunning
	store.topics["job-out-allow"] = "job.default"
	store.reqs["job-out-allow"] = &pb.JobRequest{
		JobId:       "job-out-allow",
		Topic:       "job.default",
		TenantId:    "tenant-a",
		PrincipalId: "principal-a",
	}

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true).
		WithAsyncFailMode("open")

	err := engine.handleJobResult(&pb.JobResult{
		JobId:     "job-out-allow",
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:job-out-allow",
	})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	state, err := store.GetState(context.Background(), "job-out-allow")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateSucceeded {
		t.Fatalf("expected state succeeded, got %s", state)
	}
	decision, err := store.GetOutputDecision(context.Background(), "job-out-allow")
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if decision.Decision != OutputAllow {
		t.Fatalf("expected output decision allow, got %#v", decision)
	}
	if checker.metaCalls.Load() != 1 {
		t.Fatalf("expected meta check called once, got %d", checker.metaCalls.Load())
	}
	published := bus.snapshotPublished()
	if len(published) != 0 {
		t.Fatalf("expected no DLQ publish, got %d", len(published))
	}
}

func TestHandleJobResultOutputQuarantined(t *testing.T) {
	checker := &stubOutputChecker{metaRecord: OutputSafetyRecord{Decision: OutputQuarantine, Reason: "secret leak"}}
	bus := &fakeBus{}
	store := newSagaJobStore()
	store.states["job-out-quarantine"] = JobStateRunning
	store.topics["job-out-quarantine"] = "job.default"
	store.reqs["job-out-quarantine"] = &pb.JobRequest{JobId: "job-out-quarantine", Topic: "job.default", TenantId: "tenant-a"}

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	err := engine.handleJobResult(&pb.JobResult{JobId: "job-out-quarantine", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-out-quarantine"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	state, err := store.GetState(context.Background(), "job-out-quarantine")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateQuarantined {
		t.Fatalf("expected state output_quarantined, got %s", state)
	}
	decision, err := store.GetOutputDecision(context.Background(), "job-out-quarantine")
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if decision.Decision != OutputQuarantine {
		t.Fatalf("expected output decision quarantine, got %#v", decision)
	}
	foundDLQ := false
	foundAudit := false
	published := bus.snapshotPublished()
	for _, msg := range published {
		switch msg.subject {
		case capsdk.SubjectDLQ:
			jr := msg.packet.GetJobResult()
			if jr != nil && jr.GetErrorCode() == outputPolicyReason {
				foundDLQ = true
			}
		case outputPolicyAudit:
			jp := msg.packet.GetJobProgress()
			if jp != nil && jp.GetJobId() == "job-out-quarantine" && jp.GetStepId() == "output_policy" {
				foundAudit = true
			}
		}
	}
	if !foundDLQ {
		t.Fatalf("expected quarantine DLQ publish, got %#v", published)
	}
	if !foundAudit {
		t.Fatalf("expected quarantine audit event, got %#v", published)
	}
}

func TestHandleJobResultOutputCheckDisabled(t *testing.T) {
	checker := &stubOutputChecker{metaRecord: OutputSafetyRecord{Decision: OutputQuarantine, Reason: "secret"}}
	bus := &fakeBus{}
	store := newSagaJobStore()
	store.states["job-out-disabled"] = JobStateRunning
	store.topics["job-out-disabled"] = "job.default"
	store.reqs["job-out-disabled"] = &pb.JobRequest{JobId: "job-out-disabled", Topic: "job.default", TenantId: "tenant-a"}

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(false)

	err := engine.handleJobResult(&pb.JobResult{JobId: "job-out-disabled", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-out-disabled"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	state, err := store.GetState(context.Background(), "job-out-disabled")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateSucceeded {
		t.Fatalf("expected state succeeded, got %s", state)
	}
	if checker.metaCalls.Load() != 0 {
		t.Fatalf("expected no output checks when disabled, got meta calls=%d", checker.metaCalls.Load())
	}
}

func TestHandleJobResultOutputCheckFailOpen(t *testing.T) {
	checker := &stubOutputChecker{
		metaErr:    errors.New("policy unavailable"),
		contentErr: errors.New("skip async"),
	}
	bus := &fakeBus{}
	store := newSagaJobStore()
	store.states["job-out-failopen"] = JobStateRunning
	store.topics["job-out-failopen"] = "job.default"
	store.reqs["job-out-failopen"] = &pb.JobRequest{JobId: "job-out-failopen", Topic: "job.default", TenantId: "tenant-a"}

	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true).
		WithAsyncFailMode("open")

	err := engine.handleJobResult(&pb.JobResult{JobId: "job-out-failopen", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-out-failopen"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	state, err := store.GetState(context.Background(), "job-out-failopen")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateSucceeded {
		t.Fatalf("expected fail-open succeeded state, got %s", state)
	}
	decision, err := store.GetOutputDecision(context.Background(), "job-out-failopen")
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if decision.Decision != "" {
		t.Fatalf("expected no persisted output decision on meta error, got %#v", decision)
	}
}

func TestHandleJobResultFailedJobSkipsOutputCheck(t *testing.T) {
	checker := &stubOutputChecker{metaRecord: OutputSafetyRecord{Decision: OutputQuarantine, Reason: "secret"}}
	store := newSagaJobStore()
	store.states["job-out-failed"] = JobStateRunning
	store.topics["job-out-failed"] = "job.default"
	store.reqs["job-out-failed"] = &pb.JobRequest{JobId: "job-out-failed", Topic: "job.default", TenantId: "tenant-a"}

	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	err := engine.handleJobResult(&pb.JobResult{JobId: "job-out-failed", Status: pb.JobStatus_JOB_STATUS_FAILED, ResultPtr: "redis://res:job-out-failed"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	if checker.metaCalls.Load() != 0 || checker.contentCalls.Load() != 0 {
		t.Fatalf("expected no output checks for failed result, got meta=%d content=%d", checker.metaCalls.Load(), checker.contentCalls.Load())
	}
	state, err := store.GetState(context.Background(), "job-out-failed")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != JobStateFailed {
		t.Fatalf("expected failed state, got %s", state)
	}
}

func TestHandleJobResultAsyncQuarantine(t *testing.T) {
	jobID := "job-out-async"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a", PrincipalId: "principal-a"}

	checker := &stubOutputChecker{
		metaRecord:    OutputSafetyRecord{Decision: OutputAllow, Reason: "ok"},
		contentRecord: OutputSafetyRecord{Decision: OutputQuarantine, Reason: "async secret"},
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	err := engine.handleJobResult(&pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-out-async"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := store.GetState(ctx, jobID)
		if state == JobStateQuarantined {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	state, _ := store.GetState(ctx, jobID)
	if state != JobStateQuarantined {
		t.Fatalf("expected async quarantine state, got %s", state)
	}
	decision, err := store.GetOutputDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if decision.Decision != OutputQuarantine {
		t.Fatalf("expected async output decision quarantine, got %#v", decision)
	}

	foundAsyncDLQ := false
	published := bus.snapshotPublished()
	for _, msg := range published {
		jr := msg.packet.GetJobResult()
		if msg.subject == capsdk.SubjectDLQ && jr != nil && jr.GetErrorCode() == outputPolicyAsync {
			foundAsyncDLQ = true
			break
		}
	}
	if !foundAsyncDLQ {
		t.Fatalf("expected async quarantine DLQ event, got %#v", published)
	}
	foundAsyncAudit := false
	for _, msg := range published {
		jp := msg.packet.GetJobProgress()
		if msg.subject == outputPolicyAudit && jp != nil && jp.GetJobId() == jobID {
			foundAsyncAudit = true
			break
		}
	}
	if !foundAsyncAudit {
		t.Fatalf("expected async quarantine audit event, got %#v", published)
	}
}

func TestHandleJobResultSecretContentEventuallyQuarantined(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer resultClient.Close()

	secret := []byte("leak AKIA1234567890ABCDEF in output")
	if err := resultClient.Set(context.Background(), "res:job-real-secret", secret, 0).Err(); err != nil {
		t.Fatalf("seed result content: %v", err)
	}

	fakePolicy := &fakeOutputPolicyClient{
		decide: func(req *pb.OutputCheckRequest) (*pb.OutputCheckResponse, error) {
			if bytes.Contains(req.GetOutputContent(), []byte("AKIA")) {
				return &pb.OutputCheckResponse{
					Decision: pb.OutputDecision_OUTPUT_DECISION_QUARANTINE,
					Reason:   "secret detected",
				}, nil
			}
			return &pb.OutputCheckResponse{
				Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW,
			}, nil
		},
	}
	checker := &OutputSafetyClient{
		client:       fakePolicy,
		resultClient: resultClient,
		cb: NewRedisCircuitBreaker(nil, "cordum:cb:safety:output:test", CircuitBreakerOpts{
			FailThreshold: outputCircuitFailBudget,
			OpenDuration:  outputCircuitOpenFor,
		}),
	}

	jobID := "job-real-secret"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a"}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	if err := engine.handleJobResult(&pb.JobResult{
		JobId:     jobID,
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:job-real-secret",
	}); err != nil {
		t.Fatalf("handle result: %v", err)
	}

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := store.GetState(ctx, jobID)
		if state == JobStateQuarantined {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	state, _ := store.GetState(ctx, jobID)
	if state != JobStateQuarantined {
		t.Fatalf("expected secret output to be quarantined, got %s", state)
	}
}

func TestHandleJobResultCleanContentRemainsAllowed(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer mr.Close()

	resultClient, err := redisutil.NewClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("new redis client: %v", err)
	}
	defer resultClient.Close()

	if err := resultClient.Set(context.Background(), "res:job-real-clean", []byte("safe output"), 0).Err(); err != nil {
		t.Fatalf("seed result content: %v", err)
	}

	fakePolicy := &fakeOutputPolicyClient{
		decide: func(req *pb.OutputCheckRequest) (*pb.OutputCheckResponse, error) {
			if bytes.Contains(req.GetOutputContent(), []byte("AKIA")) {
				return &pb.OutputCheckResponse{
					Decision: pb.OutputDecision_OUTPUT_DECISION_QUARANTINE,
					Reason:   "secret detected",
				}, nil
			}
			return &pb.OutputCheckResponse{
				Decision: pb.OutputDecision_OUTPUT_DECISION_ALLOW,
			}, nil
		},
	}
	checker := &OutputSafetyClient{
		client:       fakePolicy,
		resultClient: resultClient,
		cb: NewRedisCircuitBreaker(nil, "cordum:cb:safety:output:test", CircuitBreakerOpts{
			FailThreshold: outputCircuitFailBudget,
			OpenDuration:  outputCircuitOpenFor,
		}),
	}

	jobID := "job-real-clean"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a"}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	if err := engine.handleJobResult(&pb.JobResult{
		JobId:     jobID,
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: "redis://res:job-real-clean",
	}); err != nil {
		t.Fatalf("handle result: %v", err)
	}

	engine.wg.Wait()
	state, _ := store.GetState(context.Background(), jobID)
	if state != JobStateSucceeded {
		t.Fatalf("expected clean output to remain succeeded, got %s", state)
	}
	published := bus.snapshotPublished()
	for _, msg := range published {
		if msg.subject == capsdk.SubjectDLQ {
			t.Fatalf("expected no DLQ message for clean output, got %#v", msg.packet.GetJobResult())
		}
	}
}

func TestAsyncOutputCheckFailClosedQuarantinesOnError(t *testing.T) {
	jobID := "job-async-failclosed"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a"}

	checker := &stubOutputChecker{
		metaRecord: OutputSafetyRecord{Decision: OutputAllow, Reason: "ok"},
		contentErr: errors.New("service unavailable"),
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)
	// Default asyncFailMode is "" which means fail-closed (isAsyncFailClosed returns true)

	err := engine.handleJobResult(&pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-async-failclosed"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := store.GetState(ctx, jobID)
		if state == JobStateQuarantined {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	state, _ := store.GetState(ctx, jobID)
	if state != JobStateQuarantined {
		t.Fatalf("expected fail-closed to quarantine on async error, got %s", state)
	}
	decision, err := store.GetOutputDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if decision.Decision != OutputQuarantine {
		t.Fatalf("expected quarantine decision, got %#v", decision)
	}
	if decision.Phase != "async" {
		t.Fatalf("expected phase async, got %q", decision.Phase)
	}
}

func TestAsyncOutputCheckFailOpenAllowsOnError(t *testing.T) {
	jobID := "job-async-failopen"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a"}

	checker := &stubOutputChecker{
		metaRecord: OutputSafetyRecord{Decision: OutputAllow, Reason: "ok"},
		contentErr: errors.New("service unavailable"),
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true).
		WithAsyncFailMode("open")

	err := engine.handleJobResult(&pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-async-failopen"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	engine.wg.Wait()

	state, _ := store.GetState(context.Background(), jobID)
	if state != JobStateSucceeded {
		t.Fatalf("expected fail-open to keep succeeded on async error, got %s", state)
	}
	// Should NOT have quarantine DLQ events.
	published := bus.snapshotPublished()
	for _, msg := range published {
		if msg.subject == capsdk.SubjectDLQ {
			t.Fatalf("expected no DLQ on fail-open, got %#v", msg.packet.GetJobResult())
		}
	}
}

func TestAsyncOutputWaitGroupTracked(t *testing.T) {
	jobID := "job-async-wg"
	store := newSagaJobStore()
	store.states[jobID] = JobStateRunning
	store.topics[jobID] = "job.default"
	store.reqs[jobID] = &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "tenant-a"}

	// Slow content check to verify WaitGroup blocks Stop.
	checker := &stubOutputChecker{
		metaRecord:    OutputSafetyRecord{Decision: OutputAllow, Reason: "ok"},
		contentRecord: OutputSafetyRecord{Decision: OutputAllow, Reason: "clean"},
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithOutputChecker(checker).
		WithOutputSafetyEnabled(true)

	err := engine.handleJobResult(&pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://res:job-async-wg"})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}

	// engine.wg.Wait should return once async goroutine finishes.
	done := make(chan struct{})
	go func() {
		engine.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// WaitGroup properly tracked and completed.
	case <-time.After(3 * time.Second):
		t.Fatal("WaitGroup never completed — async goroutine leak")
	}

	if checker.contentCalls.Load() != 1 {
		t.Fatalf("expected 1 content call, got %d", checker.contentCalls.Load())
	}
}
