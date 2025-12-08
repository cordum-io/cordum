package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
)

const (
	jobStateKeyPrefix     = "job:state:"
	jobResultPtrKeyPrefix = "job:result_ptr:"
	jobMetaKeyPrefix      = "job:meta:"
	jobEventsKeyPrefix    = "job:events:"
)

var (
	terminalStates = map[scheduler.JobState]bool{
		scheduler.JobStateSucceeded: true,
		scheduler.JobStateFailed:    true,
		scheduler.JobStateCancelled: true,
		scheduler.JobStateTimeout:   true,
		scheduler.JobStateDenied:    true,
	}
	allStates = []scheduler.JobState{
		scheduler.JobStatePending,
		scheduler.JobStateScheduled,
		scheduler.JobStateDispatched,
		scheduler.JobStateRunning,
		scheduler.JobStateSucceeded,
		scheduler.JobStateFailed,
		scheduler.JobStateCancelled,
		scheduler.JobStateTimeout,
		scheduler.JobStateDenied,
	}
	allowedTransitions = map[scheduler.JobState][]scheduler.JobState{
		"":                           {scheduler.JobStatePending, scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning},
		scheduler.JobStatePending:    {scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied},
		scheduler.JobStateScheduled:  {scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied},
		scheduler.JobStateDispatched: {scheduler.JobStateRunning, scheduler.JobStateSucceeded, scheduler.JobStateFailed, scheduler.JobStateCancelled, scheduler.JobStateTimeout},
		scheduler.JobStateRunning:    {scheduler.JobStateSucceeded, scheduler.JobStateFailed, scheduler.JobStateCancelled, scheduler.JobStateTimeout},
		scheduler.JobStateSucceeded:  {},
		scheduler.JobStateFailed:     {},
		scheduler.JobStateCancelled:  {},
		scheduler.JobStateTimeout:    {},
		scheduler.JobStateDenied:     {},
	}
)

// RedisJobStore implements scheduler.JobStore backed by Redis.
type RedisJobStore struct {
	client *redis.Client
}

// NewRedisJobStore constructs a Redis-backed JobStore using a redis:// URL.
func NewRedisJobStore(url string) (*RedisJobStore, error) {
	if url == "" {
		url = defaultRedisURL
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return &RedisJobStore{client: client}, nil
}

func (s *RedisJobStore) SetState(ctx context.Context, jobID string, state scheduler.JobState) error {
	if jobID == "" || state == "" {
		return fmt.Errorf("invalid jobID or state")
	}

	now := time.Now().Unix()
	metaKey := jobMetaKey(jobID)

	prev, _ := s.client.HGet(ctx, metaKey, "state").Result()
	prevState := scheduler.JobState(prev)
	if !isAllowedTransition(prevState, state) {
		return fmt.Errorf("invalid transition %s -> %s", prevState, state)
	}

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, metaKey, map[string]any{
		"state":      string(state),
		"updated_at": now,
	})
	// keep legacy key for compatibility
	pipe.Set(ctx, jobStateKey(jobID), string(state), 0)

	// maintain per-state index for reconciliation
	prevIdx := stateIndexKey(prevState)
	if prevIdx != "" {
		pipe.ZRem(ctx, prevIdx, jobID)
	}
	idx := stateIndexKey(state)
	if idx != "" {
		pipe.ZAdd(ctx, idx, redis.Z{Score: float64(now), Member: jobID})
	}

	// Maintain global recent jobs list (score = updated_at)
	// Cap it at, say, 1000 items to avoid infinite growth?
	// For MVP, just adding is fine, user can manage cleanup later or we can add ZREMRANGEBYRANK.
	pipe.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID})
	// Keep only last 1000
	pipe.ZRemRangeByRank(ctx, "job:recent", 0, -1001)

	// append event
	pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, state))

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// ListRecentJobs returns the N most recently updated jobs.
func (s *RedisJobStore) ListRecentJobs(ctx context.Context, limit int64) ([]scheduler.JobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	members, err := s.client.ZRevRangeWithScores(ctx, "job:recent", 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.JobRecord, 0, len(members))
	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok {
			continue
		}
		// Fetch current state for each job to be helpful
		// Using pipeline to fetch state for all jobs would be better, but loop is okay for MVP
		state, _ := s.GetState(ctx, jobID)

		out = append(out, scheduler.JobRecord{
			ID:        jobID,
			UpdatedAt: int64(m.Score),
			State:     state,
		})
	}
	return out, nil
}

func (s *RedisJobStore) GetState(ctx context.Context, jobID string) (scheduler.JobState, error) {
	metaKey := jobMetaKey(jobID)
	val, err := s.client.HGet(ctx, metaKey, "state").Result()
	if err == nil {
		return scheduler.JobState(val), nil
	}
	// fallback legacy key
	val, err = s.client.Get(ctx, jobStateKey(jobID)).Result()
	if err != nil {
		return "", err
	}
	return scheduler.JobState(val), nil
}

func (s *RedisJobStore) SetResultPtr(ctx context.Context, jobID, resultPtr string) error {
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, jobResultPtrKey(jobID), resultPtr, 0)
	pipe.HSet(ctx, jobMetaKey(jobID), "result_ptr", resultPtr)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *RedisJobStore) GetResultPtr(ctx context.Context, jobID string) (string, error) {
	val, err := s.client.HGet(ctx, jobMetaKey(jobID), "result_ptr").Result()
	if err == nil {
		return val, nil
	}
	val, err = s.client.Get(ctx, jobResultPtrKey(jobID)).Result()
	if err != nil {
		return "", err
	}
	return val, nil
}

// ListJobsByState returns jobs in the given state last updated at or before the given unix timestamp.
func (s *RedisJobStore) ListJobsByState(ctx context.Context, state scheduler.JobState, updatedBeforeUnix int64, limit int64) ([]scheduler.JobRecord, error) {
	key := stateIndexKey(state)
	if key == "" {
		return nil, fmt.Errorf("unknown state %s", state)
	}
	if limit <= 0 {
		limit = 100
	}
	members, err := s.client.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", updatedBeforeUnix),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.JobRecord, 0, len(members))
	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok {
			continue
		}
		out = append(out, scheduler.JobRecord{
			ID:        jobID,
			UpdatedAt: int64(m.Score),
		})
	}
	return out, nil
}

func (s *RedisJobStore) Close() error {
	return s.client.Close()
}

func jobStateKey(jobID string) string {
	return jobStateKeyPrefix + jobID
}

func jobResultPtrKey(jobID string) string {
	return jobResultPtrKeyPrefix + jobID
}

func jobMetaKey(jobID string) string {
	return jobMetaKeyPrefix + jobID
}

func jobEventsKey(jobID string) string {
	return jobEventsKeyPrefix + jobID
}

func stateIndexKey(state scheduler.JobState) string {
	if state == "" {
		return ""
	}
	return "job:index:" + strings.ToLower(string(state))
}

func (s *RedisJobStore) AddJobToTrace(ctx context.Context, traceID, jobID string) error {
	// Add to set of jobs for this trace
	return s.client.SAdd(ctx, "trace:"+traceID, jobID).Err()
}

func (s *RedisJobStore) SetTopic(ctx context.Context, jobID, topic string) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), "topic", topic).Err()
}

func (s *RedisJobStore) GetTopic(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), "topic").Result()
}

func (s *RedisJobStore) GetTraceJobs(ctx context.Context, traceID string) ([]scheduler.JobRecord, error) {
	jobIDs, err := s.client.SMembers(ctx, "trace:"+traceID).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.JobRecord, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		state, _ := s.GetState(ctx, jobID)
		out = append(out, scheduler.JobRecord{
			ID:    jobID,
			State: state,
		})
	}
	return out, nil
}

func isAllowedTransition(from, to scheduler.JobState) bool {
	if from == to {
		return true
	}
	allowed, ok := allowedTransitions[from]
	if !ok {
		return false
	}
	for _, target := range allowed {
		if target == to {
			return true
		}
	}
	return false
}
