package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// jobIndexSweepMaxBatches bounds the batches one sweep call reads per key;
// the next call continues where this one stopped.
const jobIndexSweepMaxBatches = 100

// SweepExpiredJobIndexes removes the id of every job whose job:meta and
// job:state keys have both expired from each job:index:<state> key and from
// job:recent, and deletes that job's job:events list. Candidates are members
// last scored at or before now minus JOB_META_TTL; a candidate whose job:meta
// or job:state key still exists is kept. It returns the number of index
// members removed. Only single-key commands are used, so it runs unchanged on
// Redis Cluster.
func (s *RedisJobStore) SweepExpiredJobIndexes(ctx context.Context, now time.Time) (int64, error) {
	cutoff := strconv.FormatInt(now.Add(-s.metaTTL).UnixMicro(), 10)
	keys := make([]string, 0, len(projectedJobStates)+1)
	for _, state := range projectedJobStates {
		keys = append(keys, stateIndexKey(state))
	}
	keys = append(keys, "job:recent")
	var removed int64
	for _, key := range keys {
		n, err := s.sweepJobIndexKey(ctx, key, cutoff)
		removed += n
		if err != nil {
			return removed, fmt.Errorf("job index sweep %s: %w", key, err)
		}
	}
	return removed, nil
}

// sweepJobIndexKey reads the candidates of one index key oldest first. Kept
// members stay ahead of the unread ones, so offset advances by the number
// kept and a full batch of live jobs never hides the expired ids behind it.
func (s *RedisJobStore) sweepJobIndexKey(ctx context.Context, key, cutoff string) (int64, error) {
	var removed, offset int64
	for range jobIndexSweepMaxBatches {
		ids, err := s.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
			Min: "-inf", Max: cutoff, Offset: offset, Count: cleanupBatchSize,
		}).Result()
		if err != nil {
			return removed, err
		}
		expired, err := s.expiredJobIDs(ctx, ids)
		if err != nil {
			return removed, err
		}
		n, err := s.dropExpiredJobs(ctx, key, expired)
		removed += n
		if err != nil {
			return removed, err
		}
		offset += int64(len(ids) - len(expired))
		if len(ids) < cleanupBatchSize {
			break
		}
	}
	return removed, nil
}

// expiredJobIDs returns the ids whose job:meta and job:state keys are both gone.
func (s *RedisJobStore) expiredJobIDs(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := s.client.Pipeline()
	meta := make([]*redis.IntCmd, len(ids))
	state := make([]*redis.IntCmd, len(ids))
	for i, id := range ids {
		meta[i] = pipe.Exists(ctx, jobMetaKey(id))
		state[i] = pipe.Exists(ctx, jobStateKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	var expired []string
	for i, id := range ids {
		if meta[i].Val() == 0 && state[i].Val() == 0 {
			expired = append(expired, id)
		}
	}
	return expired, nil
}

// dropExpiredJobs deletes each job's event list before removing its id from
// key, so a failed delete leaves the member that leads the next sweep back.
func (s *RedisJobStore) dropExpiredJobs(ctx context.Context, key string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	pipe := s.client.Pipeline()
	members := make([]any, len(ids))
	for i, id := range ids {
		pipe.Del(ctx, jobEventsKey(id))
		members[i] = id
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return s.client.ZRem(ctx, key, members...).Result()
}
