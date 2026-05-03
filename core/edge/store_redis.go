package edge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/redis/go-redis/v9"
)

const (
	defaultHeartbeatTTL             = 30 * time.Second
	defaultMaxEventBytes            = 128 * 1024
	defaultIdempotencyTTL           = 24 * time.Hour
	defaultMaxIdempotencyReplayBody = 256 * 1024
	edgeEventAppendCASMaxAttempts   = 5
	// maxSessionEventScan is the legacy hard-stop threshold retained as a
	// regression-test fixture. Production session event listing no longer
	// truncates at this count; it stops per request after the cursor window has
	// enough events to answer the current page.
	maxSessionEventScan = 10000

	storeCursorVersion = 1
)

type storeCursor struct {
	Version        int     `json:"v"`
	Kind           string  `json:"kind"`
	Scope          string  `json:"scope,omitempty"`
	Score          float64 `json:"score,omitempty"`
	ID             string  `json:"id,omitempty"`
	ExecutionScore float64 `json:"execution_score,omitempty"`
	ExecutionID    string  `json:"execution_id,omitempty"`
}

type sessionEventRef struct {
	ExecutionID string
	Seq         int
	EventID     string
}

// StoreOption customizes RedisStore behavior. Options are primarily used by
// tests to pin clock and safety limits without changing production defaults.
type StoreOption func(*RedisStore)

// RedisStore persists Edge evidence in Redis using the PRD edge:* keyspace.
type RedisStore struct {
	client                   redis.UniversalClient
	now                      func() time.Time
	heartbeatTTL             time.Duration
	maxEventBytes            int
	idempotencyTTL           time.Duration
	maxIdempotencyReplayBody int
}

type redisEventGroup struct {
	events    []int
	execution *AgentExecution
}

type redisEventAppendPayload struct {
	index   int
	payload []byte
}

type redisIdempotentAppendPlan struct {
	appended            []AgentActionEvent
	payloadsByExecution map[string][]redisEventAppendPayload
	record              *EdgeIdempotencyRecord
	recordPayload       []byte
}

// NewRedisStoreFromClient returns a Redis-backed Edge store using an existing
// go-redis client. The caller owns closing the client.
func NewRedisStoreFromClient(client redis.UniversalClient, opts ...StoreOption) *RedisStore {
	s := &RedisStore{
		client:                   client,
		now:                      func() time.Time { return time.Now().UTC() },
		heartbeatTTL:             defaultHeartbeatTTL,
		maxEventBytes:            defaultMaxEventBytes,
		idempotencyTTL:           defaultIdempotencyTTL,
		maxIdempotencyReplayBody: defaultMaxIdempotencyReplayBody,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.now == nil {
		s.now = func() time.Time { return time.Now().UTC() }
	}
	if s.heartbeatTTL <= 0 {
		s.heartbeatTTL = defaultHeartbeatTTL
	}
	if s.maxEventBytes <= 0 {
		s.maxEventBytes = defaultMaxEventBytes
	}
	if s.idempotencyTTL <= 0 {
		s.idempotencyTTL = defaultIdempotencyTTL
	}
	if s.maxIdempotencyReplayBody <= 0 {
		s.maxIdempotencyReplayBody = defaultMaxIdempotencyReplayBody
	}
	return s
}

// WithClock pins the store clock for tests.
func WithClock(now func() time.Time) StoreOption {
	return func(s *RedisStore) {
		s.now = now
	}
}

// WithHeartbeatTTL overrides the heartbeat key TTL.
func WithHeartbeatTTL(ttl time.Duration) StoreOption {
	return func(s *RedisStore) {
		s.heartbeatTTL = ttl
	}
}

// WithMaxEventBytes overrides the serialized AgentActionEvent byte limit.
func WithMaxEventBytes(max int) StoreOption {
	return func(s *RedisStore) {
		s.maxEventBytes = max
	}
}

// WithIdempotencyTTL overrides the Edge API idempotency replay TTL.
func WithIdempotencyTTL(ttl time.Duration) StoreOption {
	return func(s *RedisStore) {
		s.idempotencyTTL = ttl
	}
}

// WithMaxIdempotencyReplayBody overrides the maximum cached Edge idempotency
// response body size. It is primarily intended for tests.
func WithMaxIdempotencyReplayBody(max int) StoreOption {
	return func(s *RedisStore) {
		s.maxIdempotencyReplayBody = max
	}
}

func (s *RedisStore) ensureReady() error {
	if s == nil || s.client == nil {
		return fmt.Errorf("edge redis store unavailable")
	}
	return nil
}

func edgeSessionKey(sessionID string) string {
	return "edge:session:" + strings.TrimSpace(sessionID)
}

func edgeExecutionKey(executionID string) string {
	return "edge:execution:" + strings.TrimSpace(executionID)
}

func edgeEventsKey(executionID string) string {
	return "edge:events:" + strings.TrimSpace(executionID)
}

func edgeEventSeqKey(executionID string) string {
	return "edge:events:seq:" + strings.TrimSpace(executionID)
}

func edgeEventIDIndexKey(executionID string) string {
	return "edge:index:event_id:" + strings.TrimSpace(executionID)
}

func edgeSessionEventsIndexKey(sessionID string) string {
	return "edge:index:session_events:" + strings.TrimSpace(sessionID)
}

func edgeTenantIndexKey(tenantID string) string {
	return "edge:index:tenant:" + strings.TrimSpace(tenantID)
}

func edgePrincipalIndexKey(tenantID, principalID string) string {
	return "edge:index:principal:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(principalID)
}

func edgeJobIndexKey(jobID string) string {
	return "edge:index:job:" + strings.TrimSpace(jobID)
}

// Trace/run indexes are intentionally split by entity type. The original
// pre-GA keyspace used edge:index:trace:* and edge:index:run:* for both
// sessions and executions, so a session_id matching an execution_id could
// overwrite/remove the other entity's ZSET member. The new keys are a
// pre-GA breaking change that keeps session cleanup isolated from execution
// list paths.
func edgeSessionTraceIndexKey(traceID string) string {
	return "edge:index:session_trace:" + strings.TrimSpace(traceID)
}

func edgeExecutionTraceIndexKey(traceID string) string {
	return "edge:index:execution_trace:" + strings.TrimSpace(traceID)
}

func edgeSessionRunIndexKey(workflowRunID string) string {
	return "edge:index:session_run:" + strings.TrimSpace(workflowRunID)
}

func edgeExecutionRunIndexKey(workflowRunID string) string {
	return "edge:index:execution_run:" + strings.TrimSpace(workflowRunID)
}

func edgeSessionExecutionsIndexKey(sessionID string) string {
	return "edge:index:session_executions:" + strings.TrimSpace(sessionID)
}

func edgeSessionHeartbeatKey(sessionID string) string {
	return "edge:session:heartbeat:" + strings.TrimSpace(sessionID)
}

func edgeIdempotencyKey(tenantID, endpoint, key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(endpoint) + "\x00" + strings.TrimSpace(key)))
	return "edge:idempotency:" + hex.EncodeToString(sum[:])
}

func (s *RedisStore) CreateSession(ctx context.Context, session EdgeSession) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate edge session %s: %w", session.SessionID, err)
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal edge session %s: %w", session.SessionID, err)
	}
	key := edgeSessionKey(session.SessionID)
	score := float64(session.StartedAt.UTC().UnixMicro())
	err = s.client.Watch(ctx, func(tx *redis.Tx) error {
		exists, err := tx.Exists(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("check edge session %s existence: %w", session.SessionID, err)
		}
		if exists > 0 {
			return fmt.Errorf("edge session %s already exists", session.SessionID)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, 0)
			pipe.ZAdd(ctx, edgeTenantIndexKey(session.TenantID), redis.Z{Score: score, Member: session.SessionID})
			if strings.TrimSpace(session.PrincipalID) != "" {
				pipe.ZAdd(ctx, edgePrincipalIndexKey(session.TenantID, session.PrincipalID), redis.Z{Score: score, Member: session.SessionID})
			}
			if strings.TrimSpace(session.TraceID) != "" {
				pipe.ZAdd(ctx, edgeSessionTraceIndexKey(session.TraceID), redis.Z{Score: score, Member: session.SessionID})
			}
			if strings.TrimSpace(session.WorkflowRunID) != "" {
				pipe.ZAdd(ctx, edgeSessionRunIndexKey(session.WorkflowRunID), redis.Z{Score: score, Member: session.SessionID})
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("write edge session %s: %w", session.SessionID, err)
		}
		return nil
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		return fmt.Errorf("create edge session %s conflict: %w", session.SessionID, err)
	}
	return err
}

func (s *RedisStore) GetSession(ctx context.Context, tenantID, sessionID string) (*EdgeSession, bool, error) {
	if err := s.ensureReady(); err != nil {
		return nil, false, err
	}
	session, ok, err := s.loadSession(ctx, sessionID)
	if err != nil || !ok {
		return nil, ok, err
	}
	if session.TenantID != strings.TrimSpace(tenantID) {
		return nil, false, nil
	}
	return session, true, nil
}

func (s *RedisStore) ListSessions(ctx context.Context, query ListSessionsQuery) (SessionPage, error) {
	if err := s.ensureReady(); err != nil {
		return SessionPage{}, err
	}
	tenantID := strings.TrimSpace(query.TenantID)
	if tenantID == "" {
		return SessionPage{}, fmt.Errorf("tenant_id is required")
	}
	indexKey := edgeTenantIndexKey(tenantID)
	if principalID := strings.TrimSpace(query.PrincipalID); principalID != "" {
		indexKey = edgePrincipalIndexKey(tenantID, principalID)
	}
	limit := normalizeStoreLimit(query.Limit)
	// Fetch only the requested page (+1 sentinel) instead of the entire index.
	// Tenants with millions of sessions previously caused unbounded memory and
	// per-call Redis fan-out; bound to limit+1 so the request stays O(limit).
	ids, nextCursor, err := s.listZSetIDs(ctx, indexKey, query.Cursor, "sessions", limit)
	if err != nil {
		return SessionPage{}, fmt.Errorf("list edge sessions index %s: %w", indexKey, err)
	}
	items := make([]EdgeSession, 0, len(ids))
	for _, id := range ids {
		session, ok, err := s.loadSession(ctx, id)
		if err != nil {
			return SessionPage{}, err
		}
		if !ok || session.TenantID != tenantID {
			continue
		}
		items = append(items, *session)
	}
	return SessionPage{Items: items, NextCursor: nextCursor}, nil
}

func (s *RedisStore) EndSession(ctx context.Context, tenantID, sessionID string, endedAt time.Time, status SessionStatus) (*EdgeSession, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	sessionID = strings.TrimSpace(sessionID)
	if tenantID == "" || sessionID == "" {
		return nil, fmt.Errorf("tenant_id and session_id are required")
	}
	if !isTerminalSessionStatus(status) {
		return nil, fmt.Errorf("session end status must be terminal")
	}
	if endedAt.IsZero() {
		return nil, fmt.Errorf("ended_at is required")
	}
	key := edgeSessionKey(sessionID)
	var updated *EdgeSession
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get edge session %s for end: %w", sessionID, err)
		}
		var session EdgeSession
		if err := json.Unmarshal(raw, &session); err != nil {
			return fmt.Errorf("unmarshal edge session %s: %w", sessionID, err)
		}
		if session.TenantID != tenantID {
			return ErrNotFound
		}
		if session.EndedAt != nil || isTerminalSessionStatus(session.Status) {
			return fmt.Errorf("edge session %s is already terminal", sessionID)
		}
		ended := endedAt.UTC()
		session.EndedAt = &ended
		session.Status = status
		if err := session.Validate(); err != nil {
			return fmt.Errorf("validate ended edge session %s: %w", sessionID, err)
		}
		payload, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("marshal ended edge session %s: %w", sessionID, err)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, 0)
			// Drop the heartbeat key in the same transaction so HeartbeatAlive
			// stops returning true the moment a session is ended. Without this
			// a terminal session would still look alive until the heartbeat
			// TTL elapsed (up to s.heartbeatTTL).
			pipe.Del(ctx, edgeSessionHeartbeatKey(sessionID))
			return nil
		})
		if err != nil {
			return fmt.Errorf("write ended edge session %s: %w", sessionID, err)
		}
		updated = &session
		return nil
	}, key)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteSession removes an Edge session and session-scoped evidence indexes.
// It is intentionally idempotent so Gateway compensation can call it after a
// partially failed create flow without leaking whether a tenant/session exists.
func (s *RedisStore) DeleteSession(ctx context.Context, tenantID, sessionID string) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	tenantID = strings.TrimSpace(tenantID)
	sessionID = strings.TrimSpace(sessionID)
	if tenantID == "" || sessionID == "" {
		return fmt.Errorf("tenant_id and session_id are required")
	}
	session, ok, err := s.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if !ok || session == nil {
		return nil
	}

	executionIDs, err := s.client.ZRange(ctx, edgeSessionExecutionsIndexKey(sessionID), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("list executions for edge session cleanup %s: %w", sessionID, err)
	}
	executions := make([]AgentExecution, 0, len(executionIDs))
	for _, executionID := range executionIDs {
		execution, ok, err := s.loadExecution(ctx, executionID)
		if err != nil {
			return err
		}
		if !ok || execution == nil {
			continue
		}
		if execution.TenantID != tenantID || execution.SessionID != sessionID {
			continue
		}
		executions = append(executions, *execution)
	}

	_, err = s.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, edgeSessionKey(sessionID), edgeSessionHeartbeatKey(sessionID), edgeSessionExecutionsIndexKey(sessionID), edgeSessionEventsIndexKey(sessionID))
		pipe.ZRem(ctx, edgeTenantIndexKey(session.TenantID), sessionID)
		if strings.TrimSpace(session.PrincipalID) != "" {
			pipe.ZRem(ctx, edgePrincipalIndexKey(session.TenantID, session.PrincipalID), sessionID)
		}
		if strings.TrimSpace(session.TraceID) != "" {
			pipe.ZRem(ctx, edgeSessionTraceIndexKey(session.TraceID), sessionID)
		}
		if strings.TrimSpace(session.WorkflowRunID) != "" {
			pipe.ZRem(ctx, edgeSessionRunIndexKey(session.WorkflowRunID), sessionID)
		}
		for _, execution := range executions {
			pipe.Del(ctx, edgeExecutionKey(execution.ExecutionID), edgeEventsKey(execution.ExecutionID), edgeEventSeqKey(execution.ExecutionID), edgeEventIDIndexKey(execution.ExecutionID))
			if strings.TrimSpace(execution.JobID) != "" {
				pipe.ZRem(ctx, edgeJobIndexKey(execution.JobID), execution.ExecutionID)
			}
			if strings.TrimSpace(execution.TraceID) != "" {
				pipe.ZRem(ctx, edgeExecutionTraceIndexKey(execution.TraceID), execution.ExecutionID)
			}
			if strings.TrimSpace(execution.WorkflowRunID) != "" {
				pipe.ZRem(ctx, edgeExecutionRunIndexKey(execution.WorkflowRunID), execution.ExecutionID)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete edge session %s: %w", sessionID, err)
	}
	return nil
}

func (s *RedisStore) TouchHeartbeat(ctx context.Context, tenantID, sessionID string) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	tenantID = strings.TrimSpace(tenantID)
	sessionID = strings.TrimSpace(sessionID)
	key := edgeSessionKey(sessionID)
	value := s.now().UTC().Format(time.RFC3339Nano)
	for attempt := 0; attempt < 8; attempt++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				return fmt.Errorf("%w: edge session %s", ErrNotFound, sessionID)
			}
			if err != nil {
				return fmt.Errorf("load edge session %s: %w", sessionID, err)
			}
			var session EdgeSession
			if err := json.Unmarshal(raw, &session); err != nil {
				return fmt.Errorf("unmarshal edge session %s: %w", sessionID, err)
			}
			if session.TenantID != tenantID {
				return fmt.Errorf("%w: edge session %s", ErrNotFound, sessionID)
			}
			if session.EndedAt != nil || isTerminalSessionStatus(session.Status) {
				return fmt.Errorf("edge session %s is terminal; cannot touch heartbeat", session.SessionID)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, edgeSessionHeartbeatKey(session.SessionID), value, s.heartbeatTTL)
				return nil
			})
			if err != nil {
				return fmt.Errorf("touch edge session heartbeat %s: %w", session.SessionID, err)
			}
			return nil
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("touch edge session heartbeat %s conflict: %w", sessionID, redis.TxFailedErr)
}

func (s *RedisStore) HeartbeatAlive(ctx context.Context, tenantID, sessionID string) (bool, error) {
	if err := s.ensureReady(); err != nil {
		return false, err
	}
	session, ok, err := s.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return false, err
	}
	if !ok || session == nil {
		return false, nil
	}
	_, err = s.client.Get(ctx, edgeSessionHeartbeatKey(session.SessionID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read edge session heartbeat %s: %w", session.SessionID, err)
	}
	return true, nil
}

func (s *RedisStore) CreateExecution(ctx context.Context, execution AgentExecution) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := execution.Validate(); err != nil {
		return fmt.Errorf("validate agent execution %s: %w", execution.ExecutionID, err)
	}
	parent, ok, err := s.GetSession(ctx, execution.TenantID, execution.SessionID)
	if err != nil {
		return fmt.Errorf("load parent edge session %s: %w", execution.SessionID, err)
	}
	if !ok || parent == nil {
		return fmt.Errorf("%w: parent edge session %s", ErrNotFound, execution.SessionID)
	}
	payload, err := json.Marshal(execution)
	if err != nil {
		return fmt.Errorf("marshal agent execution %s: %w", execution.ExecutionID, err)
	}
	key := edgeExecutionKey(execution.ExecutionID)
	score := float64(execution.StartedAt.UTC().UnixMicro())
	err = s.client.Watch(ctx, func(tx *redis.Tx) error {
		exists, err := tx.Exists(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("check agent execution %s existence: %w", execution.ExecutionID, err)
		}
		if exists > 0 {
			return fmt.Errorf("agent execution %s already exists", execution.ExecutionID)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, 0)
			pipe.ZAdd(ctx, edgeSessionExecutionsIndexKey(execution.SessionID), redis.Z{Score: score, Member: execution.ExecutionID})
			if strings.TrimSpace(execution.JobID) != "" {
				pipe.ZAdd(ctx, edgeJobIndexKey(execution.JobID), redis.Z{Score: score, Member: execution.ExecutionID})
			}
			if strings.TrimSpace(execution.TraceID) != "" {
				pipe.ZAdd(ctx, edgeExecutionTraceIndexKey(execution.TraceID), redis.Z{Score: score, Member: execution.ExecutionID})
			}
			if strings.TrimSpace(execution.WorkflowRunID) != "" {
				pipe.ZAdd(ctx, edgeExecutionRunIndexKey(execution.WorkflowRunID), redis.Z{Score: score, Member: execution.ExecutionID})
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("write agent execution %s: %w", execution.ExecutionID, err)
		}
		return nil
	}, key)
	if errors.Is(err, redis.TxFailedErr) {
		return fmt.Errorf("create agent execution %s conflict: %w", execution.ExecutionID, err)
	}
	return err
}

func (s *RedisStore) GetExecution(ctx context.Context, tenantID, executionID string) (*AgentExecution, bool, error) {
	if err := s.ensureReady(); err != nil {
		return nil, false, err
	}
	execution, ok, err := s.loadExecution(ctx, executionID)
	if err != nil || !ok {
		return nil, ok, err
	}
	if execution.TenantID != strings.TrimSpace(tenantID) {
		return nil, false, nil
	}
	return execution, true, nil
}

func (s *RedisStore) ListExecutions(ctx context.Context, query ListExecutionsQuery) (ExecutionPage, error) {
	if err := s.ensureReady(); err != nil {
		return ExecutionPage{}, err
	}
	tenantID := strings.TrimSpace(query.TenantID)
	if tenantID == "" {
		return ExecutionPage{}, fmt.Errorf("tenant_id is required")
	}
	indexKey := ""
	switch {
	case strings.TrimSpace(query.SessionID) != "":
		indexKey = edgeSessionExecutionsIndexKey(query.SessionID)
	case strings.TrimSpace(query.JobID) != "":
		indexKey = edgeJobIndexKey(query.JobID)
	case strings.TrimSpace(query.TraceID) != "":
		indexKey = edgeExecutionTraceIndexKey(query.TraceID)
	case strings.TrimSpace(query.WorkflowRunID) != "":
		indexKey = edgeExecutionRunIndexKey(query.WorkflowRunID)
	default:
		return ExecutionPage{}, fmt.Errorf("execution list index is required")
	}
	limit := normalizeStoreLimit(query.Limit)
	// Bounded ZRevRange — see ListSessions for rationale.
	ids, nextCursor, err := s.listZSetIDs(ctx, indexKey, query.Cursor, "executions", limit)
	if err != nil {
		return ExecutionPage{}, fmt.Errorf("list agent executions index %s: %w", indexKey, err)
	}
	items := make([]AgentExecution, 0, len(ids))
	for _, id := range ids {
		execution, ok, err := s.loadExecution(ctx, id)
		if err != nil {
			return ExecutionPage{}, err
		}
		if !ok || execution.TenantID != tenantID {
			continue
		}
		items = append(items, *execution)
	}
	return ExecutionPage{Items: items, NextCursor: nextCursor}, nil
}

func (s *RedisStore) EndExecution(ctx context.Context, tenantID, executionID string, endedAt time.Time, status ExecutionStatus) (*AgentExecution, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	executionID = strings.TrimSpace(executionID)
	if tenantID == "" || executionID == "" {
		return nil, fmt.Errorf("tenant_id and execution_id are required")
	}
	if !isTerminalExecutionStatus(status) {
		return nil, fmt.Errorf("execution end status must be terminal")
	}
	if endedAt.IsZero() {
		return nil, fmt.Errorf("ended_at is required")
	}
	key := edgeExecutionKey(executionID)
	var updated *AgentExecution
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Bytes()
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get agent execution %s for end: %w", executionID, err)
		}
		var execution AgentExecution
		if err := json.Unmarshal(raw, &execution); err != nil {
			return fmt.Errorf("unmarshal agent execution %s: %w", executionID, err)
		}
		if execution.TenantID != tenantID {
			return ErrNotFound
		}
		if execution.EndedAt != nil || isTerminalExecutionStatus(execution.Status) {
			return fmt.Errorf("agent execution %s is already terminal", executionID)
		}
		ended := endedAt.UTC()
		execution.EndedAt = &ended
		execution.Status = status
		if err := execution.Validate(); err != nil {
			return fmt.Errorf("validate ended agent execution %s: %w", executionID, err)
		}
		payload, err := json.Marshal(execution)
		if err != nil {
			return fmt.Errorf("marshal ended agent execution %s: %w", executionID, err)
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, payload, 0)
			return nil
		})
		if err != nil {
			return fmt.Errorf("write ended agent execution %s: %w", executionID, err)
		}
		updated = &execution
		return nil
	}, key)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *RedisStore) AppendEvent(ctx context.Context, event AgentActionEvent) (AgentActionEvent, error) {
	appended, err := s.AppendEvents(ctx, []AgentActionEvent{event})
	if err != nil {
		return AgentActionEvent{}, err
	}
	if len(appended) != 1 {
		return AgentActionEvent{}, fmt.Errorf("append agent action event %s returned %d events", event.EventID, len(appended))
	}
	return appended[0], nil
}

func (s *RedisStore) groupAppendEvents(ctx context.Context, events []AgentActionEvent) (map[string]*redisEventGroup, error) {
	groups := make(map[string]*redisEventGroup)
	for i, event := range events {
		tenantID := strings.TrimSpace(event.TenantID)
		executionID := strings.TrimSpace(event.ExecutionID)
		if tenantID == "" || executionID == "" {
			return nil, fmt.Errorf("tenant_id and execution_id are required")
		}
		group, exists := groups[executionID]
		if !exists {
			execution, ok, err := s.GetExecution(ctx, tenantID, executionID)
			if err != nil {
				return nil, fmt.Errorf("load event execution %s: %w", executionID, err)
			}
			if !ok || execution == nil {
				return nil, fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
			}
			group = &redisEventGroup{execution: execution}
			groups[executionID] = group
		}
		if group.execution.TenantID != tenantID {
			return nil, fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
		}
		if group.execution.SessionID != strings.TrimSpace(event.SessionID) {
			return nil, fmt.Errorf("event session_id %s does not match execution session_id %s", event.SessionID, group.execution.SessionID)
		}
		group.events = append(group.events, i)
	}
	return groups, nil
}

func (s *RedisStore) AppendEvents(ctx context.Context, events []AgentActionEvent) ([]AgentActionEvent, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return []AgentActionEvent{}, nil
	}

	groups, err := s.groupAppendEvents(ctx, events)
	if err != nil {
		return nil, err
	}

	watchKeys := make([]string, 0, len(groups)*4)
	for executionID := range groups {
		// Watch the execution document too: a concurrent EndExecution must
		// invalidate this transaction so we never append events past a
		// terminal state. The seq key + list key alone do not catch a
		// status-only mutation.
		watchKeys = append(watchKeys, edgeEventSeqKey(executionID), edgeEventsKey(executionID), edgeEventIDIndexKey(executionID), edgeExecutionKey(executionID))
	}
	appended := make([]AgentActionEvent, len(events))
	err = redisutil.Retry(ctx, s.client, func(tx *redis.Tx) error {
		// Re-read each execution inside the watched transaction and reject
		// the batch if it is missing, cross-tenant, or already terminal.
		// Without this re-check, a TOCTOU window between the GetExecution
		// done outside the closure (line ~597) and the seq read below
		// would let events land on a session/execution that has since been
		// ended, deleted, or moved to another tenant.
		for executionID, group := range groups {
			raw, err := tx.Get(ctx, edgeExecutionKey(executionID)).Bytes()
			if errors.Is(err, redis.Nil) {
				return fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
			}
			if err != nil {
				return fmt.Errorf("re-read agent execution %s: %w", executionID, err)
			}
			var fresh AgentExecution
			if err := json.Unmarshal(raw, &fresh); err != nil {
				return fmt.Errorf("unmarshal agent execution %s: %w", executionID, err)
			}
			if fresh.TenantID != group.execution.TenantID {
				return fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
			}
			if fresh.EndedAt != nil || isTerminalExecutionStatus(fresh.Status) {
				return fmt.Errorf("agent execution %s is terminal; cannot append events", executionID)
			}
			group.execution = &fresh
		}
		payloadsByExecution := make(map[string][]redisEventAppendPayload, len(groups))
		for executionID, group := range groups {
			lastSeq, err := readEventSeqInTx(ctx, tx, executionID)
			if err != nil {
				return err
			}
			payloads := make([]redisEventAppendPayload, 0, len(group.events))
			for _, index := range group.events {
				payload, next, err := s.prepareAppendEventPayload(events[index], lastSeq)
				if err != nil {
					return err
				}
				payloads = append(payloads, redisEventAppendPayload{index: index, payload: payload})
				appended[index] = next
				lastSeq = next.Seq
			}
			payloadsByExecution[executionID] = payloads
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			for executionID := range groups {
				payloads := payloadsByExecution[executionID]
				for _, payload := range payloads {
					pipe.RPush(ctx, edgeEventsKey(executionID), payload.payload)
					event := appended[payload.index]
					pipe.ZAdd(ctx, edgeSessionEventsIndexKey(event.SessionID), redis.Z{
						Score:  float64(event.Timestamp.UTC().UnixMicro()),
						Member: sessionEventIndexMember(event),
					})
					pipe.HSet(ctx, edgeEventIDIndexKey(event.ExecutionID), event.EventID, event.Seq)
				}
				if len(payloads) > 0 {
					last := appended[payloads[len(payloads)-1].index]
					pipe.Set(ctx, edgeEventSeqKey(executionID), last.Seq, 0)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("append agent action event batch: %w", err)
		}
		return nil
	}, redisutil.WithKeys(watchKeys...), redisutil.WithMaxAttempts(edgeEventAppendCASMaxAttempts))
	if errors.Is(err, redis.TxFailedErr) || errors.Is(err, redisutil.ErrMaxAttemptsExceeded) {
		return nil, fmt.Errorf("append agent action event batch conflict: %w", err)
	}
	if err != nil {
		return nil, err
	}
	return appended, nil
}

func (s *RedisStore) AppendEventsWithIdempotency(
	ctx context.Context,
	req EdgeIdempotencyRequest,
	events []AgentActionEvent,
	buildResponse EdgeIdempotencyResponseBuilder,
) (EdgeIdempotentAppendResult, error) {
	if err := s.ensureReady(); err != nil {
		return EdgeIdempotentAppendResult{}, err
	}
	normalized, err := normalizeEdgeIdempotencyRequest(req)
	if err != nil {
		return EdgeIdempotentAppendResult{}, err
	}
	if len(events) == 0 {
		return EdgeIdempotentAppendResult{}, fmt.Errorf("events are required")
	}
	if buildResponse == nil {
		return EdgeIdempotentAppendResult{}, fmt.Errorf("idempotency response builder is required")
	}

	groups, err := s.groupAppendEvents(ctx, events)
	if err != nil {
		return EdgeIdempotentAppendResult{}, err
	}
	key := edgeIdempotencyKey(normalized.TenantID, normalized.Endpoint, normalized.Key)
	watchKeys := []string{key}
	for executionID := range groups {
		watchKeys = append(watchKeys, edgeEventSeqKey(executionID), edgeEventsKey(executionID), edgeEventIDIndexKey(executionID), edgeExecutionKey(executionID))
	}
	return s.appendEventsWithIdempotencyTx(ctx, normalized, key, watchKeys, groups, events, buildResponse)
}

func (s *RedisStore) appendEventsWithIdempotencyTx(
	ctx context.Context,
	req EdgeIdempotencyRequest,
	key string,
	watchKeys []string,
	groups map[string]*redisEventGroup,
	events []AgentActionEvent,
	buildResponse EdgeIdempotencyResponseBuilder,
) (EdgeIdempotentAppendResult, error) {
	var result EdgeIdempotentAppendResult
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			replay, handled, err := loadExistingEdgeIdempotencyForAppend(ctx, tx, key, req)
			if err != nil || handled {
				result = replay
				return err
			}
			if err := refreshAppendExecutionsInTx(ctx, tx, groups); err != nil {
				return err
			}
			plan, err := s.planIdempotentAppend(ctx, tx, req, groups, events, buildResponse)
			if err != nil {
				return err
			}
			if err := s.commitIdempotentAppend(ctx, tx, key, groups, plan); err != nil {
				return err
			}
			result = EdgeIdempotentAppendResult{
				State:  EdgeIdempotencyCompleted,
				Events: plan.appended,
				Record: plan.record,
			}
			return nil
		}, watchKeys...)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if errors.Is(err, ErrIdempotencyWindowExpired) {
			replay, ok, replayErr := s.loadCompletedAppendReplay(ctx, key, req)
			if replayErr != nil {
				return EdgeIdempotentAppendResult{}, replayErr
			}
			if ok {
				return replay, nil
			}
		}
		if err != nil {
			return EdgeIdempotentAppendResult{}, err
		}
		return result, nil
	}
	return EdgeIdempotentAppendResult{}, fmt.Errorf("edge idempotent append conflict: %w", redis.TxFailedErr)
}

func (s *RedisStore) loadCompletedAppendReplay(ctx context.Context, key string, req EdgeIdempotencyRequest) (EdgeIdempotentAppendResult, bool, error) {
	raw, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return EdgeIdempotentAppendResult{}, false, nil
	}
	if err != nil {
		return EdgeIdempotentAppendResult{}, false, fmt.Errorf("read edge idempotency record after duplicate event: %w", err)
	}
	record, err := decodeEdgeIdempotencyRecord(raw)
	if err != nil {
		return EdgeIdempotentAppendResult{}, false, err
	}
	if record.RequestHash != req.RequestHash {
		return EdgeIdempotentAppendResult{}, false, ErrIdempotencyConflict
	}
	if record.Status != EdgeIdempotencyCompleted || len(record.Response.Body) == 0 || record.Response.StatusCode == 0 {
		return EdgeIdempotentAppendResult{}, false, nil
	}
	return EdgeIdempotentAppendResult{State: EdgeIdempotencyReplay, Record: record}, true, nil
}

func loadExistingEdgeIdempotencyForAppend(ctx context.Context, tx *redis.Tx, key string, req EdgeIdempotencyRequest) (EdgeIdempotentAppendResult, bool, error) {
	raw, err := tx.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return EdgeIdempotentAppendResult{}, false, nil
	}
	if err != nil {
		return EdgeIdempotentAppendResult{}, false, fmt.Errorf("read edge idempotency record for append: %w", err)
	}
	record, err := decodeEdgeIdempotencyRecord(raw)
	if err != nil {
		return EdgeIdempotentAppendResult{}, false, err
	}
	if record.RequestHash != req.RequestHash {
		return EdgeIdempotentAppendResult{}, true, ErrIdempotencyConflict
	}
	if record.Status == EdgeIdempotencyCompleted && len(record.Response.Body) > 0 && record.Response.StatusCode > 0 {
		return EdgeIdempotentAppendResult{State: EdgeIdempotencyReplay, Record: record}, true, nil
	}
	return EdgeIdempotentAppendResult{}, true, ErrIdempotencyPending
}

func refreshAppendExecutionsInTx(ctx context.Context, tx *redis.Tx, groups map[string]*redisEventGroup) error {
	for executionID, group := range groups {
		raw, err := tx.Get(ctx, edgeExecutionKey(executionID)).Bytes()
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
		}
		if err != nil {
			return fmt.Errorf("re-read agent execution %s: %w", executionID, err)
		}
		var fresh AgentExecution
		if err := json.Unmarshal(raw, &fresh); err != nil {
			return fmt.Errorf("unmarshal agent execution %s: %w", executionID, err)
		}
		if fresh.TenantID != group.execution.TenantID {
			return fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
		}
		if fresh.EndedAt != nil || isTerminalExecutionStatus(fresh.Status) {
			return fmt.Errorf("agent execution %s is terminal; cannot append events", executionID)
		}
		group.execution = &fresh
	}
	return nil
}

func (s *RedisStore) planIdempotentAppend(
	ctx context.Context,
	tx *redis.Tx,
	req EdgeIdempotencyRequest,
	groups map[string]*redisEventGroup,
	events []AgentActionEvent,
	buildResponse EdgeIdempotencyResponseBuilder,
) (redisIdempotentAppendPlan, error) {
	appended := make([]AgentActionEvent, len(events))
	payloadsByExecution := make(map[string][]redisEventAppendPayload, len(groups))
	for executionID, group := range groups {
		payloads, err := s.planIdempotentExecutionAppend(ctx, tx, executionID, group, events, appended)
		if err != nil {
			return redisIdempotentAppendPlan{}, err
		}
		payloadsByExecution[executionID] = payloads
	}
	record, payload, err := s.buildIdempotentAppendRecord(req, appended, buildResponse)
	if err != nil {
		return redisIdempotentAppendPlan{}, err
	}
	return redisIdempotentAppendPlan{
		appended:            appended,
		payloadsByExecution: payloadsByExecution,
		record:              record,
		recordPayload:       payload,
	}, nil
}

func (s *RedisStore) planIdempotentExecutionAppend(
	ctx context.Context,
	tx *redis.Tx,
	executionID string,
	group *redisEventGroup,
	events []AgentActionEvent,
	appended []AgentActionEvent,
) ([]redisEventAppendPayload, error) {
	lastSeq, err := readEventSeqInTx(ctx, tx, executionID)
	if err != nil {
		return nil, err
	}
	payloads := make([]redisEventAppendPayload, 0, len(group.events))
	plannedEventIDs := make(map[string]struct{}, len(group.events))
	for _, index := range group.events {
		next := events[index]
		eventID := strings.TrimSpace(next.EventID)
		if eventID != "" {
			if _, ok, err := loadEventByIDInTx(ctx, tx, executionID, eventID); err != nil {
				return nil, err
			} else if ok {
				return nil, ErrIdempotencyWindowExpired
			}
			if _, ok := plannedEventIDs[eventID]; ok {
				return nil, fmt.Errorf("duplicate event_id %s in append batch", eventID)
			}
		}
		payload, planned, err := s.prepareAppendEventPayload(next, lastSeq)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, redisEventAppendPayload{index: index, payload: payload})
		appended[index] = planned
		if eventID != "" {
			plannedEventIDs[eventID] = struct{}{}
		}
		lastSeq = planned.Seq
	}
	return payloads, nil
}

func readEventSeqInTx(ctx context.Context, tx *redis.Tx, executionID string) (int, error) {
	lastSeq, err := tx.Get(ctx, edgeEventSeqKey(executionID)).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read event seq for execution %s: %w", executionID, err)
	}
	return lastSeq, nil
}

func (s *RedisStore) prepareAppendEventPayload(event AgentActionEvent, lastSeq int) ([]byte, AgentActionEvent, error) {
	if event.Seq == 0 {
		event.Seq = lastSeq + 1
	}
	if event.Seq != lastSeq+1 {
		return nil, AgentActionEvent{}, fmt.Errorf("event seq %d must be next after %d", event.Seq, lastSeq)
	}
	if err := event.Validate(); err != nil {
		return nil, AgentActionEvent{}, fmt.Errorf("validate agent action event %s: %w", event.EventID, err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, AgentActionEvent{}, fmt.Errorf("marshal agent action event %s: %w", event.EventID, err)
	}
	if len(payload) > s.maxEventBytes {
		return nil, AgentActionEvent{}, fmt.Errorf("agent action event %s JSON size %d exceeds max %d bytes", event.EventID, len(payload), s.maxEventBytes)
	}
	return payload, event, nil
}

func (s *RedisStore) buildIdempotentAppendRecord(
	req EdgeIdempotencyRequest,
	appended []AgentActionEvent,
	buildResponse EdgeIdempotencyResponseBuilder,
) (*EdgeIdempotencyRecord, []byte, error) {
	response, err := buildResponse(appended)
	if err != nil {
		return nil, nil, err
	}
	response = normalizeEdgeIdempotencyResponse(response)
	if len(response.Body) > s.maxIdempotencyReplayBody {
		return nil, nil, fmt.Errorf("edge idempotency response body %d exceeds max %d bytes", len(response.Body), s.maxIdempotencyReplayBody)
	}
	now := s.now().UTC()
	record := &EdgeIdempotencyRecord{
		TenantID:    req.TenantID,
		Endpoint:    req.Endpoint,
		RequestHash: req.RequestHash,
		Status:      EdgeIdempotencyCompleted,
		Response:    response,
		CreatedAt:   now,
		CompletedAt: &now,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal completed edge idempotency record: %w", err)
	}
	return record, payload, nil
}

func (s *RedisStore) commitIdempotentAppend(
	ctx context.Context,
	tx *redis.Tx,
	key string,
	groups map[string]*redisEventGroup,
	plan redisIdempotentAppendPlan,
) error {
	_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for executionID := range groups {
			s.queueEventAppendPipeline(ctx, pipe, executionID, plan)
		}
		pipe.Set(ctx, key, plan.recordPayload, s.idempotencyTTL)
		return nil
	})
	if err != nil {
		return fmt.Errorf("append edge events and complete idempotency: %w", err)
	}
	return nil
}

func (s *RedisStore) queueEventAppendPipeline(ctx context.Context, pipe redis.Pipeliner, executionID string, plan redisIdempotentAppendPlan) {
	payloads := plan.payloadsByExecution[executionID]
	for _, payload := range payloads {
		pipe.RPush(ctx, edgeEventsKey(executionID), payload.payload)
		event := plan.appended[payload.index]
		pipe.ZAdd(ctx, edgeSessionEventsIndexKey(event.SessionID), redis.Z{
			Score:  float64(event.Timestamp.UTC().UnixMicro()),
			Member: sessionEventIndexMember(event),
		})
		pipe.HSet(ctx, edgeEventIDIndexKey(event.ExecutionID), event.EventID, event.Seq)
	}
	if len(payloads) > 0 {
		last := plan.appended[payloads[len(payloads)-1].index]
		pipe.Set(ctx, edgeEventSeqKey(executionID), last.Seq, 0)
	}
}

func (s *RedisStore) ReserveIdempotency(ctx context.Context, req EdgeIdempotencyRequest) (EdgeIdempotencyReservation, error) {
	if err := s.ensureReady(); err != nil {
		return EdgeIdempotencyReservation{}, err
	}
	normalized, err := normalizeEdgeIdempotencyRequest(req)
	if err != nil {
		return EdgeIdempotencyReservation{}, err
	}
	key := edgeIdempotencyKey(normalized.TenantID, normalized.Endpoint, normalized.Key)
	now := s.now().UTC()
	pending := EdgeIdempotencyRecord{
		TenantID:    normalized.TenantID,
		Endpoint:    normalized.Endpoint,
		RequestHash: normalized.RequestHash,
		Status:      EdgeIdempotencyPending,
		CreatedAt:   now,
	}
	payload, err := json.Marshal(pending)
	if err != nil {
		return EdgeIdempotencyReservation{}, fmt.Errorf("marshal edge idempotency reservation: %w", err)
	}

	var reservation EdgeIdempotencyReservation
	for attempt := 0; attempt < 8; attempt++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, key).Bytes()
			if err == nil {
				record, err := decodeEdgeIdempotencyRecord(raw)
				if err != nil {
					return err
				}
				if record.RequestHash != normalized.RequestHash {
					return ErrIdempotencyConflict
				}
				if record.Status == EdgeIdempotencyCompleted && len(record.Response.Body) > 0 && record.Response.StatusCode > 0 {
					reservation = EdgeIdempotencyReservation{State: EdgeIdempotencyReplay, Record: record}
					return nil
				}
				reservation = EdgeIdempotencyReservation{State: EdgeIdempotencyPending, Record: record}
				return nil
			}
			if !errors.Is(err, redis.Nil) {
				return fmt.Errorf("read edge idempotency reservation: %w", err)
			}
			reservation = EdgeIdempotencyReservation{State: EdgeIdempotencyReserved, Record: &pending}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, s.idempotencyTTL)
				return nil
			})
			if err != nil {
				return fmt.Errorf("write edge idempotency reservation: %w", err)
			}
			return nil
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return EdgeIdempotencyReservation{}, err
		}
		return reservation, nil
	}
	return EdgeIdempotencyReservation{}, fmt.Errorf("edge idempotency reservation conflict: %w", redis.TxFailedErr)
}

func (s *RedisStore) CompleteIdempotency(ctx context.Context, req EdgeIdempotencyRequest, response EdgeIdempotencyResponse) (*EdgeIdempotencyRecord, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	normalized, err := normalizeEdgeIdempotencyRequest(req)
	if err != nil {
		return nil, err
	}
	response = normalizeEdgeIdempotencyResponse(response)
	if len(response.Body) > s.maxIdempotencyReplayBody {
		return nil, fmt.Errorf("edge idempotency response body %d exceeds max %d bytes", len(response.Body), s.maxIdempotencyReplayBody)
	}
	key := edgeIdempotencyKey(normalized.TenantID, normalized.Endpoint, normalized.Key)
	var completed *EdgeIdempotencyRecord
	for attempt := 0; attempt < 8; attempt++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("read edge idempotency reservation for complete: %w", err)
			}
			record, err := decodeEdgeIdempotencyRecord(raw)
			if err != nil {
				return err
			}
			if record.RequestHash != normalized.RequestHash {
				return ErrIdempotencyConflict
			}
			if record.Status == EdgeIdempotencyCompleted && len(record.Response.Body) > 0 && record.Response.StatusCode > 0 {
				completed = record
				return nil
			}
			now := s.now().UTC()
			record.TenantID = normalized.TenantID
			record.Endpoint = normalized.Endpoint
			record.RequestHash = normalized.RequestHash
			record.Status = EdgeIdempotencyCompleted
			record.Response = response
			record.CompletedAt = &now
			if record.CreatedAt.IsZero() {
				record.CreatedAt = now
			}
			payload, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("marshal completed edge idempotency record: %w", err)
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, payload, s.idempotencyTTL)
				return nil
			})
			if err != nil {
				return fmt.Errorf("write completed edge idempotency record: %w", err)
			}
			completed = record
			return nil
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return completed, nil
	}
	return nil, fmt.Errorf("edge idempotency completion conflict: %w", redis.TxFailedErr)
}

func (s *RedisStore) ReleaseIdempotency(ctx context.Context, req EdgeIdempotencyRequest) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	normalized, err := normalizeEdgeIdempotencyRequest(req)
	if err != nil {
		return err
	}
	key := edgeIdempotencyKey(normalized.TenantID, normalized.Endpoint, normalized.Key)
	for attempt := 0; attempt < 8; attempt++ {
		err = s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("read edge idempotency reservation for release: %w", err)
			}
			record, err := decodeEdgeIdempotencyRecord(raw)
			if err != nil {
				return err
			}
			if record.RequestHash != normalized.RequestHash || record.Status == EdgeIdempotencyCompleted {
				return nil
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, key)
				return nil
			})
			if err != nil {
				return fmt.Errorf("delete edge idempotency reservation: %w", err)
			}
			return nil
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
	return fmt.Errorf("edge idempotency release conflict: %w", redis.TxFailedErr)
}

func normalizeEdgeIdempotencyRequest(req EdgeIdempotencyRequest) (EdgeIdempotencyRequest, error) {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.Endpoint = strings.TrimSpace(req.Endpoint)
	req.Key = strings.TrimSpace(req.Key)
	req.RequestHash = strings.TrimSpace(req.RequestHash)
	if req.TenantID == "" || req.Endpoint == "" || req.Key == "" || req.RequestHash == "" {
		return EdgeIdempotencyRequest{}, fmt.Errorf("tenant_id, endpoint, idempotency key, and request_hash are required")
	}
	return req, nil
}

func normalizeEdgeIdempotencyResponse(response EdgeIdempotencyResponse) EdgeIdempotencyResponse {
	if response.StatusCode == 0 {
		response.StatusCode = 200
	}
	response.ContentType = strings.TrimSpace(response.ContentType)
	if response.ContentType == "" {
		response.ContentType = "application/json"
	}
	response.Body = append([]byte(nil), response.Body...)
	return response
}

func decodeEdgeIdempotencyRecord(raw []byte) (*EdgeIdempotencyRecord, error) {
	var record EdgeIdempotencyRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("unmarshal edge idempotency record: %w", err)
	}
	return &record, nil
}

func (s *RedisStore) ListEvents(ctx context.Context, query ListEventsQuery) (EventPage, error) {
	if err := s.ensureReady(); err != nil {
		return EventPage{}, err
	}
	tenantID := strings.TrimSpace(query.TenantID)
	executionID := strings.TrimSpace(query.ExecutionID)
	sessionID := strings.TrimSpace(query.SessionID)
	if tenantID == "" || (executionID == "" && sessionID == "") {
		return EventPage{}, fmt.Errorf("tenant_id and execution_id or session_id are required")
	}
	limit := normalizeStoreLimit(query.Limit)
	if executionID != "" {
		execution, ok, err := s.GetExecution(ctx, tenantID, executionID)
		if err != nil {
			return EventPage{}, err
		}
		if !ok || execution == nil {
			return EventPage{Items: []AgentActionEvent{}}, nil
		}
		return s.listEventsForExecutionPage(ctx, query, executionID, query.Cursor, limit)
	} else {
		session, ok, err := s.GetSession(ctx, tenantID, sessionID)
		if err != nil {
			return EventPage{}, err
		}
		if !ok || session == nil {
			return EventPage{Items: []AgentActionEvent{}}, nil
		}
		return s.listEventsForSessionPage(ctx, query, sessionID, query.Cursor, limit)
	}
}

func (s *RedisStore) listEventsForSessionPage(ctx context.Context, query ListEventsQuery, sessionID, rawCursor string, limit int) (EventPage, error) {
	cursor, hasCursor, err := parseSessionIndexEventCursor(rawCursor)
	if err != nil {
		return EventPage{}, err
	}
	items := make([]AgentActionEvent, 0, limit)
	indexKey := edgeSessionEventsIndexKey(sessionID)
	var lastReturned redis.Z
	for {
		rows, err := s.listSessionEventIndexRows(ctx, indexKey, cursor, hasCursor, maxStorePageLimit)
		if err != nil {
			return EventPage{}, err
		}
		if len(rows) == 0 {
			return EventPage{Items: items}, nil
		}
		for _, row := range rows {
			cursor = cursorFromZSetRow("events", row)
			hasCursor = true
			ref, err := parseSessionEventIndexMember(row.Member)
			if err != nil {
				return EventPage{}, err
			}
			event, ok, err := s.loadEventByRef(ctx, ref)
			if err != nil {
				return EventPage{}, err
			}
			if !ok || !eventMatchesQuery(event, query, ref.ExecutionID) {
				continue
			}
			if len(items) == limit {
				return EventPage{Items: items, NextCursor: encodeStoreCursor(cursorFromZSetRow("events", lastReturned))}, nil
			}
			items = append(items, event)
			lastReturned = row
		}
		if len(rows) < maxStorePageLimit {
			return EventPage{Items: items}, nil
		}
	}
}

func (s *RedisStore) listEventsForExecutionPage(ctx context.Context, query ListEventsQuery, executionID, rawCursor string, limit int) (EventPage, error) {
	cursor, hasCursor, err := parseExecutionEventCursor(rawCursor, executionID)
	if err != nil {
		return EventPage{}, err
	}
	start := int64(0)
	if hasCursor {
		start = int64(cursor.Score)
		if start < 0 {
			return EventPage{}, fmt.Errorf("invalid cursor")
		}
	}
	items := make([]AgentActionEvent, 0, limit+1)
	for len(items) <= limit {
		rawEvents, err := s.client.LRange(ctx, edgeEventsKey(executionID), start, start+int64(maxStorePageLimit)-1).Result()
		if err != nil {
			return EventPage{}, fmt.Errorf("list agent action events for execution %s: %w", executionID, err)
		}
		if len(rawEvents) == 0 {
			break
		}
		for i, raw := range rawEvents {
			event, err := decodeStoreEvent(raw, executionID, int(start)+i)
			if err != nil {
				return EventPage{}, err
			}
			if eventMatchesQuery(event, query, executionID) {
				items = append(items, event)
				if len(items) > limit {
					break
				}
			}
		}
		if len(rawEvents) < maxStorePageLimit {
			break
		}
		start += int64(len(rawEvents))
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := EventPage{Items: items}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = encodeStoreCursor(storeCursor{
			Kind:        "events",
			Scope:       "execution",
			ExecutionID: executionID,
			Score:       float64(last.Seq),
			ID:          last.EventID,
		})
	}
	return page, nil
}

func (s *RedisStore) loadSession(ctx context.Context, sessionID string) (*EdgeSession, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, false, nil
	}
	raw, err := s.client.Get(ctx, edgeSessionKey(sessionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get edge session %s: %w", sessionID, err)
	}
	var session EdgeSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, false, fmt.Errorf("unmarshal edge session %s: %w", sessionID, err)
	}
	return &session, true, nil
}

func (s *RedisStore) loadExecution(ctx context.Context, executionID string) (*AgentExecution, bool, error) {
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return nil, false, nil
	}
	raw, err := s.client.Get(ctx, edgeExecutionKey(executionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get agent execution %s: %w", executionID, err)
	}
	var execution AgentExecution
	if err := json.Unmarshal(raw, &execution); err != nil {
		return nil, false, fmt.Errorf("unmarshal agent execution %s: %w", executionID, err)
	}
	return &execution, true, nil
}

func (s *RedisStore) listZSetIDs(ctx context.Context, indexKey, rawCursor, kind string, limit int) ([]string, string, error) {
	cursor, hasCursor, err := parseZSetStoreCursor(rawCursor, kind)
	if err != nil {
		return nil, "", err
	}
	var rows []redis.Z
	if !hasCursor {
		rows, err = s.client.ZRevRangeWithScores(ctx, indexKey, 0, int64(limit)).Result()
		if err != nil {
			return nil, "", err
		}
	} else {
		rows, err = s.listZSetRowsAfterCursor(ctx, indexKey, cursor, limit)
		if err != nil {
			return nil, "", err
		}
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, zSetMemberString(row.Member))
	}
	nextCursor := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		id := zSetMemberString(last.Member)
		nextCursor = encodeStoreCursor(storeCursor{
			Version: storeCursorVersion,
			Kind:    kind,
			Score:   last.Score,
			ID:      id,
		})
	}
	return ids, nextCursor, nil
}

func (s *RedisStore) listZSetRowsAfterCursor(ctx context.Context, indexKey string, cursor storeCursor, limit int) ([]redis.Z, error) {
	rank, err := s.client.ZRevRank(ctx, indexKey, cursor.ID).Result()
	if err == nil {
		score, err := s.client.ZScore(ctx, indexKey, cursor.ID).Result()
		if err == nil && score == cursor.Score {
			return s.client.ZRevRangeWithScores(ctx, indexKey, int64(rank)+1, int64(rank)+int64(limit)+1).Result()
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		return s.listZSetRowsAfterMissingCursor(ctx, indexKey, cursor, limit)
	}
	if !errors.Is(err, redis.Nil) {
		return nil, err
	}
	return s.listZSetRowsAfterMissingCursor(ctx, indexKey, cursor, limit)
}

func (s *RedisStore) listZSetRowsAfterMissingCursor(ctx context.Context, indexKey string, cursor storeCursor, limit int) ([]redis.Z, error) {
	rows := make([]redis.Z, 0, limit+1)
	if err := s.appendSameScoreRowsAfterMissingCursorDesc(ctx, indexKey, cursor, limit+1, &rows); err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return rows, nil
	}
	lowerRows, err := s.client.ZRevRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
		Max:   "(" + formatStoreScore(cursor.Score),
		Min:   "-inf",
		Count: int64(limit + 1 - len(rows)),
	}).Result()
	if err != nil {
		return nil, err
	}
	rows = append(rows, lowerRows...)
	return rows, nil
}

func (s *RedisStore) appendSameScoreRowsAfterMissingCursorDesc(ctx context.Context, indexKey string, cursor storeCursor, target int, rows *[]redis.Z) error {
	for offset := int64(0); len(*rows) < target; {
		sameScoreRows, err := s.client.ZRevRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
			Max:    formatStoreScore(cursor.Score),
			Min:    formatStoreScore(cursor.Score),
			Offset: offset,
			Count:  int64(maxStorePageLimit),
		}).Result()
		if err != nil {
			return err
		}
		if len(sameScoreRows) == 0 {
			break
		}
		for _, row := range sameScoreRows {
			id := zSetMemberString(row.Member)
			if id < cursor.ID {
				*rows = append(*rows, row)
				if len(*rows) == target {
					return nil
				}
			}
		}
		offset += int64(len(sameScoreRows))
		if len(sameScoreRows) < maxStorePageLimit {
			break
		}
	}
	return nil
}

func (s *RedisStore) listSessionEventIndexRows(ctx context.Context, indexKey string, cursor storeCursor, hasCursor bool, limit int) ([]redis.Z, error) {
	if !hasCursor {
		return s.client.ZRangeWithScores(ctx, indexKey, 0, int64(limit)-1).Result()
	}
	rank, err := s.client.ZRank(ctx, indexKey, cursor.ID).Result()
	if err == nil {
		score, err := s.client.ZScore(ctx, indexKey, cursor.ID).Result()
		if err == nil && score == cursor.Score {
			return s.client.ZRangeWithScores(ctx, indexKey, int64(rank)+1, int64(rank)+int64(limit)).Result()
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		return s.listSessionEventRowsAfterMissingCursor(ctx, indexKey, cursor, limit)
	}
	if !errors.Is(err, redis.Nil) {
		return nil, err
	}
	return s.listSessionEventRowsAfterMissingCursor(ctx, indexKey, cursor, limit)
}

func (s *RedisStore) listSessionEventRowsAfterMissingCursor(ctx context.Context, indexKey string, cursor storeCursor, limit int) ([]redis.Z, error) {
	rows := make([]redis.Z, 0, limit)
	if err := s.appendSameScoreRowsAfterMissingCursorAsc(ctx, indexKey, cursor, limit, &rows); err != nil {
		return nil, err
	}
	if len(rows) == limit {
		return rows, nil
	}
	higherRows, err := s.client.ZRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
		Min:   "(" + formatStoreScore(cursor.Score),
		Max:   "+inf",
		Count: int64(limit - len(rows)),
	}).Result()
	if err != nil {
		return nil, err
	}
	rows = append(rows, higherRows...)
	return rows, nil
}

func (s *RedisStore) appendSameScoreRowsAfterMissingCursorAsc(ctx context.Context, indexKey string, cursor storeCursor, target int, rows *[]redis.Z) error {
	for offset := int64(0); len(*rows) < target; {
		sameScoreRows, err := s.client.ZRangeByScoreWithScores(ctx, indexKey, &redis.ZRangeBy{
			Min:    formatStoreScore(cursor.Score),
			Max:    formatStoreScore(cursor.Score),
			Offset: offset,
			Count:  int64(maxStorePageLimit),
		}).Result()
		if err != nil {
			return err
		}
		if len(sameScoreRows) == 0 {
			break
		}
		for _, row := range sameScoreRows {
			id := zSetMemberString(row.Member)
			if id > cursor.ID {
				*rows = append(*rows, row)
				if len(*rows) == target {
					return nil
				}
			}
		}
		offset += int64(len(sameScoreRows))
		if len(sameScoreRows) < maxStorePageLimit {
			break
		}
	}
	return nil
}

func parseZSetStoreCursor(raw, kind string) (storeCursor, bool, error) {
	cursor, err := parseOpaqueStoreCursor(raw, kind)
	if err != nil {
		return storeCursor{}, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return cursor, false, nil
	}
	if cursor.ID == "" {
		return storeCursor{}, false, fmt.Errorf("invalid cursor")
	}
	return cursor, true, nil
}

func parseExecutionEventCursor(raw, executionID string) (storeCursor, bool, error) {
	cursor, err := parseOpaqueStoreCursor(raw, "events")
	if err != nil {
		return storeCursor{}, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return cursor, false, nil
	}
	if cursor.Scope != "execution" || cursor.ExecutionID != executionID || cursor.ID == "" || cursor.Score < 0 {
		return storeCursor{}, false, fmt.Errorf("invalid cursor")
	}
	return cursor, true, nil
}

func parseSessionIndexEventCursor(raw string) (storeCursor, bool, error) {
	cursor, err := parseOpaqueStoreCursor(raw, "events")
	if err != nil {
		return storeCursor{}, false, err
	}
	if strings.TrimSpace(raw) == "" {
		return cursor, false, nil
	}
	if cursor.Scope != "session_index" || cursor.ID == "" || cursor.Score < 0 {
		return storeCursor{}, false, fmt.Errorf("invalid cursor")
	}
	return cursor, true, nil
}

func parseOpaqueStoreCursor(raw, kind string) (storeCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return storeCursor{Version: storeCursorVersion, Kind: kind}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return storeCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor storeCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return storeCursor{}, fmt.Errorf("invalid cursor")
	}
	if cursor.Version != storeCursorVersion || cursor.Kind != kind {
		return storeCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func parseApprovalOffsetCursor(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return offset, nil
}

func encodeStoreCursor(cursor storeCursor) string {
	cursor.Version = storeCursorVersion
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func cursorFromZSetRow(kind string, row redis.Z) storeCursor {
	return storeCursor{
		Kind:  kind,
		Scope: "session_index",
		Score: row.Score,
		ID:    zSetMemberString(row.Member),
	}
}

func sessionEventIndexMember(event AgentActionEvent) string {
	return strings.Join([]string{
		event.ExecutionID,
		fmt.Sprintf("%020d", event.Seq),
		event.EventID,
	}, "\x00")
}

func parseSessionEventIndexMember(member any) (sessionEventRef, error) {
	parts := strings.SplitN(zSetMemberString(member), "\x00", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return sessionEventRef{}, fmt.Errorf("invalid session event index member")
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil || seq <= 0 {
		return sessionEventRef{}, fmt.Errorf("invalid session event index member")
	}
	return sessionEventRef{
		ExecutionID: parts[0],
		Seq:         seq,
		EventID:     parts[2],
	}, nil
}

func loadEventByIDInTx(ctx context.Context, tx *redis.Tx, executionID, eventID string) (AgentActionEvent, bool, error) {
	rawSeq, err := tx.HGet(ctx, edgeEventIDIndexKey(executionID), eventID).Result()
	if errors.Is(err, redis.Nil) {
		return AgentActionEvent{}, false, nil
	}
	if err != nil {
		return AgentActionEvent{}, false, fmt.Errorf("load event_id index for execution %s: %w", executionID, err)
	}
	seq, err := strconv.Atoi(rawSeq)
	if err != nil || seq <= 0 {
		return AgentActionEvent{}, false, fmt.Errorf("invalid event_id index for execution %s event %s", executionID, eventID)
	}
	raw, err := tx.LIndex(ctx, edgeEventsKey(executionID), int64(seq-1)).Result()
	if errors.Is(err, redis.Nil) {
		return AgentActionEvent{}, false, fmt.Errorf("stale event_id index for execution %s event %s", executionID, eventID)
	}
	if err != nil {
		return AgentActionEvent{}, false, fmt.Errorf("load indexed event %s[%d]: %w", executionID, seq-1, err)
	}
	event, err := decodeStoreEvent(raw, executionID, seq-1)
	if err != nil {
		return AgentActionEvent{}, false, err
	}
	if event.ExecutionID != executionID || event.EventID != eventID || event.Seq != seq {
		return AgentActionEvent{}, false, fmt.Errorf("event_id index mismatch for execution %s event %s", executionID, eventID)
	}
	return event, true, nil
}

func (s *RedisStore) loadEventByRef(ctx context.Context, ref sessionEventRef) (AgentActionEvent, bool, error) {
	if strings.TrimSpace(ref.ExecutionID) == "" || ref.Seq <= 0 || strings.TrimSpace(ref.EventID) == "" {
		return AgentActionEvent{}, false, fmt.Errorf("invalid session event reference")
	}
	raw, err := s.client.LIndex(ctx, edgeEventsKey(ref.ExecutionID), int64(ref.Seq-1)).Result()
	if errors.Is(err, redis.Nil) {
		return AgentActionEvent{}, false, nil
	}
	if err != nil {
		return AgentActionEvent{}, false, fmt.Errorf("load agent action event %s[%d]: %w", ref.ExecutionID, ref.Seq-1, err)
	}
	event, err := decodeStoreEvent(raw, ref.ExecutionID, ref.Seq-1)
	if err != nil {
		return AgentActionEvent{}, false, err
	}
	if event.ExecutionID != ref.ExecutionID || event.Seq != ref.Seq || event.EventID != ref.EventID {
		return AgentActionEvent{}, false, nil
	}
	return event, true, nil
}

func decodeStoreEvent(raw, executionID string, index int) (AgentActionEvent, error) {
	var event AgentActionEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return AgentActionEvent{}, fmt.Errorf("unmarshal agent action event %s[%d]: %w", executionID, index, err)
	}
	return event, nil
}

func eventMatchesQuery(event AgentActionEvent, query ListEventsQuery, executionID string) bool {
	if event.TenantID != strings.TrimSpace(query.TenantID) || event.ExecutionID != executionID {
		return false
	}
	if query.SessionID != "" && event.SessionID != strings.TrimSpace(query.SessionID) {
		return false
	}
	if query.Kind != "" && event.Kind != query.Kind {
		return false
	}
	if query.Decision != "" && event.Decision != query.Decision {
		return false
	}
	if !query.Since.IsZero() && event.Timestamp.Before(query.Since) {
		return false
	}
	if !query.Until.IsZero() && event.Timestamp.After(query.Until) {
		return false
	}
	return true
}

func formatStoreScore(score float64) string {
	return strconv.FormatFloat(score, 'f', -1, 64)
}

func zSetMemberString(member any) string {
	if id, ok := member.(string); ok {
		return id
	}
	return fmt.Sprint(member)
}

func isTerminalSessionStatus(status SessionStatus) bool {
	return status == SessionStatusEnded || status == SessionStatusFailed
}

func isTerminalExecutionStatus(status ExecutionStatus) bool {
	switch status {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusCancelled, ExecutionStatusTimeout:
		return true
	default:
		return false
	}
}
