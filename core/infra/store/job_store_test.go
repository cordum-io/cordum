package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
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
	defer store.Close()

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
	defer store.Close()

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

func TestRedisJobStoreTransitionGuard(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	jobID := "job-456"

	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// invalid backwards transition
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("same state should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateDispatched); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateScheduled); err == nil {
		t.Fatalf("expected invalid backward transition")
	}

	jobPendingFail := "job-456-pending-fail"
	if err := store.SetState(ctx, jobPendingFail, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, model.JobStateFailed); err != nil {
		t.Fatalf("pending -> failed should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, model.JobStateFailed); err != nil {
		t.Fatalf("same terminal state should be ok: %v", err)
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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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

func TestRedisJobStoreCancelJob(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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

func TestRedisJobStoreTraceAndPrincipal(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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
	defer store.Close()

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

func TestRedisJobStoreIncrAttempts(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

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
	defer store.Close()

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
	// Self-transition is always allowed.
	if !isAllowedTransition(model.JobStateQuarantined, model.JobStateQuarantined) {
		t.Fatal("Quarantined → Quarantined (self) should be allowed")
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
	defer store.Close()

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
	defer store.Close()

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
	os.Unsetenv(envJobMetaTTL)

	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("failed to create job store: %v", err)
	}
	defer store.Close()

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
	defer store.Close()

	if store.metaTTL != defaultJobMetaTTL {
		t.Fatalf("expected default TTL %v, got %v", defaultJobMetaTTL, store.metaTTL)
	}

	logOutput := logBuf.String()
	if !bytes.Contains([]byte(logOutput), []byte("invalid")) {
		t.Fatalf("expected warning about invalid TTL, got: %s", logOutput)
	}
}
