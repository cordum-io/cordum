package gateway

import (
	"context"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

// TestBusTapDispatchJobResult checks the rule for a worker session's job result
// on the gateway's sys.job.> tap: it changes a workflow run, a stored key or a
// tenant's stream only when the job is recorded under the session's tenant and,
// once the job has a dispatch record, is dispatched to that worker. Any other
// result is acknowledged and has no effect. The assigned worker's own result
// completes the step.
func TestBusTapDispatchJobResult(t *testing.T) {
	for _, c := range []struct {
		name       string
		mode       scheduler.HandshakeMode
		production bool
	}{
		{"enforce", scheduler.HandshakeModeEnforce, false},
		{"production", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, bus := busTapGateway(t, c.mode, c.production)
			run := busTapWorkflow(t, s, bus, "tenant-b")
			job := run.Steps["step"].JobID
			bus.mu.Lock()
			handlers := bus.subs["sys.job.>"]
			bus.mu.Unlock()
			if len(handlers) != 1 {
				t.Fatalf("sys.job.> handlers=%d, want 1", len(handlers))
			}
			listeners := map[string]*wsClient{
				"tenant-a": registerGatewayEdgeStreamClient(t, s, "tenant-a", false),
				"tenant-b": registerGatewayEdgeStreamClient(t, s, "tenant-b", false),
			}
			relayed := func(tenant string) int {
				return (&audienceMatrix{s: s}).matrixRelayed(t, listeners[tenant], job)
			}
			workerA := busTapDispatchResult(t, s, "worker-a", "tenant-a")
			workerB := busTapDispatchResult(t, s, "worker-b", "tenant-b")
			workerB2 := busTapDispatchResult(t, s, "worker-b2", "tenant-b")
			noEffect := func(label string, p *pb.BusPacket) {
				t.Helper()
				keys, published := snapshotPackKeyspace(t, s), storeWriterPublished(bus)
				before, err := s.workflowStore.GetRun(ctx, run.ID)
				if err != nil {
					t.Fatalf("%s: read run: %v", label, err)
				}
				if err := handlers[0](p); err != nil {
					t.Errorf("%s: handler returned %v, want nil", label, err)
				}
				if diff := diffPackKeyspace(keys, snapshotPackKeyspace(t, s)); len(diff) != 0 {
					t.Errorf("%s: keys changed: %v", label, diff)
				}
				if n := storeWriterPublished(bus) - published; n != 0 {
					t.Errorf("%s: published %d messages", label, n)
				}
				if after, err := s.workflowStore.GetRun(ctx, run.ID); err != nil || !reflect.DeepEqual(before, after) {
					t.Errorf("%s: run changed (read error %v)", label, err)
				}
				for _, tenant := range []string{"tenant-a", "tenant-b"} {
					if n := relayed(tenant); n != 0 {
						t.Errorf("%s: %s listener got %d events", label, tenant, n)
					}
				}
			}

			statuses := []pb.JobStatus{pb.JobStatus_JOB_STATUS_SUCCEEDED, pb.JobStatus_JOB_STATUS_FAILED}
			for _, status := range statuses {
				noEffect("tenant-a worker, no dispatch record, "+status.String(), workerA(job, status))
			}
			if err := s.jobStore.SetState(ctx, job, model.JobStateRunning); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.jobStore.BeginDispatch(ctx, job, "worker-b", "tenant-b"); err != nil {
				t.Fatal(err)
			}
			for _, status := range statuses {
				noEffect("tenant-a worker, dispatched to worker-b, "+status.String(), workerA(job, status))
			}
			noEffect("tenant-b worker, dispatched to worker-b", workerB2(job, pb.JobStatus_JOB_STATUS_SUCCEEDED))

			if err := handlers[0](workerB(job, pb.JobStatus_JOB_STATUS_SUCCEEDED)); err != nil {
				t.Fatalf("assigned worker's result: handler returned %v", err)
			}
			after, err := s.workflowStore.GetRun(ctx, run.ID)
			if err != nil || after.Status != wf.RunStatusSucceeded || after.Steps["step"].Status != wf.StepStatusSucceeded {
				t.Fatalf("assigned worker's result: run=%v err=%v", after, err)
			}
			if a, b := relayed("tenant-a"), relayed("tenant-b"); a != 0 || b != 1 {
				t.Fatalf("assigned worker's result relayed to tenant-a=%d tenant-b=%d, want 0 and 1", a, b)
			}
		})
	}
}

// busTapDispatchResult mints worker's bound session in tenant once and returns
// a builder for that worker's job results.
func busTapDispatchResult(t *testing.T, s *server, worker, tenant string) func(job string, status pb.JobStatus) *pb.BusPacket {
	t.Helper()
	token, _, err := s.sessionIssuer.IssueBound(context.Background(), scheduler.SessionBinding{
		WorkerID: worker, AgentID: "agent-" + worker, Tenant: tenant,
		Audience: scheduler.WorkerHandshakeAudience, ProofKeyID: "proof-" + worker, SDKVersion: "test-sdk",
	})
	if err != nil {
		t.Fatalf("session for %s: %v", worker, err)
	}
	return func(job string, status pb.JobStatus) *pb.BusPacket {
		return &pb.BusPacket{SenderId: worker, AuthToken: token, Payload: &pb.BusPacket_JobResult{
			JobResult: &pb.JobResult{JobId: job, WorkerId: worker, Status: status},
		}}
	}
}
