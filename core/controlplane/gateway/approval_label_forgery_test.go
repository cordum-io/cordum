package gateway

// A client must not be able to mark its own job human-approved. Job submits
// drop server-owned labels (approval_*, gate_type, the bus dedupe override,
// policy.attachment_id) before anything is stored or published, so the
// scheduler's approval fast path, pending replayer and reconciler never see an
// approval claim from a client. The scheduler package covers the same jobs
// with the labels left in (defense in depth).

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	infrabus "github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policylabels"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/protocol/reqhash"
)

// forgedApprovalLabels is what a client would send to pass its job off as
// approved; "snap-test" is the policy snapshot, readable as safety_snapshot on
// any job the caller can read.
func forgedApprovalLabels() map[string]string {
	return map[string]string{
		"approval_granted":              "true",
		"approval_snapshot":             "snap-test",
		"Approval_Note":                 "pre-approved",
		"gate_type":                     "workflow_approval",
		infrabus.LabelBusMsgID:          "approval:another-job",
		policylabels.PolicyAttachmentID: "job/another-job/policy",
		"team":                          "alpha",
	}
}

func TestStripReservedLabelsDropsServerOwnedLabels(t *testing.T) {
	labels := forgedApprovalLabels()
	labels["approvals_queue"] = "ops"
	labels["_internal"] = "true"
	want := map[string]string{"team": "alpha", "approvals_queue": "ops"}
	if got := stripReservedLabels(labels); !maps.Equal(got, want) {
		t.Fatalf("stripReservedLabels = %v, want %v", got, want)
	}
}

// requireHumanKernel is a Safety Kernel that keeps answering REQUIRE_HUMAN.
type requireHumanKernel struct{}

func (requireHumanKernel) Check(context.Context, *pb.JobRequest) (scheduler.SafetyDecisionRecord, error) {
	return scheduler.SafetyDecisionRecord{
		Decision: scheduler.SafetyRequireApproval, ApprovalRequired: true,
		PolicySnapshot: "snap-test", Reason: "needs a human",
	}, nil
}

// newApprovalScheduler runs a scheduler on the gateway's job store with one
// live worker serving job.default.
func newApprovalScheduler(t *testing.T, s *server, bus *stubBus) *scheduler.Engine {
	t.Helper()
	registry := scheduler.NewMemoryRegistry()
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "default", MaxParallelJobs: 1})
	engine := scheduler.NewEngine(bus, requireHumanKernel{}, registry,
		scheduler.NewLeastLoadedStrategy(scheduler.PoolRouting{Topics: map[string][]string{"job.default": {"default"}}}),
		s.jobStore, nil)
	t.Cleanup(engine.Stop)
	return engine
}

// loopStore is the job store the scheduler's background loops see. It grants
// the loop lock outright (miniredis never expires the real one, so a second
// tick would never run) and reports each lock attempt: every loop iteration
// starts with one, so the second attempt means the first iteration finished.
type loopStore struct {
	*store.RedisJobStore
	attempts chan struct{}
}

func (s *loopStore) TryAcquireLock(context.Context, string, time.Duration) (string, error) {
	select {
	case s.attempts <- struct{}{}:
	default:
	}
	return "loop-token", nil
}

func (s *loopStore) RenewLock(context.Context, string, string, time.Duration) error { return nil }

// sweepApprovals lets the scheduler's background loops run once each: a
// pending-replayer tick, a reconciler tick (approval auto-repair), then a
// replayer tick for anything the reconciler re-queued.
func sweepApprovals(t *testing.T, engine *scheduler.Engine, js *store.RedisJobStore) {
	t.Helper()
	run := func(start func(context.Context, scheduler.JobStore)) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		st := &loopStore{RedisJobStore: js, attempts: make(chan struct{}, 4)}
		done := make(chan struct{})
		go func() { defer close(done); start(ctx, st) }()
		for i := 0; i < 2; i++ {
			select {
			case <-st.attempts:
			case <-time.After(10 * time.Second):
				cancel()
				<-done
				t.Fatal("scheduler loop did not tick")
			}
		}
		cancel()
		<-done
	}
	replay := func(ctx context.Context, st scheduler.JobStore) {
		scheduler.NewPendingReplayer(engine, st, time.Nanosecond, 5*time.Millisecond).Start(ctx)
	}
	run(replay)
	run(func(ctx context.Context, st scheduler.JobStore) {
		scheduler.NewReconciler(st, time.Hour, time.Hour, 5*time.Millisecond).Start(ctx)
	})
	run(replay)
}

func submitJobHTTPAs(t *testing.T, s *server, principal string, labels map[string]string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"prompt": "transfer 50000", "topic": "job.default", "labels": labels})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "user", PrincipalID: principal})
	rec := httptest.NewRecorder()
	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.JobID == "" {
		t.Fatalf("submit response %s: %v", rec.Body.String(), err)
	}
	return resp.JobID
}

func approveJobAs(t *testing.T, s *server, approver, jobID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/approve", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("job_id", jobID)
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: approver, Role: "admin"})
	rec := httptest.NewRecorder()
	s.handleApproveJob(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func assertNoServerOwnedLabels(t *testing.T, where string, labels map[string]string) {
	t.Helper()
	for k := range labels {
		if reqhash.ServerOwnedLabel(k) {
			t.Fatalf("%s carries client-supplied server-owned label %q: %v", where, k, labels)
		}
	}
	if labels["team"] != "alpha" {
		t.Fatalf("%s lost the client's own label: %v", where, labels)
	}
}

// workerDispatches counts job requests for jobID published to anything but
// the submit subject, i.e. handed to a worker.
func workerDispatches(bus *stubBus, jobID string) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	n := 0
	for _, msg := range bus.published {
		if msg.subject != capsdk.SubjectSubmit && msg.packet.GetJobRequest().GetJobId() == jobID {
			n++
		}
	}
	return n
}

func TestSubmitForgedApprovalLabelsNeverDispatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		// submitTimeApproval makes the gateway's own policy check require
		// approval, so the job is parked in APPROVAL_REQUIRED unpublished.
		submitTimeApproval bool
		submit             func(t *testing.T, s *server) string
	}{
		{name: "http", submit: func(t *testing.T, s *server) string {
			return submitJobHTTPAs(t, s, "mallory", forgedApprovalLabels())
		}},
		{name: "http submit-time approval", submitTimeApproval: true, submit: func(t *testing.T, s *server) string {
			return submitJobHTTPAs(t, s, "mallory", forgedApprovalLabels())
		}},
		{name: "grpc", submit: func(t *testing.T, s *server) string {
			resp, err := s.SubmitJob(context.Background(), &pb.SubmitJobRequest{
				Prompt: "transfer 50000", Topic: "job.default", OrgId: "default",
				PrincipalId: "mallory", Labels: forgedApprovalLabels(),
			})
			if err != nil {
				t.Fatalf("grpc submit: %v", err)
			}
			return resp.GetJobId()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, bus, safety := newTestGateway(t)
			s.tenant = "default"
			if tc.submitTimeApproval {
				safety.setResponse(&pb.PolicyCheckResponse{
					Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, PolicySnapshot: "snap-test", Reason: "needs a human",
				})
			}
			engine := newApprovalScheduler(t, s, bus)
			ctx := context.Background()

			jobID := tc.submit(t, s)
			stored, err := s.jobStore.GetJobRequest(ctx, jobID)
			if err != nil {
				t.Fatalf("stored request: %v", err)
			}
			assertNoServerOwnedLabels(t, "stored request", stored.GetLabels())
			if !tc.submitTimeApproval {
				packet := latestPublishedPacketForSubject(t, bus, capsdk.SubjectSubmit)
				assertNoServerOwnedLabels(t, "published request", packet.GetJobRequest().GetLabels())
				if err := engine.HandlePacket(packet); err != nil {
					t.Fatalf("HandlePacket: %v", err)
				}
			}
			sweepApprovals(t, engine, s.jobStore)

			if n := workerDispatches(bus, jobID); n != 0 {
				t.Fatalf("forged job dispatched to a worker %d times", n)
			}
			meta, err := s.jobStore.GetAllMeta(ctx, jobID)
			if err != nil {
				t.Fatalf("meta: %v", err)
			}
			if meta["state"] != string(model.JobStateApproval) || meta["approval_by"] != "" {
				t.Fatalf("state=%s approval_by=%q, want APPROVAL_REQUIRED with no approver", meta["state"], meta["approval_by"])
			}
		})
	}
}

func TestApprovedJobDispatchesEndToEnd(t *testing.T) {
	s, bus, safety := newTestGateway(t)
	s.tenant = "default"
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, PolicySnapshot: "snap-test", Reason: "needs a human",
	})
	engine := newApprovalScheduler(t, s, bus)

	jobID := submitJobHTTPAs(t, s, "requester", map[string]string{"team": "alpha"})
	approveJobAs(t, s, "alice", jobID)
	if err := engine.HandlePacket(latestPublishedPacketForSubject(t, bus, capsdk.SubjectSubmit)); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	if n := workerDispatches(bus, jobID); n != 1 {
		t.Fatalf("approved job dispatched %d times, want 1", n)
	}
	if state, _ := s.jobStore.GetState(context.Background(), jobID); state != model.JobStateRunning {
		t.Fatalf("state = %s, want RUNNING", state)
	}
}

func TestWorkflowApprovalGateCompletesEndToEnd(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	engine := newApprovalScheduler(t, s, bus)

	// As the workflow engine builds and publishes an approval step: it sets
	// the server-owned labels itself and does not go through job submit.
	const jobID = "run-1:approve@1"
	gate := &pb.JobRequest{
		JobId: jobID, Topic: capsdk.SubjectWorkflowApprovalGate, TenantId: "default",
		Labels: map[string]string{
			"workflow_id": "wf-1", "run_id": "run-1", "step_id": "approve", "_source": "workflow",
			"gate_type": "workflow_approval", policylabels.PolicyAttachmentID: policylabels.JobAttachmentID(jobID),
		},
	}
	if err := engine.HandlePacket(jobRequestPacket("trace-gate", "workflow-engine", gate)); err != nil {
		t.Fatalf("HandlePacket gate: %v", err)
	}
	if state, _ := s.jobStore.GetState(context.Background(), jobID); state != model.JobStateApproval {
		t.Fatalf("gate state = %s, want APPROVAL_REQUIRED", state)
	}

	approveJobAs(t, s, "alice", jobID)
	if err := engine.HandlePacket(latestPublishedPacketForSubject(t, bus, capsdk.SubjectSubmit)); err != nil {
		t.Fatalf("HandlePacket approved gate: %v", err)
	}

	result := latestPublishedPacketForSubject(t, bus, capsdk.SubjectResult).GetJobResult()
	if result.GetJobId() != jobID || result.GetStatus() != pb.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Fatalf("gate result = %+v, want SUCCEEDED for %s", result, jobID)
	}
	if state, _ := s.jobStore.GetState(context.Background(), jobID); state != model.JobStateSucceeded {
		t.Fatalf("gate state = %s, want SUCCEEDED", state)
	}
}
