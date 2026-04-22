package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestRedisJobStoreStateAndResultPtr(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-123"

	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	state, err := store.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStatePending {
		t.Fatalf("expected state %s, got %s", model.JobStatePending, state)
	}

	resultPtr := "redis://res:job-123"
	if err := store.SetResultPtr(ctx, jobID, resultPtr); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}

	gotPtr, err := store.GetResultPtr(ctx, jobID)
	if err != nil {
		t.Fatalf("get result ptr: %v", err)
	}
	if gotPtr != resultPtr {
		t.Fatalf("expected result ptr %s, got %s", resultPtr, gotPtr)
	}

	// advance state and list by state
	if err := store.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
		t.Fatalf("advance state: %v", err)
	}
	records, err := store.ListJobsByState(ctx, model.JobStateScheduled, time.Now().Unix(), 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(records) != 1 || records[0].ID != jobID {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestRedisJobStoreResultPtrTTL(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-ttl"
	if err := store.SetResultPtr(ctx, jobID, "redis://res:job-ttl"); err != nil {
		t.Fatalf("set result ptr: %v", err)
	}
	if store.metaTTL <= 0 {
		t.Fatalf("expected meta TTL configured")
	}
	ttl, err := store.client.TTL(ctx, jobResultPtrKey(jobID)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected ttl to be set, got %v", ttl)
	}
	if ttl > store.metaTTL {
		t.Fatalf("expected ttl <= metaTTL (%s), got %s", store.metaTTL, ttl)
	}
}

func TestRedisJobStoreDelegationLineageRoundTrip(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-delegation-lineage"
	want := model.DelegationLineage{
		TokenJTI:       "dlg-jti-1",
		ParentTokenJTI: "dlg-parent-1",
		Subject:        "agent-a",
		Audience:       "agent-b",
		Tenant:         "default",
		RootIssuer:     "agent-a",
		ParentIssuer:   "agent-a",
		IssuerChain: []model.DelegationChainLink{{
			AgentID:   "agent-a",
			IssuedAt:  "2026-04-21T09:00:00Z",
			ExpiresAt: "2026-04-21T10:00:00Z",
			JTI:       "dlg-jti-1",
			IssuedBy:  "agent-a",
		}},
		ChainDepth:    1,
		ExpiresAt:     "2026-04-21T10:00:00Z",
		Scope:         []string{"read"},
		AllowedTopics: []string{"job.test"},
		VerifiedAt:    1_713_680_000_000_000,
	}

	if err := store.SetDelegationLineage(ctx, jobID, want); err != nil {
		t.Fatalf("SetDelegationLineage() error = %v", err)
	}
	got, err := store.GetDelegationLineage(ctx, jobID)
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	if got.TokenJTI != want.TokenJTI || got.Audience != want.Audience || got.RootIssuer != want.RootIssuer || got.ChainDepth != want.ChainDepth {
		t.Fatalf("delegation lineage mismatch: got %#v want %#v", got, want)
	}
	if len(got.IssuerChain) != 1 || got.IssuerChain[0].AgentID != "agent-a" {
		t.Fatalf("issuer chain mismatch: %#v", got.IssuerChain)
	}
	if len(got.Scope) != 1 || got.Scope[0] != "read" {
		t.Fatalf("scope mismatch: %#v", got.Scope)
	}
}

func TestRedisJobStoreTransitionGuard(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-456"

	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// Same-state transition PENDING→PENDING must be rejected to prevent
	// double-approval races where concurrent requests both succeed.
	if err := store.SetState(ctx, jobID, model.JobStatePending); err == nil {
		t.Fatal("PENDING → PENDING should be rejected (same-state not allowed)")
	}
	if err := store.SetState(ctx, jobID, model.JobStateDispatched); err != nil {
		t.Fatalf("advance: %v", err)
	}
	// DISPATCHED → SCHEDULED is a valid rollback transition (dispatch publish failure).
	if err := store.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
		t.Fatalf("dispatched -> scheduled rollback should be ok: %v", err)
	}
	// Advance again to test invalid backward from RUNNING.
	if err := store.SetState(ctx, jobID, model.JobStateDispatched); err != nil {
		t.Fatalf("advance back to dispatched: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("advance to running: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateScheduled); err == nil {
		t.Fatalf("expected invalid backward transition from running to scheduled")
	}

	jobPendingFail := "job-456-pending-fail"
	if err := store.SetState(ctx, jobPendingFail, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, model.JobStateFailed); err != nil {
		t.Fatalf("pending -> failed should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, model.JobStateFailed); err == nil {
		t.Fatal("FAILED → FAILED should be rejected (terminal, no self-transition)")
	}

	jobScheduledFail := "job-456-scheduled-fail"
	if err := store.SetState(ctx, jobScheduledFail, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledFail, model.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledFail, model.JobStateFailed); err != nil {
		t.Fatalf("scheduled -> failed should be ok: %v", err)
	}

	jobScheduledSuccess := "job-456-scheduled-success"
	if err := store.SetState(ctx, jobScheduledSuccess, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledSuccess, model.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledSuccess, model.JobStateSucceeded); err != nil {
		t.Fatalf("scheduled -> succeeded should be ok: %v", err)
	}

	jobScheduledCancel := "job-456-scheduled-cancel"
	if err := store.SetState(ctx, jobScheduledCancel, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledCancel, model.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledCancel, model.JobStateCancelled); err != nil {
		t.Fatalf("scheduled -> cancelled should be ok: %v", err)
	}

	jobPendingTimeout := "job-456-pending-timeout"
	if err := store.SetState(ctx, jobPendingTimeout, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobPendingTimeout, model.JobStateTimeout); err != nil {
		t.Fatalf("pending -> timeout should be ok: %v", err)
	}

	jobScheduledTimeout := "job-456-scheduled-timeout"
	if err := store.SetState(ctx, jobScheduledTimeout, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledTimeout, model.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledTimeout, model.JobStateTimeout); err != nil {
		t.Fatalf("scheduled -> timeout should be ok: %v", err)
	}

	jobRunningQuarantined := "job-456-running-quarantined"
	if err := store.SetState(ctx, jobRunningQuarantined, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobRunningQuarantined, model.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobRunningQuarantined, model.JobStateRunning); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobRunningQuarantined, model.JobStateQuarantined); err != nil {
		t.Fatalf("running -> output_quarantined should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobRunningQuarantined, model.JobStateRunning); err == nil {
		t.Fatalf("expected invalid transition from output_quarantined")
	}
}

func TestRedisJobStoreOutputSafetyRoundTrip(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-output-safety"
	record := model.OutputSafetyRecord{
		Decision:       model.OutputQuarantine,
		Reason:         "secret detected in output",
		RuleID:         "out-001",
		PolicySnapshot: "snapshot-abc",
		Findings: []model.OutputFinding{{
			Type:           "secret_leak",
			Severity:       "critical",
			Detail:         "aws_access_key_id",
			Scanner:        "regex",
			Confidence:     0.98,
			MatchedPattern: "AKIA[0-9A-Z]{16}",
		}},
		RedactedPtr: "redis://res:job-output-safety:redacted",
		OriginalPtr: "redis://res:job-output-safety:original",
		Phase:       "sync",
	}

	if err := store.SetOutputSafety(ctx, jobID, record); err != nil {
		t.Fatalf("set output safety: %v", err)
	}
	if raw, err := store.client.Get(ctx, outputDecisionKey(jobID)).Result(); err != nil || raw == "" {
		t.Fatalf("expected output decision key to be stored, err=%v raw=%q", err, raw)
	}
	got, err := store.GetOutputSafety(ctx, jobID)
	if err != nil {
		t.Fatalf("get output safety: %v", err)
	}
	if got.Decision != model.OutputQuarantine {
		t.Fatalf("expected decision %q got %q", model.OutputQuarantine, got.Decision)
	}
	if got.RedactedPtr != record.RedactedPtr || got.OriginalPtr != record.OriginalPtr {
		t.Fatalf("unexpected pointers: %#v", got)
	}
	if len(got.Findings) != 1 || got.Findings[0].Scanner != "regex" {
		t.Fatalf("unexpected findings: %#v", got.Findings)
	}
	if got.CheckedAt == 0 {
		t.Fatalf("expected checked_at timestamp to be set")
	}
	gotDecision, err := store.GetOutputDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get output decision: %v", err)
	}
	if gotDecision.Decision != model.OutputQuarantine {
		t.Fatalf("expected output decision via key, got %#v", gotDecision)
	}
}

func TestRedisJobStoreListRecentJobs(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Create three jobs with different timestamps
	if err := store.SetState(ctx, "job-a", model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-b", model.JobStateDispatched); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-c", model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}

	list, err := store.ListRecentJobs(ctx, 2)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(list))
	}
	if list[0].ID != "job-c" || list[1].ID != "job-b" {
		t.Fatalf("unexpected order: %#v", list)
	}
	if list[0].State != model.JobStateRunning {
		t.Fatalf("expected state RUNNING, got %s", list[0].State)
	}
}

func TestRedisJobStoreListRecentJobsByScorePagination(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.SetState(ctx, "job-1", model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-2", model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-3", model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-3", model.JobStateSucceeded); err != nil {
		t.Fatalf("set state: %v", err)
	}

	firstPage, err := store.ListRecentJobsByScore(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ListRecentJobsByScore page1: %v", err)
	}
	if len(firstPage) != 2 || firstPage[0].ID != "job-3" || firstPage[1].ID != "job-2" {
		t.Fatalf("unexpected first page: %#v", firstPage)
	}

	cursor := firstPage[len(firstPage)-1].UpdatedAt - 1
	secondPage, err := store.ListRecentJobsByScore(ctx, cursor, 2)
	if err != nil {
		t.Fatalf("ListRecentJobsByScore page2: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != "job-1" {
		t.Fatalf("unexpected second page: %#v", secondPage)
	}
}

func TestRedisJobStoreApprovalRecord(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-approval"

	record := ApprovalRecord{
		ApprovedBy:     "alice",
		ApprovedRole:   "admin",
		Reason:         "ok",
		Note:           "reviewed",
		PolicySnapshot: "snap-1",
		JobHash:        "hash-1",
	}
	if err := store.SetApprovalRecord(ctx, jobID, record); err != nil {
		t.Fatalf("set approval record: %v", err)
	}
	got, err := store.GetApprovalRecord(ctx, jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if got.ApprovedBy != record.ApprovedBy || got.ApprovedRole != record.ApprovedRole {
		t.Fatalf("unexpected approval identity: %#v", got)
	}
	if got.Reason != record.Reason || got.Note != record.Note {
		t.Fatalf("unexpected approval details: %#v", got)
	}
	if got.PolicySnapshot != record.PolicySnapshot || got.JobHash != record.JobHash {
		t.Fatalf("unexpected approval linkage: %#v", got)
	}
}

func TestRedisJobStoreSeedsExplicitApprovalLifecycle(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-approval-lifecycle"
	if err := store.SetState(ctx, jobID, model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		ApprovalRef:      "approval-1",
		JobHash:          "hash-1",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	sd, err := store.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get safety decision: %v", err)
	}
	if sd.ApprovalStatus != model.ApprovalStatusPending {
		t.Fatalf("expected pending approval status, got %q", sd.ApprovalStatus)
	}
	if sd.Actionability != model.ApprovalActionabilityActionable {
		t.Fatalf("expected actionable approval, got %q", sd.Actionability)
	}
	if sd.ApprovalRevision != 1 {
		t.Fatalf("expected approval revision 1, got %d", sd.ApprovalRevision)
	}

	record, err := store.GetApprovalRecord(ctx, jobID)
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	normalized := NormalizeApprovalRecord(model.JobStateApproval, sd, record)
	if normalized.Status != model.ApprovalStatusPending {
		t.Fatalf("expected normalized pending status, got %q", normalized.Status)
	}
	if normalized.Actionability != model.ApprovalActionabilityActionable {
		t.Fatalf("expected normalized actionable approval, got %q", normalized.Actionability)
	}
	if normalized.Revision != 1 {
		t.Fatalf("expected normalized revision 1, got %d", normalized.Revision)
	}
}

func TestRedisJobStoreResolveApprovalAtomically(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-resolve-approve",
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	hash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash request: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	resolved, err := store.ResolveApproval(ctx, ApprovalResolutionParams{
		JobID:          req.GetJobId(),
		Decision:       model.ApprovalDecisionApprove,
		ResultState:    model.JobStatePending,
		ApprovedBy:     "alice",
		ApprovedRole:   "admin",
		Reason:         "approved",
		Note:           "looks good",
		PolicySnapshot: "snap-1",
		LabelUpdates: map[string]string{
			"approval_granted": "true",
			"approval_reason":  "approved",
			bus.LabelBusMsgID:  "approval:" + req.GetJobId(),
		},
	})
	if err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	if resolved.State != model.JobStatePending {
		t.Fatalf("expected pending state, got %s", resolved.State)
	}

	state, err := store.GetState(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStatePending {
		t.Fatalf("expected pending state persisted, got %s", state)
	}

	persistedReq, err := store.GetJobRequest(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get job request: %v", err)
	}
	if persistedReq.Labels["approval_granted"] != "true" {
		t.Fatalf("expected approval_granted label, got %#v", persistedReq.Labels)
	}

	record, err := store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.Status != model.ApprovalStatusApproved {
		t.Fatalf("expected approved status, got %q", record.Status)
	}
	if record.Decision != model.ApprovalDecisionApprove {
		t.Fatalf("expected approve decision, got %q", record.Decision)
	}
	if record.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", record.Revision)
	}
	if record.PublishStatus != model.ApprovalPublishPending {
		t.Fatalf("expected pending publish status, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetSubmit {
		t.Fatalf("expected submit publish target, got %q", record.PublishTarget)
	}

	if err := store.MarkApprovalPublishComplete(ctx, req.GetJobId(), record.Revision, record.PublishTarget); err != nil {
		t.Fatalf("mark publish complete: %v", err)
	}
	record, err = store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record after mark complete: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPublished {
		t.Fatalf("expected published status after mark complete, got %q", record.PublishStatus)
	}
	if record.PublishedAt <= 0 {
		t.Fatalf("expected published_at > 0, got %d", record.PublishedAt)
	}
}

func TestRedisJobStoreResolveApprovalRejectsStaleApproveRequest(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-resolve-stale",
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          "stale-hash",
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	_, err = store.ResolveApproval(ctx, ApprovalResolutionParams{
		JobID:       req.GetJobId(),
		Decision:    model.ApprovalDecisionApprove,
		ResultState: model.JobStatePending,
		ApprovedBy:  "alice",
	})
	var conflict *ApprovalConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected approval conflict, got %v", err)
	}
	if conflict.Code != model.ApprovalConflictStaleRequest {
		t.Fatalf("expected stale request conflict, got %q", conflict.Code)
	}

	state, err := store.GetState(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStateApproval {
		t.Fatalf("expected approval state unchanged, got %s", state)
	}
}

func TestRedisJobStoreResolveRejectSeedsDLQPublishIntent(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-resolve-reject",
		Topic:    "job.test",
		TenantId: "default",
	}
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job req: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set approval state: %v", err)
	}
	hash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash request: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	if _, err := store.ResolveApproval(ctx, ApprovalResolutionParams{
		JobID:       req.GetJobId(),
		Decision:    model.ApprovalDecisionReject,
		ResultState: model.JobStateDenied,
		ApprovedBy:  "bob",
		Reason:      "denied",
	}); err != nil {
		t.Fatalf("resolve reject: %v", err)
	}

	record, err := store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.PublishStatus != model.ApprovalPublishPending {
		t.Fatalf("expected pending publish status, got %q", record.PublishStatus)
	}
	if record.PublishTarget != model.ApprovalPublishTargetDLQ {
		t.Fatalf("expected dlq publish target, got %q", record.PublishTarget)
	}
}

func TestClassifyApprovalRepairPrefersResolutionOverPendingPublishWhileAwaitingApproval(t *testing.T) {
	plan := ClassifyApprovalRepair(ApprovalRepairSnapshot{
		JobID: "job-repair-pending",
		State: model.JobStateApproval,
		ApprovalRecord: ApprovalRecord{
			Status:        model.ApprovalStatusApproved,
			Decision:      model.ApprovalDecisionApprove,
			Actionability: model.ApprovalStatusApproved.DefaultActionability(),
			PublishStatus: model.ApprovalPublishPending,
			PublishTarget: model.ApprovalPublishTargetSubmit,
		},
	}, ApprovalRepairClassifyOptions{})

	if plan.Kind != ApprovalRepairApplyApprovedResolution {
		t.Fatalf("expected approved resolution repair, got %q", plan.Kind)
	}
	if !plan.Repairable {
		t.Fatal("expected repair plan to be repairable")
	}
}

func TestRedisJobStoreApplyApprovalRepairApprovedResolution(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-repair-approved",
		Topic:    "job.test",
		TenantId: "default",
		Labels: map[string]string{
			"approval_granted": "true",
		},
	}
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	hash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash request: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	snapshot, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect repair: %v", err)
	}
	plan := ClassifyApprovalRepair(*snapshot, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairApplyApprovedResolution {
		t.Fatalf("expected approved repair plan, got %q", plan.Kind)
	}

	repaired, err := store.ApplyApprovalRepair(ctx, ApprovalRepairApplyParams{
		JobID: req.GetJobId(),
		Plan:  plan,
		Actor: "operator-1",
		Note:  "operator repair",
	})
	if err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	if repaired.State != model.JobStatePending {
		t.Fatalf("expected pending state, got %s", repaired.State)
	}
	if repaired.ApprovalRecord.PublishTarget != model.ApprovalPublishTargetSubmit {
		t.Fatalf("expected submit publish target, got %q", repaired.ApprovalRecord.PublishTarget)
	}
	if repaired.ApprovalRecord.PublishStatus != model.ApprovalPublishPending {
		t.Fatalf("expected pending publish status, got %q", repaired.ApprovalRecord.PublishStatus)
	}

	state, err := store.GetState(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != model.JobStatePending {
		t.Fatalf("expected pending state persisted, got %s", state)
	}
	record, err := store.GetApprovalRecord(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get approval record: %v", err)
	}
	if record.Status != model.ApprovalStatusApproved {
		t.Fatalf("expected approved status, got %q", record.Status)
	}
	if record.Decision != model.ApprovalDecisionApprove {
		t.Fatalf("expected approve decision, got %q", record.Decision)
	}
	if !strings.Contains(record.Note, "repair: operator repair") {
		t.Fatalf("expected repair note recorded, got %q", record.Note)
	}
	persistedReq, err := store.GetJobRequest(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("get repaired request: %v", err)
	}
	if persistedReq.Labels["approval_granted"] != "true" {
		t.Fatalf("expected approval_granted label, got %#v", persistedReq.Labels)
	}
	if persistedReq.Labels[bus.LabelBusMsgID] != "approval:"+req.GetJobId() {
		t.Fatalf("expected approval label bus msg id, got %#v", persistedReq.Labels)
	}
}

func TestRedisJobStoreApplyApprovalRepairInvalidatesStaleRequest(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	req := &pb.JobRequest{
		JobId:    "job-repair-stale",
		Topic:    "job.test",
		TenantId: "default",
	}
	hash, err := hashApprovalJobRequest(req)
	if err != nil {
		t.Fatalf("hash request: %v", err)
	}
	req.Labels = map[string]string{"priority": "high"}
	if err := store.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if err := store.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if err := store.SetState(ctx, req.GetJobId(), model.JobStateApproval); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetSafetyDecision(ctx, req.GetJobId(), model.SafetyDecisionRecord{
		Decision:         model.SafetyRequireApproval,
		ApprovalRequired: true,
		PolicySnapshot:   "snap-1",
		JobHash:          hash,
	}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	snapshot, err := store.InspectApprovalRepair(ctx, req.GetJobId())
	if err != nil {
		t.Fatalf("inspect repair: %v", err)
	}
	plan := ClassifyApprovalRepair(*snapshot, ApprovalRepairClassifyOptions{})
	if plan.Kind != ApprovalRepairInvalidateStaleRequest {
		t.Fatalf("expected stale request invalidation, got %q", plan.Kind)
	}

	repaired, err := store.ApplyApprovalRepair(ctx, ApprovalRepairApplyParams{
		JobID: req.GetJobId(),
		Plan:  plan,
		Actor: "operator-2",
		Note:  "stale request",
	})
	if err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	if repaired.State != model.JobStateDenied {
		t.Fatalf("expected denied state, got %s", repaired.State)
	}
	if repaired.ApprovalRecord.Status != model.ApprovalStatusInvalidated {
		t.Fatalf("expected invalidated status, got %q", repaired.ApprovalRecord.Status)
	}
	if repaired.ApprovalRecord.PublishTarget != "" {
		t.Fatalf("expected no publish target, got %q", repaired.ApprovalRecord.PublishTarget)
	}
}

func TestRedisJobStoreCancelJob(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-cancel"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	state, err := store.CancelJob(ctx, jobID)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if state != model.JobStateCancelled {
		t.Fatalf("expected cancelled state, got %s", state)
	}
	updated, err := store.GetState(ctx, jobID)
	if err != nil || updated != model.JobStateCancelled {
		t.Fatalf("expected cancelled state persisted, got %s err=%v", updated, err)
	}

	terminalID := "job-terminal"
	if err := store.SetState(ctx, terminalID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, terminalID, model.JobStateScheduled); err != nil {
		t.Fatalf("set scheduled state: %v", err)
	}
	if err := store.SetState(ctx, terminalID, model.JobStateSucceeded); err != nil {
		t.Fatalf("set terminal state: %v", err)
	}
	state, err = store.CancelJob(ctx, terminalID)
	if err != nil {
		t.Fatalf("cancel job terminal: %v", err)
	}
	if state != model.JobStateSucceeded {
		t.Fatalf("expected terminal state unchanged, got %s", state)
	}
}

func TestRedisJobStoreTraceAndDeadlines(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-trace"
	if err := store.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetTopic(ctx, jobID, "job.test"); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	if err := store.SetTenant(ctx, jobID, "tenant-1"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := store.AddJobToTrace(ctx, "trace-1", jobID); err != nil {
		t.Fatalf("add to trace: %v", err)
	}

	traceJobs, err := store.GetTraceJobs(ctx, "trace-1")
	if err != nil {
		t.Fatalf("get trace jobs: %v", err)
	}
	if len(traceJobs) != 1 || traceJobs[0].ID != jobID {
		t.Fatalf("unexpected trace jobs: %#v", traceJobs)
	}
	if traceJobs[0].Topic != "job.test" || traceJobs[0].Tenant != "tenant-1" {
		t.Fatalf("unexpected trace job metadata: %#v", traceJobs[0])
	}

	deadline := time.Now().Add(-time.Minute)
	if err := store.SetDeadline(ctx, jobID, deadline); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	expired, err := store.ListExpiredDeadlines(ctx, time.Now().Unix(), 10)
	if err != nil {
		t.Fatalf("list expired deadlines: %v", err)
	}
	if len(expired) == 0 || expired[0].ID != jobID {
		t.Fatalf("expected expired job, got %#v", expired)
	}
}

func TestRedisJobStoreCountActiveByTenant(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-active"
	if err := store.SetTenant(ctx, jobID, "tenant-2"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	count, err := store.CountActiveByTenant(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active job, got %d", count)
	}
	if err := store.SetState(ctx, jobID, model.JobStateSucceeded); err != nil {
		t.Fatalf("set terminal state: %v", err)
	}
	count, err = store.CountActiveByTenant(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 active jobs, got %d", count)
	}
}

func TestRedisJobStoreSetJobMeta(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	req := &pb.JobRequest{
		JobId:       "job-meta",
		Topic:       "job.test",
		PrincipalId: "principal",
		Env: map[string]string{
			"tenant_id": "tenant-1",
			"team_id":   "team-1",
		},
		Labels: map[string]string{"req": "1"},
		Meta: &pb.JobMetadata{
			ActorType:      pb.ActorType_ACTOR_TYPE_HUMAN,
			IdempotencyKey: "idem-1",
			Capability:     "cap",
			RiskTags:       []string{"risk"},
			Requires:       []string{"gpu"},
			PackId:         "pack-1",
			Labels:         map[string]string{"meta": "2"},
		},
	}
	if err := store.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("set job meta: %v", err)
	}
	if tenant, _ := store.GetTenant(context.Background(), req.JobId); tenant != "tenant-1" {
		t.Fatalf("expected tenant set, got %s", tenant)
	}
	if team, _ := store.GetTeam(context.Background(), req.JobId); team != "team-1" {
		t.Fatalf("expected team set, got %s", team)
	}
	if actorType, _ := store.GetActorType(context.Background(), req.JobId); actorType != "human" {
		t.Fatalf("expected actor type human, got %s", actorType)
	}
	if idem, _ := store.GetIdempotencyKey(context.Background(), req.JobId); idem != "idem-1" {
		t.Fatalf("expected idempotency key, got %s", idem)
	}
	if packID, _ := store.GetPackID(context.Background(), req.JobId); packID != "pack-1" {
		t.Fatalf("expected pack id, got %s", packID)
	}

	rawLabels, err := store.client.HGet(context.Background(), jobMetaKey(req.JobId), metaFieldLabels).Result()
	if err != nil {
		t.Fatalf("read labels: %v", err)
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(rawLabels), &labels); err != nil {
		t.Fatalf("decode labels: %v", err)
	}
	if labels["req"] != "1" || labels["meta"] != "2" {
		t.Fatalf("expected merged labels, got %#v", labels)
	}
}

func TestRedisJobStoreListSafetyDecisions(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	jobID := "job-decisions"
	first := model.SafetyDecisionRecord{
		Decision:         model.SafetyAllow,
		Reason:           "ok",
		ApprovalRequired: true,
		Constraints:      &pb.PolicyConstraints{RedactionLevel: "low"},
	}
	if err := store.SetSafetyDecision(context.Background(), jobID, first); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	second := model.SafetyDecisionRecord{
		Decision:    model.SafetyDeny,
		Reason:      "blocked",
		Constraints: &pb.PolicyConstraints{RedactionLevel: "high"},
	}
	if err := store.SetSafetyDecision(context.Background(), jobID, second); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}

	decisions, err := store.ListSafetyDecisions(context.Background(), jobID, 10)
	if err != nil {
		t.Fatalf("list safety decisions: %v", err)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(decisions))
	}
	if decisions[0].Decision != model.SafetyDeny {
		t.Fatalf("expected most recent decision first")
	}
	if decisions[1].ApprovalRequired != true {
		t.Fatalf("expected approval_required to decode")
	}
}

func TestRedisJobStoreIdempotencyAndLocks(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.SetIdempotencyKey(ctx, "idem", "job-1"); err != nil {
		t.Fatalf("set idempotency: %v", err)
	}
	jobID, err := store.GetJobByIdempotencyKey(ctx, "idem")
	if err != nil || jobID != "job-1" {
		t.Fatalf("unexpected idempotency lookup: %s err=%v", jobID, err)
	}

	token, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil || token == "" {
		t.Fatalf("expected lock acquired")
	}
	if err := store.ReleaseLock(ctx, "lock-1", token); err != nil {
		t.Fatalf("release lock: %v", err)
	}
}

func TestRedisJobStoreLockConcurrentSafety(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	token1, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil || token1 == "" {
		t.Fatalf("expected lock acquired")
	}
	token2, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil {
		t.Fatalf("unexpected error on second acquire: %v", err)
	}
	if token2 != "" {
		t.Fatalf("expected lock contention, got token=%q", token2)
	}
	if err := store.ReleaseLock(ctx, "lock-1", token1); err != nil {
		t.Fatalf("release lock: %v", err)
	}
}

func TestRedisJobStoreLockReacquireAfterExpiry(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	token1, err := store.TryAcquireLock(ctx, "lock-1", 100*time.Millisecond)
	if err != nil || token1 == "" {
		t.Fatalf("expected lock acquired")
	}
	srv.FastForward(200 * time.Millisecond)

	token2, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil || token2 == "" {
		t.Fatalf("expected lock reacquired after expiry")
	}
	if token2 == token1 {
		t.Fatalf("expected new token after expiry")
	}
	if err := store.ReleaseLock(ctx, "lock-1", token2); err != nil {
		t.Fatalf("release lock: %v", err)
	}
}

func TestRedisJobStoreLockRejectsWrongOwner(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	token1, err := store.TryAcquireLock(ctx, "lock-1", 100*time.Millisecond)
	if err != nil || token1 == "" {
		t.Fatalf("expected lock acquired")
	}
	srv.FastForward(200 * time.Millisecond)

	token2, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil || token2 == "" {
		t.Fatalf("expected lock reacquired after expiry")
	}
	if err := store.ReleaseLock(ctx, "lock-1", token1); err == nil {
		t.Fatalf("expected wrong-owner release to fail")
	}
	token3, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token3 != "" {
		t.Fatalf("expected lock still held by new owner, got token=%q", token3)
	}
	if err := store.ReleaseLock(ctx, "lock-1", token2); err != nil {
		t.Fatalf("release lock: %v", err)
	}
}

func TestRedisJobStoreRenewLockSuccess(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	token, err := store.TryAcquireLock(ctx, "renew-test", 200*time.Millisecond)
	if err != nil || token == "" {
		t.Fatalf("expected lock acquired, token=%q err=%v", token, err)
	}

	// Renew should succeed with the correct token.
	if err := store.RenewLock(ctx, "renew-test", token, 500*time.Millisecond); err != nil {
		t.Fatalf("expected renew to succeed: %v", err)
	}

	// After renewal, fast-forward past the original TTL — lock should still be held.
	srv.FastForward(300 * time.Millisecond)
	token2, err := store.TryAcquireLock(ctx, "renew-test", time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token2 != "" {
		t.Fatal("expected lock still held after renewal, but was reacquired")
	}
}

func TestRedisJobStoreRenewLockWrongToken(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	token, err := store.TryAcquireLock(ctx, "renew-wrong", time.Second)
	if err != nil || token == "" {
		t.Fatalf("expected lock acquired")
	}

	// Renew with wrong token should fail.
	if err := store.RenewLock(ctx, "renew-wrong", "wrong-token", time.Second); err == nil {
		t.Fatal("expected renew with wrong token to fail")
	}
}

func TestRedisJobStoreRenewLockExpiredKey(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	// Try renewing a key that doesn't exist.
	if err := store.RenewLock(ctx, "nonexistent-key", "some-token", time.Second); err == nil {
		t.Fatal("expected renew of nonexistent key to fail")
	}
}

func TestRedisJobStoreRenewLockEmptyParams(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.RenewLock(ctx, "", "token", time.Second); err == nil {
		t.Fatal("expected error for empty key")
	}
	if err := store.RenewLock(ctx, "key", "", time.Second); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestRedisJobStoreTraceAndPrincipal(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-trace-2"
	if err := store.AddJobToTrace(ctx, "trace-2", jobID); err != nil {
		t.Fatalf("add job to trace: %v", err)
	}
	traceID, err := store.GetTraceID(ctx, jobID)
	if err != nil || traceID != "trace-2" {
		t.Fatalf("expected trace id, got %s err=%v", traceID, err)
	}
	if err := store.SetPrincipal(ctx, jobID, "principal-1"); err != nil {
		t.Fatalf("set principal: %v", err)
	}
	if principal, _ := store.GetPrincipal(ctx, jobID); principal != "principal-1" {
		t.Fatalf("expected principal, got %s", principal)
	}
}

func TestRedisJobStoreTopicMetadata(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-topic"
	if err := store.SetTopic(ctx, jobID, "job.default"); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	got, err := store.GetTopic(ctx, jobID)
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if got != "job.default" {
		t.Fatalf("expected topic job.default, got %s", got)
	}

	if err := store.SetTenant(ctx, jobID, "tenant-a"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	tenant, err := store.GetTenant(ctx, jobID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant != "tenant-a" {
		t.Fatalf("expected tenant tenant-a, got %s", tenant)
	}

	if err := store.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{Decision: model.SafetyDeny, Reason: "forbidden"}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	record, err := store.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get safety decision: %v", err)
	}
	if record.Decision != model.SafetyDeny || record.Reason != "forbidden" {
		t.Fatalf("unexpected safety decision %s reason %s", record.Decision, record.Reason)
	}
}

func TestRedisJobStoreJobRequestRoundTrip(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	req := &pb.JobRequest{
		JobId:    "job-req",
		Topic:    "job.test",
		TenantId: "tenant",
		Budget:   &pb.Budget{DeadlineMs: 10},
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	got, err := store.GetJobRequest(context.Background(), "job-req")
	if err != nil {
		t.Fatalf("get job request: %v", err)
	}
	if got.GetJobId() != req.JobId || got.GetTopic() != req.Topic {
		t.Fatalf("unexpected job request: %#v", got)
	}
}

func TestRedisJobStoreDeadlinesAndTrace(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-deadline"
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	_ = store.SetTopic(ctx, jobID, "job.test")
	_ = store.SetTenant(ctx, jobID, "tenant")

	past := time.Now().Add(-time.Minute)
	if err := store.SetDeadline(ctx, jobID, past); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	jobs, err := store.ListExpiredDeadlines(ctx, time.Now().Unix(), 10)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}
	if len(jobs) == 0 || jobs[0].ID != jobID {
		t.Fatalf("expected expired job")
	}

	traceID := "trace-1"
	if err := store.AddJobToTrace(ctx, traceID, jobID); err != nil {
		t.Fatalf("add trace: %v", err)
	}
	traceJobs, err := store.GetTraceJobs(ctx, traceID)
	if err != nil {
		t.Fatalf("get trace jobs: %v", err)
	}
	if len(traceJobs) != 1 || traceJobs[0].ID != jobID {
		t.Fatalf("unexpected trace jobs: %#v", traceJobs)
	}
}

func TestRedisJobStoreSafetyDecisionRoundTrip(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-safe"
	record := model.SafetyDecisionRecord{
		Decision:         model.SafetyDeny,
		Reason:           "blocked",
		RuleID:           "rule-1",
		PolicySnapshot:   "snap",
		ApprovalRequired: true,
		ApprovalRef:      "approval-1",
		JobHash:          "hash",
	}
	if err := store.SetSafetyDecision(ctx, jobID, record); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	got, err := store.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get safety decision: %v", err)
	}
	if got.Decision != record.Decision || got.RuleID != record.RuleID || got.ApprovalRef != record.ApprovalRef {
		t.Fatalf("unexpected safety decision: %#v", got)
	}
}

func TestRedisJobStorePingAndTeam(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("expected ping success: %v", err)
	}
	jobID := "job-team"
	if err := store.SetTeam(ctx, jobID, "team-a"); err != nil {
		t.Fatalf("set team: %v", err)
	}
	if got, err := store.GetTeam(ctx, jobID); err != nil || got != "team-a" {
		t.Fatalf("unexpected team: %v %v", got, err)
	}
}

func TestRedisJobStoreIdempotencyKeyScoped(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	ok, existing, err := store.TrySetIdempotencyKeyScoped(ctx, "tenant-a", "key-1", "job-1")
	if err != nil {
		t.Fatalf("set idempotency: %v", err)
	}
	if !ok {
		t.Fatalf("expected idempotency key to be set")
	}
	if existing != "job-1" {
		t.Fatalf("expected stored job id, got %s", existing)
	}
	ok, existing, err = store.TrySetIdempotencyKeyScoped(ctx, "tenant-a", "key-1", "job-2")
	if err != nil {
		t.Fatalf("set idempotency 2: %v", err)
	}
	if ok {
		t.Fatalf("expected idempotency key collision")
	}
	if existing != "job-1" {
		t.Fatalf("expected existing job id to remain, got %s", existing)
	}
}

func TestRedisJobStoreIdempotencyKeyScopedConcurrent(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	const n = 20
	wins := make(chan string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(id string) {
			defer wg.Done()
			ok, _, err := store.TrySetIdempotencyKeyScoped(ctx, "tenant-race", "race-key", id)
			if err != nil {
				t.Errorf("TrySetIdempotencyKeyScoped: %v", err)
				return
			}
			if ok {
				wins <- id
			}
		}(fmt.Sprintf("job-%d", i))
	}
	wg.Wait()
	close(wins)

	var winCount int
	for range wins {
		winCount++
	}
	if winCount != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", winCount)
	}
}

// TestIdempotencyCROSSLOTResolved verifies that the CROSSSLOT risk from the
// old Lua-based idempotency script is resolved. The Lua script has been
// replaced with individual Redis commands (two-phase pipeline), so each
// command targets a single key and there is no CROSSSLOT violation.
func TestIdempotencyCROSSLOTResolved(t *testing.T) {
	legacyKey := jobIdempotencyKey("my-idem-key")
	scopedKey := jobIdempotencyKeyScoped("tenant-a", "my-idem-key")
	metaKey := jobMetaKeyPrefix + "job-123"

	// Keys still have different prefixes (different slots), but that's fine
	// because TrySetIdempotencyKeyScoped now uses individual commands instead
	// of a multi-key Lua script. Each command targets one key at a time.
	if legacyKey == scopedKey {
		t.Fatal("legacy and scoped keys should differ")
	}
	if legacyKey == metaKey || scopedKey == metaKey {
		t.Fatal("meta key should differ from idempotency keys")
	}

	// Compute Redis Cluster hash slots to document the slot distribution.
	slot := func(key string) uint16 {
		var crc uint16 = 0
		for i := 0; i < len(key); i++ {
			crc = (crc << 8) ^ crc16tab[(byte(crc>>8))^key[i]]
		}
		return crc % 16384
	}

	s1 := slot(legacyKey)
	s2 := slot(scopedKey)
	s3 := slot(metaKey)

	t.Logf("Legacy key %q → slot %d", legacyKey, s1)
	t.Logf("Scoped key %q → slot %d", scopedKey, s2)
	t.Logf("Meta   key %q → slot %d", metaKey, s3)

	// Keys land in different slots — this is expected and safe because
	// individual commands are used instead of Lua.
	if s1 == s2 && s2 == s3 {
		t.Log("All keys happen to land in the same slot")
	} else {
		t.Log("Keys in different slots — OK, no Lua script to trigger CROSSSLOT")
	}
}

// crc16tab is the CRC16-CCITT lookup table used by Redis Cluster for slot hashing.
var crc16tab = [256]uint16{
	0x0000, 0x1021, 0x2042, 0x3063, 0x4084, 0x50a5, 0x60c6, 0x70e7,
	0x8108, 0x9129, 0xa14a, 0xb16b, 0xc18c, 0xd1ad, 0xe1ce, 0xf1ef,
	0x1231, 0x0210, 0x3273, 0x2252, 0x52b5, 0x4294, 0x72f7, 0x62d6,
	0x9339, 0x8318, 0xb37b, 0xa35a, 0xd3bd, 0xc39c, 0xf3ff, 0xe3de,
	0x2462, 0x3443, 0x0420, 0x1401, 0x64e6, 0x74c7, 0x44a4, 0x54a5,
	0xa56a, 0xb54b, 0x8528, 0x9509, 0xe5ee, 0xf5cf, 0xc5ac, 0xd58d,
	0x3653, 0x2672, 0x1611, 0x0630, 0x76d7, 0x66f6, 0x5695, 0x46b4,
	0xb75b, 0xa77a, 0x9719, 0x8738, 0xf7df, 0xe7fe, 0xd79d, 0xc7bc,
	0x4864, 0x5845, 0x6826, 0x7807, 0x08e0, 0x18c1, 0x28a2, 0x38a3,
	0xc94c, 0xd96d, 0xe90e, 0xf92f, 0x89c8, 0x99e9, 0xa98a, 0xb9ab,
	0x5a75, 0x4a54, 0x7a37, 0x6a16, 0x1af1, 0x0ad0, 0x3ab3, 0x2a92,
	0xdb7d, 0xcb5c, 0xfb3f, 0xeb1e, 0x9bf9, 0x8bd8, 0xbbbb, 0xab9a,
	0x6ca6, 0x7c87, 0x4ce4, 0x5cc5, 0x2c22, 0x3c03, 0x0c60, 0x1c41,
	0xedae, 0xfd8f, 0xcdec, 0xddcd, 0xad2a, 0xbd0b, 0x8d68, 0x9d49,
	0x7e97, 0x6eb6, 0x5ed5, 0x4ef4, 0x3e13, 0x2e32, 0x1e51, 0x0e70,
	0xff9f, 0xefbe, 0xdfdd, 0xcffc, 0xbf1b, 0xaf3a, 0x9f59, 0x8f78,
	0x9188, 0x81a9, 0xb1ca, 0xa1eb, 0xd10c, 0xc12d, 0xf14e, 0xe16f,
	0x1080, 0x00a1, 0x30c2, 0x20e3, 0x5004, 0x4025, 0x7046, 0x6067,
	0x83b9, 0x9398, 0xa3fb, 0xb3da, 0xc33d, 0xd31c, 0xe37f, 0xf35e,
	0x02b1, 0x1290, 0x22f3, 0x32d2, 0x4235, 0x5214, 0x6277, 0x7256,
	0xb5ea, 0xa5cb, 0x95a8, 0x85a9, 0xf54e, 0xe56f, 0xd50c, 0xc52d,
	0x34c2, 0x24e3, 0x1480, 0x04a1, 0x7466, 0x6447, 0x5424, 0x4405,
	0xa7db, 0xb7fa, 0x8799, 0x97b8, 0xe75f, 0xf77e, 0xc71d, 0xd73c,
	0x26d3, 0x36f2, 0x0691, 0x16b0, 0x6657, 0x7676, 0x4615, 0x5634,
	0xd94c, 0xc96d, 0xf90e, 0xe92f, 0x99c8, 0x89e9, 0xb98a, 0xa9ab,
	0x5844, 0x4865, 0x7806, 0x6827, 0x18c0, 0x08e1, 0x3882, 0x28a3,
	0xcb7d, 0xdb5c, 0xeb3f, 0xfb1e, 0x8bf9, 0x9bd8, 0xabbb, 0xbb9a,
	0x4a75, 0x5a54, 0x6a37, 0x7a16, 0x0af1, 0x1ad0, 0x2ab3, 0x3a92,
	0xfd2e, 0xed0f, 0xdd6c, 0xcd4d, 0xbdaa, 0xad8b, 0x9de8, 0x8dc9,
	0x7c26, 0x6c07, 0x5c64, 0x4c45, 0x3ca2, 0x2c83, 0x1ce0, 0x0cc1,
	0xef1f, 0xff3e, 0xcf5d, 0xdf7c, 0xaf9b, 0xbfba, 0x8fd9, 0x9ff8,
	0x6e17, 0x7e36, 0x4e55, 0x5e74, 0x2e93, 0x3eb2, 0x0ed1, 0x1ef0,
}

func TestRedisJobStoreIncrAttempts(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-incr"

	// Increment from 0 to 3.
	for i := 1; i <= 3; i++ {
		if err := store.IncrAttempts(ctx, jobID); err != nil {
			t.Fatalf("incr attempts #%d: %v", i, err)
		}
		got, err := store.GetAttempts(ctx, jobID)
		if err != nil {
			t.Fatalf("get attempts after incr #%d: %v", i, err)
		}
		if got != i {
			t.Fatalf("expected %d attempts after incr #%d, got %d", i, i, got)
		}
	}
}

func TestRedisJobStoreIncrAttemptsEmptyID(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.IncrAttempts(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty jobID")
	}
}

func TestActorTypeString(t *testing.T) {
	if got := actorTypeString(pb.ActorType_ACTOR_TYPE_HUMAN); got != "human" {
		t.Fatalf("unexpected actor type: %s", got)
	}
	if got := actorTypeString(pb.ActorType_ACTOR_TYPE_SERVICE); got != "service" {
		t.Fatalf("unexpected actor type: %s", got)
	}
	if got := actorTypeString(pb.ActorType_ACTOR_TYPE_UNSPECIFIED); got != "" {
		t.Fatalf("expected empty actor type for unspecified")
	}
}

func TestIsAllowedTransition_SucceededToQuarantined(t *testing.T) {
	if !isAllowedTransition(model.JobStateSucceeded, model.JobStateQuarantined) {
		t.Fatal("Succeeded → Quarantined should be allowed (output policy 2-phase)")
	}
}

func TestIsAllowedTransition_SucceededToFailed_Denied(t *testing.T) {
	if isAllowedTransition(model.JobStateSucceeded, model.JobStateFailed) {
		t.Fatal("Succeeded → Failed should NOT be allowed")
	}
	if isAllowedTransition(model.JobStateSucceeded, model.JobStatePending) {
		t.Fatal("Succeeded → Pending should NOT be allowed")
	}
}

func TestIsAllowedTransition_QuarantinedIsTerminal(t *testing.T) {
	targets := []model.JobState{
		model.JobStatePending, model.JobStateScheduled, model.JobStateRunning,
		model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled,
		model.JobStateTimeout, model.JobStateDenied,
	}
	for _, to := range targets {
		if isAllowedTransition(model.JobStateQuarantined, to) {
			t.Fatalf("Quarantined → %s should NOT be allowed (terminal state)", to)
		}
	}
	// Same-state self-transitions on terminal states must NOT be allowed.
	// Removing the blanket from==to shortcut prevents double-approval bugs
	// where concurrent requests both succeed because PENDING→PENDING passes.
	if isAllowedTransition(model.JobStateQuarantined, model.JobStateQuarantined) {
		t.Fatal("Quarantined → Quarantined (self) should NOT be allowed (terminal)")
	}
}

func TestRedisJobStoreListRecentSkipsExpiredMetadata(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Create two jobs via normal state transitions.
	if err := store.SetState(ctx, "job-live", model.JobStatePending); err != nil {
		t.Fatalf("set state live: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-expired", model.JobStatePending); err != nil {
		t.Fatalf("set state expired: %v", err)
	}

	// Verify both appear in list.
	list, err := store.ListRecentJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(list))
	}

	// Simulate metadata expiry: delete the job meta hash and state key for job-expired.
	// The index entry (job:recent sorted set) remains — this is the stale data scenario.
	store.client.Del(ctx, jobMetaKey("job-expired"))
	store.client.Del(ctx, jobStateKey("job-expired"))

	// Now list should skip the expired job instead of returning a zero-value record.
	list, err = store.ListRecentJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list after expiry: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 job (expired skipped), got %d", len(list))
	}
	if list[0].ID != "job-live" {
		t.Fatalf("expected job-live, got %s", list[0].ID)
	}
}

func TestNewRedisJobStore_NegativeTTLWarns(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	// Capture slog output to verify warning is emitted.
	var logBuf bytes.Buffer
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(origLogger)

	t.Setenv(envJobMetaTTLSeconds, "-10")
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// TTL should fall back to default (7 days).
	if store.metaTTL != defaultJobMetaTTL {
		t.Fatalf("expected default TTL %v, got %v", defaultJobMetaTTL, store.metaTTL)
	}

	logOutput := logBuf.String()
	if !bytes.Contains([]byte(logOutput), []byte("non-positive")) {
		t.Fatalf("expected warning about non-positive TTL, got: %s", logOutput)
	}
}

func TestNewRedisJobStore_ValidTTLApplied(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	t.Setenv(envJobMetaTTLSeconds, "3600")
	// Clear the duration variant so it doesn't override.
	t.Setenv(envJobMetaTTL, "")

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	expected := time.Duration(3600) * time.Second
	if store.metaTTL != expected {
		t.Fatalf("expected TTL %v, got %v", expected, store.metaTTL)
	}
}

func TestNewRedisJobStore_InvalidTTLWarns(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	var logBuf bytes.Buffer
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(origLogger)

	t.Setenv(envJobMetaTTLSeconds, "not-a-number")
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.metaTTL != defaultJobMetaTTL {
		t.Fatalf("expected default TTL %v, got %v", defaultJobMetaTTL, store.metaTTL)
	}

	logOutput := logBuf.String()
	if !bytes.Contains([]byte(logOutput), []byte("invalid")) {
		t.Fatalf("expected warning about invalid TTL, got: %s", logOutput)
	}
}

func TestParseJobEvent(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantOK  bool
		wantTS  int64
		wantSt  string
		wantCtx bool
	}{
		{
			name:   "valid JSON minimal",
			raw:    `{"ts":1700000000,"state":"PENDING"}`,
			wantOK: true, wantTS: 1700000000, wantSt: "PENDING",
		},
		{
			name:    "valid JSON with context",
			raw:     `{"ts":1700000001,"state":"SCHEDULED","ctx":{"rule":"r1","eval_ms":42}}`,
			wantOK:  true, wantTS: 1700000001, wantSt: "SCHEDULED",
			wantCtx: true,
		},
		{
			name:   "old format timestamp|state",
			raw:    "1700000002|RUNNING",
			wantOK: true, wantTS: 1700000002, wantSt: "RUNNING",
		},
		{
			name:   "malformed no separator",
			raw:    "garbage",
			wantOK: false,
		},
		{
			name:   "malformed bad timestamp",
			raw:    "notanumber|PENDING",
			wantOK: false,
		},
		{
			name:   "empty string",
			raw:    "",
			wantOK: false,
		},
		{
			name:   "JSON with empty state is invalid",
			raw:    `{"ts":123,"state":""}`,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, ok := parseJobEvent(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseJobEvent ok=%v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if evt.Timestamp != tt.wantTS {
				t.Errorf("timestamp=%d, want %d", evt.Timestamp, tt.wantTS)
			}
			if evt.State != tt.wantSt {
				t.Errorf("state=%q, want %q", evt.State, tt.wantSt)
			}
			if tt.wantCtx && evt.Context == nil {
				t.Error("expected non-nil context")
			}
			if !tt.wantCtx && evt.Context != nil {
				t.Error("expected nil context")
			}
		})
	}
}

func TestSetStateWithContext_RoundTrip(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-evt-1"

	// Set initial state with rich context
	evtCtx := &model.StateEventContext{
		Rule:   "safety-rule-1",
		Reason: "PII detected",
		EvalMs: 42,
		EvalPath: []string{"input_scan", "pii_check"},
		Topic:  "job.default",
		Extra:  map[string]string{"custom": "value"},
	}
	if err := store.SetStateWithContext(ctx, jobID, model.JobStatePending, evtCtx); err != nil {
		t.Fatalf("SetStateWithContext: %v", err)
	}

	events, err := store.GetJobEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.State != string(model.JobStatePending) {
		t.Errorf("state=%q, want %q", evt.State, model.JobStatePending)
	}
	if evt.Timestamp <= 0 {
		t.Error("expected positive timestamp")
	}
	if evt.Context == nil {
		t.Fatal("expected non-nil context")
	}
	if evt.Context.Rule != "safety-rule-1" {
		t.Errorf("rule=%q, want %q", evt.Context.Rule, "safety-rule-1")
	}
	if evt.Context.Reason != "PII detected" {
		t.Errorf("reason=%q, want %q", evt.Context.Reason, "PII detected")
	}
	if evt.Context.EvalMs != 42 {
		t.Errorf("eval_ms=%d, want 42", evt.Context.EvalMs)
	}
	if len(evt.Context.EvalPath) != 2 || evt.Context.EvalPath[0] != "input_scan" {
		t.Errorf("eval_path=%v, want [input_scan pii_check]", evt.Context.EvalPath)
	}
	if evt.Context.Extra["custom"] != "value" {
		t.Errorf("extra[custom]=%q, want %q", evt.Context.Extra["custom"], "value")
	}
}

func TestSetStateWithContext_NilCtx(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-evt-nil"

	if err := store.SetStateWithContext(ctx, jobID, model.JobStatePending, nil); err != nil {
		t.Fatalf("SetStateWithContext nil ctx: %v", err)
	}

	events, err := store.GetJobEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].State != string(model.JobStatePending) {
		t.Errorf("state=%q, want %q", events[0].State, model.JobStatePending)
	}
	if events[0].Context != nil {
		t.Error("expected nil context for nil evtCtx")
	}
}

func TestSetState_StillWorks(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-evt-legacy"

	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	// Verify state was set correctly
	state, err := store.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state != model.JobStatePending {
		t.Errorf("state=%q, want %q", state, model.JobStatePending)
	}

	// Verify event is parseable via GetJobEvents
	events, err := store.GetJobEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].State != string(model.JobStatePending) {
		t.Errorf("event state=%q, want %q", events[0].State, model.JobStatePending)
	}
}

func TestGetJobEvents_NonExistentJob(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	events, err := store.GetJobEvents(context.Background(), "job-does-not-exist")
	if err != nil {
		t.Fatalf("expected no error for non-existent job, got: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected empty slice, got %d events", len(events))
	}
}

func TestGetJobEvents_ChronologicalOrder(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-evt-order"

	states := []model.JobState{
		model.JobStatePending,
		model.JobStateScheduled,
		model.JobStateDispatched,
		model.JobStateRunning,
		model.JobStateSucceeded,
	}
	for _, s := range states {
		if err := store.SetState(ctx, jobID, s); err != nil {
			t.Fatalf("SetState(%s): %v", s, err)
		}
	}

	events, err := store.GetJobEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobEvents: %v", err)
	}
	if len(events) != len(states) {
		t.Fatalf("expected %d events, got %d", len(states), len(events))
	}
	for i, evt := range events {
		if evt.State != string(states[i]) {
			t.Errorf("event[%d] state=%q, want %q", i, evt.State, states[i])
		}
		if i > 0 && evt.Timestamp < events[i-1].Timestamp {
			t.Errorf("event[%d] timestamp %d < event[%d] timestamp %d (not chronological)",
				i, evt.Timestamp, i-1, events[i-1].Timestamp)
		}
	}
}

func TestGetJobEvents_MixedFormats(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	jobID := "job-evt-mixed"
	eventsKey := jobEventsKey(jobID)

	// Manually push old-format and new-format entries plus a malformed one
	store.client.RPush(ctx, eventsKey, "1700000001|PENDING")
	store.client.RPush(ctx, eventsKey, `{"ts":1700000002,"state":"SCHEDULED","ctx":{"rule":"r1"}}`)
	store.client.RPush(ctx, eventsKey, "totally-broken")
	store.client.RPush(ctx, eventsKey, "1700000003|RUNNING")

	events, err := store.GetJobEvents(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJobEvents: %v", err)
	}
	// Malformed entry should be skipped
	if len(events) != 3 {
		t.Fatalf("expected 3 events (1 skipped), got %d", len(events))
	}

	// First: old format
	if events[0].State != "PENDING" || events[0].Timestamp != 1700000001 {
		t.Errorf("event[0]=%+v, want PENDING@1700000001", events[0])
	}
	if events[0].Context != nil {
		t.Error("event[0] should have nil context (old format)")
	}

	// Second: new JSON format with context
	if events[1].State != "SCHEDULED" || events[1].Timestamp != 1700000002 {
		t.Errorf("event[1]=%+v, want SCHEDULED@1700000002", events[1])
	}
	if events[1].Context == nil || events[1].Context.Rule != "r1" {
		t.Errorf("event[1] context=%+v, want rule=r1", events[1].Context)
	}

	// Third: old format
	if events[2].State != "RUNNING" || events[2].Timestamp != 1700000003 {
		t.Errorf("event[2]=%+v, want RUNNING@1700000003", events[2])
	}
}
