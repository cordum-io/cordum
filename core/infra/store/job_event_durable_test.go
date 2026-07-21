package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

func TestJobEventFenceUsesOneClusterSlotKey(t *testing.T) {
	jobID := "tenant/a:job{unsafe}"
	first := jobDispatchEventsKey(jobID, "dispatch-1")
	second := jobDispatchEventsKey(jobID, "dispatch-2")
	if first != second {
		t.Fatalf("event fence keys differ: %q != %q", first, second)
	}
	tag := redisHashTag(first)
	if tag == "" || strings.ContainsAny(tag, "{}") {
		t.Fatalf("event fence key %q has unsafe hash tag %q", first, tag)
	}
}

func TestApplyJobResultRejectsMessageIDDigestConflict(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	dispatchID, attempt := beginRunningDispatch(t, store, "job-conflict")
	apply := durableResultApply("job-conflict", dispatchID, attempt)
	if got, err := store.ApplyJobResult(context.Background(), apply); err != nil || got != model.JobEventApplied {
		t.Fatalf("ApplyJobResult(first) = (%v, %v)", got, err)
	}
	apply.Digest = bytes.Repeat([]byte{0x6b}, 32)
	if _, err := store.ApplyJobResult(context.Background(), apply); !errors.Is(err, ErrJobEventDigestConflict) {
		t.Fatalf("ApplyJobResult(conflict) error = %v, want %v", err, ErrJobEventDigestConflict)
	}
}

func TestApplyJobResultRejectsWrongAuthenticatedTenant(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	dispatchID, attempt := beginRunningDispatch(t, store, "job-wrong-tenant")
	apply := durableResultApply("job-wrong-tenant", dispatchID, attempt)
	apply.Tenant = "tenant-evil"
	if got, err := store.ApplyJobResult(context.Background(), apply); err != nil || got != model.JobEventRejected {
		t.Fatalf("ApplyJobResult(wrong tenant) = (%v, %v), want rejected", got, err)
	}
	state, err := store.GetState(context.Background(), apply.JobID)
	if err != nil || state != model.JobStateRunning {
		t.Fatalf("state after rejected tenant = (%q, %v), want running", state, err)
	}
}

func TestJobEventFenceUsesJobLifecycleTTL(t *testing.T) {
	srv := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dispatchID, attempt := beginRunningDispatch(t, store, "job-long-running")
	if accepted, err := store.AcceptJobEvent(
		context.Background(), "job-long-running", dispatchID, attempt,
		"worker-1", "tenant-a", "progress-1",
	); err != nil || !accepted {
		t.Fatalf("AcceptJobEvent(first) = (%v, %v)", accepted, err)
	}
	srv.FastForward(2 * time.Hour)
	if accepted, err := store.AcceptJobEvent(
		context.Background(), "job-long-running", dispatchID, attempt,
		"worker-1", "tenant-a", "progress-1",
	); err != nil || accepted {
		t.Fatalf("AcceptJobEvent(after two hours) = (%v, %v), want duplicate", accepted, err)
	}
}

func TestAcceptSignedJobEventBindsMessageIDToDigest(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	dispatchID, attempt := beginRunningDispatch(t, store, "job-signed-progress")
	messageID := []byte("0123456789abcdef")
	digest := bytes.Repeat([]byte{0x31}, 32)
	disposition, err := store.AcceptSignedJobEvent(
		context.Background(), "job-signed-progress", dispatchID, attempt,
		"worker-1", "tenant-a", messageID, digest,
	)
	if err != nil || disposition != model.JobEventApplied {
		t.Fatalf("AcceptSignedJobEvent(first) = (%v, %v)", disposition, err)
	}
	disposition, err = store.AcceptSignedJobEvent(
		context.Background(), "job-signed-progress", dispatchID, attempt,
		"worker-1", "tenant-a", messageID, digest,
	)
	if err != nil || disposition != model.JobEventDuplicate {
		t.Fatalf("AcceptSignedJobEvent(redelivery) = (%v, %v)", disposition, err)
	}
	_, err = store.AcceptSignedJobEvent(
		context.Background(), "job-signed-progress", dispatchID, attempt,
		"worker-1", "tenant-a", messageID, bytes.Repeat([]byte{0x32}, 32),
	)
	if !errors.Is(err, ErrJobEventDigestConflict) {
		t.Fatalf("AcceptSignedJobEvent(conflict) error = %v", err)
	}
}

func TestAckJobEffectRequiresCommittedDigest(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	dispatchID, attempt := beginRunningDispatch(t, store, "job-ack")
	apply := durableResultApply("job-ack", dispatchID, attempt)
	if _, err := store.ApplyJobResult(context.Background(), apply); err != nil {
		t.Fatalf("ApplyJobResult() error = %v", err)
	}
	effects, err := store.PendingJobEffects(context.Background(), 10)
	if err != nil || len(effects) != 1 {
		t.Fatalf("PendingJobEffects() = (%v, %v)", effects, err)
	}
	wrong := effects[0]
	wrong.Digest = bytes.Repeat([]byte{0xff}, 32)
	if acked, err := store.AckJobEffect(context.Background(), wrong); err != nil || acked {
		t.Fatalf("AckJobEffect(wrong digest) = (%v, %v), want false", acked, err)
	}
	if acked, err := store.AckJobEffect(context.Background(), effects[0]); err != nil || !acked {
		t.Fatalf("AckJobEffect(correct) = (%v, %v), want true", acked, err)
	}
}

func TestPendingJobEffectsFailsClosedOnCorruptEffect(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	key := jobRuntimeKey("job-corrupt-effect")
	if err := store.client.HSet(
		context.Background(), key, runtimeEffectPrefix+"event", "not-base64",
	).Err(); err != nil {
		t.Fatalf("seed corrupt effect error = %v", err)
	}
	if _, err := store.PendingJobEffects(context.Background(), 10); err == nil {
		t.Fatal("PendingJobEffects() accepted corrupt durable effect")
	}
}

func TestApplyJobResultCommitsStatePointerAndOneOutboxEffect(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	dispatchID, attempt := beginRunningDispatch(t, store, "job-apply")
	apply := durableResultApply("job-apply", dispatchID, attempt)

	disposition, err := store.ApplyJobResult(ctx, apply)
	if err != nil || disposition != model.JobEventApplied {
		t.Fatalf("ApplyJobResult(first) = (%v, %v), want (applied, nil)", disposition, err)
	}
	state, err := store.GetState(ctx, apply.JobID)
	if err != nil || state != model.JobStateSucceeded {
		t.Fatalf("GetState() = (%q, %v), want succeeded", state, err)
	}
	resultPtr, err := store.GetResultPtr(ctx, apply.JobID)
	if err != nil || resultPtr != apply.ResultPtr {
		t.Fatalf("GetResultPtr() = (%q, %v), want %q", resultPtr, err, apply.ResultPtr)
	}
	effects, err := store.PendingJobEffects(ctx, 10)
	if err != nil || len(effects) != 1 || !bytes.Equal(effects[0].Payload, apply.Effect) {
		t.Fatalf("PendingJobEffects() = (%v, %v), want one committed effect", effects, err)
	}

	disposition, err = store.ApplyJobResult(ctx, apply)
	if err != nil || disposition != model.JobEventDuplicate {
		t.Fatalf("ApplyJobResult(redelivery) = (%v, %v), want (duplicate, nil)", disposition, err)
	}
	effects, err = store.PendingJobEffects(ctx, 10)
	if err != nil || len(effects) != 1 {
		t.Fatalf("redelivery effects = (%d, %v), want exactly one", len(effects), err)
	}
}

func TestProjectJobResultIsIdempotentAndCompletesLegacyIndexes(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	ctx := context.Background()
	jobID := "job-project"
	dispatchID, attempt := beginRunningDispatch(t, store, jobID)
	if err := store.client.HSet(ctx, jobMetaKey(jobID), metaFieldTenant, "tenant-a", metaFieldDeadline, "42").Err(); err != nil {
		t.Fatalf("seed job meta error = %v", err)
	}
	if err := store.client.SAdd(ctx, tenantActiveKey("tenant-a"), jobID).Err(); err != nil {
		t.Fatalf("seed active index error = %v", err)
	}
	if err := store.client.ZAdd(ctx, deadlineIndexKey(), redis.Z{Score: 42, Member: jobID}).Err(); err != nil {
		t.Fatalf("seed deadline index error = %v", err)
	}
	apply := durableResultApply(jobID, dispatchID, attempt)
	if _, err := store.ApplyJobResult(ctx, apply); err != nil {
		t.Fatalf("ApplyJobResult() error = %v", err)
	}
	for range 2 {
		if err := store.ProjectJobResult(ctx, jobID, apply.State, apply.ResultPtr, apply.WorkerID); err != nil {
			t.Fatalf("ProjectJobResult() error = %v", err)
		}
	}
	if active, err := store.client.SIsMember(ctx, tenantActiveKey("tenant-a"), jobID).Result(); err != nil || active {
		t.Fatalf("tenant active after terminal project = (%v, %v), want false", active, err)
	}
	if deadline, err := store.client.ZScore(ctx, deadlineIndexKey(), jobID).Result(); err != redis.Nil {
		t.Fatalf("deadline after terminal project = (%v, %v), want redis.Nil", deadline, err)
	}
	if score, err := store.client.ZScore(ctx, stateIndexKey(model.JobStateSucceeded), jobID).Result(); err != nil || score == 0 {
		t.Fatalf("succeeded index = (%v, %v), want populated", score, err)
	}
	if worker, err := store.client.HGet(ctx, jobMetaKey(jobID), metaFieldWorkerID).Result(); err != nil || worker != apply.WorkerID {
		t.Fatalf("projected worker = (%q, %v), want %q", worker, err, apply.WorkerID)
	}
	if _, err := store.client.ZScore(ctx, workerJobsKey(apply.WorkerID), jobID).Result(); err != nil {
		t.Fatalf("worker index error = %v", err)
	}
	if _, err := store.client.ZScore(ctx, stateIndexKey(model.JobStateRunning), jobID).Result(); err != redis.Nil {
		t.Fatalf("running index error = %v, want redis.Nil", err)
	}
}

func TestApplyJobResultConcurrentMessageHasOneWinner(t *testing.T) {
	store := newDispatchFencingTestStore(t)
	dispatchID, attempt := beginRunningDispatch(t, store, "job-apply-race")
	apply := durableResultApply("job-apply-race", dispatchID, attempt)
	const goroutines = 20
	results := make(chan model.JobEventApplyDisposition, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.ApplyJobResult(context.Background(), apply)
			if err != nil {
				t.Errorf("ApplyJobResult() error = %v", err)
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result == model.JobEventApplied {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("applied winners = %d, want 1", winners)
	}
}

func beginRunningDispatch(t *testing.T, store *RedisJobStore, jobID string) (string, int) {
	t.Helper()
	ctx := context.Background()
	if err := store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState(pending) error = %v", err)
	}
	dispatchID, attempt, err := store.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch() error = %v", err)
	}
	if err := store.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("SetState(running) error = %v", err)
	}
	return dispatchID, attempt
}

func durableResultApply(jobID, dispatchID string, attempt int) model.JobResultApply {
	return model.JobResultApply{
		JobID: jobID, DispatchID: dispatchID, Attempt: attempt,
		WorkerID: "worker-1", Tenant: "tenant-a",
		MessageID: []byte("0123456789abcdef"), Digest: bytes.Repeat([]byte{0x5a}, 32),
		State: model.JobStateSucceeded, ResultPtr: "redis://result/" + jobID,
		Effect: []byte("accepted-result:" + jobID),
	}
}

func redisHashTag(key string) string {
	start := strings.IndexByte(key, '{')
	if start < 0 {
		return ""
	}
	rest := key[start+1:]
	end := strings.IndexByte(rest, '}')
	if end <= 0 {
		return ""
	}
	return rest[:end]
}
