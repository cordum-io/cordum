package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
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
	if err := s.jobStore.SetState(ctx, jobID, scheduler.JobStateRunning); err != nil {
		t.Fatalf("set job state: %v", err)
	}

	s.startBusTaps()
	t.Cleanup(func() { close(s.eventsCh) })

	bus.emit(capsdk.SubjectHeartbeat, &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "w1"}}})
	s.workerMu.RLock()
	_, ok := s.workers["w1"]
	s.workerMu.RUnlock()
	if !ok {
		t.Fatalf("expected worker heartbeat to register")
	}

	bus.emit(capsdk.SubjectDLQ, &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: jobID, Status: pb.JobStatus_JOB_STATUS_FAILED, ErrorMessage: "boom"}}})
	entry, err := s.dlqStore.Get(ctx, jobID)
	if err != nil || entry == nil {
		t.Fatalf("expected dlq entry, err=%v", err)
	}

	bus.emit("sys.job.test", &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "run-1:step@1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}}})
	updated, _ := s.workflowStore.GetRun(ctx, run.ID)
	if updated.Status != wf.RunStatusSucceeded {
		t.Fatalf("expected run succeeded, got %s", updated.Status)
	}
}
