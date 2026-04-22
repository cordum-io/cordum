package scheduler

// Regression coverage for the scheduler half of PR #204 blocker 2:
// the approval fast-path must bind the short-circuit-to-SafetyAllow
// decision to the PolicySnapshot under which the human actually
// approved. Without this binding, an approval granted under policy v1
// would dispatch under policy v2 if the safety kernel's policy
// rotated between admission and dispatch.
//
// The gateway writes approval_snapshot onto the resubmitted
// JobRequest and rotates the stored SafetyDecisionRecord.PolicySnapshot
// to the human-approved snapshot. The scheduler fast-path requires the
// two to match (via snapshotBase, which ignores config-overlay churn).
// A mismatch emits an approval.revision_mismatch SIEM event and falls
// through to a full safety re-check.

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/audit"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestCheckSafetyDecision_ApprovalFastPathRequiresSnapshotBinding
// exercises both sides of the binding:
//
//   - Accept: approval_snapshot label matches stored PolicySnapshot
//     (base-compared) → fast-path short-circuits to SafetyAllow.
//   - Reject (missing label): no approval_snapshot label → fast-path
//     falls through and emits approval.revision_mismatch.
//   - Reject (mismatched label): approval_snapshot carries a stale
//     snapshot vs stored → fast-path falls through and emits
//     approval.revision_mismatch.
func TestCheckSafetyDecision_ApprovalFastPathRequiresSnapshotBinding(t *testing.T) {
	t.Parallel()

	// Shared fixture: fresh engine + sink + seeded safety record
	// under policy v2 (post-rotation snapshot). The gateway has
	// rotated SafetyDecisionRecord.PolicySnapshot to v2 on approve.
	const (
		snapshotV1 = "sha256:policy-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		snapshotV2 = "sha256:policy-v2-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	newFixture := func(t *testing.T, jobID string) (*Engine, *recordingSink, *fakeJobStore, *pb.JobRequest) {
		t.Helper()
		sink := &recordingSink{}
		jobStore := newFakeJobStore()
		engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).
			WithDispatchAuditSink(sink)

		req := &pb.JobRequest{
			JobId: jobID,
			Topic: "job.gtm-engine.send-email",
			Labels: map[string]string{
				"approval_granted": "true",
				"workflow_id":      "wf-1",
				"run_id":           "run-1",
				"step_id":          "send-email",
			},
		}
		hash, err := HashJobRequest(req)
		if err != nil {
			t.Fatalf("hash job: %v", err)
		}
		// Simulate gateway having just resolved the approval: stored
		// PolicySnapshot now reflects the snapshot under which the
		// human approved (v2, after drift re-evaluation).
		jobStore.safety[jobID] = SafetyDecisionRecord{
			Decision:         SafetyRequireApproval,
			ApprovalRequired: true,
			PolicySnapshot:   snapshotV2,
			JobHash:          hash,
		}
		return engine, sink, jobStore, req
	}

	t.Run("accept_matching_snapshot", func(t *testing.T) {
		engine, sink, jobStore, req := newFixture(t, "job-accept")
		req.Labels["approval_snapshot"] = snapshotV2 + "|cfg:any-overlay"

		rec, err := engine.checkSafetyDecision(context.Background(), req)
		if err != nil {
			t.Fatalf("checkSafetyDecision returned unexpected error: %v", err)
		}
		if rec.Decision != SafetyAllow {
			t.Fatalf("expected fast-path SafetyAllow, got %q (reason=%q)", rec.Decision, rec.Reason)
		}
		if rec.Reason != "approval granted" {
			t.Fatalf("expected fast-path reason 'approval granted', got %q", rec.Reason)
		}
		// Fast-path persists the SafetyAllow record.
		stored, ok := jobStore.safety["job-accept"]
		if !ok {
			t.Fatalf("expected safety record persisted")
		}
		if stored.Decision != SafetyAllow {
			t.Fatalf("expected stored Decision=SafetyAllow, got %q", stored.Decision)
		}
		// No mismatch event emitted on the happy path.
		for i := 0; i < sink.count(); i++ {
			ev := sink.events[i]
			if ev.EventType == audit.EventApprovalRevisionMismatch {
				t.Fatalf("unexpected revision_mismatch event on matching snapshot: %+v", ev)
			}
		}
	})

	t.Run("reject_missing_snapshot_label", func(t *testing.T) {
		engine, sink, _, req := newFixture(t, "job-missing")
		// No approval_snapshot label (old gateway, pre-binding payload).

		rec, err := engine.checkSafetyDecision(context.Background(), req)
		// checkSafetyDecision falls through to SafetyBasic which
		// happily allows this job; the important assertion is that
		// the fast-path did NOT short-circuit and the mismatch event
		// was emitted.
		if err != nil {
			t.Fatalf("checkSafetyDecision returned unexpected error: %v", err)
		}
		if rec.Reason == "approval granted" {
			t.Fatalf("fast-path must not short-circuit without approval_snapshot label")
		}
		if sink.count() != 1 {
			t.Fatalf("expected exactly 1 SIEM event, got %d", sink.count())
		}
		ev := sink.last()
		if ev.EventType != audit.EventApprovalRevisionMismatch {
			t.Fatalf("expected event_type=%q, got %q", audit.EventApprovalRevisionMismatch, ev.EventType)
		}
		if ev.Severity != audit.SeverityHigh {
			t.Fatalf("expected severity=HIGH, got %q", ev.Severity)
		}
		if ev.Extra["stored_snapshot"] != snapshotV2 {
			t.Errorf("extra.stored_snapshot=%q want %q", ev.Extra["stored_snapshot"], snapshotV2)
		}
		if ev.Extra["asserted_snapshot"] != "" {
			t.Errorf("extra.asserted_snapshot=%q want empty", ev.Extra["asserted_snapshot"])
		}
	})

	t.Run("reject_mismatched_snapshot_label", func(t *testing.T) {
		engine, sink, _, req := newFixture(t, "job-mismatch")
		// approval_snapshot predates the policy rotation (v1) but
		// the stored SafetyDecisionRecord now reflects v2. This is
		// the TOCTOU window: gateway resolved under v2 but an
		// attacker-controlled resubmit with a stale label should
		// not be honored by the fast-path.
		req.Labels["approval_snapshot"] = snapshotV1

		rec, err := engine.checkSafetyDecision(context.Background(), req)
		if err != nil {
			t.Fatalf("checkSafetyDecision returned unexpected error: %v", err)
		}
		if rec.Reason == "approval granted" {
			t.Fatalf("fast-path must not short-circuit on snapshot mismatch")
		}
		if sink.count() != 1 {
			t.Fatalf("expected exactly 1 SIEM event, got %d", sink.count())
		}
		ev := sink.last()
		if ev.EventType != audit.EventApprovalRevisionMismatch {
			t.Fatalf("expected event_type=%q, got %q", audit.EventApprovalRevisionMismatch, ev.EventType)
		}
		if ev.Extra["stored_snapshot"] != snapshotV2 {
			t.Errorf("extra.stored_snapshot=%q want %q", ev.Extra["stored_snapshot"], snapshotV2)
		}
		if ev.Extra["asserted_snapshot"] != snapshotV1 {
			t.Errorf("extra.asserted_snapshot=%q want %q", ev.Extra["asserted_snapshot"], snapshotV1)
		}
		if ev.Extra["topic"] != "job.gtm-engine.send-email" {
			t.Errorf("extra.topic=%q want job.gtm-engine.send-email", ev.Extra["topic"])
		}
		if ev.Action != "scheduler.approval_fast_path_reject" {
			t.Errorf("action=%q want scheduler.approval_fast_path_reject", ev.Action)
		}
	})
}

// Ensure the import is retained even if future refactors drop
// the only consumer accidentally.
var _ context.Context = context.Background()
