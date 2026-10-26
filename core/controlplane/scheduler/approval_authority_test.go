package scheduler

// Approval labels are a lookup hint, never authority. A job that carries
// approval_granted/approval_snapshot without a recorded human approval must
// not reach a worker through the fast path, the pending replayer, or the
// reconciler's approval auto-repair. Labels can still arrive that way from
// any path that skips the gateway's label stripping (a direct sys.job.submit
// publisher when handshakes are off or in warn mode, a DLQ retry that copies
// the original job's labels).

import (
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

const approvalTestSnapshot = "snap-1"

// approvalHarness is a scheduler on a real Redis job store with a kernel that
// keeps answering REQUIRE_HUMAN and one live worker serving job.default.
type approvalHarness struct {
	t      *testing.T
	store  *infraStore.RedisJobStore
	bus    *fakeBus
	engine *Engine
}

func newApprovalHarness(t *testing.T, routing PoolRouting) *approvalHarness {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	js, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	t.Cleanup(func() { _ = js.Close() })
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Type: "cpu", Pool: "default"})
	kernel := &fixedSafetyRecordChecker{record: SafetyDecisionRecord{
		Decision: SafetyRequireApproval, ApprovalRequired: true,
		PolicySnapshot: approvalTestSnapshot, Reason: "needs a human",
	}}
	bus := &fakeBus{}
	engine := NewEngine(bus, kernel, registry, NewLeastLoadedStrategy(routing), js, nil)
	return &approvalHarness{t: t, store: js, bus: bus, engine: engine}
}

func defaultRouting() PoolRouting {
	return PoolRouting{
		Topics: map[string][]string{"job.default": {"default"}},
		Pools:  map[string]PoolProfile{"default": {}},
	}
}

func (h *approvalHarness) must(step string, err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatalf("%s: %v", step, err)
	}
}

// awaitApproval stores req the way the gateway's submit-time REQUIRE_HUMAN
// path does: APPROVAL_REQUIRED with the job hash and snapshot pinned.
func (h *approvalHarness) awaitApproval(req *pb.JobRequest) {
	h.t.Helper()
	ctx := testCtx(h.t)
	h.must("state", h.store.SetState(ctx, req.GetJobId(), model.JobStateApproval))
	h.must("meta", h.store.SetJobMeta(ctx, req))
	h.must("request", h.store.SetJobRequest(ctx, req))
	hash, err := HashJobRequest(req)
	h.must("hash", err)
	h.must("safety", h.store.SetSafetyDecision(ctx, req.GetJobId(), SafetyDecisionRecord{
		Decision: SafetyRequireApproval, ApprovalRequired: true,
		PolicySnapshot: approvalTestSnapshot, JobHash: hash,
	}))
}

// approve resolves the approval as handleApproveJob does and returns the
// request it republishes.
func (h *approvalHarness) approve(jobID string) *pb.JobRequest {
	h.t.Helper()
	resolved, err := h.store.ResolveApproval(testCtx(h.t), infraStore.ApprovalResolutionParams{
		JobID: jobID, Decision: model.ApprovalDecisionApprove, ResultState: model.JobStatePending,
		ApprovedBy: "alice", PolicySnapshot: approvalTestSnapshot,
		LabelUpdates: map[string]string{"approval_granted": "true", "approval_snapshot": approvalTestSnapshot},
	})
	h.must("approve", err)
	return resolved.Request
}

// sweep runs the background loops a live scheduler runs between deliveries:
// the replayer's PENDING and APPROVAL_REQUIRED passes, the reconciler's
// approval auto-repair, then the PENDING pass again for anything re-queued.
func (h *approvalHarness) sweep() {
	h.t.Helper()
	ctx := testCtx(h.t)
	replayer := NewPendingReplayer(h.engine, h.store, time.Nanosecond, time.Minute)
	later := time.Now().Add(time.Hour)
	replayer.replayPending(ctx, later)
	replayer.replayApproved(ctx, later)
	NewReconciler(h.store, time.Hour, time.Hour, time.Hour).handleApprovalRepairs(ctx, time.Now())
	replayer.replayPending(ctx, later)
}

func (h *approvalHarness) dispatches(jobID string) int {
	n := 0
	for _, msg := range h.bus.snapshotPublished() {
		if msg.packet.GetJobRequest().GetJobId() == jobID {
			n++
		}
	}
	return n
}

func (h *approvalHarness) meta(jobID string) map[string]string {
	h.t.Helper()
	meta, err := h.store.GetAllMeta(testCtx(h.t), jobID)
	h.must("meta", err)
	return meta
}

func TestForgedApprovalLabelsNeverDispatch(t *testing.T) {
	forged := func(id string) *pb.JobRequest {
		// approval_snapshot is readable as safety_snapshot on any job the
		// caller can read, so assume the forger has the current one.
		return &pb.JobRequest{JobId: id, Topic: "job.default", Labels: map[string]string{
			"approval_granted": "true", "approval_snapshot": approvalTestSnapshot,
		}}
	}
	t.Run("scheduler requires approval", func(t *testing.T) {
		h := newApprovalHarness(t, defaultRouting())
		req := forged("forged-sched")
		h.must("submit", h.engine.handleJobRequest(proto.Clone(req).(*pb.JobRequest), "trace"))
		h.sweep()
		// A redelivery of the forged request takes the fast path again.
		_ = h.engine.handleJobRequest(proto.Clone(req).(*pb.JobRequest), "trace")
		assertNotApproved(t, h, req.GetJobId())
	})
	t.Run("submit-time approval", func(t *testing.T) {
		h := newApprovalHarness(t, defaultRouting())
		req := forged("forged-submit")
		h.awaitApproval(req)
		h.sweep()
		assertNotApproved(t, h, req.GetJobId())
	})
}

func assertNotApproved(t *testing.T, h *approvalHarness, jobID string) {
	t.Helper()
	if n := h.dispatches(jobID); n != 0 {
		t.Fatalf("forged job dispatched %d times", n)
	}
	meta := h.meta(jobID)
	if meta["state"] != string(model.JobStateApproval) {
		t.Fatalf("state = %s, want %s", meta["state"], model.JobStateApproval)
	}
	if meta["approval_by"] != "" || meta["approval_decision"] != "" {
		t.Fatalf("approval minted without a human: by=%q decision=%q", meta["approval_by"], meta["approval_decision"])
	}
}

func TestRecordedApprovalDispatches(t *testing.T) {
	h := newApprovalHarness(t, defaultRouting())
	const id = "approved-1"
	h.awaitApproval(&pb.JobRequest{JobId: id, Topic: "job.default"})
	h.must("republish", h.engine.handleJobRequest(h.approve(id), "trace"))
	if n := h.dispatches(id); n != 1 {
		t.Fatalf("approved job dispatched %d times, want 1", n)
	}
	if state := h.meta(id)["state"]; state != string(model.JobStateRunning) {
		t.Fatalf("state = %s, want RUNNING", state)
	}
}

func TestApprovalBinding(t *testing.T) {
	req := &pb.JobRequest{JobId: "j", Topic: "job.default", Labels: map[string]string{
		"approval_granted": "true", "approval_snapshot": "snap-1|cfg:a",
	}}
	hash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	pinned := SafetyDecisionRecord{Decision: SafetyRequireApproval, ApprovalRequired: true, PolicySnapshot: "snap-1|cfg:b", JobHash: hash}
	drifted := pinned
	drifted.PolicySnapshot = "snap-2"
	changed := proto.Clone(req).(*pb.JobRequest)
	changed.Labels["amount"] = "50000"
	gate := &pb.JobRequest{JobId: "g", Topic: capsdk.SubjectWorkflowApprovalGate, Labels: map[string]string{"approval_granted": "true"}}
	gateHash, err := HashJobRequest(gate)
	if err != nil {
		t.Fatalf("hash gate: %v", err)
	}
	gatePinned := SafetyDecisionRecord{Decision: SafetyRequireApproval, ApprovalRequired: true, JobHash: gateHash}
	approved := ApprovalRecord{Decision: ApprovalDecisionApprove, ApprovedBy: "alice"}

	for _, tc := range []struct {
		name     string
		req      *pb.JobRequest
		prev     SafetyDecisionRecord
		approval ApprovalRecord
		want     error
	}{
		{"recorded approval", req, pinned, approved, nil},
		{"labels only", req, pinned, ApprovalRecord{}, errNoRecordedApproval},
		{"rejected", req, pinned, ApprovalRecord{Decision: ApprovalDecisionReject, ApprovedBy: "bob"}, errNoRecordedApproval},
		{"approve without approver", req, pinned, ApprovalRecord{Decision: ApprovalDecisionApprove}, errNoRecordedApproval},
		{"request changed", changed, pinned, approved, errApprovalHashMismatch},
		{"snapshot drifted", req, drifted, approved, errApprovalSnapshotMismatch},
		{"workflow gate skips snapshot", gate, gatePinned, approved, nil},
		{"workflow gate still needs a human", gate, gatePinned, ApprovalRecord{}, errNoRecordedApproval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalBinding(tc.req, tc.prev, tc.approval); !errors.Is(got, tc.want) {
				t.Fatalf("approvalBinding = %v, want %v", got, tc.want)
			}
		})
	}
}
