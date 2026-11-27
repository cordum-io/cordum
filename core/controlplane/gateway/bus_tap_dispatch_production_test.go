package gateway

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"google.golang.org/protobuf/proto"
)

// TestBusTapProductionJobResultDispatch checks the production profile's rule
// for a worker session's workflow step result on the gateway's stream taps:
// the result counts only when a dispatch record names that worker in its
// tenant. A same-tenant worker's result for a step job with no dispatch record
// is refused on both taps and has no effect, even when it carries the run's
// identity. The assigned worker's result completes its step. Not parallel: it
// swaps the default logger.
func TestBusTapProductionJobResultDispatch(t *testing.T) {
	var logs syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, c := range []struct {
		name   string
		step   *wf.Step
		state  model.JobState
		status wf.StepStatus
		owner  bool
	}{
		{"worker_step", &wf.Step{ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"}, "", wf.StepStatusRunning, true},
		{"approval_step", &wf.Step{ID: "step", Type: wf.StepTypeApproval}, model.JobStateApproval, wf.StepStatusWaiting, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, bus := busTapGateway(t, "", true)
			run, job := busTapProductionRun(t, s, bus, c.step, c.status)
			if c.state != "" {
				if err := s.jobStore.SetState(ctx, job, c.state); err != nil {
					t.Fatal(err)
				}
			}
			handlers := map[string]func(*pb.BusPacket) error{}
			bus.mu.Lock()
			for _, subject := range []string{"sys.job.>", "sys.audit.>"} {
				if len(bus.subs[subject]) == 1 {
					handlers[subject] = bus.subs[subject][0]
				}
			}
			bus.mu.Unlock()
			if len(handlers) != 2 {
				t.Fatalf("stream tap handlers=%d, want one on each of sys.job.> and sys.audit.>", len(handlers))
			}
			listener := registerGatewayEdgeStreamClient(t, s, "tenant-b", false)
			relayed := func() int { return (&audienceMatrix{s: s}).matrixRelayed(t, listener, job) }
			result := func(worker string) func() *pb.BusPacket {
				build := busTapDispatchResult(t, s, worker, "tenant-b")
				return func() *pb.BusPacket {
					p := build(job, pb.JobStatus_JOB_STATUS_SUCCEEDED)
					p.GetJobResult().Identity = proto.Clone(run.Identity).(*pb.IdentityBinding)
					return p
				}
			}
			workerB, workerB2 := result("worker-b"), result("worker-b2")

			for _, tap := range []struct{ subject, name string }{{"sys.job.>", busTapJobStream}, {"sys.audit.>", busTapAuditStream}} {
				label := tap.name + ": tenant-b worker, no dispatch record"
				line := `level=WARN msg="bus tap packet refused" tap=` + tap.name + " reason=dispatch\n"
				keys, published, logged := snapshotPackKeyspace(t, s), storeWriterPublished(bus), strings.Count(logs.String(), line)
				before, err := s.workflowStore.GetRun(ctx, run.ID)
				if err != nil {
					t.Fatalf("%s: read run: %v", label, err)
				}
				if err := handlers[tap.subject](workerB2()); err != nil {
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
				if n := relayed(); n != 0 {
					t.Errorf("%s: tenant-b listener got %d events", label, n)
				}
				if n := strings.Count(logs.String(), line) - logged; n != 1 {
					t.Errorf("%s: logged %d %q lines, want 1", label, n, strings.TrimSpace(line))
				}
			}
			if !c.owner {
				return
			}

			if err := s.jobStore.SetState(ctx, job, model.JobStateRunning); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.jobStore.BeginDispatch(ctx, job, "worker-b", "tenant-b"); err != nil {
				t.Fatal(err)
			}
			if err := handlers["sys.job.>"](workerB()); err != nil {
				t.Fatalf("assigned worker's result: handler returned %v", err)
			}
			after, err := s.workflowStore.GetRun(ctx, run.ID)
			if err != nil || after.Status != wf.RunStatusSucceeded || after.Steps["step"].Status != wf.StepStatusSucceeded {
				t.Fatalf("assigned worker's result: run=%v err=%v", after, err)
			}
			if n := relayed(); n != 1 {
				t.Fatalf("assigned worker's result relayed %d events to tenant-b, want 1", n)
			}
		})
	}
}

// busTapProductionRun starts a one-step tenant-b run whose step is step, on a
// workflow engine wired as the production gateway wires it, and records the
// step job's tenant as the scheduler does at submit. It fails unless the step
// has a job and the status want, so a broken fixture never reads as a refusal.
func busTapProductionRun(t *testing.T, s *server, bus *stubBus, step *wf.Step, want wf.StepStatus) (*wf.WorkflowRun, string) {
	t.Helper()
	ctx := context.Background()
	s.workflowEng = wf.NewEngine(s.workflowStore, bus).WithMemory(s.memStore).WithProductionIdentityEnforcement(true)
	definition := &wf.Workflow{ID: "wf-production", OrgID: "tenant-b", Steps: map[string]*wf.Step{"step": step}}
	if err := s.workflowStore.SaveWorkflow(ctx, definition); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := &wf.WorkflowRun{ID: "run-production", WorkflowID: definition.ID, OrgID: "tenant-b",
		Identity: &pb.IdentityBinding{TenantId: "tenant-b", PrincipalId: "principal-b", ActorId: "principal-b"},
		Steps:    map[string]*wf.StepRun{}, Status: wf.RunStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.workflowEng.StartRun(ctx, definition.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	started, err := s.workflowStore.GetRun(ctx, run.ID)
	if err != nil || started == nil || started.Steps["step"] == nil || started.Steps["step"].JobID == "" || started.Steps["step"].Status != want {
		t.Fatalf("expected a %s step with a job: run=%+v err=%v", want, started, err)
	}
	job := started.Steps["step"].JobID
	if err := s.jobStore.SetJobMeta(ctx, &pb.JobRequest{JobId: job, TenantId: "tenant-b"}); err != nil {
		t.Fatalf("record job tenant: %v", err)
	}
	return started, job
}
