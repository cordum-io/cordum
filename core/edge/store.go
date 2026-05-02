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

// ErrIdempotencyConflict is returned when an idempotency key was already used
// for the same tenant/endpoint with a different normalized request hash.
var ErrIdempotencyConflict = errors.New("edge idempotency: request hash conflict")

// ErrIdempotencyPending is returned when a duplicate request observes an
// in-flight reservation that has not yet been completed with a replayable
// response.
var ErrIdempotencyPending = errors.New("edge idempotency: request pending")

// ErrIdempotencyWindowExpired is returned when an auto-seq retry arrives after
// the idempotency replay record expired but the logical event is already
// persisted. Callers must not append a duplicate event in this case.
var ErrIdempotencyWindowExpired = errors.New("edge idempotency: replay window expired")

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
	AppendEventsWithIdempotency(ctx context.Context, req EdgeIdempotencyRequest, events []AgentActionEvent, buildResponse EdgeIdempotencyResponseBuilder) (EdgeIdempotentAppendResult, error)
	ListEvents(ctx context.Context, query ListEventsQuery) (EventPage, error)
	ReserveIdempotency(ctx context.Context, req EdgeIdempotencyRequest) (EdgeIdempotencyReservation, error)
	CompleteIdempotency(ctx context.Context, req EdgeIdempotencyRequest, response EdgeIdempotencyResponse) (*EdgeIdempotencyRecord, error)
	ReleaseIdempotency(ctx context.Context, req EdgeIdempotencyRequest) error

	EnqueueApproval(ctx context.Context, req EdgeApprovalRequest) (*EdgeApproval, error)
	GetApproval(ctx context.Context, tenantID, approvalRef string) (*EdgeApproval, bool, error)
	ListApprovals(ctx context.Context, query ListApprovalsQuery) (ApprovalPage, error)
	ApproveApproval(ctx context.Context, req ApprovalResolution) (*EdgeApproval, error)
	RejectApproval(ctx context.Context, req ApprovalResolution) (*EdgeApproval, error)
	ClaimApproval(ctx context.Context, req ApprovalClaimRequest) (*EdgeApproval, bool, error)
	ExpireApprovals(ctx context.Context, tenantID string, now time.Time) (int, error)
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

// EdgeIdempotencyRequest identifies a retry-safe Edge API write. RequestHash
// must be computed from the normalized, redacted request shape and never from
// raw unredacted payload bytes.
type EdgeIdempotencyRequest struct {
	TenantID    string
	Endpoint    string
	Key         string
	RequestHash string
}

// EdgeIdempotencyState describes the result of reserving an idempotency key.
type EdgeIdempotencyState string

const (
	EdgeIdempotencyReserved  EdgeIdempotencyState = "reserved"
	EdgeIdempotencyReplay    EdgeIdempotencyState = "replay"
	EdgeIdempotencyPending   EdgeIdempotencyState = "pending"
	EdgeIdempotencyCompleted EdgeIdempotencyState = "completed"
)

// EdgeIdempotencyReservation is returned by ReserveIdempotency.
type EdgeIdempotencyReservation struct {
	State  EdgeIdempotencyState
	Record *EdgeIdempotencyRecord
}

// EdgeIdempotencyResponse is the bounded response snapshot stored for future
// same-key/same-request retries. ResponseBody must already be sanitized
// response JSON, not a raw request body.
type EdgeIdempotencyResponse struct {
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// EdgeIdempotencyRecord is persisted as the Edge-owned replay record. It stores
// only identity metadata, the normalized request hash, state, and bounded
// response metadata/body; it deliberately does not store the raw request body or
// raw client-provided idempotency key.
type EdgeIdempotencyRecord struct {
	TenantID    string                  `json:"tenant_id,omitempty"`
	Endpoint    string                  `json:"endpoint,omitempty"`
	RequestHash string                  `json:"request_hash"`
	Status      EdgeIdempotencyState    `json:"status"`
	Response    EdgeIdempotencyResponse `json:"response,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

// EdgeIdempotencyResponseBuilder builds the replay response after the store has
// assigned final event sequence numbers but before the atomic Redis write
// commits.
type EdgeIdempotencyResponseBuilder func([]AgentActionEvent) (EdgeIdempotencyResponse, error)

// EdgeIdempotentAppendResult is returned by RedisStore's atomic idempotent
// append primitive.
type EdgeIdempotentAppendResult struct {
	State  EdgeIdempotencyState
	Events []AgentActionEvent
	Record *EdgeIdempotencyRecord
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
