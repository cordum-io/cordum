package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	jobStateKeyPrefix              = "job:state:"
	jobResultPtrKeyPrefix          = "job:result_ptr:"
	jobMetaKeyPrefix               = "job:meta:"
	jobRequestKeyPrefix            = "job:req:"
	jobEventsKeyPrefix             = "job:events:"
	jobOutputDecisionKeySuffix     = ":output_decision"
	metaFieldWorkerID              = "worker_id"
	metaFieldTopic                 = "topic"
	metaFieldTenant                = "tenant"
	metaFieldPrincipal             = "principal"
	metaFieldTeam                  = "team"
	metaFieldMemory                = "memory_id"
	metaFieldTraceID               = "trace_id"
	metaFieldLabels                = "labels"
	metaFieldActorID               = "actor_id"
	metaFieldActorType             = "actor_type"
	metaFieldIdempotencyKey        = "idempotency_key"
	metaFieldCapability            = "capability"
	metaFieldRiskTags              = "risk_tags"
	metaFieldRequires              = "requires"
	metaFieldPackID                = "pack_id"
	metaFieldAgentID               = "agent_id"
	metaFieldAgentName             = "agent_name"
	metaFieldAgentRiskTier         = "agent_risk_tier"
	metaFieldSubmittedBy           = "submitted_by"
	metaFieldAttempts              = "attempts"
	metaFieldDeadline              = "deadline_unix"
	metaFieldSafetyDecision        = "safety_decision"
	metaFieldDelegationLineage     = "delegation_lineage"
	metaFieldDelegationDispatch    = "delegation_dispatch_token"
	metaFieldSafetyReason          = "safety_reason"
	metaFieldSafetyRuleID          = "safety_rule_id"
	metaFieldSafetySnapshot        = "safety_snapshot"
	metaFieldSafetyChecked         = "safety_checked_at"
	metaFieldSafetyConstraints     = "safety_constraints"
	metaFieldSafetyRemediations    = "safety_remediations"
	metaFieldOutputSafety          = "output_safety"
	metaFieldApprovalRequired      = "safety_approval_required"
	metaFieldApprovalRef           = "safety_approval_ref"
	metaFieldSafetyJobHash         = "safety_job_hash"
	metaFieldApprovalBy            = "approval_by"
	metaFieldApprovalRole          = "approval_role"
	metaFieldApprovalAt            = "approval_at"
	metaFieldApprovalReason        = "approval_reason"
	metaFieldApprovalNote          = "approval_note"
	metaFieldApprovalSnapshot      = "approval_policy_snapshot"
	metaFieldApprovalJobHash       = "approval_job_hash"
	metaFieldApprovalStatus        = "approval_status"
	metaFieldApprovalActionable    = "approval_actionability"
	metaFieldApprovalRevision      = "approval_revision"
	metaFieldApprovalDecision      = "approval_decision"
	metaFieldApprovalPublishStatus = "approval_publish_status"
	metaFieldApprovalPublishTarget = "approval_publish_target"
	metaFieldApprovalPublishedAt   = "approval_published_at"
	envJobMetaTTL                  = "JOB_META_TTL"
	envJobMetaTTLSeconds           = "JOB_META_TTL_SECONDS"
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
		"":                     {model.JobStatePending, model.JobStateApproval, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateFailed},
		model.JobStatePending:  {model.JobStateApproval, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout, model.JobStateSucceeded},
		model.JobStateApproval: {model.JobStatePending, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout, model.JobStateSucceeded},
		// Quarantined transitions from active states: output policy 2-phase evaluation
		// can quarantine a job at any point during execution if the output scanner
		// detects unsafe content. See ADR-005 (output-policy-2-phase).
		// SCHEDULED self-transition allowed for dispatch publish rollback
		// (DISPATCHED→SCHEDULED fails, second replay starts at SCHEDULED).
		model.JobStateScheduled:  {model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning, model.JobStateDenied, model.JobStateFailed, model.JobStateTimeout, model.JobStateSucceeded, model.JobStateCancelled, model.JobStateQuarantined},
		model.JobStateDispatched: {model.JobStateScheduled, model.JobStateRunning, model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateTimeout, model.JobStateQuarantined},
		model.JobStateRunning:    {model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateTimeout, model.JobStateQuarantined},
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

func hashApprovalJobRequest(req *pb.JobRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("job request required")
	}
	clone, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || clone == nil {
		return "", fmt.Errorf("job request clone failed")
	}
	if clone.Labels != nil {
		for key := range clone.Labels {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "approval_") || key == bus.LabelBusMsgID {
				delete(clone.Labels, key)
			}
		}
		if len(clone.Labels) == 0 {
			clone.Labels = nil
		}
	}
	if clone.Env != nil {
		delete(clone.Env, config.EffectiveConfigEnvVar)
		if len(clone.Env) == 0 {
			clone.Env = nil
		}
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// RedisJobStore implements model.JobStore backed by Redis.
type RedisJobStore struct {
	client         redis.UniversalClient
	metaTTL        time.Duration
	idempotencyTTL time.Duration // TTL for idempotency keys; must outlive job lifecycle
}

// Client returns the underlying Redis client for use by other subsystems
// (e.g., distributed rate limiting) that need shared Redis access.
func (s *RedisJobStore) Client() redis.UniversalClient {
	return s.client
}

// ApprovalRecord captures approval audit metadata stored on a job.
type ApprovalRecord = model.ApprovalRecord

// ApprovalConflictError captures a machine-readable approval conflict.
type ApprovalConflictError struct {
	Code    model.ApprovalConflictCode
	Message string
}

func (e *ApprovalConflictError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return string(e.Code)
	}
	return "approval conflict"
}

type ApprovalResolutionParams struct {
	JobID          string
	Decision       model.ApprovalDecision
	ResultState    model.JobState
	ApprovedBy     string
	ApprovedRole   string
	Reason         string
	Note           string
	PolicySnapshot string
	LabelUpdates   map[string]string
	PublishTarget  model.ApprovalPublishTarget
}

type ApprovalResolutionResult struct {
	JobID          string
	TraceID        string
	State          model.JobState
	Request        *pb.JobRequest
	ApprovalRecord ApprovalRecord
	SafetyRecord   model.SafetyDecisionRecord
}

type ApprovalRepairKind string

const (
	ApprovalRepairNone                    ApprovalRepairKind = "none"
	ApprovalRepairReplayPendingPublish    ApprovalRepairKind = "replay_pending_publish"
	ApprovalRepairApplyApprovedResolution ApprovalRepairKind = "apply_approved_resolution"
	ApprovalRepairApplyRejectedResolution ApprovalRepairKind = "apply_rejected_resolution"
	ApprovalRepairInvalidateTerminalRun   ApprovalRepairKind = "invalidate_terminal_run"
	ApprovalRepairInvalidateStaleRequest  ApprovalRepairKind = "invalidate_stale_request"
	ApprovalRepairInvalidateStaleSnapshot ApprovalRepairKind = "invalidate_stale_snapshot"
)

type ApprovalRepairClassifyOptions struct {
	WorkflowTerminal bool
	StaleSnapshot    bool
}

type ApprovalRepairSnapshot struct {
	JobID          string
	State          model.JobState
	TraceID        string
	Topic          string
	RunID          string
	Request        *pb.JobRequest
	RequestHash    string
	SafetyRecord   model.SafetyDecisionRecord
	ApprovalRecord ApprovalRecord
}

type ApprovalRepairPlan struct {
	JobID          string                      `json:"job_id"`
	Kind           ApprovalRepairKind          `json:"kind"`
	Repairable     bool                        `json:"repairable"`
	Reason         string                      `json:"reason,omitempty"`
	CurrentState   model.JobState              `json:"current_state,omitempty"`
	TargetState    model.JobState              `json:"target_state,omitempty"`
	ApprovalStatus model.ApprovalStatus        `json:"approval_status,omitempty"`
	Actionability  model.ApprovalActionability `json:"actionability,omitempty"`
	Decision       model.ApprovalDecision      `json:"decision,omitempty"`
	PublishTarget  model.ApprovalPublishTarget `json:"publish_target,omitempty"`
	Topic          string                      `json:"topic,omitempty"`
	RunID          string                      `json:"run_id,omitempty"`
	RequestHash    string                      `json:"request_hash,omitempty"`
}

type ApprovalRepairApplyParams struct {
	JobID string
	Plan  ApprovalRepairPlan
	Actor string
	Note  string
}

type ApprovalRepairResult struct {
	JobID          string
	TraceID        string
	State          model.JobState
	Request        *pb.JobRequest
	ApprovalRecord ApprovalRecord
	Plan           ApprovalRepairPlan
}

func derivedApprovalStatus(state model.JobState, safety model.SafetyDecisionRecord, record ApprovalRecord) model.ApprovalStatus {
	if record.Status != "" {
		return record.Status
	}
	switch record.Decision {
	case model.ApprovalDecisionApprove:
		return model.ApprovalStatusApproved
	case model.ApprovalDecisionReject:
		return model.ApprovalStatusRejected
	case model.ApprovalDecisionExpire:
		return model.ApprovalStatusExpired
	case model.ApprovalDecisionInvalidate:
		return model.ApprovalStatusInvalidated
	case model.ApprovalDecisionRepair:
		return model.ApprovalStatusRepaired
	}
	switch state {
	case model.JobStateApproval:
		if safety.ApprovalRequired || safety.Decision == model.SafetyRequireApproval {
			return model.ApprovalStatusPending
		}
	case model.JobStateDenied:
		return model.ApprovalStatusRejected
	case model.JobStateTimeout:
		if safety.ApprovalRequired || safety.Decision == model.SafetyRequireApproval {
			return model.ApprovalStatusExpired
		}
	case model.JobStatePending, model.JobStateScheduled, model.JobStateDispatched, model.JobStateRunning,
		model.JobStateSucceeded, model.JobStateFailed, model.JobStateCancelled, model.JobStateQuarantined:
		if record.ApprovedBy != "" || safety.ApprovalRequired || safety.Decision == model.SafetyRequireApproval {
			return model.ApprovalStatusApproved
		}
	}
	if record.ApprovedBy != "" {
		return model.ApprovalStatusApproved
	}
	if safety.ApprovalRequired || safety.Decision == model.SafetyRequireApproval {
		return model.ApprovalStatusPending
	}
	return ""
}

func derivedApprovalDecision(record ApprovalRecord) model.ApprovalDecision {
	if record.Decision != "" {
		return record.Decision
	}
	switch record.Status {
	case model.ApprovalStatusApproved:
		return model.ApprovalDecisionApprove
	case model.ApprovalStatusRejected:
		return model.ApprovalDecisionReject
	case model.ApprovalStatusExpired:
		return model.ApprovalDecisionExpire
	case model.ApprovalStatusInvalidated:
		return model.ApprovalDecisionInvalidate
	case model.ApprovalStatusRepaired:
		return model.ApprovalDecisionRepair
	default:
		return ""
	}
}

func derivedApprovalRevision(safety model.SafetyDecisionRecord, record ApprovalRecord) int64 {
	if record.Revision > 0 {
		return record.Revision
	}
	if safety.ApprovalRevision > 0 {
		return safety.ApprovalRevision
	}
	if safety.ApprovalRequired || safety.Decision == model.SafetyRequireApproval {
		return 1
	}
	return 0
}

// NormalizeApprovalRecord returns a canonical lifecycle view even for legacy
// approvals that only persisted approval_by + job state.
func NormalizeApprovalRecord(state model.JobState, safety model.SafetyDecisionRecord, record ApprovalRecord) ApprovalRecord {
	record.Status = derivedApprovalStatus(state, safety, record)
	record.Decision = derivedApprovalDecision(record)
	if record.Actionability == "" {
		record.Actionability = record.Status.DefaultActionability()
	}
	record.Revision = derivedApprovalRevision(safety, record)
	return record
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

		tenant, _ := tx.HGet(ctx, metaKey, metaFieldTenant).Result()

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

		// Remove from tenant active set — CANCELLED is terminal.
		// No TTL on active set — it self-manages via SAdd/SRem on state transitions.
		if tenant != "" {
			pipe.SRem(ctx, tenantActiveKey(tenant), jobID)
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

		cancelEvtStr, serErr := serializeJobEvent(model.JobStateCancelled, now, nil)
		if serErr != nil {
			return serErr
		}
		pipe.RPush(ctx, jobEventsKey(jobID), cancelEvtStr)

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

	// Idempotency keys must outlive the job lifecycle to prevent
	// duplicate jobs on late retries. Default: 90 days.
	idempotencyTTL := 90 * 24 * time.Hour
	if v := os.Getenv("CORDUM_IDEMPOTENCY_TTL"); v != "" {
		if parsed, err := time.ParseDuration(v); err != nil {
			slog.Warn("invalid CORDUM_IDEMPOTENCY_TTL, using default", "value", v, "default", idempotencyTTL)
		} else if parsed > 0 {
			idempotencyTTL = parsed
		}
	}

	slog.Debug("job store connected", "component", "store", "metaTTL", ttl.String(), "idempotencyTTL", idempotencyTTL.String())
	return &RedisJobStore{client: client, metaTTL: ttl, idempotencyTTL: idempotencyTTL}, nil
}

func (s *RedisJobStore) SetState(ctx context.Context, jobID string, state model.JobState) error {
	return s.SetStateWithContext(ctx, jobID, state, nil)
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

// CountRecentJobsSince returns the number of jobs updated on or after the given
// time using the bounded recent-jobs index.
func (s *RedisJobStore) CountRecentJobsSince(ctx context.Context, since time.Time) (int64, error) {
	count, err := s.client.ZCount(ctx, "job:recent", fmt.Sprintf("%d", since.UTC().UnixMicro()), "+inf").Result()
	if err != nil {
		return 0, fmt.Errorf("job store count recent jobs since %s: %w", since.UTC().Format(time.RFC3339), err)
	}
	return count, nil
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

// ListRecentJobsByTimeRange returns job IDs from the job:recent sorted set whose
// scores fall within [fromMicros, toMicros]. Results are paginated via cursor
// (offset) and limit. Scores are stored as microsecond timestamps.
func (s *RedisJobStore) ListRecentJobsByTimeRange(ctx context.Context, fromMicros, toMicros, cursor, limit int64) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	if cursor < 0 {
		cursor = 0
	}
	members, err := s.client.ZRangeByScore(ctx, "job:recent", &redis.ZRangeBy{
		Min:    fmt.Sprintf("%d", fromMicros),
		Max:    fmt.Sprintf("%d", toMicros),
		Offset: cursor,
		Count:  limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list recent jobs by time range: %w", err)
	}
	return members, nil
}

// GetJobRequests batch-fetches serialized job request payloads for the given IDs
// using a Redis pipeline. Returns raw bytes keyed by job ID; IDs whose keys have
// expired or are missing are silently omitted.
func (s *RedisJobStore) GetJobRequests(ctx context.Context, jobIDs []string) (map[string][]byte, error) {
	if len(jobIDs) == 0 {
		return map[string][]byte{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringCmd, len(jobIDs))
	for _, id := range jobIDs {
		if id == "" {
			continue
		}
		cmds[id] = pipe.Get(ctx, jobRequestKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("job store get job requests pipeline: %w", err)
	}
	out := make(map[string][]byte, len(cmds))
	for id, cmd := range cmds {
		data, err := cmd.Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			slog.Warn("job-store: pipeline GET job request failed", "job_id", id, "error", err)
			continue
		}
		out[id] = data
	}
	return out, nil
}

// GetJobMetas batch-fetches job metadata hashes for the given IDs using a Redis
// pipeline. Returns a map of job ID to field map. Missing or expired keys are
// silently omitted.
func (s *RedisJobStore) GetJobMetas(ctx context.Context, jobIDs []string) (map[string]map[string]string, error) {
	if len(jobIDs) == 0 {
		return map[string]map[string]string{}, nil
	}
	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd, len(jobIDs))
	for _, id := range jobIDs {
		if id == "" {
			continue
		}
		cmds[id] = pipe.HGetAll(ctx, jobMetaKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("job store get job metas pipeline: %w", err)
	}
	out := make(map[string]map[string]string, len(cmds))
	for id, cmd := range cmds {
		fields, err := cmd.Result()
		if err != nil {
			slog.Warn("job-store: pipeline HGETALL job meta failed", "job_id", id, "error", err)
			continue
		}
		if len(fields) == 0 {
			continue
		}
		out[id] = fields
	}
	return out, nil
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
		return nil, fmt.Errorf("job store build records pipeline: %w", err)
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
			WorkerID:       meta[metaFieldWorkerID],
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
			AgentID:        meta[metaFieldAgentID],
			AgentName:      meta[metaFieldAgentName],
			AgentRiskTier:  meta[metaFieldAgentRiskTier],
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
		pipe.HSet(ctx, metaKey, metaFieldDeadline, deadline.UnixMicro())
		pipe.ZAdd(ctx, deadlineIndexKey(), redis.Z{Score: float64(deadline.UnixMicro()), Member: req.GetJobId()})
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

// SetSubmittedBy stores the submitter identity on the job metadata hash.
// The identity string is an opaque composite (e.g. "apikey:abc12345|principal:admin-user")
// used for self-approval prevention.
func (s *RedisJobStore) SetSubmittedBy(ctx context.Context, jobID, identity string) error {
	if jobID == "" || identity == "" {
		return nil
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), metaFieldSubmittedBy, identity).Err()
}

// GetSubmittedBy returns the stored submitter identity for a job, or empty string if not set.
func (s *RedisJobStore) GetSubmittedBy(ctx context.Context, jobID string) (string, error) {
	if jobID == "" {
		return "", nil
	}
	val, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldSubmittedBy).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", fmt.Errorf("get submitted_by for %s: %w", jobID, err)
	}
	return val, nil
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
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldDeadline, deadline.UnixMicro())
	pipe.ZAdd(ctx, deadlineIndexKey(), redis.Z{Score: float64(deadline.UnixMicro()), Member: jobID})
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set deadline %s: %w", jobID, err)
	}
	return nil
}

// ListExpiredDeadlines returns jobs whose deadline has passed.
// The nowUnix parameter is normalized to microsecond precision for consistency
// with deadline scores (which are stored as microseconds).
func (s *RedisJobStore) ListExpiredDeadlines(ctx context.Context, nowUnix int64, limit int64) ([]model.JobRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	nowMicros := normalizeTimestampMicrosUpper(nowUnix)
	members, err := s.client.ZRangeByScoreWithScores(ctx, deadlineIndexKey(), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    fmt.Sprintf("%d", nowMicros),
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

// CountJobsByState returns the current number of jobs indexed for a state.
func (s *RedisJobStore) CountJobsByState(ctx context.Context, state model.JobState) (int64, error) {
	key := stateIndexKey(state)
	if key == "" {
		return 0, fmt.Errorf("unknown state %s", state)
	}
	count, err := s.client.ZCount(ctx, key, "-inf", "+inf").Result()
	if err != nil {
		return 0, fmt.Errorf("job store count jobs by state %s: %w", state, err)
	}
	return count, nil
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

func workerJobsKey(workerID string) string {
	return "worker:jobs:" + workerID
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

// SetAgentInfo persists the resolved agent identity for a job.
func (s *RedisJobStore) SetAgentInfo(ctx context.Context, jobID, agentID, agentName, agentRiskTier string) error {
	if jobID == "" || agentID == "" {
		return nil
	}
	fields := map[string]interface{}{
		metaFieldAgentID: agentID,
	}
	if agentName != "" {
		fields[metaFieldAgentName] = agentName
	}
	if agentRiskTier != "" {
		fields[metaFieldAgentRiskTier] = agentRiskTier
	}
	return s.client.HSet(ctx, jobMetaKey(jobID), fields).Err()
}

// SetWorkerID persists the worker that processed a job and maintains a
// per-worker index for efficient lookups.
func (s *RedisJobStore) SetWorkerID(ctx context.Context, jobID, workerID string) error {
	if jobID == "" || workerID == "" {
		return fmt.Errorf("jobID and workerID required")
	}
	now := nowUnixMicros()
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldWorkerID, workerID)
	wKey := workerJobsKey(workerID)
	pipe.ZAdd(ctx, wKey, redis.Z{Score: float64(now), Member: jobID})
	pipe.ZRemRangeByRank(ctx, wKey, 0, -1001) // keep last 1000
	if s.metaTTL > 0 {
		pipe.Expire(ctx, wKey, s.metaTTL)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("job store set worker_id %s/%s: %w", jobID, workerID, err)
	}
	return nil
}

// ListWorkerJobs returns the most recent jobs processed by a specific worker.
func (s *RedisJobStore) ListWorkerJobs(ctx context.Context, workerID string, limit int64) ([]model.JobRecord, error) {
	if workerID == "" {
		return nil, fmt.Errorf("workerID required")
	}
	if limit <= 0 {
		limit = 20
	}
	members, err := s.client.ZRevRangeWithScores(ctx, workerJobsKey(workerID), 0, limit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("job store list worker jobs %s: %w", workerID, err)
	}
	return s.buildJobRecords(ctx, members)
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

// TrySetIdempotencyKeyScoped checks the legacy idempotency key and scoped key
// using individual Redis commands instead of Lua. This is Redis Cluster safe
// since each command targets a single key (no CROSSSLOT risk).
//
// Minor TOCTOU window: between the legacy GET and scoped SetNX, a concurrent
// request could race. This is acceptable because the job submit path has
// external idempotency via the caller's retry logic.
func (s *RedisJobStore) TrySetIdempotencyKeyScoped(ctx context.Context, tenant, key, jobID string) (bool, string, error) {
	if key == "" || jobID == "" {
		return false, "", fmt.Errorf("idempotency key and jobID required")
	}
	tenant = strings.TrimSpace(tenant)

	idKey := jobIdempotencyKeyScoped(tenant, key)
	if idKey == "" {
		return false, "", fmt.Errorf("idempotency key required")
	}

	// Phase 1: Check legacy (unscoped) key for backward compatibility.
	if tenant != "" {
		legacyKey := jobIdempotencyKey(key)
		legacyID, err := s.client.Get(ctx, legacyKey).Result()
		if err != nil && err != redis.Nil {
			return false, "", fmt.Errorf("job store idempotency legacy check %s: %w", jobID, err)
		}
		if err == nil && legacyID != "" {
			// Verify tenant ownership via job meta.
			metaKey := jobMetaKeyPrefix + legacyID
			metaTenant, tErr := s.client.HGet(ctx, metaKey, "tenant").Result()
			if tErr != nil && tErr != redis.Nil {
				return false, "", fmt.Errorf("job store idempotency tenant check %s: %w", jobID, tErr)
			}
			// Match if: no tenant in meta, empty tenant, or same tenant.
			if metaTenant == "" || metaTenant == tenant {
				return false, legacyID, nil
			}
		}
	}

	// Phase 2: Try to claim the scoped key.
	// Use idempotencyTTL (90d default) instead of metaTTL (7d default)
	// to prevent duplicate jobs on late retries after metaTTL expiry.
	ok, err := s.client.SetNX(ctx, idKey, jobID, s.idempotencyTTL).Result()
	if err != nil {
		return false, "", fmt.Errorf("job store try set idempotency key scoped %s: %w", jobID, err)
	}
	if ok {
		return true, jobID, nil
	}

	// Key already exists — return the existing job ID.
	existing, err := s.client.Get(ctx, idKey).Result()
	if err != nil && err != redis.Nil {
		return false, "", fmt.Errorf("job store try set idempotency key scoped %s: %w", jobID, err)
	}
	return false, existing, nil
}

func (s *RedisJobStore) SetIdempotencyKeyScoped(ctx context.Context, tenant, key, jobID string) error {
	if key == "" || jobID == "" {
		return fmt.Errorf("idempotency key and jobID required")
	}
	idKey := jobIdempotencyKeyScoped(tenant, key)
	if idKey == "" {
		return fmt.Errorf("idempotency key required")
	}
	_, err := s.client.SetNX(ctx, idKey, jobID, s.idempotencyTTL).Result()
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
	approvalStr := ""
	if record.ApprovalRequired {
		approvalStr = "true"
	}
	approvalStatus := record.ApprovalStatus
	approvalActionability := record.Actionability
	approvalRevision := record.ApprovalRevision
	if record.ApprovalRequired {
		if approvalStatus == "" {
			approvalStatus = model.ApprovalStatusPending
		}
		if approvalActionability == "" {
			approvalActionability = approvalStatus.DefaultActionability()
		}
		if approvalRevision <= 0 {
			approvalRevision = 1
		}
	}
	fields := map[string]any{
		metaFieldSafetyDecision:   string(record.Decision),
		metaFieldSafetyReason:     record.Reason,
		metaFieldSafetyRuleID:     record.RuleID,
		metaFieldSafetySnapshot:   record.PolicySnapshot,
		metaFieldSafetyChecked:    record.CheckedAt,
		metaFieldApprovalRequired: approvalStr,
		metaFieldApprovalRef:      record.ApprovalRef,
	}
	if record.JobHash != "" {
		fields[metaFieldSafetyJobHash] = record.JobHash
	}
	if record.ApprovalRequired {
		fields[metaFieldApprovalStatus] = string(approvalStatus)
		fields[metaFieldApprovalActionable] = string(approvalActionability)
		fields[metaFieldApprovalRevision] = approvalRevision
		if record.ApprovalDecision != "" {
			fields[metaFieldApprovalDecision] = string(record.ApprovalDecision)
		}
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
		"approval_status":   approvalStatus,
		"actionability":     approvalActionability,
		"approval_revision": approvalRevision,
		"approval_decision": record.ApprovalDecision,
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

// SetDelegationLineage stores the verified dispatch-time delegation lineage on
// the job metadata hash as a JSON blob.
func (s *RedisJobStore) SetDelegationLineage(ctx context.Context, jobID string, lineage model.DelegationLineage) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	encoded, err := json.Marshal(lineage)
	if err != nil {
		return fmt.Errorf("marshal delegation lineage: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldDelegationLineage, string(encoded))
	if s.metaTTL > 0 {
		pipe.Expire(ctx, jobMetaKey(jobID), s.metaTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("job store set delegation lineage %s: %w", jobID, err)
	}
	return nil
}

// GetDelegationLineage loads the verified dispatch-time delegation lineage from
// the job metadata hash.
func (s *RedisJobStore) GetDelegationLineage(ctx context.Context, jobID string) (model.DelegationLineage, error) {
	if jobID == "" {
		return model.DelegationLineage{}, fmt.Errorf("jobID required")
	}
	raw, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldDelegationLineage).Result()
	if err == redis.Nil || raw == "" {
		return model.DelegationLineage{}, nil
	}
	if err != nil {
		return model.DelegationLineage{}, fmt.Errorf("job store get delegation lineage %s: %w", jobID, err)
	}
	var lineage model.DelegationLineage
	if err := json.Unmarshal([]byte(raw), &lineage); err != nil {
		return model.DelegationLineage{}, fmt.Errorf("decode delegation lineage %s: %w", jobID, err)
	}
	return lineage, nil
}

// SetDelegationDispatchToken stores the raw delegation token required for
// dispatch-time re-verification. It is intentionally persisted separately from
// the public job request snapshot.
func (s *RedisJobStore) SetDelegationDispatchToken(ctx context.Context, jobID string, token model.DelegationDispatchToken) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	if strings.TrimSpace(token.Token) == "" {
		return nil
	}
	token.Token = strings.TrimSpace(token.Token)
	token.Audience = strings.TrimSpace(token.Audience)
	encoded, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal delegation dispatch token: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, jobMetaKey(jobID), metaFieldDelegationDispatch, string(encoded))
	if s.metaTTL > 0 {
		pipe.Expire(ctx, jobMetaKey(jobID), s.metaTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("job store set delegation dispatch token %s: %w", jobID, err)
	}
	return nil
}

// ClearDelegationDispatchToken deletes the raw delegation bearer token from
// the job metadata hash. Callers MUST invoke this as soon as dispatch-time
// re-verification completes (successfully or with a fail-closed decision)
// so the raw token does not sit in the 7-day job-metadata TTL where it
// could be recovered via admin tooling, backups, or operator access. The
// persisted DelegationLineage on the job record continues to carry the
// non-sensitive chain metadata (JTI, issuer chain, scope) for audit and
// read-side APIs after this wipe.
func (s *RedisJobStore) ClearDelegationDispatchToken(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	if _, err := s.client.HDel(ctx, jobMetaKey(jobID), metaFieldDelegationDispatch).Result(); err != nil {
		return fmt.Errorf("job store clear delegation dispatch token %s: %w", jobID, err)
	}
	return nil
}

// GetDelegationDispatchToken loads the raw delegation token stored for
// dispatch-time re-verification.
func (s *RedisJobStore) GetDelegationDispatchToken(ctx context.Context, jobID string) (model.DelegationDispatchToken, error) {
	if jobID == "" {
		return model.DelegationDispatchToken{}, fmt.Errorf("jobID required")
	}
	raw, err := s.client.HGet(ctx, jobMetaKey(jobID), metaFieldDelegationDispatch).Result()
	if err == redis.Nil || raw == "" {
		return model.DelegationDispatchToken{}, nil
	}
	if err != nil {
		return model.DelegationDispatchToken{}, fmt.Errorf("job store get delegation dispatch token %s: %w", jobID, err)
	}
	var token model.DelegationDispatchToken
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return model.DelegationDispatchToken{}, fmt.Errorf("decode delegation dispatch token %s: %w", jobID, err)
	}
	return token, nil
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
	if record.Status != "" && record.Actionability == "" {
		record.Actionability = record.Status.DefaultActionability()
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
	if record.Status != "" {
		fields[metaFieldApprovalStatus] = string(record.Status)
	}
	if record.Actionability != "" {
		fields[metaFieldApprovalActionable] = string(record.Actionability)
	}
	if record.Revision > 0 {
		fields[metaFieldApprovalRevision] = record.Revision
	}
	if record.Decision != "" {
		fields[metaFieldApprovalDecision] = string(record.Decision)
	}
	if record.PublishStatus != "" {
		fields[metaFieldApprovalPublishStatus] = string(record.PublishStatus)
	}
	if record.PublishTarget != "" {
		fields[metaFieldApprovalPublishTarget] = string(record.PublishTarget)
	}
	if record.PublishedAt > 0 {
		fields[metaFieldApprovalPublishedAt] = record.PublishedAt
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
		Status:         model.ApprovalStatus(data[metaFieldApprovalStatus]),
		Actionability:  model.ApprovalActionability(data[metaFieldApprovalActionable]),
		Decision:       model.ApprovalDecision(data[metaFieldApprovalDecision]),
		PublishStatus:  model.ApprovalPublishStatus(data[metaFieldApprovalPublishStatus]),
		PublishTarget:  model.ApprovalPublishTarget(data[metaFieldApprovalPublishTarget]),
	}
	if raw := data[metaFieldApprovalAt]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.ApprovedAt = parsed
		}
	}
	if raw := data[metaFieldApprovalRevision]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.Revision = parsed
		}
	}
	if raw := data[metaFieldApprovalPublishedAt]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.PublishedAt = parsed
		}
	}
	return record, nil
}

// MarkApprovalPublishComplete marks the durable approval side-effect intent as
// published for the expected approval revision.
func (s *RedisJobStore) MarkApprovalPublishComplete(ctx context.Context, jobID string, revision int64, target model.ApprovalPublishTarget) error {
	if jobID == "" {
		return fmt.Errorf("jobID required")
	}
	metaKey := jobMetaKey(jobID)
	return s.client.Watch(ctx, func(tx *redis.Tx) error {
		meta, err := tx.HMGet(ctx, metaKey,
			metaFieldApprovalRevision,
			metaFieldApprovalPublishStatus,
			metaFieldApprovalPublishTarget,
		).Result()
		if err != nil {
			return fmt.Errorf("job store mark approval publish complete %s: %w", jobID, err)
		}

		currentRevision := int64(0)
		if raw, ok := meta[0].(string); ok && raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				currentRevision = parsed
			}
		}
		if revision > 0 && currentRevision > 0 && currentRevision != revision {
			return nil
		}

		currentStatus, _ := meta[1].(string)
		currentTarget, _ := meta[2].(string)
		if currentStatus == string(model.ApprovalPublishPublished) {
			return nil
		}
		if target != "" && currentTarget != "" && currentTarget != string(target) {
			return nil
		}
		if strings.TrimSpace(currentTarget) == "" {
			return nil
		}

		now := nowUnixMicros()
		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, map[string]any{
			metaFieldApprovalPublishStatus: string(model.ApprovalPublishPublished),
			metaFieldApprovalPublishedAt:   now,
		})
		if s.metaTTL > 0 {
			pipe.Expire(ctx, metaKey, s.metaTTL)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("job store mark approval publish complete %s exec: %w", jobID, err)
		}
		return nil
	}, metaKey)
}

func (s *RedisJobStore) InspectApprovalRepair(ctx context.Context, jobID string) (*ApprovalRepairSnapshot, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID required")
	}
	state, err := s.GetState(ctx, jobID)
	if err != nil {
		return nil, err
	}
	req, err := s.GetJobRequest(ctx, jobID)
	if err != nil {
		return nil, err
	}
	safetyRecord, err := s.GetSafetyDecision(ctx, jobID)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	approvalRecord, err := s.GetApprovalRecord(ctx, jobID)
	if err != nil && err != redis.Nil {
		return nil, err
	}
	approvalRecord = NormalizeApprovalRecord(state, safetyRecord, approvalRecord)
	requestHash := ""
	if req != nil {
		if hash, err := hashApprovalJobRequest(req); err == nil {
			requestHash = hash
		}
	}
	traceID, _ := s.GetTraceID(ctx, jobID)
	topic := ""
	runID := ""
	if req != nil {
		topic = strings.TrimSpace(req.GetTopic())
		if req.Labels != nil {
			runID = strings.TrimSpace(req.Labels["run_id"])
		}
	}
	return &ApprovalRepairSnapshot{
		JobID:          jobID,
		State:          state,
		TraceID:        traceID,
		Topic:          topic,
		RunID:          runID,
		Request:        req,
		RequestHash:    requestHash,
		SafetyRecord:   safetyRecord,
		ApprovalRecord: approvalRecord,
	}, nil
}

func ClassifyApprovalRepair(snapshot ApprovalRepairSnapshot, opts ApprovalRepairClassifyOptions) ApprovalRepairPlan {
	plan := ApprovalRepairPlan{
		JobID:        snapshot.JobID,
		Kind:         ApprovalRepairNone,
		CurrentState: snapshot.State,
		Topic:        snapshot.Topic,
		RunID:        snapshot.RunID,
		RequestHash:  snapshot.RequestHash,
	}
	approval := snapshot.ApprovalRecord

	if snapshot.State != model.JobStateApproval && approval.HasPendingPublish() {
		plan.Kind = ApprovalRepairReplayPendingPublish
		plan.Repairable = true
		plan.Reason = "approval decision committed but publish intent is still pending"
		plan.TargetState = snapshot.State
		plan.ApprovalStatus = approval.Status
		plan.Actionability = approval.Actionability
		plan.Decision = approval.Decision
		plan.PublishTarget = approval.PublishTarget
		return plan
	}

	if snapshot.State != model.JobStateApproval {
		return plan
	}

	isApprovedResolution := approval.Decision == model.ApprovalDecisionApprove || approval.Status == model.ApprovalStatusApproved
	if !isApprovedResolution && snapshot.Request != nil && snapshot.Request.Labels != nil {
		isApprovedResolution = strings.EqualFold(strings.TrimSpace(snapshot.Request.Labels["approval_granted"]), "true")
	}
	if isApprovedResolution {
		plan.Kind = ApprovalRepairApplyApprovedResolution
		plan.Repairable = true
		plan.Reason = "approval was already resolved as approved while the job remained awaiting approval"
		plan.TargetState = model.JobStatePending
		plan.ApprovalStatus = model.ApprovalStatusApproved
		plan.Actionability = model.ApprovalStatusApproved.DefaultActionability()
		plan.Decision = model.ApprovalDecisionApprove
		plan.PublishTarget = model.ApprovalPublishTargetSubmit
		return plan
	}

	isRejectedResolution := approval.Decision == model.ApprovalDecisionReject || approval.Status == model.ApprovalStatusRejected
	if isRejectedResolution {
		plan.Kind = ApprovalRepairApplyRejectedResolution
		plan.Repairable = true
		plan.Reason = "approval was already resolved as rejected while the job remained awaiting approval"
		plan.TargetState = model.JobStateDenied
		plan.ApprovalStatus = model.ApprovalStatusRejected
		plan.Actionability = model.ApprovalStatusRejected.DefaultActionability()
		plan.Decision = model.ApprovalDecisionReject
		plan.PublishTarget = model.ApprovalPublishTargetDLQ
		if snapshot.Topic == capsdk.SubjectWorkflowApprovalGate {
			plan.PublishTarget = model.ApprovalPublishTargetDLQAndResult
		}
		return plan
	}

	if opts.WorkflowTerminal {
		plan.Kind = ApprovalRepairInvalidateTerminalRun
		plan.Repairable = true
		plan.Reason = "workflow run already reached a terminal state; approval is no longer valid"
		plan.TargetState = model.JobStateDenied
		plan.ApprovalStatus = model.ApprovalStatusInvalidated
		plan.Actionability = model.ApprovalStatusInvalidated.DefaultActionability()
		plan.Decision = model.ApprovalDecisionInvalidate
		return plan
	}

	if opts.StaleSnapshot {
		plan.Kind = ApprovalRepairInvalidateStaleSnapshot
		plan.Repairable = true
		plan.Reason = "policy snapshot changed since the approval request was created"
		plan.TargetState = model.JobStateDenied
		plan.ApprovalStatus = model.ApprovalStatusInvalidated
		plan.Actionability = model.ApprovalStatusInvalidated.DefaultActionability()
		plan.Decision = model.ApprovalDecisionInvalidate
		return plan
	}

	if snapshot.SafetyRecord.JobHash != "" && snapshot.RequestHash != "" && snapshot.RequestHash != snapshot.SafetyRecord.JobHash {
		plan.Kind = ApprovalRepairInvalidateStaleRequest
		plan.Repairable = true
		plan.Reason = "job request changed since the approval request was created"
		plan.TargetState = model.JobStateDenied
		plan.ApprovalStatus = model.ApprovalStatusInvalidated
		plan.Actionability = model.ApprovalStatusInvalidated.DefaultActionability()
		plan.Decision = model.ApprovalDecisionInvalidate
		return plan
	}

	return plan
}

func (s *RedisJobStore) ApplyApprovalRepair(ctx context.Context, params ApprovalRepairApplyParams) (*ApprovalRepairResult, error) {
	if strings.TrimSpace(params.JobID) == "" {
		return nil, fmt.Errorf("jobID required")
	}
	if params.Plan.Kind == ApprovalRepairNone {
		return nil, fmt.Errorf("approval repair plan required")
	}
	jobID := strings.TrimSpace(params.JobID)
	metaKey := jobMetaKey(jobID)
	stateKey := jobStateKey(jobID)
	reqKey := jobRequestKey(jobID)

	var result ApprovalRepairResult
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		meta, err := tx.HGetAll(ctx, metaKey).Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("job store apply approval repair %s: %w", jobID, err)
		}
		stateValue := meta["state"]
		if stateValue == "" {
			stateValue, err = tx.Get(ctx, stateKey).Result()
			if err == redis.Nil {
				return redis.Nil
			}
			if err != nil {
				return fmt.Errorf("job store apply approval repair %s state: %w", jobID, err)
			}
		}
		currentState := model.JobState(stateValue)
		if params.Plan.CurrentState != "" && currentState != params.Plan.CurrentState {
			return fmt.Errorf("approval repair state changed from %s to %s", params.Plan.CurrentState, currentState)
		}

		reqBytes, err := tx.Get(ctx, reqKey).Bytes()
		if err == redis.Nil {
			reqBytes = nil
		} else if err != nil {
			return fmt.Errorf("job store apply approval repair %s request: %w", jobID, err)
		}
		var req *pb.JobRequest
		if len(reqBytes) > 0 {
			var decoded pb.JobRequest
			if err := protojson.Unmarshal(reqBytes, &decoded); err != nil {
				return fmt.Errorf("unmarshal job request: %w", err)
			}
			req = &decoded
		}

		safetyRecord := model.SafetyDecisionRecord{
			Decision:         model.SafetyDecision(meta[metaFieldSafetyDecision]),
			Reason:           meta[metaFieldSafetyReason],
			RuleID:           meta[metaFieldSafetyRuleID],
			PolicySnapshot:   meta[metaFieldSafetySnapshot],
			ApprovalRequired: meta[metaFieldApprovalRequired] == "true",
			ApprovalRef:      meta[metaFieldApprovalRef],
			JobHash:          meta[metaFieldSafetyJobHash],
			ApprovalStatus:   model.ApprovalStatus(meta[metaFieldApprovalStatus]),
			Actionability:    model.ApprovalActionability(meta[metaFieldApprovalActionable]),
			ApprovalDecision: model.ApprovalDecision(meta[metaFieldApprovalDecision]),
		}
		if raw := meta[metaFieldApprovalRevision]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				safetyRecord.ApprovalRevision = parsed
			}
		}

		approvalRecord := ApprovalRecord{
			ApprovedBy:     meta[metaFieldApprovalBy],
			ApprovedRole:   meta[metaFieldApprovalRole],
			Reason:         meta[metaFieldApprovalReason],
			Note:           meta[metaFieldApprovalNote],
			PolicySnapshot: meta[metaFieldApprovalSnapshot],
			JobHash:        meta[metaFieldApprovalJobHash],
			Status:         model.ApprovalStatus(meta[metaFieldApprovalStatus]),
			Actionability:  model.ApprovalActionability(meta[metaFieldApprovalActionable]),
			Decision:       model.ApprovalDecision(meta[metaFieldApprovalDecision]),
			PublishStatus:  model.ApprovalPublishStatus(meta[metaFieldApprovalPublishStatus]),
			PublishTarget:  model.ApprovalPublishTarget(meta[metaFieldApprovalPublishTarget]),
		}
		if raw := meta[metaFieldApprovalAt]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.ApprovedAt = parsed
			}
		}
		if raw := meta[metaFieldApprovalRevision]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.Revision = parsed
			}
		}
		if raw := meta[metaFieldApprovalPublishedAt]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.PublishedAt = parsed
			}
		}
		approvalRecord = NormalizeApprovalRecord(currentState, safetyRecord, approvalRecord)

		if params.Plan.Kind == ApprovalRepairReplayPendingPublish {
			result = ApprovalRepairResult{
				JobID:          jobID,
				TraceID:        meta[metaFieldTraceID],
				State:          currentState,
				Request:        req,
				ApprovalRecord: approvalRecord,
				Plan:           params.Plan,
			}
			return nil
		}

		now := nowUnixMicros()
		revision := approvalRecord.Revision + 1
		if revision <= 0 {
			revision = 1
		}
		approvalStatus := params.Plan.ApprovalStatus
		if approvalStatus == "" {
			approvalStatus = approvalRecord.Status
		}
		actionability := params.Plan.Actionability
		if actionability == "" {
			actionability = approvalStatus.DefaultActionability()
		}
		decision := params.Plan.Decision
		if decision == "" {
			decision = approvalRecord.Decision
		}
		publishTarget := params.Plan.PublishTarget
		publishStatus := model.ApprovalPublishStatus("")
		if publishTarget != "" {
			publishStatus = model.ApprovalPublishPending
		}
		approvedBy := strings.TrimSpace(approvalRecord.ApprovedBy)
		approvedRole := strings.TrimSpace(approvalRecord.ApprovedRole)
		approvedAt := approvalRecord.ApprovedAt
		if approvedAt == 0 {
			approvedAt = now
		}
		if approvedBy == "" {
			if actor := strings.TrimSpace(params.Actor); actor != "" {
				approvedBy = actor
				if approvedRole == "" {
					approvedRole = "system"
				}
			} else {
				approvedBy = "system/repair"
				if approvedRole == "" {
					approvedRole = "system"
				}
			}
		}
		reason := strings.TrimSpace(approvalRecord.Reason)
		if reason == "" && decision == model.ApprovalDecisionInvalidate {
			reason = strings.TrimSpace(params.Plan.Reason)
		}
		note := strings.TrimSpace(approvalRecord.Note)
		repairNote := strings.TrimSpace(params.Note)
		if repairNote == "" {
			repairNote = strings.TrimSpace(params.Plan.Reason)
		}
		if repairNote != "" {
			repairEntry := "repair: " + repairNote
			if note == "" {
				note = repairEntry
			} else if !strings.Contains(note, repairEntry) {
				note = note + "\n" + repairEntry
			}
		}
		policySnapshot := strings.TrimSpace(approvalRecord.PolicySnapshot)
		if policySnapshot == "" {
			policySnapshot = strings.TrimSpace(safetyRecord.PolicySnapshot)
		}
		jobHash := strings.TrimSpace(approvalRecord.JobHash)
		if jobHash == "" {
			jobHash = strings.TrimSpace(safetyRecord.JobHash)
		}

		if req != nil {
			if req.Labels == nil {
				req.Labels = map[string]string{}
			}
			if params.Plan.Kind == ApprovalRepairApplyApprovedResolution {
				req.Labels["approval_granted"] = "true"
				req.Labels[bus.LabelBusMsgID] = "approval:" + jobID
				if reason != "" {
					req.Labels["approval_reason"] = reason
				}
				if note != "" {
					req.Labels["approval_note"] = note
				}
			}
		}

		fields := map[string]any{
			"state":                     string(params.Plan.TargetState),
			"updated_at":                now,
			metaFieldApprovalBy:         approvedBy,
			metaFieldApprovalRole:       approvedRole,
			metaFieldApprovalAt:         approvedAt,
			metaFieldApprovalReason:     reason,
			metaFieldApprovalNote:       note,
			metaFieldApprovalSnapshot:   policySnapshot,
			metaFieldApprovalJobHash:    jobHash,
			metaFieldApprovalStatus:     string(approvalStatus),
			metaFieldApprovalActionable: string(actionability),
			metaFieldApprovalRevision:   revision,
			metaFieldApprovalDecision:   string(decision),
		}
		if publishStatus != "" {
			fields[metaFieldApprovalPublishStatus] = string(publishStatus)
			fields[metaFieldApprovalPublishTarget] = string(publishTarget)
		}
		if req != nil && len(req.Labels) > 0 {
			if labelsJSON, err := json.Marshal(req.Labels); err == nil {
				fields[metaFieldLabels] = string(labelsJSON)
			}
		}

		attempts := 0
		if raw := meta[metaFieldAttempts]; raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
				attempts = parsed
			}
		}
		fields[metaFieldAttempts] = attempts
		reqPayload := []byte(nil)
		if req != nil {
			reqPayload, err = protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(req)
			if err != nil {
				return fmt.Errorf("marshal repaired job request: %w", err)
			}
		}

		tenant := meta[metaFieldTenant]
		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, fields)
		if publishStatus == "" {
			pipe.HDel(ctx, metaKey,
				metaFieldApprovalPublishStatus,
				metaFieldApprovalPublishTarget,
				metaFieldApprovalPublishedAt,
			)
		} else {
			pipe.HDel(ctx, metaKey, metaFieldApprovalPublishedAt)
		}
		if reqPayload != nil {
			if s.metaTTL > 0 {
				pipe.Set(ctx, reqKey, reqPayload, s.metaTTL)
			} else {
				pipe.Set(ctx, reqKey, reqPayload, 0)
			}
		}
		pipe.Set(ctx, stateKey, string(params.Plan.TargetState), 0)
		if prevIdx := stateIndexKey(currentState); prevIdx != "" && currentState != params.Plan.TargetState {
			pipe.ZRem(ctx, prevIdx, jobID)
		}
		if idx := stateIndexKey(params.Plan.TargetState); idx != "" {
			pipe.ZAdd(ctx, idx, redis.Z{Score: float64(now), Member: jobID})
		}
		if tenant != "" {
			activeKey := tenantActiveKey(tenant)
			if isActiveState(params.Plan.TargetState) {
				pipe.SAdd(ctx, activeKey, jobID)
			} else if terminalStates[params.Plan.TargetState] {
				pipe.SRem(ctx, activeKey, jobID)
			}
		}
		pipe.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID})
		pipe.ZRemRangeByRank(ctx, "job:recent", 0, -1001)
		if s.metaTTL > 0 {
			pipe.Expire(ctx, metaKey, s.metaTTL)
			pipe.Expire(ctx, stateKey, s.metaTTL)
			pipe.Expire(ctx, jobResultPtrKey(jobID), s.metaTTL)
		}
		pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, params.Plan.TargetState))
		if terminalStates[params.Plan.TargetState] {
			pipe.ZRem(ctx, deadlineIndexKey(), jobID)
			pipe.HDel(ctx, metaKey, metaFieldDeadline)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("job store apply approval repair %s exec: %w", jobID, err)
		}

		result = ApprovalRepairResult{
			JobID:   jobID,
			TraceID: meta[metaFieldTraceID],
			State:   params.Plan.TargetState,
			Request: req,
			ApprovalRecord: ApprovalRecord{
				ApprovedBy:     approvedBy,
				ApprovedRole:   approvedRole,
				ApprovedAt:     approvedAt,
				Reason:         reason,
				Note:           note,
				PolicySnapshot: policySnapshot,
				JobHash:        jobHash,
				Status:         approvalStatus,
				Actionability:  actionability,
				Revision:       revision,
				Decision:       decision,
				PublishStatus:  publishStatus,
				PublishTarget:  publishTarget,
			},
			Plan: params.Plan,
		}
		return nil
	}, metaKey, stateKey, reqKey)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ResolveApproval atomically persists the approval decision, request-label
// mutations, and state transition in a single Redis WATCH/TX block.
func (s *RedisJobStore) ResolveApproval(ctx context.Context, params ApprovalResolutionParams) (*ApprovalResolutionResult, error) {
	if strings.TrimSpace(params.JobID) == "" {
		return nil, fmt.Errorf("jobID required")
	}
	if params.Decision == "" {
		return nil, fmt.Errorf("approval decision required")
	}
	if params.ResultState == "" {
		return nil, fmt.Errorf("approval result state required")
	}
	jobID := strings.TrimSpace(params.JobID)
	metaKey := jobMetaKey(jobID)
	stateKey := jobStateKey(jobID)
	reqKey := jobRequestKey(jobID)

	var result ApprovalResolutionResult
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		meta, err := tx.HGetAll(ctx, metaKey).Result()
		if err != nil && err != redis.Nil {
			return fmt.Errorf("job store resolve approval %s: %w", jobID, err)
		}
		stateValue := meta["state"]
		if stateValue == "" {
			stateValue, err = tx.Get(ctx, stateKey).Result()
			if err == redis.Nil {
				return redis.Nil
			}
			if err != nil {
				return fmt.Errorf("job store resolve approval %s state: %w", jobID, err)
			}
		}
		currentState := model.JobState(stateValue)

		safetyRecord := model.SafetyDecisionRecord{
			Decision:         model.SafetyDecision(meta[metaFieldSafetyDecision]),
			Reason:           meta[metaFieldSafetyReason],
			RuleID:           meta[metaFieldSafetyRuleID],
			PolicySnapshot:   meta[metaFieldSafetySnapshot],
			ApprovalRequired: meta[metaFieldApprovalRequired] == "true",
			ApprovalRef:      meta[metaFieldApprovalRef],
			JobHash:          meta[metaFieldSafetyJobHash],
			ApprovalStatus:   model.ApprovalStatus(meta[metaFieldApprovalStatus]),
			Actionability:    model.ApprovalActionability(meta[metaFieldApprovalActionable]),
			ApprovalDecision: model.ApprovalDecision(meta[metaFieldApprovalDecision]),
		}
		if raw := meta[metaFieldApprovalRevision]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				safetyRecord.ApprovalRevision = parsed
			}
		}
		approvalRecord := ApprovalRecord{
			ApprovedBy:     meta[metaFieldApprovalBy],
			ApprovedRole:   meta[metaFieldApprovalRole],
			Reason:         meta[metaFieldApprovalReason],
			Note:           meta[metaFieldApprovalNote],
			PolicySnapshot: meta[metaFieldApprovalSnapshot],
			JobHash:        meta[metaFieldApprovalJobHash],
			Status:         model.ApprovalStatus(meta[metaFieldApprovalStatus]),
			Actionability:  model.ApprovalActionability(meta[metaFieldApprovalActionable]),
			Decision:       model.ApprovalDecision(meta[metaFieldApprovalDecision]),
			PublishStatus:  model.ApprovalPublishStatus(meta[metaFieldApprovalPublishStatus]),
			PublishTarget:  model.ApprovalPublishTarget(meta[metaFieldApprovalPublishTarget]),
		}
		if raw := meta[metaFieldApprovalAt]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.ApprovedAt = parsed
			}
		}
		if raw := meta[metaFieldApprovalRevision]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.Revision = parsed
			}
		}
		if raw := meta[metaFieldApprovalPublishedAt]; raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				approvalRecord.PublishedAt = parsed
			}
		}
		approvalRecord = NormalizeApprovalRecord(currentState, safetyRecord, approvalRecord)

		if currentState != model.JobStateApproval {
			conflict := &ApprovalConflictError{
				Code:    model.ApprovalConflictNotActionable,
				Message: "job not awaiting approval",
			}
			switch approvalRecord.Status {
			case model.ApprovalStatusApproved, model.ApprovalStatusRejected:
				conflict.Code = model.ApprovalConflictAlreadyResolved
				conflict.Message = "approval already resolved"
			case model.ApprovalStatusExpired:
				conflict.Message = "approval expired"
			case model.ApprovalStatusInvalidated:
				conflict.Message = "approval invalidated"
			case model.ApprovalStatusRepaired:
				conflict.Message = "approval already repaired"
			}
			return conflict
		}
		if approvalRecord.Actionability != "" && approvalRecord.Actionability != model.ApprovalActionabilityActionable {
			conflict := &ApprovalConflictError{
				Code:    model.ApprovalConflictNotActionable,
				Message: "approval is no longer actionable",
			}
			if approvalRecord.Status == model.ApprovalStatusApproved || approvalRecord.Status == model.ApprovalStatusRejected {
				conflict.Code = model.ApprovalConflictAlreadyResolved
				conflict.Message = "approval already resolved"
			}
			return conflict
		}

		reqBytes, err := tx.Get(ctx, reqKey).Bytes()
		if err == redis.Nil {
			return fmt.Errorf("job request not found")
		}
		if err != nil {
			return fmt.Errorf("job store resolve approval %s request: %w", jobID, err)
		}
		var req pb.JobRequest
		if err := protojson.Unmarshal(reqBytes, &req); err != nil {
			return fmt.Errorf("unmarshal job request: %w", err)
		}
		if params.Decision == model.ApprovalDecisionApprove {
			hash, err := hashApprovalJobRequest(&req)
			if err != nil {
				return fmt.Errorf("hash approval request: %w", err)
			}
			if safetyRecord.JobHash == "" || hash != safetyRecord.JobHash {
				return &ApprovalConflictError{
					Code:    model.ApprovalConflictStaleRequest,
					Message: "job request changed; approval no longer valid",
				}
			}
		}
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		for key, value := range params.LabelUpdates {
			if strings.TrimSpace(value) == "" {
				delete(req.Labels, key)
				continue
			}
			req.Labels[key] = value
		}
		reqPayload, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(&req)
		if err != nil {
			return fmt.Errorf("marshal resolved job request: %w", err)
		}

		now := nowUnixMicros()
		revision := approvalRecord.Revision + 1
		if revision <= 1 {
			revision = 2
		}
		status := model.ApprovalStatusApproved
		switch params.Decision {
		case model.ApprovalDecisionReject:
			status = model.ApprovalStatusRejected
		case model.ApprovalDecisionExpire:
			status = model.ApprovalStatusExpired
		case model.ApprovalDecisionInvalidate:
			status = model.ApprovalStatusInvalidated
		case model.ApprovalDecisionRepair:
			status = model.ApprovalStatusRepaired
		}
		publishTarget := params.PublishTarget
		switch params.Decision {
		case model.ApprovalDecisionApprove:
			if publishTarget == "" {
				publishTarget = model.ApprovalPublishTargetSubmit
			}
		case model.ApprovalDecisionReject:
			if publishTarget == "" {
				publishTarget = model.ApprovalPublishTargetDLQ
			}
		}
		publishStatus := model.ApprovalPublishStatus("")
		if publishTarget != "" {
			publishStatus = model.ApprovalPublishPending
		}
		resolvedRecord := ApprovalRecord{
			ApprovedBy:     strings.TrimSpace(params.ApprovedBy),
			ApprovedRole:   strings.TrimSpace(params.ApprovedRole),
			ApprovedAt:     now,
			Reason:         strings.TrimSpace(params.Reason),
			Note:           strings.TrimSpace(params.Note),
			PolicySnapshot: strings.TrimSpace(params.PolicySnapshot),
			JobHash:        safetyRecord.JobHash,
			Status:         status,
			Actionability:  status.DefaultActionability(),
			Revision:       revision,
			Decision:       params.Decision,
			PublishStatus:  publishStatus,
			PublishTarget:  publishTarget,
		}
		if resolvedRecord.PolicySnapshot == "" {
			resolvedRecord.PolicySnapshot = safetyRecord.PolicySnapshot
		}

		attempts := 0
		if raw := meta[metaFieldAttempts]; raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
				attempts = parsed
			}
		}
		tenant := meta[metaFieldTenant]
		fields := map[string]any{
			"state":                     string(params.ResultState),
			"updated_at":                now,
			metaFieldAttempts:           attempts,
			metaFieldApprovalBy:         resolvedRecord.ApprovedBy,
			metaFieldApprovalRole:       resolvedRecord.ApprovedRole,
			metaFieldApprovalAt:         resolvedRecord.ApprovedAt,
			metaFieldApprovalReason:     resolvedRecord.Reason,
			metaFieldApprovalNote:       resolvedRecord.Note,
			metaFieldApprovalSnapshot:   resolvedRecord.PolicySnapshot,
			metaFieldApprovalJobHash:    resolvedRecord.JobHash,
			metaFieldApprovalStatus:     string(resolvedRecord.Status),
			metaFieldApprovalActionable: string(resolvedRecord.Actionability),
			metaFieldApprovalRevision:   revision,
			metaFieldApprovalDecision:   string(resolvedRecord.Decision),
		}
		if resolvedRecord.PublishStatus != "" {
			fields[metaFieldApprovalPublishStatus] = string(resolvedRecord.PublishStatus)
		}
		if resolvedRecord.PublishTarget != "" {
			fields[metaFieldApprovalPublishTarget] = string(resolvedRecord.PublishTarget)
		}
		if len(req.Labels) > 0 {
			if labelsJSON, err := json.Marshal(req.Labels); err == nil {
				fields[metaFieldLabels] = string(labelsJSON)
			}
		}

		pipe := tx.TxPipeline()
		pipe.HSet(ctx, metaKey, fields)
		if s.metaTTL > 0 {
			pipe.Expire(ctx, metaKey, s.metaTTL)
		}
		if s.metaTTL > 0 {
			pipe.Set(ctx, reqKey, reqPayload, s.metaTTL)
		} else {
			pipe.Set(ctx, reqKey, reqPayload, 0)
		}
		pipe.Set(ctx, stateKey, string(params.ResultState), 0)

		if prevIdx := stateIndexKey(currentState); prevIdx != "" {
			pipe.ZRem(ctx, prevIdx, jobID)
		}
		if idx := stateIndexKey(params.ResultState); idx != "" {
			pipe.ZAdd(ctx, idx, redis.Z{Score: float64(now), Member: jobID})
		}

		if tenant != "" {
			activeKey := tenantActiveKey(tenant)
			if isActiveState(params.ResultState) {
				pipe.SAdd(ctx, activeKey, jobID)
			} else if terminalStates[params.ResultState] {
				pipe.SRem(ctx, activeKey, jobID)
			}
		}

		pipe.ZAdd(ctx, "job:recent", redis.Z{Score: float64(now), Member: jobID})
		pipe.ZRemRangeByRank(ctx, "job:recent", 0, -1001)
		if s.metaTTL > 0 {
			pipe.Expire(ctx, stateKey, s.metaTTL)
			pipe.Expire(ctx, jobResultPtrKey(jobID), s.metaTTL)
		}
		pipe.RPush(ctx, jobEventsKey(jobID), fmt.Sprintf("%d|%s", now, params.ResultState))
		if terminalStates[params.ResultState] {
			pipe.ZRem(ctx, deadlineIndexKey(), jobID)
			pipe.HDel(ctx, metaKey, metaFieldDeadline)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			if errors.Is(err, redis.TxFailedErr) {
				return &ApprovalConflictError{
					Code:    model.ApprovalConflictRetryableLock,
					Message: "concurrent approval conflict; retry",
				}
			}
			return fmt.Errorf("job store resolve approval %s exec: %w", jobID, err)
		}

		result = ApprovalResolutionResult{
			JobID:          jobID,
			TraceID:        meta[metaFieldTraceID],
			State:          params.ResultState,
			Request:        &req,
			ApprovalRecord: resolvedRecord,
			SafetyRecord:   safetyRecord,
		}
		return nil
	}, metaKey, stateKey, reqKey)
	if err != nil {
		if err == redis.Nil {
			return nil, err
		}
		var conflict *ApprovalConflictError
		if errors.As(err, &conflict) {
			return nil, conflict
		}
		return nil, err
	}
	return &result, nil
}

func (s *RedisJobStore) GetSafetyDecision(ctx context.Context, jobID string) (model.SafetyDecisionRecord, error) {
	meta := jobMetaKey(jobID)
	data, err := s.client.HGetAll(ctx, meta).Result()
	if err != nil && err != redis.Nil {
		return model.SafetyDecisionRecord{}, fmt.Errorf("job store get safety decision %s: %w", jobID, err)
	}
	record := model.SafetyDecisionRecord{
		Decision:         model.SafetyDecision(data[metaFieldSafetyDecision]),
		Reason:           data[metaFieldSafetyReason],
		RuleID:           data[metaFieldSafetyRuleID],
		PolicySnapshot:   data[metaFieldSafetySnapshot],
		ApprovalRef:      data[metaFieldApprovalRef],
		JobHash:          data[metaFieldSafetyJobHash],
		ApprovalStatus:   model.ApprovalStatus(data[metaFieldApprovalStatus]),
		Actionability:    model.ApprovalActionability(data[metaFieldApprovalActionable]),
		ApprovalDecision: model.ApprovalDecision(data[metaFieldApprovalDecision]),
	}
	if data[metaFieldApprovalRequired] == "true" {
		record.ApprovalRequired = true
	}
	if raw := data[metaFieldApprovalRevision]; raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			record.ApprovalRevision = parsed
		}
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
			Decision:         model.SafetyDecision(stringFromEntry(entry, "decision")),
			Reason:           stringFromEntry(entry, "reason"),
			RuleID:           stringFromEntry(entry, "rule_id"),
			PolicySnapshot:   stringFromEntry(entry, "policy_snapshot"),
			ApprovalRef:      stringFromEntry(entry, "approval_ref"),
			JobHash:          stringFromEntry(entry, "job_hash"),
			ApprovalStatus:   model.ApprovalStatus(stringFromEntry(entry, "approval_status")),
			Actionability:    model.ApprovalActionability(stringFromEntry(entry, "actionability")),
			ApprovalDecision: model.ApprovalDecision(stringFromEntry(entry, "approval_decision")),
		}
		if val, ok := entry["approval_required"].(bool); ok {
			record.ApprovalRequired = val
		}
		if val, ok := entry["checked_at"].(float64); ok {
			record.CheckedAt = int64(val)
		}
		if val, ok := entry["approval_revision"].(float64); ok {
			record.ApprovalRevision = int64(val)
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
			WorkerID:       meta[metaFieldWorkerID],
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

// parseJobEvent parses a raw event string from the job:events:{id} LIST.
// It tries JSON first (new format), then falls back to "timestamp|state"
// (old format). Returns (zero, false) for malformed entries.
func parseJobEvent(raw string) (model.JobEvent, bool) {
	var evt model.JobEvent
	if err := json.Unmarshal([]byte(raw), &evt); err == nil && evt.State != "" {
		return evt, true
	}

	// Fall back to old "timestamp|state" format.
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		slog.Warn("malformed job event entry", "raw", raw)
		return model.JobEvent{}, false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		slog.Warn("malformed job event timestamp", "raw", raw, "err", err)
		return model.JobEvent{}, false
	}
	return model.JobEvent{Timestamp: ts, State: parts[1]}, true
}

// serializeJobEvent creates the JSON string for a job event entry.
func serializeJobEvent(state model.JobState, nowMicros int64, evtCtx *model.StateEventContext) (string, error) {
	evt := model.JobEvent{
		Timestamp: nowMicros,
		State:     string(state),
		Context:   evtCtx,
	}
	b, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("marshal job event: %w", err)
	}
	return string(b), nil
}

// SetStateWithContext transitions a job to a new state and appends a rich
// JSON event (with optional context) to the job:events:{id} LIST.
// When evtCtx is nil, a minimal {"ts":...,"state":"..."} event is stored.
func (s *RedisJobStore) SetStateWithContext(ctx context.Context, jobID string, state model.JobState, evtCtx *model.StateEventContext) error {
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

		// append rich event
		eventStr, err := serializeJobEvent(state, now, evtCtx)
		if err != nil {
			return err
		}
		pipe.RPush(ctx, jobEventsKey(jobID), eventStr)

		if terminalStates[state] {
			pipe.ZRem(ctx, deadlineIndexKey(), jobID)
			pipe.HDel(ctx, metaKey, metaFieldDeadline)
		}

		_, execErr := pipe.Exec(ctx)
		return execErr
	}, metaKey, jobStateKey(jobID))
}

// GetJobEvents returns all state transition events for a job, parsed from
// the job:events:{id} LIST. Handles both JSON (new) and "timestamp|state"
// (old) formats. Malformed entries are skipped with a warning log.
func (s *RedisJobStore) GetJobEvents(ctx context.Context, jobID string) ([]model.JobEvent, error) {
	if jobID == "" {
		return nil, fmt.Errorf("job store get events: empty jobID")
	}
	raw, err := s.client.LRange(ctx, jobEventsKey(jobID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("job store get events %s: %w", jobID, err)
	}
	events := make([]model.JobEvent, 0, len(raw))
	for _, entry := range raw {
		evt, ok := parseJobEvent(entry)
		if !ok {
			continue
		}
		events = append(events, evt)
	}
	return events, nil
}
