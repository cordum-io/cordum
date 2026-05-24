package store

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policylabels"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Regression guards for task-3527fdc5: single-step approval workflow
// was auto-invalidated as stale_request on a fresh deployment. Root
// cause: hash drift between submit-time (stored hash) and reconciler
// re-hash (re-canonicalised stored JobRequest). The fix is:
//   (a) scheduler captures preMutationHash BEFORE attachEffectiveConfig
//       / applyConstraints (engine.go checkSafetyDecision preMutationHash arg)
//   (b) the canonicaliser drops approval_* labels + LabelBusMsgID +
//       EffectiveConfigEnvVar, and protojson-roundtrips to strip
//       unknown proto fields so scheduler and reconciler see the same
//       wire form
//
// These tests pin the two sides of the contract:
//   - no-op approval: same JobRequest bytes stored + re-hashed →
//     classifier returns None, approval can be consumed
//   - legitimate drift: JobRequest body changes between submit and
//     approve → classifier returns StaleRequest
//
// The previously-shipped 60s grace period is gone; it masked the real
// bug by pushing the auto-invalidate past the smoke test's 10s window
// but a long-in-flight approval would still trip it incorrectly.

func TestClassifyApprovalRepair_NoOpApproval_ReturnsNone(t *testing.T) {
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	// Mirror the scheduler's submit-time shape: TenantId set, Labels
	// populated with workflow routing fields — exactly what the
	// workflow engine emits for a single-step approval gate.
	req := &pb.JobRequest{
		JobId:    "job-noop",
		Topic:    "job.approval-gate",
		TenantId: "default",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "approve",
			"workflow_id": "wf-1",
		},
	}
	// 1. Store the request BEFORE mutating (matches engine.go:1176).
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	// 2. Compute hash on the pre-mutation request (matches the
	//    preMutationHash path in engine.go).
	preHash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// 3. Store the safety decision with the pre-mutation hash. This
	//    is what the SafetyDecisionRecord.JobHash should carry after
	//    the engine.go:1964 switch (existingHash → preMutationHash →
	//    fresh) resolves.
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	// Reconciler re-hashes the stored (unchanged) JobRequest — the
	// canonical form matches the pre-mutation hash we stored.
	if snap.RequestHash != snap.SafetyRecord.JobHash {
		t.Fatalf("no-op approval must not drift hashes: stored=%s recomputed=%s",
			snap.SafetyRecord.JobHash, snap.RequestHash)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairNone {
		t.Fatalf("no-op approval must NOT trip the classifier, got %q", plan.Kind)
	}
}

func TestClassifyApprovalRepair_ApprovalLabelsAdded_DoesNotTripStaleRequest(t *testing.T) {
	// Exact smoke-test scenario: POST /approve adds approval_granted
	// + bus.LabelBusMsgID + approval_reason labels via
	// ResolveApproval. Those keys are stripped by the canonicaliser,
	// so the hash must stay stable.
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-labels",
		Topic:    "job.approval-gate",
		TenantId: "default",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "approve",
			"workflow_id": "wf-1",
		},
	}
	preHash, _ := hashApprovalJobRequest(req)
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}
	// Simulate POST /approve: ResolveApproval re-stores the request
	// with approval_* labels added.
	req.Labels["approval_granted"] = "true"
	req.Labels["approval_reason"] = "looks safe"
	req.Labels[bus.LabelBusMsgID] = "approval:job-labels"
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set req: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if snap.RequestHash != preHash {
		t.Fatalf("approval labels must not affect hash: stored=%s recomputed=%s", preHash, snap.RequestHash)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind == ApprovalRepairInvalidateStaleRequest {
		t.Fatalf("approval-label-only drift must NOT trip StaleRequest")
	}
}

func TestClassifyApprovalRepair_RealPayloadDrift_StillTripsStaleRequest(t *testing.T) {
	// Counterpart to the no-op test: if the canonical body genuinely
	// changes, the classifier MUST still catch it. A regression here
	// would let an attacker alter a pending job's payload between
	// safety check and approval.
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-drift",
		Topic:    "job.approval-gate",
		TenantId: "default",
		Labels:   map[string]string{"priority": "normal"},
	}
	preHash, _ := hashApprovalJobRequest(req)
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}
	// Malicious drift: operator changed priority to high between
	// safety check and approve. Not stripped by the canonicaliser.
	req.Labels["priority"] = "high"
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set req: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairInvalidateStaleRequest {
		t.Fatalf("real payload drift must trip StaleRequest, got %q", plan.Kind)
	}
	if plan.TargetState != model.JobStateDenied {
		t.Fatalf("stale-request repair must target DENIED, got %s", plan.TargetState)
	}
}

func TestClassifyApprovalRepair_EffectiveConfigEnvMutation_NotStale(t *testing.T) {
	// Regression for the exact smoke-test path: scheduler's
	// attachEffectiveConfig adds config.EffectiveConfigEnvVar to
	// req.Env AFTER SetJobRequest. Canonicaliser strips it, so the
	// stored-vs-in-memory hash must match.
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	req := &pb.JobRequest{JobId: "job-env", Topic: "job.approval-gate", TenantId: "default"}
	// Pre-mutation hash (what the fix-path captures).
	preHash, _ := hashApprovalJobRequest(req)
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairNone {
		t.Fatalf("env-stripping drift must not trip StaleRequest, got %q", plan.Kind)
	}
}

// TestClassifyApprovalRepair_PolicyAttachmentIDInjected_DoesNotTripStaleRequest
// is the task-821a6ebb regression guard. A worker-originated workflow-step
// approval gate pins its submit-time JobHash, then policy attachment injects
// policylabels.PolicyAttachmentID onto the stored request (engine_job.go:228)
// and the scheduler's attachEffectiveConfig adds config.EffectiveConfigEnvVar
// to req.Env — both AFTER the pin, while the approval is still PENDING. reqhash
// .Canonical strips both, so the reconciler's recomputed RequestHash must still
// equal the pinned JobHash and the pending approval must classify None (survive
// the reconciler tick) instead of being auto-invalidated as stale_request
// before a human can act. Before the strip this tripped invalidate_stale_request
// roughly one tick after creation.
func TestClassifyApprovalRepair_PolicyAttachmentIDInjected_DoesNotTripStaleRequest(t *testing.T) {
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	const jobID = "job-policy-attach"
	// Submit-time shape, BEFORE policy attachment — what the JobHash pins.
	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.approval-gate",
		TenantId: "default",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "approve",
			"workflow_id": "wf-1",
		},
	}
	preHash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}

	// Platform-injected metadata that lands on the stored request after the
	// pin, while the approval is still pending. Both are stripped by Canonical.
	req.Labels[policylabels.PolicyAttachmentID] = policylabels.JobAttachmentID(jobID)
	req.Env = map[string]string{config.EffectiveConfigEnvVar: `{"tenant":"default","effective":true}`}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set req: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, jobID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if snap.RequestHash != preHash {
		t.Fatalf("policy.attachment_id + EffectiveConfigEnvVar must not drift the hash: pinned=%s recomputed=%s",
			preHash, snap.RequestHash)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind == ApprovalRepairInvalidateStaleRequest {
		t.Fatal("policy.attachment_id injection must NOT invalidate a pending approval as stale_request")
	}
	if plan.Kind != ApprovalRepairNone {
		t.Fatalf("a pending approval carrying only stripped platform churn must classify None, got %q", plan.Kind)
	}
}

// TestClassifyApprovalRepair_PolicyAttachmentPlusRealDrift_StillTripsStaleRequest
// is the fence counterpart to the survival test: stripping policy.attachment_id
// must not blind the TOCTOU check. A pending approval carrying the (stripped)
// platform label that ALSO has a genuine payload change (ContextPtr) between
// safety-check and reconcile must STILL be invalidated as stale_request.
func TestClassifyApprovalRepair_PolicyAttachmentPlusRealDrift_StillTripsStaleRequest(t *testing.T) {
	t.Parallel()
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	const jobID = "job-policy-attach-drift"
	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.approval-gate",
		TenantId:   "default",
		ContextPtr: "ctx:original",
		Labels: map[string]string{
			"run_id":      "run-1",
			"step_id":     "approve",
			"workflow_id": "wf-1",
		},
	}
	preHash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		JobHash:          preHash,
	}); err != nil {
		t.Fatalf("set safety: %v", err)
	}

	// Benign stripped platform label AND a real payload change (ContextPtr,
	// not stripped) — the latter must still trip the fence.
	req.Labels[policylabels.PolicyAttachmentID] = policylabels.JobAttachmentID(jobID)
	req.ContextPtr = "ctx:tampered"
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set req: %v", err)
	}

	snap, err := store.InspectApprovalRepair(ctx, jobID)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	plan := ClassifyApprovalRepair(*snap, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairInvalidateStaleRequest {
		t.Fatalf("real payload drift alongside a stripped platform label must still trip StaleRequest, got %q", plan.Kind)
	}
	if plan.TargetState != model.JobStateDenied {
		t.Fatalf("stale-request repair must target DENIED, got %s", plan.TargetState)
	}
}
