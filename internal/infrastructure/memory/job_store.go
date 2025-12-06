package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
)

const (
	jobStateKeyPrefix     = "job:state:"
	jobResultPtrKeyPrefix = "job:result_ptr:"
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
	return s.client.Set(ctx, jobStateKey(jobID), string(state), 0).Err()
}

func (s *RedisJobStore) GetState(ctx context.Context, jobID string) (scheduler.JobState, error) {
	val, err := s.client.Get(ctx, jobStateKey(jobID)).Result()
	if err != nil {
		return "", err
	}
	return scheduler.JobState(val), nil
}

func (s *RedisJobStore) SetResultPtr(ctx context.Context, jobID, resultPtr string) error {
	return s.client.Set(ctx, jobResultPtrKey(jobID), resultPtr, 0).Err()
}

func (s *RedisJobStore) GetResultPtr(ctx context.Context, jobID string) (string, error) {
	val, err := s.client.Get(ctx, jobResultPtrKey(jobID)).Result()
	if err != nil {
		return "", err
	}
	return val, nil
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
