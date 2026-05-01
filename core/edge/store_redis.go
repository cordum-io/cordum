package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultHeartbeatTTL  = 30 * time.Second
	defaultMaxEventBytes = 128 * 1024
	// maxSessionEventScan caps how many events loadEventsForSession is willing
	// to accumulate across all executions of a single session before stopping.
	// A multi-million-event session would otherwise OOM the gateway. Callers
	// that need to walk the full event history should page via execution-scoped
	// queries instead.
	maxSessionEventScan = 10000
)

// StoreOption customizes RedisStore behavior. Options are primarily used by
// tests to pin clock and safety limits without changing production defaults.
type StoreOption func(*RedisStore)

// RedisStore persists Edge evidence in Redis using the PRD edge:* keyspace.
type RedisStore struct {
	client        redis.UniversalClient
	now           func() time.Time
	heartbeatTTL  time.Duration
	maxEventBytes int
}

// NewRedisStoreFromClient returns a Redis-backed Edge store using an existing
// go-redis client. The caller owns closing the client.
func NewRedisStoreFromClient(client redis.UniversalClient, opts ...StoreOption) *RedisStore {
	s := &RedisStore{
		client:        client,
		now:           func() time.Time { return time.Now().UTC() },
		heartbeatTTL:  defaultHeartbeatTTL,
		maxEventBytes: defaultMaxEventBytes,
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

func edgeTenantIndexKey(tenantID string) string {
	return "edge:index:tenant:" + strings.TrimSpace(tenantID)
}

func edgePrincipalIndexKey(tenantID, principalID string) string {
	return "edge:index:principal:" + strings.TrimSpace(tenantID) + ":" + strings.TrimSpace(principalID)
}

func edgeJobIndexKey(jobID string) string {
	return "edge:index:job:" + strings.TrimSpace(jobID)
}

func edgeTraceIndexKey(traceID string) string {
	return "edge:index:trace:" + strings.TrimSpace(traceID)
}

func edgeRunIndexKey(workflowRunID string) string {
	return "edge:index:run:" + strings.TrimSpace(workflowRunID)
}

func edgeSessionExecutionsIndexKey(sessionID string) string {
	return "edge:index:session_executions:" + strings.TrimSpace(sessionID)
}

func edgeSessionHeartbeatKey(sessionID string) string {
	return "edge:session:heartbeat:" + strings.TrimSpace(sessionID)
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
				pipe.ZAdd(ctx, edgeTraceIndexKey(session.TraceID), redis.Z{Score: score, Member: session.SessionID})
			}
			if strings.TrimSpace(session.WorkflowRunID) != "" {
				pipe.ZAdd(ctx, edgeRunIndexKey(session.WorkflowRunID), redis.Z{Score: score, Member: session.SessionID})
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
	start, err := parseStoreCursor(query.Cursor)
	if err != nil {
		return SessionPage{}, err
	}
	limit := normalizeStoreLimit(query.Limit)
	// Fetch only the requested page (+1 sentinel) instead of the entire index.
	// Tenants with millions of sessions previously caused unbounded memory and
	// per-call Redis fan-out; bound to limit+1 so the request stays O(limit).
	ids, err := s.client.ZRevRange(ctx, indexKey, int64(start), int64(start+limit)).Result()
	if err != nil {
		return SessionPage{}, fmt.Errorf("list edge sessions index %s: %w", indexKey, err)
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
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
	page := SessionPage{Items: items}
	if hasMore {
		page.NextCursor = strconv.Itoa(start + limit)
	}
	return page, nil
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
		pipe.Del(ctx, edgeSessionKey(sessionID), edgeSessionHeartbeatKey(sessionID), edgeSessionExecutionsIndexKey(sessionID))
		pipe.ZRem(ctx, edgeTenantIndexKey(session.TenantID), sessionID)
		if strings.TrimSpace(session.PrincipalID) != "" {
			pipe.ZRem(ctx, edgePrincipalIndexKey(session.TenantID, session.PrincipalID), sessionID)
		}
		if strings.TrimSpace(session.TraceID) != "" {
			pipe.ZRem(ctx, edgeTraceIndexKey(session.TraceID), sessionID)
		}
		if strings.TrimSpace(session.WorkflowRunID) != "" {
			pipe.ZRem(ctx, edgeRunIndexKey(session.WorkflowRunID), sessionID)
		}
		for _, execution := range executions {
			pipe.Del(ctx, edgeExecutionKey(execution.ExecutionID), edgeEventsKey(execution.ExecutionID), edgeEventSeqKey(execution.ExecutionID))
			if strings.TrimSpace(execution.JobID) != "" {
				pipe.ZRem(ctx, edgeJobIndexKey(execution.JobID), execution.ExecutionID)
			}
			if strings.TrimSpace(execution.TraceID) != "" {
				pipe.ZRem(ctx, edgeTraceIndexKey(execution.TraceID), execution.ExecutionID)
			}
			if strings.TrimSpace(execution.WorkflowRunID) != "" {
				pipe.ZRem(ctx, edgeRunIndexKey(execution.WorkflowRunID), execution.ExecutionID)
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
	session, ok, err := s.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if !ok || session == nil {
		return fmt.Errorf("%w: edge session %s", ErrNotFound, strings.TrimSpace(sessionID))
	}
	// Reject heartbeats for sessions that are already terminal. EndSession
	// drops the heartbeat key in the same transaction (see above), but a
	// stray client/loop could still recreate it via TouchHeartbeat and make
	// HeartbeatAlive lie about an ended session. Refuse the write here.
	if session.EndedAt != nil || isTerminalSessionStatus(session.Status) {
		return fmt.Errorf("edge session %s is terminal; cannot touch heartbeat", session.SessionID)
	}
	value := s.now().UTC().Format(time.RFC3339Nano)
	if err := s.client.Set(ctx, edgeSessionHeartbeatKey(session.SessionID), value, s.heartbeatTTL).Err(); err != nil {
		return fmt.Errorf("touch edge session heartbeat %s: %w", session.SessionID, err)
	}
	return nil
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
				pipe.ZAdd(ctx, edgeTraceIndexKey(execution.TraceID), redis.Z{Score: score, Member: execution.ExecutionID})
			}
			if strings.TrimSpace(execution.WorkflowRunID) != "" {
				pipe.ZAdd(ctx, edgeRunIndexKey(execution.WorkflowRunID), redis.Z{Score: score, Member: execution.ExecutionID})
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
		indexKey = edgeTraceIndexKey(query.TraceID)
	case strings.TrimSpace(query.WorkflowRunID) != "":
		indexKey = edgeRunIndexKey(query.WorkflowRunID)
	default:
		return ExecutionPage{}, fmt.Errorf("execution list index is required")
	}
	start, err := parseStoreCursor(query.Cursor)
	if err != nil {
		return ExecutionPage{}, err
	}
	limit := normalizeStoreLimit(query.Limit)
	// Bounded ZRevRange — see ListSessions for rationale.
	ids, err := s.client.ZRevRange(ctx, indexKey, int64(start), int64(start+limit)).Result()
	if err != nil {
		return ExecutionPage{}, fmt.Errorf("list agent executions index %s: %w", indexKey, err)
	}
	hasMore := len(ids) > limit
	if hasMore {
		ids = ids[:limit]
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
	page := ExecutionPage{Items: items}
	if hasMore {
		page.NextCursor = strconv.Itoa(start + limit)
	}
	return page, nil
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

func (s *RedisStore) AppendEvents(ctx context.Context, events []AgentActionEvent) ([]AgentActionEvent, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return []AgentActionEvent{}, nil
	}

	type eventGroup struct {
		events    []int
		execution *AgentExecution
	}
	groups := make(map[string]*eventGroup)
	for i, event := range events {
		tenantID := strings.TrimSpace(event.TenantID)
		executionID := strings.TrimSpace(event.ExecutionID)
		if tenantID == "" || executionID == "" {
			return nil, fmt.Errorf("tenant_id and execution_id are required")
		}
		execution, ok, err := s.GetExecution(ctx, tenantID, executionID)
		if err != nil {
			return nil, fmt.Errorf("load event execution %s: %w", executionID, err)
		}
		if !ok || execution == nil {
			return nil, fmt.Errorf("%w: agent execution %s", ErrNotFound, executionID)
		}
		if execution.SessionID != strings.TrimSpace(event.SessionID) {
			return nil, fmt.Errorf("event session_id %s does not match execution session_id %s", event.SessionID, execution.SessionID)
		}
		group := groups[executionID]
		if group == nil {
			group = &eventGroup{execution: execution}
			groups[executionID] = group
		}
		group.events = append(group.events, i)
	}

	watchKeys := make([]string, 0, len(groups)*3)
	for executionID := range groups {
		// Watch the execution document too: a concurrent EndExecution must
		// invalidate this transaction so we never append events past a
		// terminal state. The seq key + list key alone do not catch a
		// status-only mutation.
		watchKeys = append(watchKeys, edgeEventSeqKey(executionID), edgeEventsKey(executionID), edgeExecutionKey(executionID))
	}
	appended := make([]AgentActionEvent, len(events))
	var err error
	err = s.client.Watch(ctx, func(tx *redis.Tx) error {
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
		payloadsByExecution := make(map[string][][]byte, len(groups))
		for executionID, group := range groups {
			lastSeq, err := tx.Get(ctx, edgeEventSeqKey(executionID)).Int()
			if errors.Is(err, redis.Nil) {
				lastSeq = 0
			} else if err != nil {
				return fmt.Errorf("read event seq for execution %s: %w", executionID, err)
			}
			payloads := make([][]byte, 0, len(group.events))
			for _, index := range group.events {
				next := events[index]
				if next.Seq == 0 {
					next.Seq = lastSeq + 1
				}
				if next.Seq != lastSeq+1 {
					return fmt.Errorf("event seq %d must be next after %d", next.Seq, lastSeq)
				}
				if err := next.Validate(); err != nil {
					return fmt.Errorf("validate agent action event %s: %w", next.EventID, err)
				}
				payload, err := json.Marshal(next)
				if err != nil {
					return fmt.Errorf("marshal agent action event %s: %w", next.EventID, err)
				}
				if len(payload) > s.maxEventBytes {
					return fmt.Errorf("agent action event %s JSON size %d exceeds max %d bytes", next.EventID, len(payload), s.maxEventBytes)
				}
				payloads = append(payloads, payload)
				appended[index] = next
				lastSeq = next.Seq
			}
			payloadsByExecution[executionID] = payloads
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			for executionID, group := range groups {
				payloads := payloadsByExecution[executionID]
				for _, payload := range payloads {
					pipe.RPush(ctx, edgeEventsKey(executionID), payload)
				}
				last := appended[group.events[len(group.events)-1]]
				pipe.Set(ctx, edgeEventSeqKey(executionID), last.Seq, 0)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("append agent action event batch: %w", err)
		}
		return nil
	}, watchKeys...)
	if errors.Is(err, redis.TxFailedErr) {
		return nil, fmt.Errorf("append agent action event batch conflict: %w", err)
	}
	if err != nil {
		return nil, err
	}
	return appended, nil
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
	var items []AgentActionEvent
	if executionID != "" {
		execution, ok, err := s.GetExecution(ctx, tenantID, executionID)
		if err != nil {
			return EventPage{}, err
		}
		if !ok || execution == nil {
			return EventPage{Items: []AgentActionEvent{}}, nil
		}
		items, err = s.loadEventsForExecution(ctx, query, executionID)
		if err != nil {
			return EventPage{}, err
		}
	} else {
		session, ok, err := s.GetSession(ctx, tenantID, sessionID)
		if err != nil {
			return EventPage{}, err
		}
		if !ok || session == nil {
			return EventPage{Items: []AgentActionEvent{}}, nil
		}
		items, err = s.loadEventsForSession(ctx, query, sessionID)
		if err != nil {
			return EventPage{}, err
		}
	}
	start, err := parseStoreCursor(query.Cursor)
	if err != nil {
		return EventPage{}, err
	}
	return pageEvents(items, start, normalizeStoreLimit(query.Limit)), nil
}

func (s *RedisStore) loadEventsForSession(ctx context.Context, query ListEventsQuery, sessionID string) ([]AgentActionEvent, error) {
	items := []AgentActionEvent{}
	cursor := ""
	for {
		page, err := s.ListExecutions(ctx, ListExecutionsQuery{
			TenantID:  query.TenantID,
			SessionID: sessionID,
			Cursor:    cursor,
			Limit:     maxStorePageLimit,
		})
		if err != nil {
			return nil, err
		}
		for _, execution := range page.Items {
			executionQuery := query
			executionQuery.SessionID = ""
			executionQuery.ExecutionID = execution.ExecutionID
			executionEvents, err := s.loadEventsForExecution(ctx, executionQuery, execution.ExecutionID)
			if err != nil {
				return nil, err
			}
			items = append(items, executionEvents...)
			if len(items) >= maxSessionEventScan {
				break
			}
		}
		if page.NextCursor == "" || len(items) >= maxSessionEventScan {
			break
		}
		cursor = page.NextCursor
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].Timestamp.Equal(items[j].Timestamp) {
			return items[i].Timestamp.Before(items[j].Timestamp)
		}
		if items[i].ExecutionID != items[j].ExecutionID {
			return items[i].ExecutionID < items[j].ExecutionID
		}
		return items[i].Seq < items[j].Seq
	})
	if len(items) > maxSessionEventScan {
		items = items[:maxSessionEventScan]
	}
	return items, nil
}

func (s *RedisStore) loadEventsForExecution(ctx context.Context, query ListEventsQuery, executionID string) ([]AgentActionEvent, error) {
	rawEvents, err := s.client.LRange(ctx, edgeEventsKey(executionID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("list agent action events for execution %s: %w", executionID, err)
	}
	tenantID := strings.TrimSpace(query.TenantID)
	items := make([]AgentActionEvent, 0, len(rawEvents))
	for i, raw := range rawEvents {
		var event AgentActionEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("unmarshal agent action event %s[%d]: %w", executionID, i, err)
		}
		if event.TenantID != tenantID || event.ExecutionID != executionID {
			continue
		}
		if query.SessionID != "" && event.SessionID != strings.TrimSpace(query.SessionID) {
			continue
		}
		if query.Kind != "" && event.Kind != query.Kind {
			continue
		}
		if query.Decision != "" && event.Decision != query.Decision {
			continue
		}
		if !query.Since.IsZero() && event.Timestamp.Before(query.Since) {
			continue
		}
		if !query.Until.IsZero() && event.Timestamp.After(query.Until) {
			continue
		}
		items = append(items, event)
	}
	return items, nil
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

func parseStoreCursor(raw string) (int, error) {
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

func pageEvents(items []AgentActionEvent, start, limit int) EventPage {
	if start >= len(items) {
		return EventPage{Items: []AgentActionEvent{}}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := EventPage{Items: append([]AgentActionEvent(nil), items[start:end]...)}
	if end < len(items) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page
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
