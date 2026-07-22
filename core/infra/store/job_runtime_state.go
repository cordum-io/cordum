package store

import (
	"context"
	"fmt"

	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

var transitionRuntimeStateScript = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], ARGV[1])
if current == ARGV[2] then return 1 end
for i = 3, #ARGV do
  if current == ARGV[i] then
    redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
    return 1
  end
end
return 0
`)

func (s *RedisJobStore) transitionRuntimeState(
	ctx context.Context, jobID string, target model.JobState,
) error {
	args := []any{runtimeStateField, string(target)}
	for prior, targets := range allowedTransitions {
		if containsJobState(targets, target) {
			args = append(args, string(prior))
		}
	}
	changed, err := transitionRuntimeStateScript.Run(
		ctx, s.client, []string{jobRuntimeKey(jobID)}, args...,
	).Int()
	if err != nil {
		return fmt.Errorf("job store transition runtime state %s: %w", jobID, err)
	}
	if changed != 1 {
		current, _ := s.client.HGet(ctx, jobRuntimeKey(jobID), runtimeStateField).Result()
		return fmt.Errorf("invalid runtime transition %s -> %s", current, target)
	}
	return nil
}

func containsJobState(states []model.JobState, target model.JobState) bool {
	for _, state := range states {
		if state == target {
			return true
		}
	}
	return false
}
