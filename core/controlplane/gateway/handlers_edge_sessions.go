package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/google/uuid"
)

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
	SessionID      string                  `json:"session_id"`
	ExecutionID    string                  `json:"execution_id"`
	TraceID        string                  `json:"trace_id"`
	PolicySnapshot string                  `json:"policy_snapshot"`
	DashboardURL   string                  `json:"dashboard_url"`
	Session        edgecore.EdgeSession    `json:"session"`
	Execution      edgecore.AgentExecution `json:"execution"`
}

type edgeSessionPageResponse struct {
	Items      []edgecore.EdgeSession `json:"items"`
	NextCursor string                 `json:"next_cursor"`
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

	// Resolve principal from auth context. resolvePrincipal returns the
	// authenticated principal (or the requested one only if the auth provider
	// allows the impersonation), so a user-role API key cannot create a
	// session claiming any principal in its tenant.
	principalID, err := s.resolvePrincipal(r, req.PrincipalID)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	principalID = strings.TrimSpace(principalID)

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
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session request", nil)
		return
	}
	traceID = redacted.String(traceID)
	policySnapshot = redacted.String(policySnapshot)

	session := edgecore.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       redacted.String(principalID),
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
		PolicySnapshot:    redacted.String(policySnapshot),
		EnforcementLayers: redacted.EnforcementLayers,
		PolicyMode:        policyMode,
		Status:            edgecore.SessionStatusRunning,
		RiskSummary: edgecore.RiskSummary{
			MaxRisk: edgecore.RiskLevelLow,
		},
		StartedAt: now,
		Labels:    redacted.Labels,
	}
	if err := session.Validate(); err != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session request", nil)
		return
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
		Labels:         redacted.Labels,
	}
	if err := execution.Validate(); err != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}

	if err := store.CreateSession(r.Context(), session); err != nil {
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge session request", nil)
			return
		}
		writeEdgeInternalError(w, r, "create edge session", err)
		return
	}
	if err := store.CreateExecution(r.Context(), execution); err != nil {
		s.cleanupFailedEdgeSessionCreate(r, tenantID, sessionID)
		if errors.Is(err, edgecore.ErrNotFound) || isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
			return
		}
		writeEdgeInternalError(w, r, "create initial edge execution", err)
		return
	}
	if err := store.TouchHeartbeat(r.Context(), tenantID, sessionID); err != nil {
		s.cleanupFailedEdgeSessionCreate(r, tenantID, sessionID)
		writeEdgeInternalError(w, r, "touch edge session heartbeat", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, edgeSessionCreateResponse{
		SessionID:      sessionID,
		ExecutionID:    executionID,
		TraceID:        traceID,
		PolicySnapshot: policySnapshot,
		DashboardURL:   "/edge/sessions/" + sessionID,
		Session:        session,
		Execution:      execution,
	})
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
	parent, found, err := store.GetSession(r.Context(), tenantID, sessionID)
	if err != nil {
		writeEdgeInternalError(w, r, "load edge execution parent session", err)
		return
	}
	if !found || parent == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
		return
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
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}
	traceID = redacted.String(traceID)
	policySnapshot = redacted.String(policySnapshot)

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
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
		return
	}
	if err := store.CreateExecution(r.Context(), execution); err != nil {
		if errors.Is(err, edgecore.ErrNotFound) {
			writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
			return
		}
		if isEdgeValidationError(err) {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge execution request", nil)
			return
		}
		writeEdgeInternalError(w, r, "create edge execution", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, execution)
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
		writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeTenantRequired, "X-Tenant-ID header is required", nil)
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

func (r redactedEdgeSessionCreateRequest) String(value string) string {
	redacted, err := redactEdgeString(value)
	if err != nil {
		return ""
	}
	return redacted
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

func (r redactedEdgeExecutionCreateRequest) String(value string) string {
	redacted, err := redactEdgeString(value)
	if err != nil {
		return ""
	}
	return redacted
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

func isEdgeValidationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "validate ") ||
		strings.Contains(msg, " is required") ||
		strings.Contains(msg, "must be") ||
		strings.Contains(msg, "unsafe value")
}
