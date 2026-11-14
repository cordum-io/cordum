package scheduler

// The Python @guard SDK files approval-only jobs on job.guard. No worker
// serves that topic, so an allowed job completes instead of retrying dispatch
// until it fails. Unlike the gate topics, it keeps the policy snapshot binding.

import (
	"testing"

	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestGuardTopicJobs(t *testing.T) {
	// defaultRouting has no pool for job.guard: a dispatch attempt would
	// leave the job PENDING.
	t.Run("approved job completes", func(t *testing.T) {
		h := newApprovalHarness(t, defaultRouting())
		const id = "guard-approved"
		h.awaitApproval(&pb.JobRequest{JobId: id, Topic: guardApprovalTopic})
		h.must("republish", h.engine.handleJobRequest(h.approve(id), "trace"))
		assertGuardCompleted(t, h, id)
	})
	t.Run("allowed job completes", func(t *testing.T) {
		h := newApprovalHarness(t, defaultRouting())
		h.engine.safety = &fixedSafetyRecordChecker{record: SafetyDecisionRecord{Decision: SafetyAllow}}
		const id = "guard-allowed"
		h.must("submit", h.engine.handleJobRequest(&pb.JobRequest{JobId: id, Topic: guardApprovalTopic}, "trace"))
		assertGuardCompleted(t, h, id)
	})
	t.Run("approval under another snapshot is not honored", func(t *testing.T) {
		h := newApprovalHarness(t, defaultRouting())
		const id = "guard-stale"
		h.awaitApproval(&pb.JobRequest{JobId: id, Topic: guardApprovalTopic})
		req := h.approve(id)
		req.Labels["approval_snapshot"] = "snap-0"
		h.must("republish", h.engine.handleJobRequest(req, "trace"))
		if state := h.meta(id)["state"]; state != string(model.JobStateApproval) {
			t.Fatalf("state = %s, want %s", state, model.JobStateApproval)
		}
	})
}

func assertGuardCompleted(t *testing.T, h *approvalHarness, id string) {
	t.Helper()
	if state := h.meta(id)["state"]; state != string(model.JobStateSucceeded) {
		t.Fatalf("state = %s, want %s", state, model.JobStateSucceeded)
	}
	if n := h.dispatches(id); n != 0 {
		t.Fatalf("guard job dispatched %d times", n)
	}
	results := 0
	for _, msg := range h.bus.snapshotPublished() {
		res := msg.packet.GetJobResult()
		if msg.subject == capsdk.SubjectResult && res.GetJobId() == id && res.GetStatus() == pb.JobStatus_JOB_STATUS_SUCCEEDED {
			results++
		}
	}
	if results != 1 {
		t.Fatalf("published %d success results, want 1", results)
	}
}
