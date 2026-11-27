package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

func TestSweepExpiredJobIndexesKeepsLiveJobViews(t *testing.T) {
	mr, client, s := newSweepTestStore(t)
	ctx := context.Background()
	succeeded := stateIndexKey(model.JobStateSucceeded)
	setSweepTestStates(t, s, "expired-job", model.JobStatePending, model.JobStateSucceeded)
	mr.FastForward(30 * time.Minute)
	setSweepTestStates(t, s, "live-job", model.JobStatePending, model.JobStateRunning, model.JobStateSucceeded)
	// A job known only by its job:state key is live too.
	if err := client.Set(ctx, jobStateKey("state-only-job"), string(model.JobStateSucceeded), time.Hour).Err(); err != nil {
		t.Fatalf("Set job:state: %v", err)
	}
	if err := client.ZAdd(ctx, succeeded, redis.Z{Score: 1, Member: "state-only-job"}).Err(); err != nil {
		t.Fatalf("ZAdd: %v", err)
	}
	mr.FastForward(31 * time.Minute)
	before := liveJobViews(t, s, "live-job")
	if removed := sweepAt(t, s, 61*time.Minute); removed != 2 {
		t.Fatalf("removed = %d; want the expired job's %s and job:recent members", removed, succeeded)
	}
	if after := liveJobViews(t, s, "live-job"); !reflect.DeepEqual(after, before) {
		t.Fatalf("live job views changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if _, err := client.ZScore(ctx, succeeded, "state-only-job").Result(); err != nil {
		t.Fatalf("state-only job left %s: %v", succeeded, err)
	}
	requireJobSwept(t, client, "expired-job")
}

func TestSweepExpiredJobIndexesKeepsMetaLiveCandidates(t *testing.T) {
	mr, client, s := newSweepTestStore(t)
	ctx := context.Background()
	live := make([]string, cleanupBatchSize)
	for i := range live {
		live[i] = fmt.Sprintf("meta-live-%03d", i)
		setSweepTestStates(t, s, live[i], model.JobStatePending, model.JobStateSucceeded)
	}
	// Written last and named to sort last, so it is read after a full batch
	// of live candidates even when it shares their score.
	const expired = "zz-expired-job"
	setSweepTestStates(t, s, expired, model.JobStatePending, model.JobStateSucceeded)
	mr.FastForward(30 * time.Minute)
	for _, id := range live {
		if err := s.AddJobToTrace(ctx, "trace-"+id, id); err != nil {
			t.Fatalf("AddJobToTrace(%s): %v", id, err)
		}
	}
	mr.FastForward(31 * time.Minute)
	if n, err := client.Exists(ctx, jobStateKey(live[0])).Result(); err != nil || n != 0 {
		t.Fatalf("job:state of %s = %d, %v; want expired so only job:meta keeps it live", live[0], n, err)
	}
	if removed := sweepAt(t, s, 61*time.Minute); removed != 2 {
		t.Fatalf("removed = %d; want only the expired job's 2 members", removed)
	}
	for _, id := range live {
		for _, key := range []string{stateIndexKey(model.JobStateSucceeded), "job:recent"} {
			if _, err := client.ZScore(ctx, key, id).Result(); err != nil {
				t.Fatalf("live job %s left %s: %v", id, key, err)
			}
		}
		if n, err := client.Exists(ctx, jobEventsKey(id)).Result(); err != nil || n != 1 {
			t.Fatalf("live job %s events exist = %d, %v; want 1", id, n, err)
		}
	}
	requireJobSwept(t, client, expired)
}

func newSweepTestStore(t *testing.T) (*miniredis.Miniredis, *redis.Client, *RedisJobStore) {
	t.Helper()
	t.Setenv(envJobMetaTTL, time.Hour.String())
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	return mr, client, NewRedisJobStoreFromClient(client)
}

func setSweepTestStates(t *testing.T, s *RedisJobStore, jobID string, states ...model.JobState) {
	t.Helper()
	for _, state := range states {
		if err := s.SetStateWithContext(context.Background(), jobID, state, &model.StateEventContext{Reason: string(state)}); err != nil {
			t.Fatalf("SetStateWithContext(%s, %s): %v", jobID, state, err)
		}
	}
}

// sweepAt sweeps at wall time plus the miniredis FastForward so far: index
// scores are wall-clock writes, while key TTLs follow the miniredis clock.
func sweepAt(t *testing.T, s *RedisJobStore, elapsed time.Duration) int64 {
	t.Helper()
	removed, err := s.SweepExpiredJobIndexes(context.Background(), time.Now().Add(elapsed))
	if err != nil {
		t.Fatalf("SweepExpiredJobIndexes: %v", err)
	}
	return removed
}

type jobViews struct {
	Events    []model.JobEvent
	Recent    model.JobRecord
	Succeeded model.JobRecord
}

func liveJobViews(t *testing.T, s *RedisJobStore, jobID string) jobViews {
	t.Helper()
	ctx := context.Background()
	events, err := s.GetJobEvents(ctx, jobID)
	if err != nil || len(events) == 0 {
		t.Fatalf("GetJobEvents(%s) = %v, %v; want events", jobID, events, err)
	}
	recent, err := s.ListRecentJobs(ctx, 100)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	byState, err := s.ListJobsByState(ctx, model.JobStateSucceeded, time.Now().Add(24*time.Hour).UnixMicro(), 100)
	if err != nil {
		t.Fatalf("ListJobsByState: %v", err)
	}
	return jobViews{Events: events, Recent: jobRecordOf(t, recent, jobID), Succeeded: jobRecordOf(t, byState, jobID)}
}

func jobRecordOf(t *testing.T, records []model.JobRecord, jobID string) model.JobRecord {
	t.Helper()
	for _, record := range records {
		if record.ID == jobID {
			return record
		}
	}
	t.Fatalf("no record of %s in %+v", jobID, records)
	return model.JobRecord{}
}

func requireJobSwept(t *testing.T, client *redis.Client, jobID string) {
	t.Helper()
	ctx := context.Background()
	for _, key := range []string{stateIndexKey(model.JobStateSucceeded), "job:recent"} {
		if _, err := client.ZScore(ctx, key, jobID).Result(); !errors.Is(err, redis.Nil) {
			t.Fatalf("%s still holds %s (err=%v)", key, jobID, err)
		}
	}
	if n, err := client.Exists(ctx, jobEventsKey(jobID)).Result(); err != nil || n != 0 {
		t.Fatalf("job:events of %s exists = %d, %v; want deleted", jobID, n, err)
	}
}
