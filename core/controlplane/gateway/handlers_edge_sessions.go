package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/google/uuid"
)

// edgeMaxExecutionsPerSession resolves the per-session AgentExecution cap.
// Reads CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION as int64; falls back to
// edgecore.DefaultMaxExecutionsPerSession when missing/invalid/<=0. The
// cap protects DeleteSession + dashboard timelines from unbounded
// per-session fanout (PR #243 senior review finding; EDGE-037).
func edgeMaxExecutionsPerSession() int64 {
	raw := strings.TrimSpace(os.Getenv("CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION"))
	if raw == "" {
		return int64(edgecore.DefaultMaxExecutionsPerSession)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return int64(edgecore.DefaultMaxExecutionsPerSession)
	}
	return n
}

type edgeSessionCreateRequest struct {
	TenantID          string                     `json:"tenant_id"`
	PrincipalID       string                     `json:"principal_id"`
	PrincipalType     edgecore.PrincipalType     `json:"principal_type"`
	AgentProduct      string                     `json:"agent_product"`
	AgentVersion      string                     `json:"agent_version"`
	Mode              edgecore.SessionMode       `json:"mode"`
	Repo              string                     `json:"repo"`
	GitRemote         string                     `json:"git_remote"`
	GitBranch         string                     `json:"git_branch"`
	GitSHA            string                     `json:"git_sha"`
	CWD               string                     `json:"cwd"`
	HostID            string                     `json:"host_id"`
	DeviceID          string                     `json:"device_id"`
	TraceID           string                     `json:"trace_id"`
	WorkflowRunID     string                     `json:"workflow_run_id"`
	JobID             string                     `json:"job_id"`
	PolicySnapshot    string                     `json:"policy_snapshot"`
	EnforcementLayers edgecore.EnforcementLayers `json:"enforcement_layers"`
	PolicyMode        edgecore.PolicyMode        `json:"policy_mode"`
	Labels            edgecore.Labels            `json:"labels"`
}

type edgeSessionCreateResponse struct {
	SessionID                string                  `json:"session_id"`
	ExecutionID              string                  `json:"execution_id"`
	TraceID                  string                  `json:"trace_id"`
	PolicySnapshot           string                  `json:"policy_snapshot"`
	WorkflowOverrideSnapshot string                  `json:"workflow_override_snapshot,omitempty"`
	JobOverrideSnapshot      string                  `json:"job_override_snapshot,omitempty"`
	DashboardURL             string                  `json:"dashboard_url"`
	Session                  edgecore.EdgeSession    `json:"session"`
	Execution                edgecore.AgentExecution `json:"execution"`
}

type edgeSessionPageResponse struct {
	Items      []edgecore.EdgeSession `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

type edgeExecutionPageResponse struct {
	Items      []edgecore.AgentExecution `json:"items"`
	NextCursor string                    `json:"next_cursor"`
}

type edgeHeartbeatResponse struct {
	SessionID      string `json:"session_id"`
	HeartbeatAlive bool   `json:"heartbeat_alive"`
}

type edgeEndSessionRequest struct {
	Status  edgecore.SessionStatus `json:"status"`
	EndedAt *time.Time             `json:"ended_at"`
}

type edgeExecutionCreateRequest struct {
	TenantID       string                 `json:"tenant_id"`
	SessionID      string                 `json:"session_id"`
	Adapter        edgecore.AgentAdapter  `json:"adapter"`
	Mode           edgecore.ExecutionMode `json:"mode"`
	WorkflowRunID  string                 `json:"workflow_run_id"`
	StepID         string                 `json:"step_id"`
	JobID          string                 `json:"job_id"`
	Attempt        int                    `json:"attempt"`
	TraceID        string                 `json:"trace_id"`
	WorkerID       string                 `json:"worker_id"`
	PolicySnapshot string                 `json:"policy_snapshot"`
	Labels         edgecore.Labels        `json:"labels"`
}

type edgeEndExecutionRequest struct {
	Status  edgecore.ExecutionStatus `json:"status"`
	EndedAt *time.Time               `json:"ended_at"`
}

func (s *server) handleCreateEdgeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}

	var req edgeSessionCreateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid edge session request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return
	}

	// Resolve principal from auth context. Edge evidence must not trust a
	// client-supplied body principal, so a user-role API key cannot create a
	// session claiming any other principal in its tenant.
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	principalID = strings.TrimSpace(principalID)

	// EDGE-060 — idempotency hash is computed AFTER tenant/principal
	// override (and using the request body as the normalized payload)
	// so a malicious client cannot reuse another tenant's key by
	// flipping the body principal_id. The request body itself is the
	// hash input — UUIDs are server-generated, so they're not in the
	// hash and a retry with the same body lands on the same record.
	normalizedReq := req
	normalizedReq.TenantID = tenantID
	normalizedReq.PrincipalID = principalID
	idempotencyReq, idempotent, handled := s.prepareEdgeIdempotencyRequest(w, r, tenantID, edgeSessionCreateEndpoint, normalizedReq)
	if handled {
		return
	}
	if idempotent {
		s.applyEdgeIdempotency(w, r, store, idempotencyReq,
			func() (edgeIdempotentWriteResult, error) {
				return s.executeCreateEdgeSession(r, store, req, tenantID, principalID)
			},
			func(err error) {
				writeCreateEdgeSessionDomainError(w, r, err)
			})
		return
	}

	result, err := s.executeCreateEdgeSession(r, store, req, tenantID, principalID)
	if err != nil {
		writeCreateEdgeSessionDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", result.ContentType)
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}

// executeCreateEdgeSession is the body of handleCreateEdgeSession factored
// out for idempotency-wrapping reuse. Returns the marshalled response on
// success and a domain error on failure (mapped to the wire envelope by
// writeCreateEdgeSessionDomainError so the idempotent + non-idempotent
// paths emit identical responses).
func (s *server) executeCreateEdgeSession(r *http.Request, store edgecore.Store, req edgeSessionCreateRequest, tenantID, principalID string) (edgeIdempotentWriteResult, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	executionID := uuid.NewString()
	traceID := strings.TrimSpace(req.TraceID)
	if traceID == "" {
		traceID = uuid.NewString()
	}
	policySnapshot := strings.TrimSpace(req.PolicySnapshot)
	principalType := req.PrincipalType
	if principalType == "" {
		principalType = edgecore.PrincipalTypeUnknown
	}
	mode := req.Mode
	if mode == "" {
		mode = edgecore.SessionModeLocalDev
	}
	policyMode := req.PolicyMode
	if policyMode == "" {
		policyMode = edgecore.PolicyModeObserve
	}
	redacted, err := redactEdgeSessionCreateRequest(req)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
	}
	traceID, err = redacted.String(traceID)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
	}
	policySnapshot, err = redacted.String(policySnapshot)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
	}
	redactedPrincipalID, err := redacted.String(principalID)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
	}
	attachmentID := edgecore.SessionPolicyAttachmentID(sessionID)
	sessionLabels := edgecore.WithPolicyAttachmentLabel(redacted.Labels, attachmentID)
	executionLabels := edgecore.WithPolicyAttachmentLabel(redacted.Labels, attachmentID)
	workflowOverrideSnapshot, jobOverrideSnapshot := edgeEvaluateScopeSnapshots(policySnapshot, sessionLabels)
	if workflowOverrideSnapshot == "" {
		workflowOverrideSnapshot = edgeTierSnapshot(policySnapshot, "workflow", redacted.WorkflowRunID)
	}

	session := edgecore.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       redactedPrincipalID,
		PrincipalType:     principalType,
		AgentProduct:      redacted.AgentProduct,
		AgentVersion:      redacted.AgentVersion,
		Mode:              mode,
		Repo:              redacted.Repo,
		GitRemote:         redacted.GitRemote,
		GitBranch:         redacted.GitBranch,
		GitSHA:            redacted.GitSHA,
		CWD:               redacted.CWD,
		HostID:            redacted.HostID,
		DeviceID:          redacted.DeviceID,
		TraceID:           traceID,
		WorkflowRunID:     redacted.WorkflowRunID,
		JobID:             redacted.JobID,
		PolicySnapshot:    policySnapshot,
		EnforcementLayers: redacted.EnforcementLayers,
		PolicyMode:        policyMode,
		Status:            edgecore.SessionStatusRunning,
		RiskSummary: edgecore.RiskSummary{
			MaxRisk: edgecore.RiskLevelLow,
		},
		StartedAt: now,
		Labels:    sessionLabels,
	}
	if err := session.Validate(); err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
	}

	execution := edgecore.AgentExecution{
		ExecutionID:    executionID,
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        edgecore.AdapterClaudeCodeHook,
		Mode:           edgecore.ExecutionMode(session.Mode),
		WorkflowRunID:  session.WorkflowRunID,
		JobID:          session.JobID,
		TraceID:        traceID,
		PolicySnapshot: policySnapshot,
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      now,
		Labels:         executionLabels,
	}
	if err := execution.Validate(); err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
	}

	if err := store.CreateSession(r.Context(), session); err != nil {
		if isEdgeValidationError(err) {
			return edgeIdempotentWriteResult{}, edgeCreateSessionInvalidErr{wrapped: err}
		}
		return edgeIdempotentWriteResult{}, edgeCreateSessionInternalErr{op: "create edge session", wrapped: err}
	}
	if err := store.CreateExecution(r.Context(), execution); err != nil {
		s.cleanupFailedEdgeSessionCreate(r, tenantID, sessionID)
		if errors.Is(err, edgecore.ErrParentSessionTerminal) {
			return edgeIdempotentWriteResult{}, err
		}
		if errors.Is(err, edgecore.ErrNotFound) || isEdgeValidationError(err) {
			return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
		}
		return edgeIdempotentWriteResult{}, edgeCreateSessionInternalErr{op: "create initial edge execution", wrapped: err}
	}
	if err := store.TouchHeartbeat(r.Context(), tenantID, sessionID); err != nil {
		s.cleanupFailedEdgeSessionCreate(r, tenantID, sessionID)
		return edgeIdempotentWriteResult{}, edgeCreateSessionInternalErr{op: "touch edge session heartbeat", wrapped: err}
	}

	// EDGE-014 step-10: emit best-effort audit events for the session and
	// initial execution lifecycle. SendSIEMEvent is nil-safe and panic-
	// recovering — audit pipeline failures must not change the response.
	edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForSessionStarted(session))
	edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForExecutionStarted(execution))

	body, err := jsonMarshalForEdgeIdempotency(edgeSessionCreateResponse{
		SessionID:                sessionID,
		ExecutionID:              executionID,
		TraceID:                  traceID,
		PolicySnapshot:           policySnapshot,
		WorkflowOverrideSnapshot: workflowOverrideSnapshot,
		JobOverrideSnapshot:      jobOverrideSnapshot,
		DashboardURL:             "/edge/sessions/" + sessionID,
		Session:                  session,
		Execution:                execution,
	})
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateSessionInternalErr{op: "marshal edge session response", wrapped: err}
	}
	return edgeIdempotentWriteResult{
		StatusCode:  http.StatusCreated,
		ContentType: "application/json",
		Body:        body,
	}, nil
}

// edgeCreateSessionInvalidErr signals a 400-shape validation failure
// from inside executeCreateEdgeSession. writeCreateEdgeSessionDomainError
// maps it to the wire envelope.
type edgeCreateSessionInvalidErr struct{ wrapped error }

func (e edgeCreateSessionInvalidErr) Error() string {
	if e.wrapped == nil {
		return "invalid edge session request"
	}
	return "invalid edge session request: " + e.wrapped.Error()
}
func (e edgeCreateSessionInvalidErr) Unwrap() error { return e.wrapped }

// edgeCreateExecutionInvalidErr is the same shape for the initial
// execution validation failures.
type edgeCreateExecutionInvalidErr struct{ wrapped error }

func (e edgeCreateExecutionInvalidErr) Error() string {
	if e.wrapped == nil {
		return "invalid edge execution request"
	}
	return "invalid edge execution request: " + e.wrapped.Error()
}
func (e edgeCreateExecutionInvalidErr) Unwrap() error { return e.wrapped }

// edgeCreateSessionInternalErr signals a 500-shape internal failure
// (Redis outage, marshal failure, etc).
type edgeCreateSessionInternalErr struct {
	op      string
	wrapped error
}

func (e edgeCreateSessionInternalErr) Error() string {
	if e.wrapped == nil {
		return e.op
	}
	return e.op + ": " + e.wrapped.Error()
}
func (e edgeCreateSessionInternalErr) Unwrap() error { return e.wrapped }

// writeCreateEdgeSessionDomainError maps the typed errors above into the
// shared edge-error wire envelope. Both the idempotent and non-idempotent
// paths funnel through here so the responses are byte-identical.
func writeCreateEdgeSessionDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		writeEdgeInternalError(w, r, "create edge session", fmt.Errorf("nil error"))
		return
	}
	var sessionInvalid edgeCreateSessionInvalidErr
	if errors.As(err, &sessionInvalid) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session request", nil)
		return
	}
	var execInvalid edgeCreateExecutionInvalidErr
	if errors.As(err, &execInvalid) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}
	if errors.Is(err, edgecore.ErrParentSessionTerminal) {
		// EDGE-054 — parent session transitioned to terminal between the
		// initial GetSession read and the WATCH commit. Map to 409 so
		// callers can distinguish "session ended" from a 400 shape error.
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeSessionTerminal, "parent edge session is terminal", nil)
		return
	}
	var internal edgeCreateSessionInternalErr
	if errors.As(err, &internal) {
		writeEdgeInternalError(w, r, internal.op, internal.wrapped)
		return
	}
	writeEdgeInternalError(w, r, "create edge session", err)
}

// jsonMarshalForEdgeIdempotency wraps json.Marshal so the executeXxx
// helper can return an error-typed marshal failure rather than panicking.
func jsonMarshalForEdgeIdempotency(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (s *server) handleListEdgeSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	query := edgecore.ListSessionsQuery{
		TenantID:    tenantID,
		PrincipalID: strings.TrimSpace(r.URL.Query().Get("principal_id")),
		Cursor:      strings.TrimSpace(r.URL.Query().Get("cursor")),
		Limit:       edgeQueryLimit(r),
	}
	page, err := store.ListSessions(r.Context(), query)
	if err != nil {
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session query", nil)
			return
		}
		writeEdgeInternalError(w, r, "list edge sessions", err)
		return
	}
	writeJSON(w, edgeSessionPageResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleGetEdgeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	sessionID, ok := requireEdgePathParam(w, r, "session_id")
	if !ok {
		return
	}
	session, found, err := store.GetSession(r.Context(), tenantID, sessionID)
	if err != nil {
		writeEdgeInternalError(w, r, "get edge session", err)
		return
	}
	if !found || session == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
		return
	}
	writeJSON(w, session)
}

func (s *server) handleHeartbeatEdgeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	sessionID, ok := requireEdgePathParam(w, r, "session_id")
	if !ok {
		return
	}
	if err := store.TouchHeartbeat(r.Context(), tenantID, sessionID); err != nil {
		if errors.Is(err, edgecore.ErrNotFound) {
			writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
			return
		}
		writeEdgeInternalError(w, r, "touch edge session heartbeat", err)
		return
	}
	alive, err := store.HeartbeatAlive(r.Context(), tenantID, sessionID)
	if err != nil {
		writeEdgeInternalError(w, r, "read edge session heartbeat", err)
		return
	}
	writeJSON(w, edgeHeartbeatResponse{SessionID: sessionID, HeartbeatAlive: alive})
}

func (s *server) handleEndEdgeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	sessionID, ok := requireEdgePathParam(w, r, "session_id")
	if !ok {
		return
	}
	req := edgeEndSessionRequest{Status: edgecore.SessionStatusEnded}
	if r.Body != nil && r.Body != http.NoBody {
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid edge session end request")
			return
		}
	}
	status := req.Status
	if status == "" {
		status = edgecore.SessionStatusEnded
	}
	if status != edgecore.SessionStatusEnded && status != edgecore.SessionStatusFailed {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "session end status must be terminal", nil)
		return
	}
	endedAt := time.Now().UTC()
	if req.EndedAt != nil {
		endedAt = req.EndedAt.UTC()
	}
	ended, err := store.EndSession(r.Context(), tenantID, sessionID, endedAt, status)
	if err != nil {
		if errors.Is(err, edgecore.ErrNotFound) {
			writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
			return
		}
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session end request", nil)
			return
		}
		writeEdgeInternalError(w, r, "end edge session", err)
		return
	}
	// EDGE-014 step-10: emit best-effort session_ended audit event.
	if ended != nil {
		edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForSessionEnded(*ended))
	}
	writeJSON(w, ended)
}

func (s *server) handleCreateEdgeExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var req edgeExecutionCreateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid edge execution request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeMissingField, "session_id is required", nil)
		return
	}

	// EDGE-060 step 3 reopen #1 — idempotency hash AFTER tenant override
	// per EDGE-008.7 invariant. SessionID + ExecutionID lifecycle: the
	// returned execution_id is server-generated, so retries with the same
	// idempotency key replay the same execution_id without creating a
	// duplicate execution row that would burn against the per-session cap.
	normalizedReq := req
	normalizedReq.TenantID = tenantID
	normalizedReq.SessionID = sessionID
	idempotencyReq, idempotent, handled := s.prepareEdgeIdempotencyRequest(w, r, tenantID, edgeExecutionCreateEndpoint, normalizedReq)
	if handled {
		return
	}
	if idempotent {
		s.applyEdgeIdempotency(w, r, store, idempotencyReq,
			func() (edgeIdempotentWriteResult, error) {
				return s.executeCreateEdgeExecution(r, store, req, tenantID, sessionID)
			},
			func(err error) {
				writeCreateEdgeExecutionDomainError(w, r, err)
			})
		return
	}

	result, err := s.executeCreateEdgeExecution(r, store, req, tenantID, sessionID)
	if err != nil {
		writeCreateEdgeExecutionDomainError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", result.ContentType)
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}

// executeCreateEdgeExecution is the body of handleCreateEdgeExecution
// factored out for idempotency-wrapping reuse. Returns the marshalled
// response on success and a typed domain error on failure (mapped to
// the wire envelope by writeCreateEdgeExecutionDomainError).
//
// Per-session execution cap and parent-session lookup live INSIDE the
// helper so cached idempotent replays bypass them — a retry that
// originally succeeded must replay successfully even if the session
// has subsequently filled its execution cap or transitioned to
// terminal.
func (s *server) executeCreateEdgeExecution(r *http.Request, store edgecore.Store, req edgeExecutionCreateRequest, tenantID, sessionID string) (edgeIdempotentWriteResult, error) {
	parent, found, err := store.GetSession(r.Context(), tenantID, sessionID)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInternalErr{op: "load edge execution parent session", wrapped: err}
	}
	if !found || parent == nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionParentNotFoundErr{}
	}

	// Per-session execution cap (EDGE-037). Reject before redaction/validate so
	// pathological retry storms don't burn CPU on payload work that would never
	// land. ZCard is O(1) on the session->executions index. The cap is exclusive
	// (count >= maxExecutions rejects); this matches the count-current-then-
	// create-one semantics, so the cap is the maximum number of stored
	// executions.
	maxExecutions := edgeMaxExecutionsPerSession()
	executionCount, err := store.CountSessionExecutions(r.Context(), tenantID, sessionID)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInternalErr{op: "count edge session executions", wrapped: err}
	}
	if executionCount >= maxExecutions {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionCapExceededErr{limit: maxExecutions, current: executionCount}
	}

	adapter := req.Adapter
	if adapter == "" {
		adapter = edgecore.AdapterClaudeCodeHook
	}
	mode := req.Mode
	if mode == "" {
		mode = edgecore.ExecutionMode(parent.Mode)
	}
	traceID := strings.TrimSpace(req.TraceID)
	if traceID == "" {
		traceID = parent.TraceID
	}
	policySnapshot := strings.TrimSpace(req.PolicySnapshot)
	if policySnapshot == "" {
		policySnapshot = parent.PolicySnapshot
	}
	redacted, err := redactEdgeExecutionCreateRequest(req)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
	}
	traceID, err = redacted.String(traceID)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
	}
	policySnapshot, err = redacted.String(policySnapshot)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
	}

	execution := edgecore.AgentExecution{
		ExecutionID:    uuid.NewString(),
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        adapter,
		Mode:           mode,
		WorkflowRunID:  redacted.WorkflowRunID,
		StepID:         redacted.StepID,
		JobID:          redacted.JobID,
		Attempt:        req.Attempt,
		TraceID:        traceID,
		WorkerID:       redacted.WorkerID,
		PolicySnapshot: policySnapshot,
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      time.Now().UTC(),
		Labels:         redacted.Labels,
	}
	if err := execution.Validate(); err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInvalidErr{wrapped: err}
	}
	if err := store.CreateExecution(r.Context(), execution); err != nil {
		return edgeIdempotentWriteResult{}, err
	}
	// EDGE-014 step-10: emit best-effort execution_started audit event.
	edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForExecutionStarted(execution))
	body, err := json.Marshal(execution)
	if err != nil {
		return edgeIdempotentWriteResult{}, edgeCreateExecutionInternalErr{op: "marshal edge execution response", wrapped: err}
	}
	return edgeIdempotentWriteResult{
		StatusCode:  http.StatusCreated,
		ContentType: "application/json",
		Body:        body,
	}, nil
}

// edgeCreateExecutionInternalErr signals a 500-shape internal failure
// from inside executeCreateEdgeExecution.
type edgeCreateExecutionInternalErr struct {
	op      string
	wrapped error
}

func (e edgeCreateExecutionInternalErr) Error() string {
	if e.wrapped == nil {
		return e.op
	}
	return e.op + ": " + e.wrapped.Error()
}
func (e edgeCreateExecutionInternalErr) Unwrap() error { return e.wrapped }

// edgeCreateExecutionParentNotFoundErr signals a 404 — parent session
// is missing or cross-tenant. Distinct envelope from "execution
// validation failed" so callers can route correctly.
type edgeCreateExecutionParentNotFoundErr struct{}

func (edgeCreateExecutionParentNotFoundErr) Error() string {
	return "edge execution parent session not found"
}

// edgeCreateExecutionCapExceededErr signals a 429 — per-session cap.
type edgeCreateExecutionCapExceededErr struct {
	limit   int64
	current int64
}

func (e edgeCreateExecutionCapExceededErr) Error() string {
	return fmt.Sprintf("session has reached the maximum of %d executions; end the session or start a new one", e.limit)
}

// writeCreateEdgeExecutionDomainError maps the typed errors from
// executeCreateEdgeExecution into the shared edge-error wire envelope.
// Both the idempotent + non-idempotent paths funnel through here so
// the responses are byte-identical.
func writeCreateEdgeExecutionDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		writeEdgeInternalError(w, r, "create edge execution", fmt.Errorf("nil error"))
		return
	}
	var execInvalid edgeCreateExecutionInvalidErr
	if errors.As(err, &execInvalid) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}
	var parentMissing edgeCreateExecutionParentNotFoundErr
	if errors.As(err, &parentMissing) {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
		return
	}
	var capErr edgeCreateExecutionCapExceededErr
	if errors.As(err, &capErr) {
		writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeMaxExecutionsExceeded,
			capErr.Error(),
			map[string]any{"limit": capErr.limit, "current": capErr.current})
		return
	}
	if errors.Is(err, edgecore.ErrSessionExecutionFanoutExceeded) {
		writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeMaxExecutionsExceeded,
			"session has reached the maximum number of executions; end the session or start a new one", nil)
		return
	}
	if errors.Is(err, edgecore.ErrParentSessionTerminal) {
		// EDGE-054 — parent session is terminal; map to 409 so callers
		// can distinguish lifecycle violations from validation failures.
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeSessionTerminal, "parent edge session is terminal", nil)
		return
	}
	if errors.Is(err, edgecore.ErrNotFound) {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
		return
	}
	if isEdgeValidationError(err) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}
	var internal edgeCreateExecutionInternalErr
	if errors.As(err, &internal) {
		writeEdgeInternalError(w, r, internal.op, internal.wrapped)
		return
	}
	writeEdgeInternalError(w, r, "create edge execution", err)
}

func (s *server) handleGetEdgeExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	executionID, ok := requireEdgePathParam(w, r, "execution_id")
	if !ok {
		return
	}
	execution, found, err := store.GetExecution(r.Context(), tenantID, executionID)
	if err != nil {
		writeEdgeInternalError(w, r, "get edge execution", err)
		return
	}
	if !found || execution == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge execution not found", nil)
		return
	}
	writeJSON(w, execution)
}

func (s *server) handleListEdgeExecutions(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	query := edgeExecutionListQuery(r, tenantID)
	page, err := store.ListExecutions(r.Context(), query)
	if err != nil {
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution query", nil)
			return
		}
		writeEdgeInternalError(w, r, "list edge executions", err)
		return
	}
	writeJSON(w, edgeExecutionPageResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleEndEdgeExecution(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	executionID, ok := requireEdgePathParam(w, r, "execution_id")
	if !ok {
		return
	}
	req := edgeEndExecutionRequest{Status: edgecore.ExecutionStatusSucceeded}
	if r.Body != nil && r.Body != http.NoBody {
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid edge execution end request")
			return
		}
	}
	status := req.Status
	if status == "" {
		status = edgecore.ExecutionStatusSucceeded
	}
	if !isTerminalEdgeExecutionStatus(status) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "execution end status must be terminal", nil)
		return
	}
	endedAt := time.Now().UTC()
	if req.EndedAt != nil {
		endedAt = req.EndedAt.UTC()
	}
	ended, err := store.EndExecution(r.Context(), tenantID, executionID, endedAt, status)
	if err != nil {
		if errors.Is(err, edgecore.ErrNotFound) {
			writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge execution not found", nil)
			return
		}
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution end request", nil)
			return
		}
		writeEdgeInternalError(w, r, "end edge execution", err)
		return
	}
	// EDGE-014 step-10: emit best-effort execution_ended audit event.
	if ended != nil {
		edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForExecutionEnded(*ended))
	}
	writeJSON(w, ended)
}

func (s *server) edgeStoreOrUnavailable(w http.ResponseWriter, r *http.Request) edgecore.Store {
	if s == nil || isNilStore(s.edgeStore) {
		slog.Error("edge store unavailable", "method", r.Method, "path", r.URL.Path)
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "edge store unavailable", nil)
		return nil
	}
	return s.edgeStore
}

func (s *server) edgeTenantFromRequest(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	headerTenant := strings.TrimSpace(auth.HeaderValue(r, "X-Tenant-ID"))
	if headerTenant == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeTenantRequired, "X-Tenant-ID header is required", nil)
		return "", false
	}
	if strings.TrimSpace(requested) != "" && strings.TrimSpace(requested) != headerTenant {
		slog.Warn("edge tenant body/header mismatch", "method", r.Method, "path", r.URL.Path)
		writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeTenantMismatch, "tenant_id in body does not match X-Tenant-ID header", nil)
		return "", false
	}
	if err := s.requireTenantAccess(r, headerTenant); err != nil {
		slog.Warn("edge tenant access denied", "method", r.Method, "path", r.URL.Path, "error", err)
		writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeTenantAccessDenied, "tenant access denied", nil)
		return "", false
	}
	return headerTenant, true
}

func edgeQueryLimit(r *http.Request) int {
	if r == nil {
		return 0
	}
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func edgeExecutionListQuery(r *http.Request, tenantID string) edgecore.ListExecutionsQuery {
	values := r.URL.Query()
	return edgecore.ListExecutionsQuery{
		TenantID:      tenantID,
		SessionID:     strings.TrimSpace(values.Get("session_id")),
		JobID:         strings.TrimSpace(values.Get("job_id")),
		TraceID:       strings.TrimSpace(values.Get("trace_id")),
		WorkflowRunID: strings.TrimSpace(values.Get("workflow_run_id")),
		Cursor:        strings.TrimSpace(values.Get("cursor")),
		Limit:         edgeQueryLimit(r),
	}
}

type redactedEdgeSessionCreateRequest struct {
	PrincipalID       string
	AgentProduct      string
	AgentVersion      string
	Repo              string
	GitRemote         string
	GitBranch         string
	GitSHA            string
	CWD               string
	HostID            string
	DeviceID          string
	TraceID           string
	WorkflowRunID     string
	JobID             string
	PolicySnapshot    string
	EnforcementLayers edgecore.EnforcementLayers
	Labels            edgecore.Labels
}

func (r redactedEdgeSessionCreateRequest) String(value string) (string, error) {
	return redactEdgeString(value)
}

type redactedEdgeExecutionCreateRequest struct {
	WorkflowRunID  string
	StepID         string
	JobID          string
	TraceID        string
	WorkerID       string
	PolicySnapshot string
	Labels         edgecore.Labels
}

func (r redactedEdgeExecutionCreateRequest) String(value string) (string, error) {
	return redactEdgeString(value)
}

func redactEdgeSessionCreateRequest(req edgeSessionCreateRequest) (redactedEdgeSessionCreateRequest, error) {
	var out redactedEdgeSessionCreateRequest
	var err error
	if out.PrincipalID, err = redactEdgeString(req.PrincipalID); err != nil {
		return out, err
	}
	if out.AgentProduct, err = redactEdgeString(req.AgentProduct); err != nil {
		return out, err
	}
	if out.AgentVersion, err = redactEdgeString(req.AgentVersion); err != nil {
		return out, err
	}
	if out.Repo, err = redactEdgeString(req.Repo); err != nil {
		return out, err
	}
	if out.GitRemote, err = redactEdgeString(req.GitRemote); err != nil {
		return out, err
	}
	if out.GitBranch, err = redactEdgeString(req.GitBranch); err != nil {
		return out, err
	}
	if out.GitSHA, err = redactEdgeString(req.GitSHA); err != nil {
		return out, err
	}
	if out.CWD, err = redactEdgeString(req.CWD); err != nil {
		return out, err
	}
	if out.HostID, err = redactEdgeString(req.HostID); err != nil {
		return out, err
	}
	if out.DeviceID, err = redactEdgeString(req.DeviceID); err != nil {
		return out, err
	}
	if out.TraceID, err = redactEdgeString(req.TraceID); err != nil {
		return out, err
	}
	if out.WorkflowRunID, err = redactEdgeString(req.WorkflowRunID); err != nil {
		return out, err
	}
	if out.JobID, err = redactEdgeString(req.JobID); err != nil {
		return out, err
	}
	if out.PolicySnapshot, err = redactEdgeString(req.PolicySnapshot); err != nil {
		return out, err
	}
	if out.EnforcementLayers, err = redactEnforcementLayers(req.EnforcementLayers); err != nil {
		return out, err
	}
	if out.Labels, err = redactEdgeLabels(req.Labels); err != nil {
		return out, err
	}
	return out, nil
}

func redactEdgeExecutionCreateRequest(req edgeExecutionCreateRequest) (redactedEdgeExecutionCreateRequest, error) {
	var out redactedEdgeExecutionCreateRequest
	var err error
	if out.WorkflowRunID, err = redactEdgeString(req.WorkflowRunID); err != nil {
		return out, err
	}
	if out.StepID, err = redactEdgeString(req.StepID); err != nil {
		return out, err
	}
	if out.JobID, err = redactEdgeString(req.JobID); err != nil {
		return out, err
	}
	if out.TraceID, err = redactEdgeString(req.TraceID); err != nil {
		return out, err
	}
	if out.WorkerID, err = redactEdgeString(req.WorkerID); err != nil {
		return out, err
	}
	if out.PolicySnapshot, err = redactEdgeString(req.PolicySnapshot); err != nil {
		return out, err
	}
	if out.Labels, err = redactEdgeLabels(req.Labels); err != nil {
		return out, err
	}
	return out, nil
}

func redactEdgeString(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	result, err := edgecore.RedactValue(value, edgecore.RedactionOptions{HashMode: edgecore.RedactionHashNone})
	if err != nil {
		return "", err
	}
	if redacted, ok := result.Value.(string); ok {
		return strings.TrimSpace(redacted), nil
	}
	return strings.TrimSpace(fmt.Sprint(result.Value)), nil
}

func redactEdgeLabels(in edgecore.Labels) (edgecore.Labels, error) {
	if len(in) == 0 {
		return nil, nil
	}
	result, err := edgecore.RedactValue(map[string]string(in), edgecore.RedactionOptions{HashMode: edgecore.RedactionHashNone})
	if err != nil {
		return nil, err
	}
	values, ok := result.Value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("edge labels redaction returned %T", result.Value)
	}
	out := make(edgecore.Labels, len(in))
	for key, value := range values {
		redactedKey, err := redactEdgeString(key)
		if err != nil {
			return nil, err
		}
		out[redactedKey] = strings.TrimSpace(fmt.Sprint(value))
	}
	return out, nil
}

func redactEnforcementLayers(in edgecore.EnforcementLayers) (edgecore.EnforcementLayers, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(edgecore.EnforcementLayers, len(in))
	for key, value := range in {
		redactedKey, err := redactEdgeString(key)
		if err != nil {
			return nil, err
		}
		out[redactedKey] = value
	}
	return out, nil
}

func (s *server) cleanupFailedEdgeSessionCreate(r *http.Request, tenantID, sessionID string) {
	if s == nil || s.edgeStore == nil {
		return
	}
	if err := s.edgeStore.DeleteSession(r.Context(), tenantID, sessionID); err != nil {
		slog.Warn("edge session create cleanup failed",
			"error", err,
			"tenant_id", tenantID,
			"session_id", sessionID,
		)
	}
}

func isTerminalEdgeExecutionStatus(status edgecore.ExecutionStatus) bool {
	switch status {
	case edgecore.ExecutionStatusSucceeded, edgecore.ExecutionStatusFailed, edgecore.ExecutionStatusCancelled, edgecore.ExecutionStatusTimeout:
		return true
	default:
		return false
	}
}

// isEdgeValidationError reports whether the error originated from an Edge
// model/store validation failure that the gateway should map to
// 400 edge_invalid_request.
//
// EDGE-038: prefer the typed sentinel `edgecore.ErrValidation` set by
// validation.go's requireString/requireTime/validateOptionalEnd. The
// substring fallback covers producers that have not yet been wrapped with
// the sentinel; the wire shape is identical either way. Producers added
// after EDGE-038 should wrap with the sentinel so the substring fallback
// can eventually be deleted.
func isEdgeValidationError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, edgecore.ErrValidation) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "validate ") ||
		strings.Contains(msg, " is required") ||
		strings.Contains(msg, "must be") ||
		strings.Contains(msg, "unsafe value")
}
