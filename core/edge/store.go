package edge

import (
	"context"
	"errors"
	"time"
)

const (
	defaultStorePageLimit = 50
	maxStorePageLimit     = 200
)

// ErrNotFound is returned by mutating store operations when the target record
// does not exist for the requested tenant. Read operations return ok=false
// instead, so API handlers can distinguish a clean miss from a Redis failure.
var ErrNotFound = errors.New("edge store: not found")

// Store persists EdgeSession, AgentExecution, and AgentActionEvent evidence.
// It is intentionally scoped to Edge records and must not mutate Scheduler Job
// state or workflow run state.
type Store interface {
	CreateSession(ctx context.Context, session EdgeSession) error
	GetSession(ctx context.Context, tenantID, sessionID string) (*EdgeSession, bool, error)
	ListSessions(ctx context.Context, query ListSessionsQuery) (SessionPage, error)
	EndSession(ctx context.Context, tenantID, sessionID string, endedAt time.Time, status SessionStatus) (*EdgeSession, error)
	DeleteSession(ctx context.Context, tenantID, sessionID string) error
	TouchHeartbeat(ctx context.Context, tenantID, sessionID string) error
	HeartbeatAlive(ctx context.Context, tenantID, sessionID string) (bool, error)

	CreateExecution(ctx context.Context, execution AgentExecution) error
	GetExecution(ctx context.Context, tenantID, executionID string) (*AgentExecution, bool, error)
	ListExecutions(ctx context.Context, query ListExecutionsQuery) (ExecutionPage, error)
	EndExecution(ctx context.Context, tenantID, executionID string, endedAt time.Time, status ExecutionStatus) (*AgentExecution, error)

	AppendEvent(ctx context.Context, event AgentActionEvent) (AgentActionEvent, error)
	AppendEvents(ctx context.Context, events []AgentActionEvent) ([]AgentActionEvent, error)
	ListEvents(ctx context.Context, query ListEventsQuery) (EventPage, error)
}

// ListSessionsQuery pages Edge sessions for one tenant. When PrincipalID is
// set, the principal index is used; otherwise the tenant index is used.
type ListSessionsQuery struct {
	TenantID    string
	PrincipalID string
	Cursor      string
	Limit       int
}

// SessionPage is one page of Edge sessions.
type SessionPage struct {
	Items      []EdgeSession
	NextCursor string
}

// ListExecutionsQuery pages AgentExecution records through one secondary
// index. SessionID, JobID, TraceID, and WorkflowRunID are mutually exclusive in
// caller intent; if more than one is supplied the Redis implementation uses the
// most-specific order documented in its list method.
type ListExecutionsQuery struct {
	TenantID      string
	SessionID     string
	JobID         string
	TraceID       string
	WorkflowRunID string
	Cursor        string
	Limit         int
}

// ExecutionPage is one page of AgentExecution records.
type ExecutionPage struct {
	Items      []AgentExecution
	NextCursor string
}

// ListEventsQuery pages AgentActionEvent records for one execution in
// increasing sequence order. Kind and Decision filters are applied without
// reordering.
type ListEventsQuery struct {
	TenantID    string
	SessionID   string
	ExecutionID string
	Cursor      string
	Limit       int
	Kind        EventKind
	Decision    EdgeDecision
	Since       time.Time
	Until       time.Time
}

// EventPage is one page of AgentActionEvent records.
type EventPage struct {
	Items      []AgentActionEvent
	NextCursor string
}

func normalizeStoreLimit(limit int) int {
	if limit <= 0 {
		return defaultStorePageLimit
	}
	if limit > maxStorePageLimit {
		return maxStorePageLimit
	}
	return limit
}
