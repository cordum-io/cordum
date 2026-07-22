package store

import (
	"context"
	"fmt"

	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

var projectedJobStates = []model.JobState{
	model.JobStatePending, model.JobStateApproval, model.JobStateScheduled,
	model.JobStateDispatched, model.JobStateRunning, model.JobStateRetrying,
	model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled,
	model.JobStateTimeout, model.JobStateDenied, model.JobStateQuarantined,
}

// ProjectJobResult materializes the authoritative runtime commit into legacy
// query keys. Every operation is individually idempotent so an outbox replay
// can resume after a crash without relying on a cross-slot transaction.
func (s *RedisJobStore) ProjectJobResult(
	ctx context.Context, jobID string, state model.JobState, resultPtr, workerID string,
) error {
	if jobID == "" || state == "" {
		return fmt.Errorf("job result projection requires job id and state")
	}
	now := nowUnixMicros()
	if err := s.projectLegacyResultMeta(ctx, jobID, state, resultPtr, workerID, now); err != nil {
		return err
	}
	if err := s.projectLegacyResultKeys(ctx, jobID, state, resultPtr); err != nil {
		return err
	}
	if err := s.projectLegacyResultIndexes(ctx, jobID, state, now); err != nil {
		return err
	}
	if err := s.projectLegacyWorker(ctx, jobID, workerID, now); err != nil {
		return err
	}
	return s.projectLegacyTerminalState(ctx, jobID, state)
}

func (s *RedisJobStore) projectLegacyResultMeta(
	ctx context.Context, jobID string, state model.JobState, resultPtr, workerID string, now int64,
) error {
	fields := map[string]any{runtimeStateField: string(state), "updated_at": now}
	if resultPtr != "" {
		fields[runtimeResultField] = resultPtr
	}
	if workerID != "" {
		fields[metaFieldWorkerID] = workerID
	}
	if err := s.client.HSet(ctx, jobMetaKey(jobID), fields).Err(); err != nil {
		return fmt.Errorf("job store project result meta %s: %w", jobID, err)
	}
	return nil
}

func (s *RedisJobStore) projectLegacyWorker(
	ctx context.Context, jobID, workerID string, now int64,
) error {
	if workerID == "" {
		return nil
	}
	key := workerJobsKey(workerID)
	pipe := s.client.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: jobID})
	pipe.ZRemRangeByRank(ctx, key, 0, -1001)
	if s.metaTTL > 0 {
		pipe.Expire(ctx, key, s.metaTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("job store project worker index %s: %w", jobID, err)
	}
	return nil
}

func (s *RedisJobStore) projectLegacyResultKeys(
	ctx context.Context, jobID string, state model.JobState, resultPtr string,
) error {
	if err := s.client.Set(ctx, jobStateKey(jobID), string(state), s.metaTTL).Err(); err != nil {
		return fmt.Errorf("job store project state %s: %w", jobID, err)
	}
	if resultPtr == "" {
		return nil
	}
	if err := s.client.Set(ctx, jobResultPtrKey(jobID), resultPtr, s.metaTTL).Err(); err != nil {
		return fmt.Errorf("job store project result pointer %s: %w", jobID, err)
	}
	return nil
}

func (s *RedisJobStore) projectLegacyResultIndexes(
	ctx context.Context, jobID string, state model.JobState, now int64,
) error {
	for _, candidate := range projectedJobStates {
		if candidate == state {
			continue
		}
		if err := s.client.ZRem(ctx, stateIndexKey(candidate), jobID).Err(); err != nil {
			return fmt.Errorf("job store project remove state index %s: %w", jobID, err)
		}
	}
	if err := s.client.ZAdd(ctx, stateIndexKey(state), redis.Z{
		Score: float64(now), Member: jobID,
	}).Err(); err != nil {
		return fmt.Errorf("job store project state index %s: %w", jobID, err)
	}
	return s.client.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID}).Err()
}

func (s *RedisJobStore) projectLegacyTerminalState(
	ctx context.Context, jobID string, state model.JobState,
) error {
	tenant, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldTenant).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("job store project tenant %s: %w", jobID, err)
	}
	if tenant != "" {
		if terminalStates[state] {
			err = s.client.SRem(ctx, tenantActiveKey(tenant), jobID).Err()
		} else if isActiveState(state) {
			err = s.client.SAdd(ctx, tenantActiveKey(tenant), jobID).Err()
		}
		if err != nil {
			return fmt.Errorf("job store project tenant active %s: %w", jobID, err)
		}
	}
	if !terminalStates[state] {
		return nil
	}
	if err := s.client.ZRem(ctx, deadlineIndexKey(), jobID).Err(); err != nil {
		return fmt.Errorf("job store project deadline index %s: %w", jobID, err)
	}
	return s.client.HDel(ctx, jobMetaKey(jobID), metaFieldDeadline).Err()
}
