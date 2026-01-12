package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/redisutil"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	jobStateKeyPrefix           = "job:state:"
	jobResultPtrKeyPrefix       = "job:result_ptr:"
	jobMetaKeyPrefix            = "job:meta:"
	jobRequestKeyPrefix         = "job:req:"
	jobEventsKeyPrefix          = "job:events:"
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
	terminalStates = map[scheduler.JobState]bool{
		scheduler.JobStateSucceeded: true,
		scheduler.JobStateFailed:    true,
		scheduler.JobStateCancelled: true,
		scheduler.JobStateTimeout:   true,
		scheduler.JobStateDenied:    true,
	}
	allStates = []scheduler.JobState{
		scheduler.JobStatePending,
		scheduler.JobStateApproval,
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
		"":                           {scheduler.JobStatePending, scheduler.JobStateApproval, scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateFailed},
		scheduler.JobStatePending:    {scheduler.JobStateApproval, scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied, scheduler.JobStateFailed, scheduler.JobStateTimeout},
		scheduler.JobStateApproval:   {scheduler.JobStatePending, scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied, scheduler.JobStateFailed, scheduler.JobStateTimeout},
		scheduler.JobStateScheduled:  {scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied, scheduler.JobStateFailed, scheduler.JobStateTimeout, scheduler.JobStateSucceeded, scheduler.JobStateCancelled},
		scheduler.JobStateDispatched: {scheduler.JobStateRunning, scheduler.JobStateSucceeded, scheduler.JobStateFailed, scheduler.JobStateCancelled, scheduler.JobStateTimeout},
		scheduler.JobStateRunning:    {scheduler.JobStateSucceeded, scheduler.JobStateFailed, scheduler.JobStateCancelled, scheduler.JobStateTimeout},
		scheduler.JobStateSucceeded:  {},
		scheduler.JobStateFailed:     {},
		scheduler.JobStateCancelled:  {},
		scheduler.JobStateTimeout:    {},
		scheduler.JobStateDenied:     {},
	}
)

var defaultJobMetaTTL = 7 * 24 * time.Hour

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

// RedisJobStore implements scheduler.JobStore backed by Redis.
type RedisJobStore struct {
	client  redis.UniversalClient
	metaTTL time.Duration
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
func (s *RedisJobStore) CancelJob(ctx context.Context, jobID string) (scheduler.JobState, error) {
	if jobID == "" {
		return "", fmt.Errorf("jobID required")
	}
	metaKey := jobMetaKey(jobID)

	var resultState scheduler.JobState
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		current, err := tx.HGet(ctx, metaKey, "state").Result()
		if err != nil && err != redis.Nil {
			return err
		}
		currState := scheduler.JobState(current)
		if terminalStates[currState] {
			resultState = currState
			return nil
		}

		now := nowUnixMicros()
		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, map[string]any{
			"state":      string(scheduler.JobStateCancelled),
			"updated_at": now,
		})
		pipe.Set(ctx, jobStateKey(jobID), string(scheduler.JobStateCancelled), 0)

		if prevIdx := stateIndexKey(currState); prevIdx != "" {
			pipe.ZRem(ctx, prevIdx, jobID)
		}
		if idx := stateIndexKey(scheduler.JobStateCancelled); idx != "" {
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

		pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, scheduler.JobStateCancelled))

		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		resultState = scheduler.JobStateCancelled
		return nil
	}, metaKey, jobStateKey(jobID))
	return resultState, err
}

// TryAcquireLock attempts to acquire a distributed lock with TTL; returns true if acquired.
func (s *RedisJobStore) TryAcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return s.client.SetNX(ctx, key, time.Now().Unix(), ttl).Result()
}

// ReleaseLock releases a distributed lock.
func (s *RedisJobStore) ReleaseLock(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	_, err := s.client.Del(ctx, key).Result()
	return err
}

// NewRedisJobStore constructs a Redis-backed JobStore using a redis:// URL.
func NewRedisJobStore(url string) (*RedisJobStore, error) {
	if url == "" {
		url = defaultRedisURL
	}

	ttl := defaultJobMetaTTL
	if v := os.Getenv(envJobMetaTTLSeconds); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			ttl = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv(envJobMetaTTL); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
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

func (s *RedisJobStore) SetState(ctx context.Context, jobID string, state scheduler.JobState) error {
	if jobID == "" || state == "" {
		return fmt.Errorf("invalid jobID or state")
	}

	now := nowUnixMicros()
	metaKey := jobMetaKey(jobID)

	return s.client.Watch(ctx, func(tx *redis.Tx) error {
		prev, err := tx.HGet(ctx, metaKey, "state").Result()
		if err != nil && err != redis.Nil {
			return err
		}
		prevState := scheduler.JobState(prev)
		if !isAllowedTransition(prevState, state) {
			return fmt.Errorf("invalid transition %s -> %s", prevState, state)
		}
		attempts := 0
		if raw, err := tx.HGet(ctx, metaKey, metaFieldAttempts).Result(); err == nil {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
				attempts = parsed
			}
		}
		if state == scheduler.JobStateScheduled && prevState != scheduler.JobStateScheduled {
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
func (s *RedisJobStore) ListRecentJobs(ctx context.Context, limit int64) ([]scheduler.JobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	members, err := s.client.ZRevRangeWithScores(ctx, "job:recent", 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	return s.buildJobRecords(ctx, members)
}

// ListRecentJobsByScore returns jobs at or below the provided updated_at score (cursor) ordered desc.
func (s *RedisJobStore) ListRecentJobsByScore(ctx context.Context, cursor int64, limit int64) ([]scheduler.JobRecord, error) {
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
		return nil, err
	}
	return s.buildJobRecords(ctx, members)
}

func (s *RedisJobStore) buildJobRecords(ctx context.Context, members []redis.Z) ([]scheduler.JobRecord, error) {
	out := make([]scheduler.JobRecord, 0, len(members))
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
	_, _ = pipe.Exec(ctx)

	for _, m := range members {
		jobID, ok := m.Member.(string)
		if !ok || jobID == "" {
			continue
		}
		meta, _ := metaCmds[jobID].Result()
		state := scheduler.JobState(meta["state"])
		if state == "" {
			if sCmd := stateCmds[jobID]; sCmd != nil {
				if val, err := sCmd.Result(); err == nil {
					state = scheduler.JobState(val)
				}
			}
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

		out = append(out, scheduler.JobRecord{
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
		return err
	}
	if idempotencyKey != "" {
		_ = s.SetIdempotencyKey(ctx, idempotencyKey, req.GetJobId())
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
		return nil, err
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
	return err
}

// ListExpiredDeadlines returns jobs whose deadline has passed.
func (s *RedisJobStore) ListExpiredDeadlines(ctx context.Context, nowUnix int64, limit int64) ([]scheduler.JobRecord, error) {
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
		return nil, err
	}

	out := make([]scheduler.JobRecord, 0, len(members))
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
		out = append(out, scheduler.JobRecord{
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
func (s *RedisJobStore) ListJobsByState(ctx context.Context, state scheduler.JobState, updatedBeforeUnix int64, limit int64) ([]scheduler.JobRecord, error) {
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
		return nil, err
	}
	out := make([]scheduler.JobRecord, 0, len(members))
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
		out = append(out, scheduler.JobRecord{
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

func jobDecisionKey(jobID string) string {
	return "job:decisions:" + jobID
}

func jobIdempotencyKey(key string) string {
	return "job:idempotency:" + key
}

func tenantActiveKey(tenant string) string {
	return "job:tenant:active:" + tenant
}

func stateIndexKey(state scheduler.JobState) string {
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
	return err
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

func (s *RedisJobStore) CountActiveByTenant(ctx context.Context, tenant string) (int, error) {
	if tenant == "" {
		return 0, fmt.Errorf("tenant required")
	}
	count, err := s.client.SCard(ctx, tenantActiveKey(tenant)).Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *RedisJobStore) SetIdempotencyKey(ctx context.Context, key, jobID string) error {
	if key == "" || jobID == "" {
		return fmt.Errorf("idempotency key and jobID required")
	}
	ttl := s.metaTTL
	if ttl <= 0 {
		return s.client.SetNX(ctx, jobIdempotencyKey(key), jobID, 0).Err()
	}
	return s.client.SetNX(ctx, jobIdempotencyKey(key), jobID, ttl).Err()
}

func (s *RedisJobStore) GetJobByIdempotencyKey(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("idempotency key required")
	}
	return s.client.Get(ctx, jobIdempotencyKey(key)).Result()
}

func (s *RedisJobStore) SetSafetyDecision(ctx context.Context, jobID string, record scheduler.SafetyDecisionRecord) error {
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
	return err
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
	return err
}

// GetApprovalRecord loads approval audit metadata from the job.
func (s *RedisJobStore) GetApprovalRecord(ctx context.Context, jobID string) (ApprovalRecord, error) {
	if jobID == "" {
		return ApprovalRecord{}, fmt.Errorf("jobID required")
	}
	meta := jobMetaKey(jobID)
	data, err := s.client.HGetAll(ctx, meta).Result()
	if err != nil && err != redis.Nil {
		return ApprovalRecord{}, err
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

func (s *RedisJobStore) GetSafetyDecision(ctx context.Context, jobID string) (scheduler.SafetyDecisionRecord, error) {
	meta := jobMetaKey(jobID)
	data, err := s.client.HGetAll(ctx, meta).Result()
	if err != nil && err != redis.Nil {
		return scheduler.SafetyDecisionRecord{}, err
	}
	record := scheduler.SafetyDecisionRecord{
		Decision:       scheduler.SafetyDecision(data[metaFieldSafetyDecision]),
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
func (s *RedisJobStore) ListSafetyDecisions(ctx context.Context, jobID string, limit int64) ([]scheduler.SafetyDecisionRecord, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID required")
	}
	if limit <= 0 {
		limit = 50
	}
	raw, err := s.client.LRange(ctx, jobDecisionKey(jobID), -limit, -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.SafetyDecisionRecord, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		var entry map[string]any
		if err := json.Unmarshal([]byte(raw[i]), &entry); err != nil {
			continue
		}
		record := scheduler.SafetyDecisionRecord{
			Decision:       scheduler.SafetyDecision(stringFromEntry(entry, "decision")),
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

func (s *RedisJobStore) GetTraceJobs(ctx context.Context, traceID string) ([]scheduler.JobRecord, error) {
	jobIDs, err := s.client.SMembers(ctx, "trace:"+traceID).Result()
	if err != nil {
		return nil, err
	}
	if len(jobIDs) == 0 {
		return []scheduler.JobRecord{}, nil
	}

	pipe := s.client.Pipeline()
	metaCmds := make(map[string]*redis.MapStringStringCmd, len(jobIDs))
	for _, id := range jobIDs {
		metaCmds[id] = pipe.HGetAll(ctx, jobMetaKey(id))
	}
	_, _ = pipe.Exec(ctx)

	out := make([]scheduler.JobRecord, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		meta, _ := metaCmds[jobID].Result()
		state := scheduler.JobState(meta["state"])
		if state == "" {
			state, _ = s.GetState(ctx, jobID)
		}
		deadlineUnix, _ := strconv.ParseInt(meta[metaFieldDeadline], 10, 64)
		riskTags := parseJSONStringSlice(meta[metaFieldRiskTags])
		requires := parseJSONStringSlice(meta[metaFieldRequires])
		attempts := parseInt(meta[metaFieldAttempts])
		out = append(out, scheduler.JobRecord{
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
		return 0, err
	}
	return val, nil
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

func isActiveState(state scheduler.JobState) bool {
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
