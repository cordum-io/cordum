package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

var acceptSignedJobEventScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[3]) ~= ARGV[4] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[5]) ~= ARGV[6] then return 0 end
if redis.call('HGET', KEYS[1], ARGV[7]) ~= ARGV[8] then return 0 end
local state = redis.call('HGET', KEYS[1], 'state')
if state ~= 'DISPATCHED' and state ~= 'RUNNING' then return 0 end
local prior = redis.call('HGET', KEYS[1], ARGV[9])
if prior then
  if prior == ARGV[10] then return 2 end
  return -1
end
redis.call('HSET', KEYS[1], ARGV[9], ARGV[10])
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[11]))
return 1
`)

var cancelAllJobAttemptsScript = redis.NewScript(`
local state = redis.call('HGET', KEYS[1], ARGV[1])
if state == 'SUCCEEDED' or state == 'FAILED' or state == 'CANCELLED' or
   state == 'TIMEOUT' or state == 'DENIED' or state == 'QUARANTINED' then
  redis.call('EXPIRE', KEYS[1], tonumber(ARGV[6]))
  return state
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('HDEL', KEYS[1], ARGV[3], ARGV[4], ARGV[5])
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[6]))
return ARGV[2]
`)

// AcceptSignedJobEvent fences progress-like events and binds each signed
// message ID to one verified digest. Reuse with different bytes fails closed.
func (s *RedisJobStore) AcceptSignedJobEvent(
	ctx context.Context, jobID, dispatchID string, attempt int, workerID, tenant string,
	messageID, digest []byte,
) (model.JobEventApplyDisposition, error) {
	if jobID == "" || dispatchID == "" || attempt <= 0 || workerID == "" || tenant == "" ||
		len(messageID) == 0 || len(digest) == 0 {
		return model.JobEventRejected, fmt.Errorf("signed job event requires complete fence, identity, message id, and digest")
	}
	if err := s.migrateRuntimeFence(ctx, jobID); err != nil {
		return model.JobEventRejected, fmt.Errorf("job store accept signed event %s: migrate fence: %w", jobID, err)
	}
	eventField := runtimeEventPrefix + base64.RawURLEncoding.EncodeToString(messageID)
	encodedDigest := base64.RawStdEncoding.EncodeToString(digest)
	result, err := acceptSignedJobEventScript.Run(ctx, s.client, []string{jobRuntimeKey(jobID)},
		metaFieldDispatchID, dispatchID, metaFieldDispatchAttempt, strconv.Itoa(attempt),
		metaFieldDispatchWorkerID, workerID, metaFieldDispatchTenant, tenant,
		eventField, encodedDigest, strconv.FormatInt(s.runtimeTTLSeconds(), 10),
	).Int()
	if err != nil {
		return model.JobEventRejected, fmt.Errorf("job store accept signed event %s: %w", jobID, err)
	}
	if result < 0 {
		return model.JobEventRejected, ErrJobEventDigestConflict
	}
	// result is one of this script's own small return codes
	// (JobEventApplyDisposition only has 3 members: 0/1/2), already
	// checked non-negative above -- never wraps.
	return model.JobEventApplyDisposition(result), nil // #nosec G115 -- see comment above
}

// CancelAllJobAttempts is the privileged control-plane operation. It does not
// accept a worker fence and atomically invalidates the current dispatch.
func (s *RedisJobStore) CancelAllJobAttempts(
	ctx context.Context, jobID string,
) (model.JobState, error) {
	if jobID == "" {
		return "", fmt.Errorf("jobID required")
	}
	if err := s.migrateRuntimeFence(ctx, jobID); err != nil {
		return "", fmt.Errorf("job store cancel all attempts %s: migrate fence: %w", jobID, err)
	}
	state, err := cancelAllJobAttemptsScript.Run(ctx, s.client, []string{jobRuntimeKey(jobID)},
		runtimeStateField, string(model.JobStateCancelled), metaFieldDispatchID,
		metaFieldDispatchWorkerID, metaFieldDispatchTenant,
		strconv.FormatInt(s.runtimeTTLSeconds(), 10),
	).Text()
	if err != nil {
		return "", fmt.Errorf("job store cancel all attempts %s: %w", jobID, err)
	}
	return model.JobState(state), nil
}
