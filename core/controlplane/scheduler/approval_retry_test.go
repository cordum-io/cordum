package scheduler

// An approved job keeps its approval when a dispatch fails and is retried. It
// stays PENDING, and each retry takes the approval fast path again instead of
// asking the kernel, which would put it back in APPROVAL_REQUIRED.

import (
	"maps"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

// reportsRouting maps job.reports to a pool that starts with no workers.
func reportsRouting() PoolRouting {
	return PoolRouting{
		Topics: map[string][]string{"job.reports": {"reports"}},
		Pools:  map[string]PoolProfile{"reports": {}},
	}
}

// approveWithoutWorkers approves a job.reports job and delivers it while the
// reports pool is empty, so its first dispatch fails with ErrNoWorkers. It
// returns the request the approve path republished.
func (h *approvalHarness) approveWithoutWorkers(id string) *pb.JobRequest {
	h.t.Helper()
	h.awaitApproval(&pb.JobRequest{JobId: id, Topic: "job.reports"})
	approved := h.approve(id)
	h.must("first delivery", h.engine.handleJobRequest(proto.Clone(approved).(*pb.JobRequest), "trace"))
	if attempts, err := h.store.GetAttempts(testCtx(h.t), id); err != nil || attempts == 0 {
		h.t.Fatalf("no failed dispatch recorded: attempts=%d err=%v", attempts, err)
	}
	h.assertStillApproved(id)
	return approved
}

func (h *approvalHarness) addReportsWorker() {
	h.registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w2", Type: "cpu", Pool: "reports"})
}

// assertStillApproved fails if the job was dispatched or re-gated. The
// harness kernel answers REQUIRE_HUMAN, so any re-evaluation shows up here.
func (h *approvalHarness) assertStillApproved(id string) {
	h.t.Helper()
	if n := h.dispatches(id); n != 0 {
		h.t.Fatalf("dispatched %d times with no worker", n)
	}
	meta := h.meta(id)
	if meta["state"] != string(model.JobStatePending) ||
		meta["approval_status"] != string(model.ApprovalStatusApproved) ||
		meta["safety_decision"] != string(SafetyAllow) {
		h.t.Fatalf("state=%s approval_status=%s safety_decision=%s, want PENDING/approved/ALLOW",
			meta["state"], meta["approval_status"], meta["safety_decision"])
	}
}

func TestApprovedJobSurvivesFailedDispatch(t *testing.T) {
	h := newApprovalHarness(t, reportsRouting())
	const id = "approved-no-workers"
	h.approveWithoutWorkers(id)
	for range 3 {
		h.sweep()
		h.assertStillApproved(id)
	}
	h.addReportsWorker()
	h.sweep()
	if n := h.dispatches(id); n != 1 {
		t.Fatalf("dispatched %d times after a worker joined, want 1", n)
	}
	if state := h.meta(id)["state"]; state != string(model.JobStateRunning) {
		t.Fatalf("state = %s, want RUNNING", state)
	}
}

// An approval that stops binding is not honored, and it settles instead of
// cycling between the reconciler (re-queue) and the scheduler (re-gate).
func TestRetriedApprovalStillBound(t *testing.T) {
	for _, tc := range []struct {
		name, id  string
		mutate    func(*pb.JobRequest)
		wantState model.JobState
		reapprove bool
	}{
		// No longer the approved request: invalidated like any stale request.
		{"request changed", "approved-changed", func(r *pb.JobRequest) { r.Labels["amount"] = "50000" }, model.JobStateDenied, false},
		// Approved under another snapshot than the stored decision: it waits
		// for a fresh human decision.
		{"snapshot no longer matches", "approved-stale-snapshot", func(r *pb.JobRequest) { r.Labels["approval_snapshot"] = "snap-0" }, model.JobStateApproval, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newApprovalHarness(t, reportsRouting())
			redelivery := h.approveWithoutWorkers(tc.id)
			h.addReportsWorker()
			tc.mutate(redelivery)
			h.must("redelivery", h.engine.handleJobRequest(redelivery, "trace"))
			h.sweep()
			settled := h.meta(tc.id)
			h.sweep()
			after := h.meta(tc.id)
			if n := h.dispatches(tc.id); n != 0 {
				t.Fatalf("dispatched %d times on an approval that no longer binds", n)
			}
			if after["state"] != string(tc.wantState) {
				t.Fatalf("state = %s, want %s", after["state"], tc.wantState)
			}
			// Settled: the second sweep leaves the job untouched.
			if !maps.Equal(settled, after) {
				t.Fatalf("still cycling: job metadata changed between sweeps: %v -> %v", settled, after)
			}
			if tc.reapprove {
				h.must("re-approval", h.engine.handleJobRequest(h.approve(tc.id), "trace"))
				if n := h.dispatches(tc.id); n != 1 {
					t.Fatalf("re-approved job dispatched %d times, want 1", n)
				}
			}
		})
	}
}
