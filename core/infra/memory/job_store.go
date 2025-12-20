package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	jobStateKeyPrefix       = "job:state:"
	jobResultPtrKeyPrefix   = "job:result_ptr:"
	jobMetaKeyPrefix        = "job:meta:"
	jobEventsKeyPrefix      = "job:events:"
	metaFieldTopic          = "topic"
	metaFieldTenant         = "tenant"
	metaFieldPrincipal      = "principal"
	metaFieldTeam           = "team"
	metaFieldMemory         = "memory_id"
	metaFieldTraceID        = "trace_id"
	metaFieldLabels         = "labels"
	metaFieldDeadline       = "deadline_unix"
	metaFieldSafetyDecision = "safety_decision"
	metaFieldSafetyReason   = "safety_reason"
	envJobMetaTTL           = "JOB_META_TTL"
	envJobMetaTTLSeconds    = "JOB_META_TTL_SECONDS"
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
		"":                           {scheduler.JobStatePending, scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateFailed},
		scheduler.JobStatePending:    {scheduler.JobStateScheduled, scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied, scheduler.JobStateFailed, scheduler.JobStateTimeout},
		scheduler.JobStateScheduled:  {scheduler.JobStateDispatched, scheduler.JobStateRunning, scheduler.JobStateDenied, scheduler.JobStateFailed, scheduler.JobStateTimeout},
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

func deadlineIndexKey() string {
	return "job:deadline"
}

// RedisJobStore implements scheduler.JobStore backed by Redis.
type RedisJobStore struct {
	client  *redis.Client
	metaTTL time.Duration
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

		now := time.Now().Unix()
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

	return &RedisJobStore{client: client, metaTTL: ttl}, nil
}

func (s *RedisJobStore) SetState(ctx context.Context, jobID string, state scheduler.JobState) error {
	if jobID == "" || state == "" {
		return fmt.Errorf("invalid jobID or state")
	}

	now := time.Now().Unix()
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

		pipe := tx.TxPipeline()
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
		max = fmt.Sprintf("%d", cursor)
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
		safetyDecision := meta[metaFieldSafetyDecision]
		safetyReason := meta[metaFieldSafetyReason]
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
			SafetyDecision: safetyDecision,
			SafetyReason:   safetyReason,
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
	tenantID := req.GetTenantId()
	if tenantID == "" {
		tenantID = req.GetEnv()["tenant_id"]
	}
	teamID := req.GetEnv()["team_id"]
	fields := map[string]any{
		metaFieldTopic:     req.GetTopic(),
		metaFieldTenant:    tenantID,
		metaFieldTeam:      teamID,
		metaFieldPrincipal: req.GetPrincipalId(),
		metaFieldMemory:    req.GetMemoryId(),
	}

	if labels := req.GetLabels(); len(labels) > 0 {
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
	return nil
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
		out = append(out, scheduler.JobRecord{
			ID:           jobID,
			UpdatedAt:    int64(m.Score),
			Topic:        topic,
			Tenant:       tenant,
			Principal:    principal,
			DeadlineUnix: int64(m.Score),
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
		safetyDecision, safetyReason, _ := s.GetSafetyDecision(ctx, jobID)
		principal, _ := s.GetPrincipal(ctx, jobID)
		deadlineUnix, _ := s.getDeadline(ctx, jobID)
		out = append(out, scheduler.JobRecord{
			ID:             jobID,
			UpdatedAt:      int64(m.Score),
			State:          state,
			Topic:          topic,
			Tenant:         tenant,
			Principal:      principal,
			SafetyDecision: safetyDecision,
			SafetyReason:   safetyReason,
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

func (s *RedisJobStore) SetSafetyDecision(ctx context.Context, jobID, decision, reason string) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), map[string]any{
		metaFieldSafetyDecision: decision,
		metaFieldSafetyReason:   reason,
	}).Err()
}

func (s *RedisJobStore) GetSafetyDecision(ctx context.Context, jobID string) (string, string, error) {
	meta := jobMetaKey(jobID)
	decision, err := s.client.HGet(ctx, meta, metaFieldSafetyDecision).Result()
	if err != nil && err != redis.Nil {
		return "", "", err
	}
	reason, err2 := s.client.HGet(ctx, meta, metaFieldSafetyReason).Result()
	if err2 != nil && err2 != redis.Nil {
		return decision, "", err2
	}
	return decision, reason, nil
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
		out = append(out, scheduler.JobRecord{
			ID:             jobID,
			TraceID:        traceID,
			State:          state,
			Topic:          meta[metaFieldTopic],
			Tenant:         meta[metaFieldTenant],
			Team:           meta[metaFieldTeam],
			Principal:      meta[metaFieldPrincipal],
			SafetyDecision: meta[metaFieldSafetyDecision],
			SafetyReason:   meta[metaFieldSafetyReason],
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
