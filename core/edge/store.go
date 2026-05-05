package edge

import (
	"context"
	"errors"
	"time"
)

const (
	defaultStorePageLimit = 50
	maxStorePageLimit     = 200

	// DefaultMaxExecutionsPerSession bounds the number of AgentExecution rows
	// any single EdgeSession may accumulate. Edge sessions are evidence
	// streams: they grow monotonically until SessionEnd. Without a cap, a
	// pathological agent loop or buggy retry storm can fan a single session
	// out to thousands of executions, blowing up cleanup memory and dashboard
	// timelines. Operators can tune via CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION.
	DefaultMaxExecutionsPerSession = 100

	// DefaultMaxEventsPerExecution bounds the number of AgentActionEvent rows
	// any single AgentExecution may accumulate. Combined with
	// DefaultMaxExecutionsPerSession this keeps the worst-case session cleanup
	// fanout bounded at 500,000 events.
	DefaultMaxEventsPerExecution = 5000
)

// ErrSessionExecutionFanoutExceeded is returned by store / handler code when a
// CreateExecution call would push the session over its configured execution
// cap. Wrap with fmt.Errorf("%w: <human message>", Err...) so gateway handlers
// map it to 429 edge_max_executions_exceeded via errors.Is.
var ErrSessionExecutionFanoutExceeded = errors.New("edge session execution fanout exceeded")

// ErrExecutionEventCapExceeded is returned when an AppendEvents call would
// push a single AgentExecution over its configured event cap. Callers should
// surface this typed error to the agent rather than silently dropping events.
var ErrExecutionEventCapExceeded = errors.New("edge execution event cap exceeded")

// ErrSessionCleanupDeadlineExceeded is returned when DeleteSession reaches its
// bounded cleanup deadline before finishing. Cleanup is idempotent and a
// background continuation is scheduled when possible.
var ErrSessionCleanupDeadlineExceeded = errors.New("edge session cleanup deadline exceeded")

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

// ErrIdempotencyRecordExpired is returned when an idempotency record's
// CreatedAt is older than the max-in-flight window (EDGE-061: 7 days). A
// long-running flow that has held a pending reservation past the cap can
// no longer be completed or retried under the same key; the caller must
// generate a fresh idempotency key. Distinct from
// ErrIdempotencyWindowExpired which fires when the redis TTL has actually
// elapsed; ErrIdempotencyRecordExpired fires while the record is still
// present in redis but its age exceeds the policy bound.
var ErrIdempotencyRecordExpired = errors.New("edge idempotency: record exceeded max in-flight window")

// ErrParentSessionTerminal is returned by CreateExecution when the parent
// EdgeSession has transitioned to a terminal status (Ended/Failed) by the
// time the WATCH/MULTI/EXEC pipeline executes. EDGE-054 closed the TOCTOU
// window where EndSession could race ahead of CreateExecution between the
// initial GetSession read and the WATCH commit; the inside-TX re-validation
// returns this sentinel so callers can map it to a stable wire envelope
// (gateway handlers map to 409 edge_parent_session_terminal via errors.Is).
var ErrParentSessionTerminal = errors.New("edge store: parent session is terminal")

// EDGE-038 — Edge gateway/store error taxonomy.
//
// Sentinels at this boundary let gateway handlers map store/model failures to
// stable Edge wire envelopes via errors.Is/errors.As instead of substring-
// matching err.Error(). Concrete store and model code wraps its existing
// human-readable message via fmt.Errorf("%w: <message>", Err...) so:
//   - existing tests asserting strings.Contains(err.Error(), "...") keep
//     working (the wrapped message is preserved by %w),
//   - log lines stay byte-identical, and
//   - errors.Is(err, edge.ErrValidation) becomes the authoritative detector.
//
// Wire mapping (handlers_edge_*.go):
//   ErrValidation       → 400 edge_invalid_request
//   ErrInvalidCursor    → 400 edge_invalid_request
//   ErrRequestTooLarge  → 413 edge_request_too_large
//   ErrNotFound         → 404 edge_not_found (already wired)
//   ErrIdempotencyConflict       → 409 edge_idempotency_conflict
//   ErrIdempotencyPending        → 409 edge_idempotency_pending
//   ErrIdempotencyWindowExpired  → 409 edge_idempotency_window_expired

// ErrValidation is the sentinel for shape/required-field/range violations
// returned by Validate() implementations on Edge models, store-level argument
// checks, and any precondition failure that should map to 400 edge_invalid_request
// at the gateway. Wrap with fmt.Errorf("%w: <human message>", ErrValidation).
var ErrValidation = errors.New("edge validation")

// ErrInvalidCursor is the sentinel for cursor-format failures during paginated
// list operations. Distinct from ErrValidation because gateway handlers used to
// emit a slightly different copy ("invalid edge event query"); kept as its own
// sentinel so wire copy can stay distinct without string-matching.
var ErrInvalidCursor = errors.New("edge invalid cursor")

// ErrRequestTooLarge is the sentinel for size-cap violations on request bodies,
// JSON payloads, label/metadata maps, etc. Wrap with fmt.Errorf("%w: ...", ErrRequestTooLarge)
// so gateway handlers map to 413 edge_request_too_large via errors.Is.
var ErrRequestTooLarge = errors.New("edge request too large")

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
	// CountSessionExecutions returns the number of executions currently
	// recorded under (tenantID, sessionID). Used by gateway handlers to
	// enforce per-session execution caps without paginating the full list.
	// Returns 0 (not an error) when the session has no executions yet.
	CountSessionExecutions(ctx context.Context, tenantID, sessionID string) (int64, error)
	EndExecution(ctx context.Context, tenantID, executionID string, endedAt time.Time, status ExecutionStatus) (*AgentExecution, error)

	// AppendEvent appends a single event atomically and returns the persisted
	// event with its assigned monotonic Seq. Equivalent to AppendEvents with a
	// one-element slice; ordering and atomicity guarantees are identical.
	AppendEvent(ctx context.Context, event AgentActionEvent) (AgentActionEvent, error)
	// AppendEvents appends a batch of events atomically: either every event in
	// the batch is persisted or none. Events are committed in slice order and
	// receive contiguous monotonically increasing Seq values per execution.
	// The implementation must reject the entire batch if any execution is
	// missing, cross-tenant, or already in a terminal state.
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
