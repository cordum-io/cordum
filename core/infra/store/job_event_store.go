package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

const jobRuntimeKeyPrefix = "job:"

const (
	runtimeStateField   = "state"
	runtimeResultField  = "result_ptr"
	runtimeEventPrefix  = "event:"
	runtimeEffectPrefix = "effect:"
)

var rollbackDispatchScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[3]) ~= ARGV[4] then return 0 end
redis.call('HSET', KEYS[1], ARGV[5], ARGV[6])
redis.call('HDEL', KEYS[1], ARGV[1], ARGV[7], ARGV[8])
return 1
`)

var migrateRuntimeFenceScript = redis.NewScript(`
for i = 1, #ARGV - 1, 2 do
  if ARGV[i + 1] ~= '' then redis.call('HSETNX', KEYS[1], ARGV[i], ARGV[i + 1]) end
end
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[#ARGV]))
return 1
`)

var (
	// ErrJobEventDigestConflict reports reuse of one signed message id with
	// different verified bytes.
	ErrJobEventDigestConflict = errors.New("job store event message digest conflict")
	applyJobResultScript      = redis.NewScript(`
if redis.call('HGET', KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[3]) ~= ARGV[4] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[5]) ~= ARGV[6] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[7]) ~= ARGV[8] then return 0 end
local prior = redis.call('HGET', KEYS[1], ARGV[9])
if prior then
  if prior == ARGV[10] then return 2 end
  return -1
end
local state = redis.call('HGET', KEYS[1], ARGV[11])
if state ~= 'DISPATCHED' and state ~= 'RUNNING' then return 0 end
redis.call('HSET', KEYS[1], ARGV[9], ARGV[10], ARGV[11], ARGV[12], ARGV[15], ARGV[16])
if ARGV[14] ~= '' then redis.call('HSET', KEYS[1], ARGV[13], ARGV[14]) end
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[17]))
return 1
`)
	ackJobEffectScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
return redis.call('HDEL', KEYS[1], ARGV[3])
`)
)

func jobRuntimeKey(jobID string) string {
	tag := base64.RawURLEncoding.EncodeToString([]byte(jobID))
	return jobRuntimeKeyPrefix + "{" + tag + "}:runtime"
}

func (s *RedisJobStore) migrateRuntimeFence(ctx context.Context, jobID string) error {
	key := jobRuntimeKey(jobID)
	legacy, err := s.client.HGetAll(ctx, jobMetaKey(jobID)).Result()
	if err != nil {
		return err
	}
	fields := []string{
		"state", metaFieldDispatchID, metaFieldDispatchAttempt,
		metaFieldDispatchWorkerID, metaFieldDispatchTenant,
	}
	args := make([]any, 0, len(fields)*2)
	for _, field := range fields {
		args = append(args, field, legacy[field])
	}
	args = append(args, strconv.FormatInt(s.runtimeTTLSeconds(), 10))
	return migrateRuntimeFenceScript.Run(ctx, s.client, []string{key}, args...).Err()
}

// RollbackDispatch clears only the exact dispatch attempt that failed to
// publish. A newer attempt is never affected by a late rollback.
func (s *RedisJobStore) RollbackDispatch(
	ctx context.Context, jobID, dispatchID string, attempt int,
) (bool, error) {
	if jobID == "" || dispatchID == "" || attempt <= 0 {
		return false, fmt.Errorf("jobID, dispatchID, and attempt required")
	}
	result, err := rollbackDispatchScript.Run(ctx, s.client, []string{jobRuntimeKey(jobID)},
		metaFieldDispatchID, dispatchID,
		metaFieldDispatchAttempt, strconv.Itoa(attempt),
		"state", string(model.JobStateScheduled),
		metaFieldDispatchWorkerID, metaFieldDispatchTenant,
	).Int()
	if err != nil {
		return false, fmt.Errorf("job store rollback dispatch %s: %w", jobID, err)
	}
	return result == 1, nil
}

// ApplyJobResult commits an authenticated result fence, state, pointer, and
// durable effect. The full Lua implementation is kept in this file so it can
// touch exactly one Redis hash-slot key.
func (s *RedisJobStore) ApplyJobResult(
	ctx context.Context, apply model.JobResultApply,
) (model.JobEventApplyDisposition, error) {
	if err := validateJobResultApply(apply); err != nil {
		return model.JobEventRejected, err
	}
	if err := s.migrateRuntimeFence(ctx, apply.JobID); err != nil {
		return model.JobEventRejected, fmt.Errorf("job store apply result %s: migrate fence: %w", apply.JobID, err)
	}
	eventID := base64.RawURLEncoding.EncodeToString(apply.MessageID)
	result, err := applyJobResultScript.Run(ctx, s.client, []string{jobRuntimeKey(apply.JobID)},
		metaFieldDispatchID, apply.DispatchID, metaFieldDispatchAttempt, strconv.Itoa(apply.Attempt),
		metaFieldDispatchWorkerID, apply.WorkerID, metaFieldDispatchTenant, apply.Tenant,
		runtimeEventPrefix+eventID, base64.RawStdEncoding.EncodeToString(apply.Digest),
		runtimeStateField, string(apply.State), runtimeResultField, apply.ResultPtr,
		runtimeEffectPrefix+eventID, base64.RawStdEncoding.EncodeToString(apply.Effect),
		strconv.FormatInt(s.runtimeTTLSeconds(), 10),
	).Int()
	if err != nil {
		return model.JobEventRejected, fmt.Errorf("job store apply result %s: %w", apply.JobID, err)
	}
	if result < 0 {
		return model.JobEventRejected, ErrJobEventDigestConflict
	}
	return model.JobEventApplyDisposition(result), nil
}

func validateJobResultApply(apply model.JobResultApply) error {
	if apply.JobID == "" || apply.DispatchID == "" || apply.WorkerID == "" || apply.Tenant == "" ||
		apply.Attempt <= 0 || len(apply.MessageID) == 0 || len(apply.Digest) == 0 ||
		apply.State == "" || len(apply.Effect) == 0 {
		return fmt.Errorf("job result apply requires complete fence, signed identity, state, and effect")
	}
	return nil
}

func (s *RedisJobStore) runtimeTTLSeconds() int64 {
	if s.metaTTL > 0 {
		return max(1, int64(s.metaTTL.Seconds()))
	}
	return int64(defaultJobMetaTTL.Seconds())
}

func (s *RedisJobStore) PendingJobEffects(ctx context.Context, limit int64) ([]model.JobEffect, error) {
	if limit <= 0 {
		limit = 100
	}
	cluster, ok := s.client.(*redis.ClusterClient)
	if !ok {
		return pendingEffectsFromClient(ctx, s.client, limit)
	}
	var mu sync.Mutex
	var effects []model.JobEffect
	err := cluster.ForEachMaster(ctx, func(ctx context.Context, client *redis.Client) error {
		found, scanErr := pendingEffectsFromClient(ctx, client, limit)
		mu.Lock()
		defer mu.Unlock()
		if scanErr != nil {
			return scanErr
		}
		remaining := max(0, int(limit)-len(effects))
		if len(found) > remaining {
			found = found[:remaining]
		}
		effects = append(effects, found...)
		return nil
	})
	return effects, err
}

func pendingEffectsFromClient(
	ctx context.Context, client redis.UniversalClient, limit int64,
) ([]model.JobEffect, error) {
	var cursor uint64
	effects := make([]model.JobEffect, 0, limit)
	for {
		keys, next, err := client.Scan(ctx, cursor, "job:{*}:runtime", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("job store scan pending effects: %w", err)
		}
		for _, key := range keys {
			effects, err = appendRuntimeEffects(ctx, client, key, effects, limit)
			if err != nil {
				return nil, fmt.Errorf("job store read pending effects: %w", err)
			}
			if int64(len(effects)) >= limit {
				return effects, nil
			}
		}
		cursor = next
		if cursor == 0 {
			return effects, nil
		}
	}
}

func appendRuntimeEffects(
	ctx context.Context, client redis.UniversalClient, key string, effects []model.JobEffect, limit int64,
) ([]model.JobEffect, error) {
	fields, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		return effects, err
	}
	jobID, ok := runtimeJobID(key)
	if !ok {
		return effects, fmt.Errorf("invalid runtime key %q", key)
	}
	for field, encoded := range fields {
		if !strings.HasPrefix(field, runtimeEffectPrefix) || int64(len(effects)) >= limit {
			continue
		}
		eventID := strings.TrimPrefix(field, runtimeEffectPrefix)
		payload, payloadErr := base64.RawStdEncoding.DecodeString(encoded)
		digest, digestErr := base64.RawStdEncoding.DecodeString(fields[runtimeEventPrefix+eventID])
		if payloadErr != nil || digestErr != nil || len(digest) == 0 {
			return effects, fmt.Errorf("invalid durable effect encoding for job %q", jobID)
		}
		effects = append(effects, model.JobEffect{JobID: jobID, EventID: eventID, Digest: digest, Payload: payload})
	}
	return effects, nil
}

func runtimeJobID(key string) (string, bool) {
	start, end := strings.IndexByte(key, '{'), strings.IndexByte(key, '}')
	if start < 0 || end <= start+1 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(key[start+1 : end])
	return string(raw), err == nil
}

func (s *RedisJobStore) AckJobEffect(ctx context.Context, effect model.JobEffect) (bool, error) {
	if effect.JobID == "" || effect.EventID == "" || len(effect.Digest) == 0 {
		return false, fmt.Errorf("job effect requires job id, event id, and digest")
	}
	result, err := ackJobEffectScript.Run(ctx, s.client, []string{jobRuntimeKey(effect.JobID)},
		runtimeEventPrefix+effect.EventID, base64.RawStdEncoding.EncodeToString(effect.Digest),
		runtimeEffectPrefix+effect.EventID,
	).Int()
	if err != nil {
		return false, fmt.Errorf("job store ack effect %s: %w", effect.JobID, err)
	}
	return result == 1, nil
}
