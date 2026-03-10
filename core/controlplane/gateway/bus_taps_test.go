package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

func TestStartBusTaps(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()

	engine := wf.NewEngine(s.workflowStore, bus).WithMemory(s.memStore)
	s.workflowEng = engine

	wfDef := &wf.Workflow{
		ID:    "wf-1",
		OrgID: "default",
		Steps: map[string]*wf.Step{
			"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
		},
	}
	if err := s.workflowStore.SaveWorkflow(ctx, wfDef); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &wf.WorkflowRun{
		ID:         "run-1",
		WorkflowID: wfDef.ID,
		OrgID:      "default",
		Steps:      map[string]*wf.StepRun{},
		Status:     wf.RunStatusPending,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := engine.StartRun(ctx, wfDef.ID, run.ID); err != nil {
		t.Fatalf("start run: %v", err)
	}

	jobID := "job-dlq-1"
	jobReq := &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "default"}
	if err := s.jobStore.SetJobMeta(ctx, jobReq); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, jobReq); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := s.jobStore.SetTopic(ctx, jobID, "job.default"); err != nil {
		t.Fatalf("set job topic: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("set job state: %v", err)
	}

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(s.stopBusTaps)

	bus.emit(capsdk.SubjectHeartbeat, &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "w1"}}})
	waitFor(t, 2*time.Second, 10*time.Millisecond, func() bool {
		s.workerMu.RLock()
		_, ok := s.workers["w1"]
		s.workerMu.RUnlock()
		return ok
	}, "expected worker heartbeat to register")

	bus.emit(capsdk.SubjectDLQ, &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_FAILED, ErrorMessage: "boom"}}})
	waitFor(t, 2*time.Second, 10*time.Millisecond, func() bool {
		entry, err := s.dlqStore.Get(ctx, jobID)
		return err == nil && entry != nil
	}, "expected dlq entry")

	bus.emit("sys.job.test", &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "run-1:step@1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}}})
	waitFor(t, 2*time.Second, 10*time.Millisecond, func() bool {
		updated, _ := s.workflowStore.GetRun(ctx, run.ID)
		return updated != nil && updated.Status == wf.RunStatusSucceeded
	}, "expected run succeeded")
}

// waitFor polls cond every interval until it returns true or timeout expires.
func waitFor(t *testing.T, timeout, interval time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatalf("timed out waiting: %s", msg)
}

func TestDLQSubscriptionUsesQueueGroup(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	// Verify DLQ subscription uses queue group "cordum-gateway" for write dedup.
	bus.mu.Lock()
	dlqGroups := bus.queueGroups[capsdk.SubjectDLQ]
	bus.mu.Unlock()

	if len(dlqGroups) != 1 {
		t.Fatalf("expected 1 DLQ subscription, got %d", len(dlqGroups))
	}
	if dlqGroups[0] != "cordum-gateway" {
		t.Fatalf("expected DLQ queue group 'cordum-gateway', got %q", dlqGroups[0])
	}

	// Verify sys.job.> (WS forwarding) uses broadcast (empty queue group).
	bus.mu.Lock()
	jobGroups := bus.queueGroups["sys.job.>"]
	bus.mu.Unlock()

	if len(jobGroups) != 1 {
		t.Fatalf("expected 1 sys.job.> subscription, got %d", len(jobGroups))
	}
	if jobGroups[0] != "" {
		t.Fatalf("expected sys.job.> broadcast (empty queue group), got %q", jobGroups[0])
	}
}

func TestDLQWriteOnlyOneReplicaViaQueueGroup(t *testing.T) {
	// Verify that DLQ persistence works through the queue-grouped subscription.
	s, bus, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	ctx := context.Background()

	jobID := "job-dlq-qg-1"
	jobReq := &pb.JobRequest{JobId: jobID, Topic: "job.default", TenantId: "default"}
	if err := s.jobStore.SetJobMeta(ctx, jobReq); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := s.jobStore.SetTopic(ctx, jobID, "job.default"); err != nil {
		t.Fatalf("set topic: %v", err)
	}

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	// Emit DLQ event — in a real multi-replica scenario, only one gateway gets it.
	bus.emit(capsdk.SubjectDLQ, &pb.BusPacket{Payload: &pb.BusPacket_JobResult{
		JobResult: &pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_FAILED, ErrorMessage: "test-fail"},
	}})

	waitFor(t, 2*time.Second, 10*time.Millisecond, func() bool {
		entry, err := s.dlqStore.Get(ctx, jobID)
		return err == nil && entry != nil
	}, "expected DLQ entry persisted via queue-grouped subscription")
}

func TestStartBusTapsStartsWorkerExpiry(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	if s.workerExpireStop != nil {
		t.Fatal("workerExpireStop should be nil before startBusTaps")
	}

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	if s.workerExpireStop == nil {
		t.Fatal("workerExpireStop should be set after startBusTaps")
	}
}

// TestBusJobHandlerRespectsShutdownCh verifies that the sys.job.> handler
// skips workflow result processing after shutdownCh is closed. Before the fix,
// the bus subscriber would start a 30s context.Background() handler even during
// shutdown, delaying graceful termination.
func TestBusJobHandlerRespectsShutdownCh(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})

	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}

	// Close shutdown channel BEFORE emitting a job event.
	close(s.shutdownCh)
	s.stopWorkerExpiry()

	// Emit a sys.job event after shutdown. The handler should return immediately
	// (no-op) instead of calling handleWorkflowJobResult with a 30s context.
	bus.emit("sys.job.result", &pb.BusPacket{Payload: &pb.BusPacket_JobResult{
		JobResult: &pb.JobResult{
			JobId:  "run-shutdown:step@1",
			Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		},
	}})

	// If the handler didn't respect shutdownCh, it would attempt to process the
	// workflow result, which could hang or error. We just verify no panic and
	// the handler returned within the test timeout.

	s.stopBusTaps()
}

func TestJobIDForBusPacket(t *testing.T) {
	cases := []struct {
		name   string
		packet *pb.BusPacket
		expect string
	}{
		{
			name:   "job_request",
			packet: &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{JobId: "job-1"}}},
			expect: "job-1",
		},
		{
			name:   "job_result",
			packet: &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "job-2"}}},
			expect: "job-2",
		},
		{
			name:   "job_progress",
			packet: &pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{JobId: "job-3"}}},
			expect: "job-3",
		},
		{
			name:   "job_cancel",
			packet: &pb.BusPacket{Payload: &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: "job-4"}}},
			expect: "job-4",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jobIDForBusPacket(tc.packet); got != tc.expect {
				t.Fatalf("expected %s, got %s", tc.expect, got)
			}
		})
	}
}
