package memory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/scheduler"
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

	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	state, err := store.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state != scheduler.JobStatePending {
		t.Fatalf("expected state %s, got %s", scheduler.JobStatePending, state)
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
	if err := store.SetState(ctx, jobID, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance state: %v", err)
	}
	records, err := store.ListJobsByState(ctx, scheduler.JobStateScheduled, time.Now().Unix(), 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(records) != 1 || records[0].ID != jobID {
		t.Fatalf("unexpected records: %#v", records)
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

	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	// invalid backwards transition
	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("same state should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobID, scheduler.JobStateDispatched); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobID, scheduler.JobStateScheduled); err == nil {
		t.Fatalf("expected invalid backward transition")
	}

	jobPendingFail := "job-456-pending-fail"
	if err := store.SetState(ctx, jobPendingFail, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, scheduler.JobStateFailed); err != nil {
		t.Fatalf("pending -> failed should be ok: %v", err)
	}
	if err := store.SetState(ctx, jobPendingFail, scheduler.JobStateFailed); err != nil {
		t.Fatalf("same terminal state should be ok: %v", err)
	}

	jobScheduledFail := "job-456-scheduled-fail"
	if err := store.SetState(ctx, jobScheduledFail, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledFail, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledFail, scheduler.JobStateFailed); err != nil {
		t.Fatalf("scheduled -> failed should be ok: %v", err)
	}

	jobScheduledSuccess := "job-456-scheduled-success"
	if err := store.SetState(ctx, jobScheduledSuccess, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledSuccess, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledSuccess, scheduler.JobStateSucceeded); err != nil {
		t.Fatalf("scheduled -> succeeded should be ok: %v", err)
	}

	jobScheduledCancel := "job-456-scheduled-cancel"
	if err := store.SetState(ctx, jobScheduledCancel, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledCancel, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledCancel, scheduler.JobStateCancelled); err != nil {
		t.Fatalf("scheduled -> cancelled should be ok: %v", err)
	}

	jobPendingTimeout := "job-456-pending-timeout"
	if err := store.SetState(ctx, jobPendingTimeout, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobPendingTimeout, scheduler.JobStateTimeout); err != nil {
		t.Fatalf("pending -> timeout should be ok: %v", err)
	}

	jobScheduledTimeout := "job-456-scheduled-timeout"
	if err := store.SetState(ctx, jobScheduledTimeout, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledTimeout, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.SetState(ctx, jobScheduledTimeout, scheduler.JobStateTimeout); err != nil {
		t.Fatalf("scheduled -> timeout should be ok: %v", err)
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
	if err := store.SetState(ctx, "job-a", scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-b", scheduler.JobStateDispatched); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.SetState(ctx, "job-c", scheduler.JobStateRunning); err != nil {
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
	if list[0].State != scheduler.JobStateRunning {
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
	if err := store.SetState(ctx, "job-1", scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-2", scheduler.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-3", scheduler.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := store.SetState(ctx, "job-3", scheduler.JobStateSucceeded); err != nil {
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
	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	state, err := store.CancelJob(ctx, jobID)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if state != scheduler.JobStateCancelled {
		t.Fatalf("expected cancelled state, got %s", state)
	}
	updated, err := store.GetState(ctx, jobID)
	if err != nil || updated != scheduler.JobStateCancelled {
		t.Fatalf("expected cancelled state persisted, got %s err=%v", updated, err)
	}

	terminalID := "job-terminal"
	if err := store.SetState(ctx, terminalID, scheduler.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if err := store.SetState(ctx, terminalID, scheduler.JobStateScheduled); err != nil {
		t.Fatalf("set scheduled state: %v", err)
	}
	if err := store.SetState(ctx, terminalID, scheduler.JobStateSucceeded); err != nil {
		t.Fatalf("set terminal state: %v", err)
	}
	state, err = store.CancelJob(ctx, terminalID)
	if err != nil {
		t.Fatalf("cancel job terminal: %v", err)
	}
	if state != scheduler.JobStateSucceeded {
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
	if err := store.SetState(ctx, jobID, scheduler.JobStateRunning); err != nil {
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
	if err := store.SetState(ctx, jobID, scheduler.JobStateRunning); err != nil {
		t.Fatalf("set state: %v", err)
	}
	count, err := store.CountActiveByTenant(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("count active: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active job, got %d", count)
	}
	if err := store.SetState(ctx, jobID, scheduler.JobStateSucceeded); err != nil {
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
	first := scheduler.SafetyDecisionRecord{
		Decision:       scheduler.SafetyAllow,
		Reason:         "ok",
		ApprovalRequired: true,
		Constraints:    &pb.PolicyConstraints{RedactionLevel: "low"},
	}
	if err := store.SetSafetyDecision(context.Background(), jobID, first); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	second := scheduler.SafetyDecisionRecord{
		Decision:       scheduler.SafetyDeny,
		Reason:         "blocked",
		Constraints:    &pb.PolicyConstraints{RedactionLevel: "high"},
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
	if decisions[0].Decision != scheduler.SafetyDeny {
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

	ok, err := store.TryAcquireLock(ctx, "lock-1", time.Second)
	if err != nil || !ok {
		t.Fatalf("expected lock acquired")
	}
	if err := store.ReleaseLock(ctx, "lock-1"); err != nil {
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

	if err := store.SetSafetyDecision(ctx, jobID, scheduler.SafetyDecisionRecord{Decision: scheduler.SafetyDeny, Reason: "forbidden"}); err != nil {
		t.Fatalf("set safety decision: %v", err)
	}
	record, err := store.GetSafetyDecision(ctx, jobID)
	if err != nil {
		t.Fatalf("get safety decision: %v", err)
	}
	if record.Decision != scheduler.SafetyDeny || record.Reason != "forbidden" {
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
	if err := store.SetState(ctx, jobID, scheduler.JobStatePending); err != nil {
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
	record := scheduler.SafetyDecisionRecord{
		Decision:       scheduler.SafetyDeny,
		Reason:         "blocked",
		RuleID:         "rule-1",
		PolicySnapshot: "snap",
		ApprovalRequired: true,
		ApprovalRef:       "approval-1",
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
