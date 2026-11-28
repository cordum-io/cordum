package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// Retention proofs cited by docs/contract/deletion-proofs.md. Each proof writes
// a marker through the production writer with its retention shortened, requires
// tracescan to find it in every key family it names, shows it survives until
// just before the shortened TTL, moves miniredis just past it, runs the cleanup
// the inventory names, then requires no trace in the writer client's keyspace.
// Exports: these writers reach none; they write only the Redis keys named here.

func TestRetentionProofDIC11(t *testing.T) {
	const ttl = time.Minute
	t.Setenv("REDIS_DATA_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisStoreFromClient(client)
	if err := s.PutContext(ctx, MakeContextKey(marker), []byte("input "+marker)); err != nil {
		t.Fatalf("PutContext: %v", err)
	}
	if err := s.PutResult(ctx, MakeResultKey(marker), []byte("output "+marker)); err != nil {
		t.Fatalf("PutResult: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "ctx:", "res:")
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC22: one job through every DI-C22 writer, then a
// terminal transition, then JOB_META_TTL.
func TestRetentionProofDIC22(t *testing.T) {
	const ttl = time.Hour
	t.Setenv("JOB_META_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisJobStoreFromClient(client)
	jobID, tenant, worker := marker+"-job", marker+"-tenant", marker+"-worker"
	req := &pb.JobRequest{
		JobId: jobID, Topic: "job." + marker, TenantId: tenant, PrincipalId: marker + "-principal",
		Labels: map[string]string{"proof": marker}, Budget: &pb.Budget{DeadlineMs: 600000},
	}
	if err := s.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}
	if err := s.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("SetJobRequest: %v", err)
	}
	if err := s.SetStateWithContext(ctx, jobID, model.JobStatePending, &model.StateEventContext{Reason: "queued " + marker}); err != nil {
		t.Fatalf("SetStateWithContext(pending): %v", err)
	}
	if err := s.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
		Decision: model.SafetyAllow, Reason: "allow " + marker, RuleID: marker + "-rule",
	}); err != nil {
		t.Fatalf("SetSafetyDecision: %v", err)
	}
	if err := s.SetOutputDecision(ctx, jobID, model.OutputSafetyRecord{Decision: model.OutputAllow, Reason: "output " + marker}); err != nil {
		t.Fatalf("SetOutputDecision: %v", err)
	}
	dispatchID, attempt, err := s.BeginDispatch(ctx, jobID, worker, tenant)
	if err != nil {
		t.Fatalf("BeginDispatch: %v", err)
	}
	// SetState moves the runtime fence to RUNNING, as the dispatch path does.
	if err := s.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("SetState(running): %v", err)
	}
	if err := s.SetWorkerID(ctx, jobID, worker); err != nil {
		t.Fatalf("SetWorkerID: %v", err)
	}
	resultPtr := PointerForKey(MakeResultKey(jobID))
	if err := s.SetResultPtr(ctx, jobID, resultPtr); err != nil {
		t.Fatalf("SetResultPtr: %v", err)
	}
	if got, err := s.ApplyJobResult(ctx, model.JobResultApply{
		JobID: jobID, DispatchID: dispatchID, Attempt: attempt, WorkerID: worker, Tenant: tenant,
		MessageID: []byte("message " + marker), Digest: bytes.Repeat([]byte{0x5a}, 32),
		State: model.JobStateSucceeded, ResultPtr: resultPtr, Effect: []byte("effect " + marker),
	}); err != nil || got != model.JobEventApplied {
		t.Fatalf("ApplyJobResult = %v, %v; want applied", got, err)
	}
	// The terminal transition clears these two, before any TTL.
	requireTrace(t, client, marker, "job:deadline:", "job:tenant:active:")
	if err := s.SetStateWithContext(ctx, jobID, model.JobStateSucceeded, &model.StateEventContext{ResultPtr: resultPtr}); err != nil {
		t.Fatalf("SetStateWithContext(succeeded): %v", err)
	}
	requireNoFamily(t, client, marker, "job:deadline:", "job:tenant:active:")
	expireJustPast(t, mr, client, ttl, marker,
		"job:meta:", "job:state:", "job:result_ptr:", "job:req:", "job:decisions:",
		"job:"+jobID+":output_decision", jobRuntimeKey(jobID), "worker:jobs:",
		"job:index:", "job:recent:", "job:events:")
	if _, err := s.SweepExpiredJobIndexes(ctx, time.Now().Add(ttl+time.Second)); err != nil {
		t.Fatalf("SweepExpiredJobIndexes: %v", err)
	}
	requireNoTrace(t, client, marker)
	// The runtime key names the job only in base64.
	requireNoTrace(t, client, base64.RawURLEncoding.EncodeToString([]byte(jobID)))
}

// TestRetentionProofDIC24: a deadline, a terminal state, 1000 newer jobs so
// the job:recent trim drops the id, then JOB_META_TTL.
func TestRetentionProofDIC24(t *testing.T) {
	const ttl = time.Hour
	t.Setenv("JOB_META_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisJobStoreFromClient(client)
	jobID := marker + "-job"
	if err := s.SetTenant(ctx, jobID, marker+"-tenant"); err != nil {
		t.Fatalf("SetTenant: %v", err)
	}
	if err := s.SetStateWithContext(ctx, jobID, model.JobStatePending, nil); err != nil {
		t.Fatalf("SetStateWithContext(pending): %v", err)
	}
	if err := s.SetDeadline(ctx, jobID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	requireTrace(t, client, marker, "job:index:", "job:recent:", "job:deadline:", "job:tenant:active:")
	if err := s.SetStateWithContext(ctx, jobID, model.JobStateSucceeded, nil); err != nil {
		t.Fatalf("SetStateWithContext(succeeded): %v", err)
	}
	for i := 0; i < 1000; i++ {
		if err := s.SetStateWithContext(ctx, fmt.Sprintf("newer-%04d", i), model.JobStatePending, nil); err != nil {
			t.Fatalf("SetStateWithContext(newer %d): %v", i, err)
		}
	}
	requireNoFamily(t, client, marker, "job:recent:", "job:deadline:", "job:tenant:active:")
	expireJustPast(t, mr, client, ttl, marker, "job:index:")
	if _, err := s.SweepExpiredJobIndexes(ctx, time.Now().Add(ttl+time.Second)); err != nil {
		t.Fatalf("SweepExpiredJobIndexes: %v", err)
	}
	requireNoTrace(t, client, marker)
}

func TestRetentionProofDIC25(t *testing.T) {
	const ttl = 2 * time.Minute
	t.Setenv("CORDUM_IDEMPOTENCY_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisJobStoreFromClient(client)
	tenant := marker + "-tenant"
	if ok, _, err := s.TrySetIdempotencyKeyScoped(ctx, tenant, marker+"-key", marker+"-job"); err != nil || !ok {
		t.Fatalf("TrySetIdempotencyKeyScoped = %v, %v; want claimed", ok, err)
	}
	if err := s.SetIdempotencyKeyScoped(ctx, tenant, marker+"-key2", marker+"-job2"); err != nil {
		t.Fatalf("SetIdempotencyKeyScoped: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "job:idempotency:")
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC28: the entry key expires with the store's entry TTL;
// dlq:index keeps the id until CleanupStaleEntries removes it.
func TestRetentionProofDIC28(t *testing.T) {
	const ttl = time.Hour
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewDLQStoreFromClient(client, ttl)
	if err := s.Add(ctx, DLQEntry{
		JobID: marker + "-job", Topic: "job." + marker, Status: "failed",
		Reason: "reason " + marker, ReasonCode: marker, LastState: "FAILED", Attempts: 3,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "dlq:entry:", "dlq:index:")
	requireTrace(t, client, marker, "dlq:index:")
	if removed, err := s.CleanupStaleEntries(ctx); err != nil || removed != 1 {
		t.Fatalf("CleanupStaleEntries = %d, %v; want 1 stale id removed", removed, err)
	}
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC37 relies on key expiry of the run record and its idx:t
// and idx:ds keys, which CreateRun gives the run TTL; it calls no cleanup by
// name.
func TestRetentionProofDIC37(t *testing.T) {
	t.Setenv("CORDUM_EVAL_RUN_TTL_SECONDS", "1")
	mr, client, marker := newProofRedis(t)
	s := NewEvalRunStoreFromClient(client)
	if err := s.CreateRun(context.Background(), runner.RunResult{
		RunID: marker + "-run", DatasetID: marker + "-ds", Tenant: "tenant-dic37", StartedAt: time.Now().UTC(),
		Entries: []runner.EntryResult{{EntryID: "entry-1", Reason: "output " + marker}},
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	expireJustPast(t, mr, client, time.Second, marker,
		"eval:run:tenant-dic37:rec:", "eval:run:tenant-dic37:idx:t:", "eval:run:tenant-dic37:idx:ds:")
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC49: no Release; the caller's lock TTL is the cleanup.
// Each owner token TryAcquireLock returns is a second marker.
func TestRetentionProofDIC49(t *testing.T) {
	const ttl = 30 * time.Second
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	jobs := NewRedisJobStoreFromClient(client)
	keys := []string{"cordum:scheduler:job:" + marker, "cordum:wf:run:lock:" + marker, "cordum:wf:run:admission:" + marker}
	tokens := make([]string, 0, len(keys))
	for _, key := range keys {
		token, err := jobs.TryAcquireLock(ctx, key, ttl)
		if err != nil || token == "" {
			t.Fatalf("TryAcquireLock(%s) = %q, %v; want a token", key, token, err)
		}
		requireTrace(t, client, token, key)
		tokens = append(tokens, token)
	}
	resource := "tenant:" + marker + ":deploy"
	if _, ok, err := locks.NewRedisStoreFromClient(client).Acquire(ctx, resource, marker+"-owner", locks.ModeExclusive, ttl); err != nil || !ok {
		t.Fatalf("locks Acquire = %v, %v; want acquired", ok, err)
	}
	expireJustPast(t, mr, client, ttl, marker,
		"cordum:scheduler:job:", "cordum:wf:run:lock:", "cordum:wf:run:admission:", "lock:tenant:")
	requireNoTrace(t, client, marker)
	for _, token := range tokens {
		requireNoTrace(t, client, token)
	}
}

func newProofRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
}

// expireJustPast requires the marker in each family, then again one second
// before ttl, then moves miniredis one second past ttl.
func expireJustPast(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient, ttl time.Duration, marker string, families ...string) {
	t.Helper()
	requireTrace(t, client, marker, families...)
	mr.FastForward(ttl - time.Second)
	requireTrace(t, client, marker, families...)
	mr.FastForward(2 * time.Second)
}

func requireTrace(t *testing.T, client redis.UniversalClient, marker string, families ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if len(hits) == 0 {
		t.Fatalf("no trace of %q", marker)
	}
	for _, family := range families {
		if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("no %s* trace of %q; hits=%v", family, marker, hits)
		}
	}
}

// requireNoFamily fails if a family still holds marker before its TTL fires.
func requireNoFamily(t *testing.T, client redis.UniversalClient, marker string, families ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	for _, family := range families {
		if slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("%s* trace of %q remains; hits=%v", family, marker, hits)
		}
	}
}

func requireNoTrace(t *testing.T, client redis.UniversalClient, marker string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
	}
}

func TestRetentionProofDIC06(t *testing.T) {
	t.Setenv("CORDUM_DECISION_LOG_TTL_SECONDS", "1")
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisDecisionLogStoreFromClient(client)
	const tenant = "tenant-dic06"
	record := model.DecisionLogRecord{
		Tenant: tenant, JobID: marker + "-job", RuleID: marker + "-rule", AgentID: marker + "-agent",
		Topic: "job." + marker, Reason: "reason " + marker, Verdict: model.SafetyDeny,
		Timestamp: time.Now().UTC().UnixMilli(),
	}
	if err := s.AppendDecision(ctx, record); err != nil {
		t.Fatalf("AppendDecision: %v", err)
	}
	recordID := decisionRecordID(record)
	page, err := s.QueryDecisions(ctx, model.DecisionQuery{Tenant: tenant, RuleID: record.RuleID, AgentID: record.AgentID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("filtered QueryDecisions = %d items, %v; want the record", len(page.Items), err)
	}
	requireTrace(t, client, marker, "gov:dec:tenant-dic06:rec:",
		"gov:dec:tenant-dic06:idx:rule:", "gov:dec:tenant-dic06:idx:agent:", "gov:dec:tenant-dic06:idx:topic:")
	expireJustPast(t, mr, client, time.Second, recordID, "gov:dec:tenant-dic06:rec:", "gov:dec:tenant-dic06:idx:t:",
		"gov:dec:tenant-dic06:idx:rule:", "gov:dec:tenant-dic06:idx:agent:", "gov:dec:tenant-dic06:idx:topic:",
		"gov:dec:tenant-dic06:idx:verdict:deny:")
	// The index trim cutoff reads the wall clock, not miniredis time.
	time.Sleep(time.Until(time.UnixMilli(record.Timestamp).Add(time.Second + 50*time.Millisecond)))
	if err := s.AppendDecision(ctx, model.DecisionLogRecord{
		Tenant: tenant, JobID: "job-later", RuleID: "rule-later", AgentID: "agent-later",
		Topic: "job.later", Verdict: model.SafetyDeny,
	}); err != nil {
		t.Fatalf("later AppendDecision: %v", err)
	}
	if _, err := s.QueryDecisions(ctx, model.DecisionQuery{Tenant: tenant}); err != nil {
		t.Fatalf("unfiltered QueryDecisions: %v", err)
	}
	requireNoTrace(t, client, marker)
	requireNoTrace(t, client, recordID)
}
