package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	jobStateKeyPrefix           = "job:state:"
	jobResultPtrKeyPrefix       = "job:result_ptr:"
	jobMetaKeyPrefix            = "job:meta:"
	jobRequestKeyPrefix         = "job:req:"
	jobEventsKeyPrefix          = "job:events:"
	jobOutputDecisionKeySuffix  = ":output_decision"
	metaFieldTopic              = "topic"
	metaFieldTenant             = "tenant"
	metaFieldPrincipal          = "principal"
	metaFieldTeam               = "team"
	metaFieldMemory             = "memory_id"
	metaFieldTraceID            = "trace_id"
	metaFieldLabels             = "labels"
	metaFieldActorID            = "actor_id"
	metaFieldActorType          = "actor_type"
	metaFieldIdempotencyKey     = "idempotency_key"
	metaFieldCapability         = "capability"
	metaFieldRiskTags           = "risk_tags"
	metaFieldRequires           = "requires"
	metaFieldPackID             = "pack_id"
	metaFieldAttempts           = "attempts"
	metaFieldDeadline           = "deadline_unix"
	metaFieldSafetyDecision     = "safety_decision"
	metaFieldSafetyReason       = "safety_reason"
	metaFieldSafetyRuleID       = "safety_rule_id"
	metaFieldSafetySnapshot     = "safety_snapshot"
	metaFieldSafetyChecked      = "safety_checked_at"
	metaFieldSafetyConstraints  = "safety_constraints"
	metaFieldSafetyRemediations = "safety_remediations"
	metaFieldOutputSafety       = "output_safety"
	metaFieldApprovalRequired   = "safety_approval_required"
	metaFieldApprovalRef        = "safety_approval_ref"
	metaFieldSafetyJobHash      = "safety_job_hash"
	metaFieldApprovalBy         = "approval_by"
	metaFieldApprovalRole       = "approval_role"
	metaFieldApprovalAt         = "approval_at"
	metaFieldApprovalReason     = "approval_reason"
	metaFieldApprovalNote       = "approval_note"
	metaFieldApprovalSnapshot   = "approval_policy_snapshot"
	metaFieldApprovalJobHash    = "approval_job_hash"
	envJobMetaTTL               = "JOB_META_TTL"
	envJobMetaTTLSeconds        = "JOB_META_TTL_SECONDS"
)

var (
	terminalStates = map[model.JobState]bool{
		model.JobStateSucceeded:   true,
		model.JobStateFailed:      true,
		model.JobStateCancelled:   true,
		model.JobStateTimeout:     true,
		model.JobStateDenied:      true,
		model.JobStateQuarantined: true,
	}
	allowedTransitions = map[model.JobState][]model.JobState{
		"":                            {model.JobStatePending, model.JobStateApproval, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateFailed},
		model.JobStatePending:     {model.JobStateApproval, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout},
		model.JobStateApproval:    {model.JobStatePending, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout},
		// Quarantined transitions from active states: output policy 2-phase evaluation
		// can quarantine a job at any point during execution if the output scanner
		// detects unsafe content. See ADR-005 (output-policy-2-phase).
		model.JobStateScheduled:   {model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout, model.JobStateSucceeded, model.JobStateCancelled, model.JobStateQuarantined},
		model.JobStateDispatched:  {model.JobStateRunning, model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateTimeout, model.JobStateQuarantined},
		model.JobStateRunning:     {model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateTimeout, model.JobStateQuarantined},
		// Succeeded → Quarantined: async output scanning may flag content after the
		// job completes. This is the only allowed post-success transition.
		model.JobStateSucceeded:   {model.JobStateQuarantined},
		model.JobStateFailed:      {},
		model.JobStateCancelled:   {},
		model.JobStateTimeout:     {},
		model.JobStateDenied:      {},
		model.JobStateQuarantined: {}, // Terminal — no further transitions allowed.
	}
)

var defaultJobMetaTTL = 7 * 24 * time.Hour

var releaseLockScript = redis.NewScript(`
if redis.call('get', KEYS[1]) == ARGV[1] then
  return redis.call('del', KEYS[1])
end
return 0
`)

var renewLockScript = redis.NewScript(`
if redis.call('get', KEYS[1]) == ARGV[1] then
  return redis.call('pexpire', KEYS[1], ARGV[2])
end
return 0
`)

const (
	microsPerSecond      = int64(1_000_000)
	microsPerMillisecond = int64(1_000)
	secondsThreshold     = int64(1_000_000_000_000)
	millisThreshold      = int64(1_000_000_000_000_000)
	microsThreshold      = int64(1_000_000_000_000_000_000)
)

func deadlineIndexKey() string {
	return "job:deadline"
}

func nowUnixMicros() int64 {
	return time.Now().UnixNano() / int64(time.Microsecond)
}

func normalizeTimestampMicrosUpper(ts int64) int64 {
	if ts <= 0 {
		return ts
	}
	switch {
	case ts < secondsThreshold:
		return ts*microsPerSecond + (microsPerSecond - 1)
	case ts < millisThreshold:
		return ts*microsPerMillisecond + (microsPerMillisecond - 1)
	case ts < microsThreshold:
		return ts
	default:
		return ts / microsPerMillisecond
	}
}

// RedisJobStore implements model.JobStore backed by Redis.
type RedisJobStore struct {
	client  redis.UniversalClient
	metaTTL time.Duration
}

// Client returns the underlying Redis client for use by other subsystems
// (e.g., distributed rate limiting) that need shared Redis access.
func (s *RedisJobStore) Client() redis.UniversalClient {
	return s.client
}

// ApprovalRecord captures approval audit metadata stored on a job.
type ApprovalRecord struct {
	ApprovedBy     string
	ApprovedRole   string
	ApprovedAt     int64
	Reason         string
	Note           string
	PolicySnapshot string
	JobHash        string
}

// CancelJob atomically cancels a job if it is not already terminal.
func (s *RedisJobStore) CancelJob(ctx context.Context, jobID string) (model.JobState, error) {
	if jobID == "" {
		return "", fmt.Errorf("jobID required")
	}
	metaKey := jobMetaKey(jobID)

	var resultState model.JobState
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		current, err := tx.HGet(ctx, metaKey, "state").Result()
		if err == redis.Nil {
			legacy, lerr := tx.Get(ctx, jobStateKey(jobID)).Result()
			if lerr == redis.Nil {
				return redis.Nil
			}
			if lerr != nil {
				return fmt.Errorf("job store get state %s: %w", jobID, lerr)
			}
			current = legacy
		} else if err != nil {
			return fmt.Errorf("job store get state %s: %w", jobID, err)
		}
		currState := model.JobState(current)
		if terminalStates[currState] {
			resultState = currState
			return nil
		}

		now := nowUnixMicros()
		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, map[string]any{
			"state":      string(model.JobStateCancelled),
			"updated_at": now,
		})
		pipe.Set(ctx, jobStateKey(jobID), string(model.JobStateCancelled), 0)

		if prevIdx := stateIndexKey(currState); prevIdx != "" {
			pipe.ZRem(ctx, prevIdx, jobID)
		}
		if idx := stateIndexKey(model.JobStateCancelled); idx != "" {
			pipe.ZAdd(ctx, idx, redis.Z{Score: float64(now), Member: jobID})
		}

		pipe.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID})
		pipe.ZRemRangeByRank(ctx, "job:recent", 0, -1001)

		pipe.ZRem(ctx, deadlineIndexKey(), jobID)
		pipe.HDel(ctx, metaKey, metaFieldDeadline)

		if s.metaTTL > 0 {
			pipe.Expire(ctx, metaKey, s.metaTTL)
			pipe.Expire(ctx, jobStateKey(jobID), s.metaTTL)
			pipe.Expire(ctx, jobResultPtrKey(jobID), s.metaTTL)
		}

		pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, model.JobStateCancelled))

		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("job store cancel %s: %w", jobID, err)
		}
		resultState = model.JobStateCancelled
		return nil
	}, metaKey, jobStateKey(jobID))
	if err != nil {
		return resultState, fmt.Errorf("job store cancel job %s: %w", jobID, err)
	}
	return resultState, nil
}

// TryAcquireLock attempts to acquire a distributed lock with TTL; returns token if acquired.
func (s *RedisJobStore) TryAcquireLock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		slog.Warn("non-positive lock TTL, using default 30s", "requested", ttl)
		ttl = 30 * time.Second
	}
	token := uuid.NewString()
	acquired, err := s.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", fmt.Errorf("job store try acquire lock %s: %w", key, err)
	}
	if !acquired {
		return "", nil
	}
	return token, nil
}

// ReleaseLock releases a distributed lock.
func (s *RedisJobStore) ReleaseLock(ctx context.Context, key, token string) error {
	if key == "" {
		return nil
	}
	if token == "" {
		slog.Warn("lock release skipped: missing token", "key", key)
		return fmt.Errorf("lock token required")
	}
	result, err := releaseLockScript.Run(ctx, s.client, []string{key}, token).Int()
	if err != nil {
		return fmt.Errorf("job store release lock %s: %w", key, err)
	}
	if result == 0 {
		slog.Warn("lock release skipped: token mismatch", "key", key)
		return fmt.Errorf("lock not owned")
	}
	return nil
}

// RenewLock extends the TTL of a held lock. Returns nil if renewed, error if not owned or Redis fails.
func (s *RedisJobStore) RenewLock(ctx context.Context, key, token string, ttl time.Duration) error {
	if key == "" || token == "" {
		return fmt.Errorf("lock key and token required")
	}
	result, err := renewLockScript.Run(ctx, s.client, []string{key}, token, ttl.Milliseconds()).Int()
	if err != nil {
		return fmt.Errorf("job store renew lock %s: %w", key, err)
	}
	if result == 0 {
		return fmt.Errorf("lock not owned")
	}
	return nil
}

// NewRedisJobStore constructs a Redis-backed JobStore using a redis:// URL.
func NewRedisJobStore(url string) (*RedisJobStore, error) {
	if url == "" {
		url = defaultRedisURL
	}

	ttl := defaultJobMetaTTL
	if v := os.Getenv(envJobMetaTTLSeconds); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			slog.Warn("invalid "+envJobMetaTTLSeconds+", using default", "value", sanitizeLogValue(v), "error", sanitizeLogValue(err.Error()), "default", defaultJobMetaTTL) // #nosec -- structured log, sanitized
		} else if secs <= 0 {
			slog.Warn("non-positive "+envJobMetaTTLSeconds+", using default", "value", secs, "default", defaultJobMetaTTL) // #nosec -- structured log, int value
		} else {
			ttl = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv(envJobMetaTTL); v != "" {
		parsed, err := time.ParseDuration(v)
		if err != nil {
			slog.Warn("invalid "+envJobMetaTTL+", using default", "value", sanitizeLogValue(v), "error", sanitizeLogValue(err.Error()), "default", defaultJobMetaTTL) // #nosec -- structured log, sanitized
		} else if parsed <= 0 {
			slog.Warn("non-positive "+envJobMetaTTL+", using default", "value", sanitizeLogValue(v), "default", defaultJobMetaTTL) // #nosec G115 G706 -- v already sanitized above
		} else {
			ttl = parsed
		}
	}
	client, err := redisutil.NewClient(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}

	return &RedisJobStore{client: client, metaTTL: ttl}, nil
}

func (s *RedisJobStore) SetState(ctx context.Context, jobID string, state model.JobState) error {
	if jobID == "" || state == "" {
		return fmt.Errorf("invalid jobID or state")
	}

	now := nowUnixMicros()
	metaKey := jobMetaKey(jobID)

	return s.client.Watch(ctx, func(tx *redis.Tx) error {
		prev, err := tx.HGet(ctx, metaKey, "state").Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("job store set state %s: %w", jobID, err)
		}
		prevState := model.JobState(prev)
		if !isAllowedTransition(prevState, state) {
			return fmt.Errorf("invalid transition %s -> %s", prevState, state)
		}
		attempts := 0
		if raw, err := tx.HGet(ctx, metaKey, metaFieldAttempts).Result(); err == nil {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
				attempts = parsed
			}
		}
		if state == model.JobStateScheduled && prevState != model.JobStateScheduled {
			attempts++
		}
		tenant, _ := tx.HGet(ctx, metaKey, metaFieldTenant).Result()

		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, map[string]any{
			"state":           string(state),
			"updated_at":      now,
			metaFieldAttempts: attempts,
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

		if tenant != "" {
			activeKey := tenantActiveKey(tenant)
			if isActiveState(state) {
				pipe.SAdd(ctx, activeKey, jobID)
			} else if terminalStates[state] {
				pipe.SRem(ctx, activeKey, jobID)
			}
			if s.metaTTL > 0 {
				pipe.Expire(ctx, activeKey, s.metaTTL)
			}
		}

		// Maintain global recent jobs list (score = updated_at)
		pipe.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID})
		// Keep only last 1000
		pipe.ZRemRangeByRank(ctx, "job:recent", 0, -1001)
		if s.metaTTL > 0 {
			pipe.Expire(ctx, metaKey, s.metaTTL)
			pipe.Expire(ctx, jobStateKey(jobID), s.metaTTL)
			pipe.Expire(ctx, jobResultPtrKey(jobID), s.metaTTL)
		}

		// append event
		pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, state))

		if terminalStates[state] {
			pipe.ZRem(ctx, deadlineIndexKey(), jobID)
			pipe.HDel(ctx, metaKey, metaFieldDeadline)
		}

		_, execErr := pipe.Exec(ctx)
		return execErr
	}, metaKey, jobStateKey(jobID))
}

// ListRecentJobs returns the N most recently updated jobs.
func (s *RedisJobStore) ListRecentJobs(ctx context.Context, limit int64) ([]model.JobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	members, err := s.client.ZRevRangeWithScores(ctx, "job:recent", 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list recent jobs: %w", err)
	}
	return s.buildJobRecords(ctx, members)
}

// ListRecentJobsByScore returns jobs at or below the provided updated_at score (cursor) ordered desc.
func (s *RedisJobStore) ListRecentJobsByScore(ctx context.Context, cursor int64, limit int64) ([]model.JobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	max := "+inf"
	if cursor > 0 {
		normalized := normalizeTimestampMicrosUpper(cursor)
		max = fmt.Sprintf("%d", normalized)
	}
	members, err := s.client.ZRevRangeByScoreWithScores(ctx, "job:recent", &redis.ZRangeBy{
		Max:   max,
		Min:   "-inf",
		Count: limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list recent jobs by score: %w", err)
	}
	return s.buildJobRecords(ctx, members)
}

func (s *RedisJobStore) buildJobRecords(ctx context.Context, members []redis.Z) ([]model.JobRecord, error) {
	out := make([]model.JobRecord, 0, len(members))
	if len(members) == 0 {
		return out, nil
	}

	// Batch fetch metadata for each job to avoid N+1 round trips.
	pipe := s.client.Pipeline()
	metaCmds := make(map[string]*redis.MapStringStringCmd, len(members))
	stateCmds := make(map[string]*redis.StringCmd, len(members))
	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok || jobID == "" {
			continue
		}
		metaCmds[jobID] = pipe.HGetAll(ctx, jobMetaKey(jobID))
		stateCmds[jobID] = pipe.Get(ctx, jobStateKey(jobID))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "build_job_records", "error", err)
	}

	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok || jobID == "" {
			continue
		}
		meta, metaErr := metaCmds[jobID].Result()
		if metaErr != nil {
			slog.Error("job-store: pipeline HGetAll failed", "job_id", jobID, "error", metaErr)
			continue
		}
		state := model.JobState(meta["state"])
		if state == "" {
			if sCmd := stateCmds[jobID]; sCmd != nil {
				if val, err := sCmd.Result(); err == nil {
					state = model.JobState(val)
				}
			}
		}
		// Skip records with no metadata — the job hash has expired or was deleted
		// while the index entry remains. Producing a zero-value record hides the issue.
		if len(meta) == 0 && state == "" {
			slog.Warn("job-store: skipping job with expired metadata", "job_id", jobID)
			continue
		}
		topic := meta[metaFieldTopic]
		tenant := meta[metaFieldTenant]
		team := meta[metaFieldTeam]
		principal := meta[metaFieldPrincipal]
		actorID := meta[metaFieldActorID]
		actorType := meta[metaFieldActorType]
		idempotencyKey := meta[metaFieldIdempotencyKey]
		capability := meta[metaFieldCapability]
		packID := meta[metaFieldPackID]
		riskTags := parseJSONStringSlice(meta[metaFieldRiskTags])
		requires := parseJSONStringSlice(meta[metaFieldRequires])
		attempts := parseInt(meta[metaFieldAttempts])
		safetyDecision := meta[metaFieldSafetyDecision]
		safetyReason := meta[metaFieldSafetyReason]
		safetyRuleID := meta[metaFieldSafetyRuleID]
		safetySnapshot := meta[metaFieldSafetySnapshot]
		deadlineUnix, _ := strconv.ParseInt(meta[metaFieldDeadline], 10, 64)

		out = append(out, model.JobRecord{
			ID:             jobID,
			TraceID:        meta[metaFieldTraceID],
			UpdatedAt:      int64(m.Score),
			State:          state,
			Topic:          topic,
			Tenant:         tenant,
			Team:           team,
			Principal:      principal,
			ActorID:        actorID,
			ActorType:      actorType,
			IdempotencyKey: idempotencyKey,
			Capability:     capability,
			RiskTags:       riskTags,
			Requires:       requires,
			PackID:         packID,
			Attempts:       attempts,
			SafetyDecision: safetyDecision,
			SafetyReason:   safetyReason,
			SafetyRuleID:   safetyRuleID,
			SafetySnapshot: safetySnapshot,
			DeadlineUnix:   deadlineUnix,
		})
	}
	return out, nil
}

func (s *RedisJobStore) GetState(ctx context.Context, jobID string) (model.JobState, error) {
	metaKey := jobMetaKey(jobID)
	val, err := s.client.HGet(ctx, metaKey, "state").Result()
	if err == nil {
		return model.JobState(val), nil
	}
	// fallback legacy key
	val, err = s.client.Get(ctx, jobStateKey(jobID)).Result()
	if err != nil {
		return "", fmt.Errorf("job store get state %s: %w", jobID, err)
	}
	return model.JobState(val), nil
}

func (s *RedisJobStore) SetResultPtr(ctx context.Context, jobID, resultPtr string) error {
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, jobResultPtrKey(jobID), resultPtr, s.metaTTL)
	pipe.HSet(ctx, jobMetaKey(jobID), "result_ptr", resultPtr)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set result ptr %s: %w", jobID, err)
	}
	return nil
}

func (s *RedisJobStore) GetResultPtr(ctx context.Context, jobID string) (string, error) {
	val, err := s.client.HGet(ctx, jobMetaKey(jobID), "result_ptr").Result()
	if err == nil {
		return val, nil
	}
	val, err = s.client.Get(ctx, jobResultPtrKey(jobID)).Result()
	if err != nil {
		return "", fmt.Errorf("job store get result ptr %s: %w", jobID, err)
	}
	return val, nil
}

// SetJobMeta stores first-class metadata for a job, including tenant, principal, memory, labels, and optional deadline.
func (s *RedisJobStore) SetJobMeta(ctx context.Context, req *pb.JobRequest) error {
	if req == nil || req.GetJobId() == "" {
		return fmt.Errorf("invalid job request")
	}
	metaKey := jobMetaKey(req.GetJobId())
	meta := req.GetMeta()
	tenantID := req.GetTenantId()
	if meta != nil && meta.GetTenantId() != "" {
		tenantID = meta.GetTenantId()
	}
	if tenantID == "" {
		tenantID = req.GetEnv()["tenant_id"]
	}
	teamID := req.GetEnv()["team_id"]
	actorID := ""
	actorType := ""
	idempotencyKey := ""
	capability := ""
	riskTags := []string{}
	requires := []string{}
	packID := ""
	if meta != nil {
		actorID = meta.GetActorId()
		actorType = actorTypeString(meta.GetActorType())
		idempotencyKey = meta.GetIdempotencyKey()
		capability = meta.GetCapability()
		riskTags = append(riskTags, meta.GetRiskTags()...)
		requires = append(requires, meta.GetRequires()...)
		packID = meta.GetPackId()
	}
	if actorID == "" {
		actorID = req.GetPrincipalId()
	}
	fields := map[string]any{
		metaFieldTopic:     req.GetTopic(),
		metaFieldTenant:    tenantID,
		metaFieldTeam:      teamID,
		metaFieldPrincipal: req.GetPrincipalId(),
		metaFieldMemory:    req.GetMemoryId(),
		metaFieldAttempts:  0,
	}
	if actorID != "" {
		fields[metaFieldActorID] = actorID
	}
	if actorType != "" {
		fields[metaFieldActorType] = actorType
	}
	if idempotencyKey != "" {
		fields[metaFieldIdempotencyKey] = idempotencyKey
	}
	if capability != "" {
		fields[metaFieldCapability] = capability
	}
	if packID != "" {
		fields[metaFieldPackID] = packID
	}
	if len(riskTags) > 0 {
		if data, err := json.Marshal(riskTags); err == nil {
			fields[metaFieldRiskTags] = string(data)
		}
	}
	if len(requires) > 0 {
		if data, err := json.Marshal(requires); err == nil {
			fields[metaFieldRequires] = string(data)
		}
	}

	labels := mergeLabels(req.GetLabels(), meta)
	if len(labels) > 0 {
		if data, err := json.Marshal(labels); err == nil {
			fields[metaFieldLabels] = string(data)
		}
	}

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, metaKey, fields)
	if s.metaTTL > 0 {
		pipe.Expire(ctx, metaKey, s.metaTTL)
	}

	if budget := req.GetBudget(); budget != nil && budget.GetDeadlineMs() > 0 {
		deadline := time.Now().Add(time.Duration(budget.GetDeadlineMs()) * time.Millisecond)
		pipe.HSet(ctx, metaKey, metaFieldDeadline, deadline.Unix())
		pipe.ZAdd(ctx, deadlineIndexKey(), redis.Z{Score: float64(deadline.Unix()), Member: req.GetJobId()})
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("job store set job meta %s: %w", req.GetJobId(), err)
	}
	if idempotencyKey != "" {
		tenantID := model.ExtractTenant(req)
		_ = s.SetIdempotencyKeyScoped(ctx, tenantID, idempotencyKey, req.GetJobId())
	}
	return nil
}

// SetJobRequest stores a serialized snapshot of the job request for replay/approval flows.
func (s *RedisJobStore) SetJobRequest(ctx context.Context, req *pb.JobRequest) error {
	if req == nil || req.GetJobId() == "" {
		return fmt.Errorf("invalid job request")
	}
	payload, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal job request: %w", err)
	}
	key := jobRequestKey(req.GetJobId())
	if s.metaTTL > 0 {
		return s.client.Set(ctx, key, payload, s.metaTTL).Err()
	}
	return s.client.Set(ctx, key, payload, 0).Err()
}

// GetJobRequest retrieves a stored job request snapshot.
func (s *RedisJobStore) GetJobRequest(ctx context.Context, jobID string) (*pb.JobRequest, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID required")
	}
	data, err := s.client.Get(ctx, jobRequestKey(jobID)).Bytes()
	if err != nil {
		return nil, fmt.Errorf("job store get job request %s: %w", jobID, err)
	}
	var req pb.JobRequest
	if err := protojson.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("unmarshal job request: %w", err)
	}
	return &req, nil
}

// SetDeadline records a per-job deadline for enforcement.
func (s *RedisJobStore) SetDeadline(ctx context.Context, jobID string, deadline time.Time) error {
	if jobID == "" || deadline.IsZero() {
		return fmt.Errorf("invalid job deadline")
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldDeadline, deadline.Unix())
	pipe.ZAdd(ctx, deadlineIndexKey(), redis.Z{Score: float64(deadline.Unix()), Member: jobID})
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set deadline %s: %w", jobID, err)
	}
	return nil
}

// ListExpiredDeadlines returns jobs whose deadline has passed.
func (s *RedisJobStore) ListExpiredDeadlines(ctx context.Context, nowUnix int64, limit int64) ([]model.JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	members, err := s.client.ZRangeByScoreWithScores(ctx, deadlineIndexKey(), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", nowUnix),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list expired deadlines: %w", err)
	}

	out := make([]model.JobRecord, 0, len(members))
	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok {
			continue
		}
		topic, _ := s.GetTopic(ctx, jobID)
		tenant, _ := s.GetTenant(ctx, jobID)
		principal, _ := s.GetPrincipal(ctx, jobID)
		actorID, _ := s.GetActorID(ctx, jobID)
		actorType, _ := s.GetActorType(ctx, jobID)
		idempotencyKey, _ := s.GetIdempotencyKey(ctx, jobID)
		capability, _ := s.GetCapability(ctx, jobID)
		packID, _ := s.GetPackID(ctx, jobID)
		riskTags, _ := s.GetRiskTags(ctx, jobID)
		requires, _ := s.GetRequires(ctx, jobID)
		attempts, _ := s.GetAttempts(ctx, jobID)
		decision, _ := s.GetSafetyDecision(ctx, jobID)
		out = append(out, model.JobRecord{
			ID:             jobID,
			UpdatedAt:      int64(m.Score),
			Topic:          topic,
			Tenant:         tenant,
			Principal:      principal,
			ActorID:        actorID,
			ActorType:      actorType,
			IdempotencyKey: idempotencyKey,
			Capability:     capability,
			RiskTags:       riskTags,
			Requires:       requires,
			PackID:         packID,
			Attempts:       attempts,
			SafetyDecision: string(decision.Decision),
			SafetyReason:   decision.Reason,
			SafetyRuleID:   decision.RuleID,
			SafetySnapshot: decision.PolicySnapshot,
			DeadlineUnix:   int64(m.Score),
		})
	}
	return out, nil
}

// ListJobsByState returns jobs in the given state last updated at or before the given unix timestamp.
func (s *RedisJobStore) ListJobsByState(ctx context.Context, state model.JobState, updatedBeforeUnix int64, limit int64) ([]model.JobRecord, error) {
	key := stateIndexKey(state)
	if key == "" {
		return nil, fmt.Errorf("unknown state %s", state)
	}
	if limit <= 0 {
		limit = 100
	}
	updatedBeforeUnix = normalizeTimestampMicrosUpper(updatedBeforeUnix)
	members, err := s.client.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", updatedBeforeUnix),
		Offset: 0,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list jobs by state %s: %w", state, err)
	}
	out := make([]model.JobRecord, 0, len(members))
	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok {
			continue
		}
		topic, _ := s.GetTopic(ctx, jobID)
		tenant, _ := s.GetTenant(ctx, jobID)
		principal, _ := s.GetPrincipal(ctx, jobID)
		actorID, _ := s.GetActorID(ctx, jobID)
		actorType, _ := s.GetActorType(ctx, jobID)
		idempotencyKey, _ := s.GetIdempotencyKey(ctx, jobID)
		capability, _ := s.GetCapability(ctx, jobID)
		packID, _ := s.GetPackID(ctx, jobID)
		riskTags, _ := s.GetRiskTags(ctx, jobID)
		requires, _ := s.GetRequires(ctx, jobID)
		attempts, _ := s.GetAttempts(ctx, jobID)
		safetyDecision, _ := s.GetSafetyDecision(ctx, jobID)
		deadlineUnix, _ := s.getDeadline(ctx, jobID)
		out = append(out, model.JobRecord{
			ID:             jobID,
			UpdatedAt:      int64(m.Score),
			State:          state,
			Topic:          topic,
			Tenant:         tenant,
			Principal:      principal,
			ActorID:        actorID,
			ActorType:      actorType,
			IdempotencyKey: idempotencyKey,
			Capability:     capability,
			RiskTags:       riskTags,
			Requires:       requires,
			PackID:         packID,
			Attempts:       attempts,
			SafetyDecision: string(safetyDecision.Decision),
			SafetyReason:   safetyDecision.Reason,
			SafetyRuleID:   safetyDecision.RuleID,
			SafetySnapshot: safetyDecision.PolicySnapshot,
			DeadlineUnix:   deadlineUnix,
		})
	}
	return out, nil
}

func (s *RedisJobStore) Close() error {
	return s.client.Close()
}

func (s *RedisJobStore) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis job store not initialized")
	}
	return s.client.Ping(ctx).Err()
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

func jobRequestKey(jobID string) string {
	return jobRequestKeyPrefix + jobID
}

func jobEventsKey(jobID string) string {
	return jobEventsKeyPrefix + jobID
}

func outputDecisionKey(jobID string) string {
	if jobID == "" {
		return ""
	}
	return "job:" + jobID + jobOutputDecisionKeySuffix
}

func jobDecisionKey(jobID string) string {
	return "job:decisions:" + jobID
}

func jobIdempotencyKey(key string) string {
	return "job:idempotency:" + key
}

func jobIdempotencyKeyScoped(tenant, key string) string {
	tenant = strings.TrimSpace(tenant)
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if tenant == "" {
		return jobIdempotencyKey(key)
	}
	return jobIdempotencyKey(tenant + ":" + key)
}

func tenantActiveKey(tenant string) string {
	return "job:tenant:active:" + tenant
}

func stateIndexKey(state model.JobState) string {
	if state == "" {
		return ""
	}
	return "job:index:" + strings.ToLower(string(state))
}

func (s *RedisJobStore) AddJobToTrace(ctx context.Context, traceID, jobID string) error {
	if traceID == "" || jobID == "" {
		return fmt.Errorf("traceID and jobID are required")
	}

	metaKey := jobMetaKey(jobID)
	pipe := s.client.TxPipeline()
	pipe.SAdd(ctx, "trace:"+traceID, jobID)
	pipe.HSet(ctx, metaKey, metaFieldTraceID, traceID)
	if s.metaTTL > 0 {
		pipe.Expire(ctx, metaKey, s.metaTTL)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store add job to trace %s: %w", jobID, err)
	}
	return nil
}

// GetTraceID returns the stored trace id for a job, if available.
func (s *RedisJobStore) GetTraceID(ctx context.Context, jobID string) (string, error) {
	if jobID == "" {
		return "", fmt.Errorf("jobID required")
	}
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldTraceID).Result()
}

func (s *RedisJobStore) SetTopic(ctx context.Context, jobID, topic string) error {
	if jobID == "" || topic == "" {
		return fmt.Errorf("jobID and topic are required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), metaFieldTopic, topic).Err()
}

func (s *RedisJobStore) GetTopic(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldTopic).Result()
}

func (s *RedisJobStore) SetTenant(ctx context.Context, jobID, tenant string) error {
	if jobID == "" || tenant == "" {
		return fmt.Errorf("jobID and tenant are required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), metaFieldTenant, tenant).Err()
}

func (s *RedisJobStore) GetTenant(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldTenant).Result()
}

func (s *RedisJobStore) SetTeam(ctx context.Context, jobID, team string) error {
	if jobID == "" || team == "" {
		return fmt.Errorf("jobID and team are required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), metaFieldTeam, team).Err()
}

func (s *RedisJobStore) GetTeam(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldTeam).Result()
}

// Optional helpers for principal metadata.
func (s *RedisJobStore) SetPrincipal(ctx context.Context, jobID, principal string) error {
	if jobID == "" || principal == "" {
		return fmt.Errorf("jobID and principal are required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), metaFieldPrincipal, principal).Err()
}

func (s *RedisJobStore) GetPrincipal(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldPrincipal).Result()
}

func (s *RedisJobStore) GetActorID(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldActorID).Result()
}

func (s *RedisJobStore) GetActorType(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldActorType).Result()
}

func (s *RedisJobStore) GetIdempotencyKey(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldIdempotencyKey).Result()
}

func (s *RedisJobStore) GetCapability(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldCapability).Result()
}

func (s *RedisJobStore) GetPackID(ctx context.Context, jobID string) (string, error) {
	return s.client.HGet(ctx, jobMetaKey(jobID), metaFieldPackID).Result()
}

func (s *RedisJobStore) GetRiskTags(ctx context.Context, jobID string) ([]string, error) {
	raw, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldRiskTags).Result()
	if err != nil {
		return nil, err
	}
	return parseJSONStringSlice(raw), nil
}

func (s *RedisJobStore) GetRequires(ctx context.Context, jobID string) ([]string, error) {
	raw, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldRequires).Result()
	if err != nil {
		return nil, err
	}
	return parseJSONStringSlice(raw), nil
}

func (s *RedisJobStore) GetAttempts(ctx context.Context, jobID string) (int, error) {
	raw, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldAttempts).Result()
	if err != nil {
		return 0, err
	}
	return parseInt(raw), nil
}

// GetAllMeta returns all hash fields for a job in a single HGETALL call.
// Callers can extract individual fields from the returned map.
func (s *RedisJobStore) GetAllMeta(ctx context.Context, jobID string) (map[string]string, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID required")
	}
	data, err := s.client.HGetAll(ctx, jobMetaKey(jobID)).Result()
	if err != nil {
		return nil, fmt.Errorf("job store get all meta %s: %w", jobID, err)
	}
	return data, nil
}

// IncrAttempts atomically increments the attempt counter for a job.
// This is used by the scheduler to escalate backoff for retryable scheduling failures.
func (s *RedisJobStore) IncrAttempts(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	return s.client.HIncrBy(ctx, jobMetaKey(jobID), metaFieldAttempts, 1).Err()
}

func (s *RedisJobStore) CountActiveByTenant(ctx context.Context, tenant string) (int, error) {
	if tenant == "" {
		return 0, fmt.Errorf("tenant required")
	}
	count, err := s.client.SCard(ctx, tenantActiveKey(tenant)).Result()
	if err != nil {
		return 0, fmt.Errorf("job store count active by tenant %s: %w", tenant, err)
	}
	return int(count), nil
}

func (s *RedisJobStore) SetFailureReason(ctx context.Context, jobID, reason string) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), "failure_reason", reason).Err()
}

func (s *RedisJobStore) GetFailureReason(ctx context.Context, jobID string) (string, error) {
	if jobID == "" {
		return "", fmt.Errorf("jobID required")
	}
	val, err := s.client.HGet(ctx, jobMetaKey(jobID), "failure_reason").Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return val, fmt.Errorf("job store get failure reason %s: %w", jobID, err)
	}
	return val, nil
}

// idempotencyScopedScript atomically checks the legacy key and performs
// SetNX on the scoped key in a single round-trip, preventing the TOCTOU
// race between the legacy GET and the scoped SetNX.
//
// TODO(cluster): CROSSSLOT — needs hash tags or pipeline split for Redis Cluster.
// KEYS[1] and KEYS[2] may hash to different slots; the dynamic HGET on
// ARGV[4]..legacyID adds a third slot. Medium risk (job submit path).
//
// KEYS[1] = legacy key, KEYS[2] = scoped key
// ARGV[1] = jobID, ARGV[2] = ttl in ms, ARGV[3] = tenant, ARGV[4] = meta key prefix
var idempotencyScopedScript = redis.NewScript(`
local legacyID = redis.call("GET", KEYS[1])
if legacyID and legacyID ~= false then
  local metaKey = ARGV[4] .. legacyID
  local metaTenant = redis.call("HGET", metaKey, "tenant")
  if not metaTenant or metaTenant == false or metaTenant == "" or metaTenant == ARGV[3] then
    return "EXISTING:" .. legacyID
  end
end
local ok = redis.call("SET", KEYS[2], ARGV[1], "NX", "PX", ARGV[2])
if ok then
  return "OK:" .. ARGV[1]
end
local existing = redis.call("GET", KEYS[2])
if existing and existing ~= false then
  return "EXISTING:" .. existing
end
return "EXISTING:"
`)

func (s *RedisJobStore) TrySetIdempotencyKeyScoped(ctx context.Context, tenant, key, jobID string) (bool, string, error) {
	if key == "" || jobID == "" {
		return false, "", fmt.Errorf("idempotency key and jobID required")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		idKey := jobIdempotencyKeyScoped("", key)
		if idKey == "" {
			return false, "", fmt.Errorf("idempotency key required")
		}
		ok, err := s.client.SetNX(ctx, idKey, jobID, s.metaTTL).Result()
		if err != nil {
			return false, "", fmt.Errorf("job store try set idempotency key scoped %s: %w", jobID, err)
		}
		if ok {
			return true, jobID, nil
		}
		existing, err := s.client.Get(ctx, idKey).Result()
		if err != nil && err != redis.Nil {
			return false, "", fmt.Errorf("job store try set idempotency key scoped %s: %w", jobID, err)
		}
		return false, existing, nil
	}

	idKey := jobIdempotencyKeyScoped(tenant, key)
	if idKey == "" {
		return false, "", fmt.Errorf("idempotency key required")
	}
	legacyKey := jobIdempotencyKey(key)
	ttlMs := s.metaTTL.Milliseconds()
	result, err := idempotencyScopedScript.Run(ctx, s.client,
		[]string{legacyKey, idKey},
		jobID, ttlMs, tenant, jobMetaKeyPrefix,
	).Text()
	if err != nil {
		return false, "", fmt.Errorf("job store try set idempotency key scoped %s: %w", jobID, err)
	}
	if strings.HasPrefix(result, "OK:") {
		return true, result[3:], nil
	}
	if strings.HasPrefix(result, "EXISTING:") {
		return false, result[9:], nil
	}
	return false, "", nil
}

func (s *RedisJobStore) SetIdempotencyKeyScoped(ctx context.Context, tenant, key, jobID string) error {
	if key == "" || jobID == "" {
		return fmt.Errorf("idempotency key and jobID required")
	}
	idKey := jobIdempotencyKeyScoped(tenant, key)
	if idKey == "" {
		return fmt.Errorf("idempotency key required")
	}
	_, err := s.client.SetNX(ctx, idKey, jobID, s.metaTTL).Result()
	if err != nil {
		return fmt.Errorf("job store set idempotency key scoped %s: %w", jobID, err)
	}
	return nil
}

func (s *RedisJobStore) GetJobByIdempotencyKeyScoped(ctx context.Context, tenant, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("idempotency key required")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return s.client.Get(ctx, jobIdempotencyKey(key)).Result()
	}

	idKey := jobIdempotencyKeyScoped(tenant, key)
	if idKey == "" {
		return "", fmt.Errorf("idempotency key required")
	}
	if val, err := s.client.Get(ctx, idKey).Result(); err == nil {
		return val, nil
	} else if err != redis.Nil {
		return "", fmt.Errorf("job store get job by idempotency key scoped: %w", err)
	}

	legacyID, err := s.client.Get(ctx, jobIdempotencyKey(key)).Result()
	if err != nil {
		return "", fmt.Errorf("job store get job by idempotency key scoped: %w", err)
	}
	if legacyID == "" {
		return "", redis.Nil
	}
	legacyTenant, terr := s.GetTenant(ctx, legacyID)
	if terr != nil && terr != redis.Nil {
		return "", fmt.Errorf("job store get job by idempotency key scoped: %w", terr)
	}
	if legacyTenant != "" && legacyTenant != tenant {
		return "", redis.Nil
	}
	_ = s.SetIdempotencyKeyScoped(ctx, tenant, key, legacyID)
	return legacyID, nil
}

func (s *RedisJobStore) SetIdempotencyKey(ctx context.Context, key, jobID string) error {
	return s.SetIdempotencyKeyScoped(ctx, "", key, jobID)
}

func (s *RedisJobStore) GetJobByIdempotencyKey(ctx context.Context, key string) (string, error) {
	return s.GetJobByIdempotencyKeyScoped(ctx, "", key)
}

func (s *RedisJobStore) SetSafetyDecision(ctx context.Context, jobID string, record model.SafetyDecisionRecord) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	if record.CheckedAt == 0 {
		record.CheckedAt = nowUnixMicros()
	}
	constraintsJSON := ""
	if record.Constraints != nil {
		if data, err := protojson.Marshal(record.Constraints); err == nil {
			constraintsJSON = string(data)
		}
	}
	remediationsJSON := ""
	if len(record.Remediations) > 0 {
		if data, err := json.Marshal(record.Remediations); err == nil {
			remediationsJSON = string(data)
		}
	}
	fields := map[string]any{
		metaFieldSafetyDecision:   string(record.Decision),
		metaFieldSafetyReason:     record.Reason,
		metaFieldSafetyRuleID:     record.RuleID,
		metaFieldSafetySnapshot:   record.PolicySnapshot,
		metaFieldSafetyChecked:    record.CheckedAt,
		metaFieldApprovalRequired: record.ApprovalRequired,
		metaFieldApprovalRef:      record.ApprovalRef,
	}
	if record.JobHash != "" {
		fields[metaFieldSafetyJobHash] = record.JobHash
	}
	if constraintsJSON != "" {
		fields[metaFieldSafetyConstraints] = constraintsJSON
	}
	if remediationsJSON != "" {
		fields[metaFieldSafetyRemediations] = remediationsJSON
	}

	var rawConstraints json.RawMessage
	if constraintsJSON != "" {
		rawConstraints = json.RawMessage(constraintsJSON)
	}
	var rawRemediations json.RawMessage
	if remediationsJSON != "" {
		rawRemediations = json.RawMessage(remediationsJSON)
	}
	entry := map[string]any{
		"decision":          string(record.Decision),
		"reason":            record.Reason,
		"rule_id":           record.RuleID,
		"policy_snapshot":   record.PolicySnapshot,
		"constraints":       rawConstraints,
		"remediations":      rawRemediations,
		"approval_required": record.ApprovalRequired,
		"approval_ref":      record.ApprovalRef,
		"job_hash":          record.JobHash,
		"checked_at":        record.CheckedAt,
	}
	encoded, _ := json.Marshal(entry)

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), fields)
	pipe.RPush(ctx, jobDecisionKey(jobID), string(encoded))
	if s.metaTTL > 0 {
		pipe.Expire(ctx, jobMetaKey(jobID), s.metaTTL)
		pipe.Expire(ctx, jobDecisionKey(jobID), s.metaTTL)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set safety decision %s: %w", jobID, err)
	}
	return nil
}

// SetOutputSafety stores output policy evaluation data on the job metadata hash.
func (s *RedisJobStore) SetOutputSafety(ctx context.Context, jobID string, record model.OutputSafetyRecord) error {
	return s.SetOutputDecision(ctx, jobID, record)
}

// SetOutputDecision stores output policy evaluation data in a dedicated key and metadata hash.
func (s *RedisJobStore) SetOutputDecision(ctx context.Context, jobID string, record model.OutputSafetyRecord) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	if record.CheckedAt == 0 {
		record.CheckedAt = nowUnixMicros()
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal output safety record: %w", err)
	}
	pipe := s.client.TxPipeline()
	ttl := time.Duration(0)
	if s.metaTTL > 0 {
		ttl = s.metaTTL
	}
	pipe.Set(ctx, outputDecisionKey(jobID), string(encoded), ttl)
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldOutputSafety, string(encoded))
	if s.metaTTL > 0 {
		pipe.Expire(ctx, jobMetaKey(jobID), s.metaTTL)
		pipe.Expire(ctx, outputDecisionKey(jobID), s.metaTTL)
	}
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set output decision %s: %w", jobID, err)
	}
	return nil
}

// GetOutputSafety loads output policy evaluation data from job metadata.
func (s *RedisJobStore) GetOutputSafety(ctx context.Context, jobID string) (model.OutputSafetyRecord, error) {
	return s.GetOutputDecision(ctx, jobID)
}

// GetOutputDecision loads output policy data, preferring dedicated key and falling back to metadata hash.
func (s *RedisJobStore) GetOutputDecision(ctx context.Context, jobID string) (model.OutputSafetyRecord, error) {
	if jobID == "" {
		return model.OutputSafetyRecord{}, fmt.Errorf("jobID required")
	}
	raw, err := s.client.Get(ctx, outputDecisionKey(jobID)).Result()
	if err == redis.Nil || raw == "" {
		raw, err = s.client.HGet(ctx, jobMetaKey(jobID), metaFieldOutputSafety).Result()
	}
	if err == redis.Nil || raw == "" {
		return model.OutputSafetyRecord{}, nil
	}
	if err != nil {
		return model.OutputSafetyRecord{}, fmt.Errorf("job store get output decision %s: %w", jobID, err)
	}
	return parseOutputSafetyRecord(raw, jobID), nil
}

// SetApprovalRecord stores approval audit metadata on the job.
func (s *RedisJobStore) SetApprovalRecord(ctx context.Context, jobID string, record ApprovalRecord) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	if record.ApprovedAt == 0 {
		record.ApprovedAt = nowUnixMicros()
	}
	fields := map[string]any{
		metaFieldApprovalBy:       record.ApprovedBy,
		metaFieldApprovalRole:     record.ApprovedRole,
		metaFieldApprovalAt:       record.ApprovedAt,
		metaFieldApprovalReason:   record.Reason,
		metaFieldApprovalNote:     record.Note,
		metaFieldApprovalSnapshot: record.PolicySnapshot,
		metaFieldApprovalJobHash:  record.JobHash,
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), fields)
	if s.metaTTL > 0 {
		pipe.Expire(ctx, jobMetaKey(jobID), s.metaTTL)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set approval record %s: %w", jobID, err)
	}
	return nil
}

// GetApprovalRecord loads approval audit metadata from the job.
func (s *RedisJobStore) GetApprovalRecord(ctx context.Context, jobID string) (ApprovalRecord, error) {
	if jobID == "" {
		return ApprovalRecord{}, fmt.Errorf("jobID required")
	}
	meta := jobMetaKey(jobID)
	data, err := s.client.HGetAll(ctx, meta).Result()
	if err != nil && err != redis.Nil {
		return ApprovalRecord{}, fmt.Errorf("job store get approval record %s: %w", jobID, err)
	}
	record := ApprovalRecord{
		ApprovedBy:     data[metaFieldApprovalBy],
		ApprovedRole:   data[metaFieldApprovalRole],
		Reason:         data[metaFieldApprovalReason],
		Note:           data[metaFieldApprovalNote],
		PolicySnapshot: data[metaFieldApprovalSnapshot],
		JobHash:        data[metaFieldApprovalJobHash],
	}
	if raw := data[metaFieldApprovalAt]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.ApprovedAt = parsed
		}
	}
	return record, nil
}

func (s *RedisJobStore) GetSafetyDecision(ctx context.Context, jobID string) (model.SafetyDecisionRecord, error) {
	meta := jobMetaKey(jobID)
	data, err := s.client.HGetAll(ctx, meta).Result()
	if err != nil && err != redis.Nil {
		return model.SafetyDecisionRecord{}, fmt.Errorf("job store get safety decision %s: %w", jobID, err)
	}
	record := model.SafetyDecisionRecord{
		Decision:       model.SafetyDecision(data[metaFieldSafetyDecision]),
		Reason:         data[metaFieldSafetyReason],
		RuleID:         data[metaFieldSafetyRuleID],
		PolicySnapshot: data[metaFieldSafetySnapshot],
		ApprovalRef:    data[metaFieldApprovalRef],
		JobHash:        data[metaFieldSafetyJobHash],
	}
	if data[metaFieldApprovalRequired] == "true" {
		record.ApprovalRequired = true
	}
	if raw := data[metaFieldSafetyChecked]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.CheckedAt = parsed
		}
	}
	if raw := data[metaFieldSafetyConstraints]; raw != "" {
		var constraints pb.PolicyConstraints
		if err := protojson.Unmarshal([]byte(raw), &constraints); err == nil {
			record.Constraints = &constraints
		}
	}
	if raw := data[metaFieldSafetyRemediations]; raw != "" {
		var remediations []*pb.PolicyRemediation
		if err := json.Unmarshal([]byte(raw), &remediations); err == nil {
			record.Remediations = remediations
		}
	}
	return record, nil
}

// ListSafetyDecisions returns recent safety decisions for a job (most recent first).
func (s *RedisJobStore) ListSafetyDecisions(ctx context.Context, jobID string, limit int64) ([]model.SafetyDecisionRecord, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID required")
	}
	if limit <= 0 {
		limit = 50
	}
	raw, err := s.client.LRange(ctx, jobDecisionKey(jobID), -limit, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list safety decisions %s: %w", jobID, err)
	}
	out := make([]model.SafetyDecisionRecord, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw[i]), &entry); err != nil {
			slog.Warn("job-store: corrupt safety decision skipped", "job_id", jobID, "error", err)
			continue
		}
		record := model.SafetyDecisionRecord{
			Decision:       model.SafetyDecision(stringFromEntry(entry, "decision")),
			Reason:         stringFromEntry(entry, "reason"),
			RuleID:         stringFromEntry(entry, "rule_id"),
			PolicySnapshot: stringFromEntry(entry, "policy_snapshot"),
			ApprovalRef:    stringFromEntry(entry, "approval_ref"),
			JobHash:        stringFromEntry(entry, "job_hash"),
		}
		if val, ok := entry["approval_required"].(bool); ok {
			record.ApprovalRequired = val
		}
		if val, ok := entry["checked_at"].(float64); ok {
			record.CheckedAt = int64(val)
		}
		if rawConstraints, ok := entry["constraints"].(map[string]any); ok && rawConstraints != nil {
			if data, err := json.Marshal(rawConstraints); err == nil {
				var constraints pb.PolicyConstraints
				if err := protojson.Unmarshal(data, &constraints); err == nil {
					record.Constraints = &constraints
				}
			}
		}
		if rawRemediations, ok := entry["remediations"].([]any); ok && rawRemediations != nil {
			if data, err := json.Marshal(rawRemediations); err == nil {
				var remediations []*pb.PolicyRemediation
				if err := json.Unmarshal(data, &remediations); err == nil {
					record.Remediations = remediations
				}
			}
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *RedisJobStore) GetTraceJobs(ctx context.Context, traceID string) ([]model.JobRecord, error) {
	jobIDs, err := s.client.SMembers(ctx, "trace:"+traceID).Result()
	if err != nil {
		return nil, fmt.Errorf("job store get trace jobs %s: %w", traceID, err)
	}
	if len(jobIDs) == 0 {
		return []model.JobRecord{}, nil
	}

	pipe := s.client.Pipeline()
	metaCmds := make(map[string]*redis.MapStringStringCmd, len(jobIDs))
	for _, id := range jobIDs {
		metaCmds[id] = pipe.HGetAll(ctx, jobMetaKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		slog.Warn("redis pipeline exec", "op", "get_trace_jobs", "error", err)
	}

	out := make([]model.JobRecord, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		meta, _ := metaCmds[jobID].Result()
		state := model.JobState(meta["state"])
		if state == "" {
			state, _ = s.GetState(ctx, jobID)
		}
		deadlineUnix, _ := strconv.ParseInt(meta[metaFieldDeadline], 10, 64)
		riskTags := parseJSONStringSlice(meta[metaFieldRiskTags])
		requires := parseJSONStringSlice(meta[metaFieldRequires])
		attempts := parseInt(meta[metaFieldAttempts])
		out = append(out, model.JobRecord{
			ID:             jobID,
			TraceID:        traceID,
			State:          state,
			Topic:          meta[metaFieldTopic],
			Tenant:         meta[metaFieldTenant],
			Team:           meta[metaFieldTeam],
			Principal:      meta[metaFieldPrincipal],
			ActorID:        meta[metaFieldActorID],
			ActorType:      meta[metaFieldActorType],
			IdempotencyKey: meta[metaFieldIdempotencyKey],
			Capability:     meta[metaFieldCapability],
			RiskTags:       riskTags,
			Requires:       requires,
			PackID:         meta[metaFieldPackID],
			Attempts:       attempts,
			SafetyDecision: meta[metaFieldSafetyDecision],
			SafetyReason:   meta[metaFieldSafetyReason],
			SafetyRuleID:   meta[metaFieldSafetyRuleID],
			SafetySnapshot: meta[metaFieldSafetySnapshot],
			DeadlineUnix:   deadlineUnix,
		})
	}
	return out, nil
}

func (s *RedisJobStore) getDeadline(ctx context.Context, jobID string) (int64, error) {
	val, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldDeadline).Int64()
	if err != nil {
		return 0, fmt.Errorf("job store get deadline %s: %w", jobID, err)
	}
	return val, nil
}

func isAllowedTransition(from, to model.JobState) bool {
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

func isActiveState(state model.JobState) bool {
	if state == "" {
		return false
	}
	return !terminalStates[state]
}

func parseJSONStringSlice(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseOutputSafetyRecord(raw string, jobID string) model.OutputSafetyRecord {
	if raw == "" {
		return model.OutputSafetyRecord{}
	}
	var record model.OutputSafetyRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		slog.Warn("job-store: corrupt output safety record", "job_id", jobID, "error", err)
		return model.OutputSafetyRecord{}
	}
	return record
}

func parseInt(raw string) int {
	if raw == "" {
		return 0
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return val
}

func actorTypeString(val pb.ActorType) string {
	switch val {
	case pb.ActorType_ACTOR_TYPE_HUMAN:
		return "human"
	case pb.ActorType_ACTOR_TYPE_SERVICE:
		return "service"
	default:
		return ""
	}
}

func mergeLabels(base map[string]string, meta *pb.JobMetadata) map[string]string {
	if len(base) == 0 && (meta == nil || len(meta.GetLabels()) == 0) {
		return nil
	}
	out := make(map[string]string)
	for k, v := range base {
		out[k] = v
	}
	if meta != nil {
		for k, v := range meta.GetLabels() {
			out[k] = v
		}
	}
	return out
}

func stringFromEntry(entry map[string]any, key string) string {
	if entry == nil {
		return ""
	}
	if val, ok := entry[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
