package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

const maxInlineRawEventPayloadBytes = 1024

type edgeEventWriteRequest struct {
	EventID          string                     `json:"event_id"`
	SessionID        string                     `json:"session_id"`
	ExecutionID      string                     `json:"execution_id"`
	TenantID         string                     `json:"tenant_id"`
	PrincipalID      string                     `json:"principal_id"`
	Seq              int                        `json:"seq"`
	Timestamp        time.Time                  `json:"ts"`
	Layer            edgecore.Layer             `json:"layer"`
	Kind             edgecore.EventKind         `json:"kind"`
	AgentProduct     string                     `json:"agent_product"`
	ToolName         string                     `json:"tool_name"`
	ToolUseID        string                     `json:"tool_use_id"`
	ActionName       string                     `json:"action_name"`
	Capability       string                     `json:"capability"`
	RiskTags         []string                   `json:"risk_tags"`
	InputRedacted    map[string]any             `json:"input_redacted"`
	InputHash        string                     `json:"input_hash"`
	Decision         edgecore.EdgeDecision      `json:"decision"`
	DecisionReason   string                     `json:"decision_reason"`
	RuleID           string                     `json:"rule_id"`
	PolicySnapshot   string                     `json:"policy_snapshot"`
	ApprovalRef      string                     `json:"approval_ref"`
	ArtifactPointers []edgecore.ArtifactPointer `json:"artifact_ptrs"`
	DurationMS       int                        `json:"duration_ms"`
	Status           edgecore.ActionStatus      `json:"status"`
	ErrorCode        string                     `json:"error_code"`
	ErrorMessage     string                     `json:"error_message"`
	Labels           edgecore.Labels            `json:"labels"`

	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResult    json.RawMessage `json:"tool_result"`
	RawInput      json.RawMessage `json:"raw_input"`
	RawTranscript json.RawMessage `json:"raw_transcript"`
	Transcript    json.RawMessage `json:"transcript"`
}

type edgeEventBatchTenantProbeRequest struct {
	Events []edgeEventWriteRequest `json:"events"`
}

type edgeEventBatchResponse struct {
	Items []edgecore.AgentActionEvent `json:"items"`
}

type edgeEventPageResponse struct {
	Items      []edgecore.AgentActionEvent `json:"items"`
	NextCursor string                      `json:"next_cursor"`
}

type edgeEventRequestError struct {
	status  int
	message string
}

func (e edgeEventRequestError) Error() string {
	return e.message
}

func (s *server) handleCreateEdgeEvent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var req edgeEventWriteRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid edge event request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return
	}
	event, err := normalizeEdgeEventRequest(req, tenantID)
	if err != nil {
		writeEdgeEventRequestError(w, err, "invalid edge event request")
		return
	}
	if err := validateEdgeEventParents(r.Context(), store, event); err != nil {
		writeEdgeEventStoreError(w, r, err, "validate edge event parents")
		return
	}
	appended, err := store.AppendEvent(r.Context(), event)
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "append edge event")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, appended)
}

func (s *server) handleCreateEdgeEventsBatch(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var req edgeEventBatchTenantProbeRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid edge event batch request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	if len(req.Events) == 0 {
		writeErrorJSON(w, http.StatusBadRequest, "edge event batch requires events")
		return
	}
	events := make([]edgecore.AgentActionEvent, 0, len(req.Events))
	for _, item := range req.Events {
		if requestedTenant := strings.TrimSpace(item.TenantID); requestedTenant != "" && requestedTenant != tenantID {
			writeForbidden(w, r, fmt.Errorf("edge tenant body/header mismatch"))
			return
		}
		event, err := normalizeEdgeEventRequest(item, tenantID)
		if err != nil {
			writeEdgeEventRequestError(w, err, "invalid edge event batch request")
			return
		}
		if err := validateEdgeEventParents(r.Context(), store, event); err != nil {
			writeEdgeEventStoreError(w, r, err, "validate edge event batch parents")
			return
		}
		events = append(events, event)
	}
	// RedisStore.AppendEvents appends the fully prevalidated batch in one
	// watched MULTI/EXEC transaction. This prevents later invalid items from
	// partially persisting earlier events; a concurrent writer may still cause a
	// conflict, which is surfaced as a safe 5xx by the shared store error mapper.
	appended, err := store.AppendEvents(r.Context(), events)
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "append edge event batch")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, edgeEventBatchResponse{Items: appended})
}

func (s *server) handleListEdgeSessionEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
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
	sessionID, ok := requirePathParam(w, r, "session_id")
	if !ok {
		return
	}
	if session, found, err := store.GetSession(r.Context(), tenantID, sessionID); err != nil {
		writeInternalError(w, r, "get edge event parent session", err)
		return
	} else if !found || session == nil {
		writeErrorJSON(w, http.StatusNotFound, "edge session not found")
		return
	}
	query, err := edgeEventListQueryFromRequest(r, tenantID)
	if err != nil {
		writeEdgeEventRequestError(w, err, "invalid edge event query")
		return
	}
	query.SessionID = sessionID
	page, err := store.ListEvents(r.Context(), query)
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "list edge session events")
		return
	}
	writeJSON(w, edgeEventPageResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleListEdgeExecutionEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
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
	executionID, ok := requirePathParam(w, r, "execution_id")
	if !ok {
		return
	}
	if execution, found, err := store.GetExecution(r.Context(), tenantID, executionID); err != nil {
		writeInternalError(w, r, "get edge event parent execution", err)
		return
	} else if !found || execution == nil {
		writeErrorJSON(w, http.StatusNotFound, "edge execution not found")
		return
	}
	query, err := edgeEventListQueryFromRequest(r, tenantID)
	if err != nil {
		writeEdgeEventRequestError(w, err, "invalid edge event query")
		return
	}
	query.ExecutionID = executionID
	page, err := store.ListEvents(r.Context(), query)
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "list edge execution events")
		return
	}
	writeJSON(w, edgeEventPageResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func edgeEventListQueryFromRequest(r *http.Request, tenantID string) (edgecore.ListEventsQuery, error) {
	query := edgecore.ListEventsQuery{
		TenantID: strings.TrimSpace(tenantID),
		Cursor:   strings.TrimSpace(r.URL.Query().Get("cursor")),
		Limit:    edgeQueryLimit(r),
		Kind:     edgecore.EventKind(strings.TrimSpace(r.URL.Query().Get("kind"))),
		Decision: edgecore.EdgeDecision(strings.TrimSpace(r.URL.Query().Get("decision"))),
	}
	if query.Decision != "" && !isValidEdgeDecision(query.Decision) {
		return edgecore.ListEventsQuery{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event query"}
	}
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		query.Since, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return edgecore.ListEventsQuery{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event query"}
		}
		query.Since = query.Since.UTC()
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("until")); raw != "" {
		query.Until, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return edgecore.ListEventsQuery{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event query"}
		}
		query.Until = query.Until.UTC()
	}
	if !query.Since.IsZero() && !query.Until.IsZero() && query.Until.Before(query.Since) {
		return edgecore.ListEventsQuery{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event query"}
	}
	return query, nil
}

func isValidEdgeDecision(value edgecore.EdgeDecision) bool {
	switch value {
	case edgecore.DecisionAllow, edgecore.DecisionDeny, edgecore.DecisionRequireApproval, edgecore.DecisionThrottle, edgecore.DecisionConstrain, edgecore.DecisionRecorded:
		return true
	default:
		return false
	}
}

func normalizeEdgeEventRequest(req edgeEventWriteRequest, tenantID string) (edgecore.AgentActionEvent, error) {
	if err := rejectRawEdgeEventPayload(req); err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	inputRedacted, inputHash, err := redactEdgeEventInput(req.InputRedacted, req.InputHash)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	riskTags, err := redactEdgeStringSlice(req.RiskTags)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	labels, err := redactEdgeLabels(req.Labels)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	event := edgecore.AgentActionEvent{
		EventID:          strings.TrimSpace(req.EventID),
		SessionID:        strings.TrimSpace(req.SessionID),
		ExecutionID:      strings.TrimSpace(req.ExecutionID),
		TenantID:         tenantID,
		PrincipalID:      mustRedactEdgeString(req.PrincipalID),
		Seq:              req.Seq,
		Timestamp:        req.Timestamp.UTC(),
		Layer:            req.Layer,
		Kind:             edgecore.EventKind(strings.TrimSpace(string(req.Kind))),
		AgentProduct:     mustRedactEdgeString(req.AgentProduct),
		ToolName:         mustRedactEdgeString(req.ToolName),
		ToolUseID:        mustRedactEdgeString(req.ToolUseID),
		ActionName:       mustRedactEdgeString(req.ActionName),
		Capability:       mustRedactEdgeString(req.Capability),
		RiskTags:         riskTags,
		InputRedacted:    inputRedacted,
		InputHash:        inputHash,
		Decision:         req.Decision,
		DecisionReason:   mustRedactEdgeString(req.DecisionReason),
		RuleID:           mustRedactEdgeString(req.RuleID),
		PolicySnapshot:   mustRedactEdgeString(req.PolicySnapshot),
		ApprovalRef:      mustRedactEdgeString(req.ApprovalRef),
		ArtifactPointers: req.ArtifactPointers,
		DurationMS:       req.DurationMS,
		Status:           req.Status,
		ErrorCode:        mustRedactEdgeString(req.ErrorCode),
		ErrorMessage:     mustRedactEdgeString(req.ErrorMessage),
		Labels:           labels,
	}
	if err := event.Validate(); err != nil {
		return edgecore.AgentActionEvent{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event request"}
	}
	if err := validateEdgeEventArtifactPointers(event); err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	return event, nil
}

func validateEdgeEventArtifactPointers(event edgecore.AgentActionEvent) error {
	for _, artifact := range event.ArtifactPointers {
		if strings.TrimSpace(artifact.TenantID) != event.TenantID ||
			strings.TrimSpace(artifact.SessionID) != event.SessionID ||
			strings.TrimSpace(artifact.ExecutionID) != event.ExecutionID ||
			strings.TrimSpace(artifact.EventID) != event.EventID {
			return edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event artifact pointer"}
		}
	}
	return nil
}

func rejectRawEdgeEventPayload(req edgeEventWriteRequest) error {
	for name, raw := range map[string]json.RawMessage{
		"tool_input":     req.ToolInput,
		"tool_result":    req.ToolResult,
		"raw_input":      req.RawInput,
		"raw_transcript": req.RawTranscript,
		"transcript":     req.Transcript,
	} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if len(raw) > maxInlineRawEventPayloadBytes {
			return edgeEventRequestError{status: http.StatusRequestEntityTooLarge, message: "large raw event payloads must use artifact_ptrs"}
		}
		return edgeEventRequestError{status: http.StatusBadRequest, message: fmt.Sprintf("%s must use input_redacted or artifact_ptrs", name)}
	}
	return nil
}

func redactEdgeEventInput(input map[string]any, providedHash string) (map[string]any, string, error) {
	inputHash, err := redactEdgeString(providedHash)
	if err != nil {
		return nil, "", err
	}
	if len(input) == 0 {
		return nil, inputHash, nil
	}
	if err := ensureEdgeInlineJSONSize("input_redacted", input, edgecore.MaxInputRedactedBytes); err != nil {
		return nil, "", err
	}
	result, err := edgecore.RedactValue(input, edgecore.RedactionOptions{HashMode: edgecore.RedactionHashBoth})
	if err != nil {
		return nil, "", err
	}
	redacted, ok := result.Value.(map[string]any)
	if !ok {
		return nil, "", edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event request"}
	}
	if err := ensureEdgeInlineJSONSize("input_redacted", redacted, edgecore.MaxInputRedactedBytes); err != nil {
		return nil, "", err
	}
	if result.OriginalHash != "" {
		inputHash = result.OriginalHash
	} else if result.RedactedHash != "" {
		inputHash = result.RedactedHash
	}
	return redacted, inputHash, nil
}

func ensureEdgeInlineJSONSize(field string, value any, maxBytes int) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event request"}
	}
	if len(payload) > maxBytes {
		return edgeEventRequestError{status: http.StatusRequestEntityTooLarge, message: field + " too large; use artifact_ptrs"}
	}
	return nil
}

func redactEdgeStringSlice(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		redacted, err := redactEdgeString(value)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(redacted) != "" {
			out = append(out, redacted)
		}
	}
	return out, nil
}

func mustRedactEdgeString(value string) string {
	redacted, err := redactEdgeString(value)
	if err != nil {
		return ""
	}
	return redacted
}

func validateEdgeEventParents(ctx context.Context, store edgecore.Store, event edgecore.AgentActionEvent) error {
	session, found, err := store.GetSession(ctx, event.TenantID, event.SessionID)
	if err != nil {
		return err
	}
	if !found || session == nil {
		return fmt.Errorf("%w: edge session", edgecore.ErrNotFound)
	}
	execution, found, err := store.GetExecution(ctx, event.TenantID, event.ExecutionID)
	if err != nil {
		return err
	}
	if !found || execution == nil {
		return fmt.Errorf("%w: edge execution", edgecore.ErrNotFound)
	}
	if execution.SessionID != event.SessionID {
		return edgeEventRequestError{status: http.StatusBadRequest, message: "event session_id does not match execution"}
	}
	return nil
}

func writeEdgeEventRequestError(w http.ResponseWriter, err error, fallback string) {
	var requestErr edgeEventRequestError
	if errors.As(err, &requestErr) {
		writeErrorJSON(w, requestErr.status, requestErr.message)
		return
	}
	writeErrorJSON(w, http.StatusBadRequest, fallback)
}

func writeEdgeEventStoreError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	var requestErr edgeEventRequestError
	if errors.As(err, &requestErr) {
		writeErrorJSON(w, requestErr.status, requestErr.message)
		return
	}
	if errors.Is(err, edgecore.ErrNotFound) {
		writeErrorJSON(w, http.StatusNotFound, "edge event parent not found")
		return
	}
	if strings.Contains(err.Error(), "invalid cursor") {
		writeErrorJSON(w, http.StatusBadRequest, "invalid edge event query")
		return
	}
	if isEdgeValidationError(err) {
		writeErrorJSON(w, http.StatusBadRequest, "invalid edge event request")
		return
	}
	if strings.Contains(err.Error(), "exceeds max") || strings.Contains(err.Error(), "too large") {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "edge event too large; use artifact_ptrs")
		return
	}
	writeInternalError(w, r, operation, err)
}
