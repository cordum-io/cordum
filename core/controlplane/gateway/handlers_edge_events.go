package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	maxInlineRawEventPayloadBytes     = 1024
	maxEdgeArtifactPointerURIBytes    = 2048
	maxEdgeIdempotencyKeyBytes        = 512
	edgeArtifactPointerErrorMessage   = "invalid edge event artifact pointer"
	edgeArtifactPointerSchemeArtifact = "artifact"
	edgeArtifactPointerSchemeEdge     = "edge-artifact"
	edgeEventCreateEndpoint           = "POST /api/v1/edge/events"
	edgeEventBatchEndpoint            = "POST /api/v1/edge/events/batch"
)

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
	Tier             string                     `json:"tier"`
	RuleTier         string                     `json:"rule_tier"`
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
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var req edgeEventWriteRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid edge event request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return
	}
	// Override client-supplied principal_id with the auth-context principal so
	// a user-role API key cannot write events claiming any principal in its
	// tenant. Mirrors handleEdgeEvaluate / handleSubmitJob.
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	req.PrincipalID = principalID
	event, err := normalizeEdgeEventRequest(req, tenantID, s.edgeRecorder)
	if err != nil {
		writeEdgeEventRequestError(w, r, err, "invalid edge event request")
		return
	}
	if err := validateEdgeEventParents(r.Context(), store, event); err != nil {
		writeEdgeEventStoreError(w, r, err, "validate edge event parents")
		return
	}
	idempotencyReq, idempotent, handled := s.prepareEdgeIdempotencyRequest(w, r, tenantID, edgeEventCreateEndpoint, event)
	if handled {
		return
	}
	if idempotent {
		s.appendEdgeEventWithIdempotency(w, r, store, idempotencyReq, event)
		return
	}
	appended, err := store.AppendEvent(r.Context(), event)
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "append edge event")
		return
	}
	responseBody, err := json.Marshal(appended)
	if err != nil {
		writeEdgeInternalError(w, r, "marshal edge event response", err)
		return
	}
	s.forwardPersistedEdgeEvent(appended)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(responseBody)
}

func (s *server) handleCreateEdgeEventsBatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var req edgeEventBatchTenantProbeRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid edge event batch request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	if len(req.Events) == 0 {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "edge event batch requires events", nil)
		return
	}
	// Resolve principal once from the auth context. Every event in the batch
	// inherits the same principal — clients cannot mix per-event principals to
	// spoof activity from another user inside their tenant.
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	events := make([]edgecore.AgentActionEvent, 0, len(req.Events))
	for _, item := range req.Events {
		if requestedTenant := strings.TrimSpace(item.TenantID); requestedTenant != "" && requestedTenant != tenantID {
			writeEdgeForbidden(w, r, fmt.Errorf("edge tenant body/header mismatch"))
			return
		}
		item.PrincipalID = principalID
		event, err := normalizeEdgeEventRequest(item, tenantID, s.edgeRecorder)
		if err != nil {
			writeEdgeEventRequestError(w, r, err, "invalid edge event batch request")
			return
		}
		if err := validateEdgeEventParents(r.Context(), store, event); err != nil {
			writeEdgeEventStoreError(w, r, err, "validate edge event batch parents")
			return
		}
		events = append(events, event)
	}
	idempotencyReq, idempotent, handled := s.prepareEdgeIdempotencyRequest(w, r, tenantID, edgeEventBatchEndpoint, events)
	if handled {
		return
	}
	if idempotent {
		s.appendEdgeEventBatchWithIdempotency(w, r, store, idempotencyReq, events)
		return
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
	responseBody, err := json.Marshal(edgeEventBatchResponse{Items: appended})
	if err != nil {
		writeEdgeInternalError(w, r, "marshal edge event batch response", err)
		return
	}
	for _, event := range appended {
		s.forwardPersistedEdgeEvent(event)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(responseBody)
}

// prepareEdgeEventIdempotencyRequest, edgeNormalizedRequestHash, and
// writeEdgeIdempotencyReplay were moved to handlers_edge_idempotency.go in
// EDGE-060 so non-event endpoints (sessions/executions/approvals) can
// reuse the same hash + replay scaffold. Event-specific call sites now
// use s.prepareEdgeIdempotencyRequest (the renamed generic helper).

func (s *server) appendEdgeEventWithIdempotency(w http.ResponseWriter, r *http.Request, store edgecore.Store, req edgecore.EdgeIdempotencyRequest, event edgecore.AgentActionEvent) {
	result, err := store.AppendEventsWithIdempotency(r.Context(), req, []edgecore.AgentActionEvent{event}, edgeSingleEventIdempotencyResponse)
	if err != nil {
		writeEdgeEventIdempotencyError(w, r, err, "append edge event with idempotency")
		return
	}
	if result.State == edgecore.EdgeIdempotencyReplay {
		writeEdgeIdempotencyReplay(w, r, result.Record)
		return
	}
	if result.State != edgecore.EdgeIdempotencyCompleted || len(result.Events) != 1 {
		writeEdgeInternalError(w, r, "append edge event with idempotency", fmt.Errorf("unexpected edge idempotency result %q with %d events", result.State, len(result.Events)))
		return
	}
	s.forwardPersistedEdgeEvent(result.Events[0])
	writeEdgeIdempotencyReplay(w, r, result.Record)
}

func (s *server) appendEdgeEventBatchWithIdempotency(w http.ResponseWriter, r *http.Request, store edgecore.Store, req edgecore.EdgeIdempotencyRequest, events []edgecore.AgentActionEvent) {
	result, err := store.AppendEventsWithIdempotency(r.Context(), req, events, edgeBatchEventIdempotencyResponse)
	if err != nil {
		writeEdgeEventIdempotencyError(w, r, err, "append edge event batch with idempotency")
		return
	}
	if result.State == edgecore.EdgeIdempotencyReplay {
		writeEdgeIdempotencyReplay(w, r, result.Record)
		return
	}
	if result.State != edgecore.EdgeIdempotencyCompleted || len(result.Events) != len(events) {
		writeEdgeInternalError(w, r, "append edge event batch with idempotency", fmt.Errorf("unexpected edge idempotency result %q with %d events", result.State, len(result.Events)))
		return
	}
	for _, event := range result.Events {
		s.forwardPersistedEdgeEvent(event)
	}
	writeEdgeIdempotencyReplay(w, r, result.Record)
}

func edgeSingleEventIdempotencyResponse(events []edgecore.AgentActionEvent) (edgecore.EdgeIdempotencyResponse, error) {
	if len(events) != 1 {
		return edgecore.EdgeIdempotencyResponse{}, fmt.Errorf("single edge event response requires 1 event, got %d", len(events))
	}
	body, err := json.Marshal(events[0])
	if err != nil {
		return edgecore.EdgeIdempotencyResponse{}, fmt.Errorf("marshal edge event response: %w", err)
	}
	return edgecore.EdgeIdempotencyResponse{StatusCode: http.StatusCreated, ContentType: "application/json", Body: body}, nil
}

func edgeBatchEventIdempotencyResponse(events []edgecore.AgentActionEvent) (edgecore.EdgeIdempotencyResponse, error) {
	body, err := json.Marshal(edgeEventBatchResponse{Items: events})
	if err != nil {
		return edgecore.EdgeIdempotencyResponse{}, fmt.Errorf("marshal edge event batch response: %w", err)
	}
	return edgecore.EdgeIdempotencyResponse{StatusCode: http.StatusCreated, ContentType: "application/json", Body: body}, nil
}

func writeEdgeEventIdempotencyError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	switch {
	case errors.Is(err, edgecore.ErrIdempotencyConflict):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeIdempotencyConflict, "idempotency key already used with a different request", nil)
	case errors.Is(err, edgecore.ErrIdempotencyPending):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeIdempotencyConflict, "idempotency key is still processing", nil)
	case errors.Is(err, edgecore.ErrIdempotencyWindowExpired):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeIdempotencyWindowExpired, "idempotency replay window expired; event already exists and no duplicate was appended", nil)
	default:
		writeEdgeEventStoreError(w, r, err, operation)
	}
}

func (s *server) handleListEdgeSessionEvents(w http.ResponseWriter, r *http.Request) {
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
	if session, found, err := store.GetSession(r.Context(), tenantID, sessionID); err != nil {
		writeEdgeInternalError(w, r, "get edge event parent session", err)
		return
	} else if !found || session == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge session not found", nil)
		return
	}
	query, err := edgeEventListQueryFromRequest(r, tenantID)
	if err != nil {
		writeEdgeEventRequestError(w, r, err, "invalid edge event query")
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
	if execution, found, err := store.GetExecution(r.Context(), tenantID, executionID); err != nil {
		writeEdgeInternalError(w, r, "get edge event parent execution", err)
		return
	} else if !found || execution == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge execution not found", nil)
		return
	}
	query, err := edgeEventListQueryFromRequest(r, tenantID)
	if err != nil {
		writeEdgeEventRequestError(w, r, err, "invalid edge event query")
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

func normalizeEdgeEventRequest(req edgeEventWriteRequest, tenantID string, recorder edgecore.Recorder) (edgecore.AgentActionEvent, error) {
	if err := rejectRawEdgeEventPayload(req); err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	inputRedacted, inputHash, err := redactEdgeEventInput(req.InputRedacted, req.InputHash, recorder)
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
		EventID:        strings.TrimSpace(req.EventID),
		SessionID:      strings.TrimSpace(req.SessionID),
		ExecutionID:    strings.TrimSpace(req.ExecutionID),
		TenantID:       tenantID,
		PrincipalID:    mustRedactEdgeString(req.PrincipalID),
		Seq:            req.Seq,
		Timestamp:      req.Timestamp.UTC(),
		Layer:          req.Layer,
		Kind:           edgecore.EventKind(strings.TrimSpace(string(req.Kind))),
		AgentProduct:   mustRedactEdgeString(req.AgentProduct),
		ToolName:       mustRedactEdgeString(req.ToolName),
		ToolUseID:      mustRedactEdgeString(req.ToolUseID),
		ActionName:     mustRedactEdgeString(req.ActionName),
		Capability:     mustRedactEdgeString(req.Capability),
		RiskTags:       riskTags,
		InputRedacted:  inputRedacted,
		InputHash:      inputHash,
		Decision:       req.Decision,
		DecisionReason: mustRedactEdgeString(req.DecisionReason),
		RuleID:         mustRedactEdgeString(req.RuleID),
		RuleTier:       edgeNormalizeRuleTier(firstEdgeEvaluateNonEmpty(req.Tier, req.RuleTier)),
		PolicySnapshot: mustRedactEdgeString(req.PolicySnapshot),
		ApprovalRef:    mustRedactEdgeString(req.ApprovalRef),
		DurationMS:     req.DurationMS,
		Status:         req.Status,
		ErrorCode:      mustRedactEdgeString(req.ErrorCode),
		ErrorMessage:   mustRedactEdgeString(req.ErrorMessage),
		Labels:         labels,
	}
	artifactPointers, err := normalizeEdgeEventArtifactPointers(req.ArtifactPointers, event)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	event.ArtifactPointers = artifactPointers
	if err := event.Validate(); err != nil {
		return edgecore.AgentActionEvent{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge event request"}
	}
	return event, nil
}

func normalizeEdgeEventArtifactPointers(artifacts []edgecore.ArtifactPointer, event edgecore.AgentActionEvent) ([]edgecore.ArtifactPointer, error) {
	if len(artifacts) == 0 {
		return nil, nil
	}
	normalized := make([]edgecore.ArtifactPointer, 0, len(artifacts))
	for _, artifact := range artifacts {
		item := edgecore.ArtifactPointer{
			ArtifactType:   artifact.ArtifactType,
			SessionID:      strings.TrimSpace(artifact.SessionID),
			ExecutionID:    strings.TrimSpace(artifact.ExecutionID),
			EventID:        strings.TrimSpace(artifact.EventID),
			TenantID:       strings.TrimSpace(artifact.TenantID),
			RetentionClass: artifact.RetentionClass,
			RedactionLevel: artifact.RedactionLevel,
			SHA256:         strings.TrimSpace(artifact.SHA256),
			URI:            strings.TrimSpace(artifact.URI),
			CreatedAt:      artifact.CreatedAt.UTC(),
		}
		if item.TenantID != event.TenantID ||
			item.SessionID != event.SessionID ||
			item.ExecutionID != event.ExecutionID ||
			item.EventID != event.EventID {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: edgeArtifactPointerErrorMessage}
		}
		if !isSafeEdgeArtifactPointerURI(item.URI) {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: edgeArtifactPointerErrorMessage}
		}
		normalized = append(normalized, item)
	}
	return normalized, nil
}

func isSafeEdgeArtifactPointerURI(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len([]byte(raw)) > maxEdgeArtifactPointerURIBytes {
		return false
	}
	redacted, err := edgecore.RedactValue(raw, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxStringBytes: maxEdgeArtifactPointerURIBytes,
		MaxTotalBytes:  maxEdgeArtifactPointerURIBytes,
	})
	if err != nil || redacted.Redacted || redacted.Truncated {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case edgeArtifactPointerSchemeArtifact, edgeArtifactPointerSchemeEdge:
	default:
		return false
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Host == "" && parsed.Opaque == "" {
		return false
	}
	return !containsSecretBearingArtifactURIComponent(raw)
}

func containsSecretBearingArtifactURIComponent(raw string) bool {
	lower := strings.ToLower(raw)
	for _, marker := range []string{
		"access_token=",
		"api_key=",
		"apikey=",
		"authorization=",
		"bearer ",
		"bearer%20",
		"client_secret=",
		"password=",
		"passwd=",
		"refresh_token=",
		"sharedaccesssignature=",
		"sig=",
		"signature=",
		"token=",
		"x-amz-credential=",
		"x-amz-security-token=",
		"x-amz-signature=",
		"x-goog-credential=",
		"x-goog-security-token=",
		"x-goog-signature=",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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

func redactEdgeEventInput(input map[string]any, providedHash string, recorder edgecore.Recorder) (map[string]any, string, error) {
	inputHash, err := redactEdgeString(providedHash)
	if err != nil {
		return nil, "", err
	}
	if len(input) == 0 {
		recordEdgeEventRedaction(recorder, "skipped")
		return nil, inputHash, nil
	}
	if err := ensureEdgeInlineJSONSize("input_redacted", input, edgecore.MaxInputRedactedBytes); err != nil {
		return nil, "", err
	}
	result, err := edgecore.RedactValue(input, edgecore.RedactionOptions{HashMode: edgecore.RedactionHashBoth})
	if err != nil {
		recordEdgeEventRedactionFailed(recorder, "gateway.edge_event_input", "redactor_error")
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
	recordEdgeEventRedaction(recorder, edgeEventRedactionOutcome(result))
	return redacted, inputHash, nil
}

func edgeEventRedactionOutcome(result edgecore.RedactionResult) string {
	if result.Truncated {
		return "partial"
	}
	if result.Redacted {
		return "applied"
	}
	return "skipped"
}

func recordEdgeEventRedaction(recorder edgecore.Recorder, outcome string) {
	if recorder != nil {
		recorder.RecordEventRedacted(outcome)
	}
}

func recordEdgeEventRedactionFailed(recorder edgecore.Recorder, site, reason string) {
	if recorder != nil {
		recorder.RecordEventRedacted("failed")
		recorder.RecordRedactionFailed(site, reason)
	}
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

func writeEdgeEventRequestError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	var requestErr edgeEventRequestError
	if errors.As(err, &requestErr) {
		writeEdgeError(w, r, requestErr.status, edgeEventRequestCode(requestErr), requestErr.message, nil)
		return
	}
	writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, fallback, nil)
}

// writeEdgeEventStoreError maps store/request failures from the Edge events
// pipeline to the standard Edge error envelope.
//
// EDGE-038: typed sentinels (edgecore.ErrValidation / ErrInvalidCursor /
// ErrRequestTooLarge / ErrNotFound) are checked via errors.Is before the
// substring fallbacks. Producers wrapped with the sentinels short-circuit
// via the typed path; legacy producers still match via the substring path
// during the EDGE-038 transition. Wire codes/statuses are byte-identical
// across both paths.
func writeEdgeEventStoreError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	var requestErr edgeEventRequestError
	if errors.As(err, &requestErr) {
		writeEdgeError(w, r, requestErr.status, edgeEventRequestCode(requestErr), requestErr.message, nil)
		return
	}
	if errors.Is(err, edgecore.ErrNotFound) {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge event parent not found", nil)
		return
	}
	if errors.Is(err, edgecore.ErrInvalidCursor) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge event query", nil)
		return
	}
	if errors.Is(err, edgecore.ErrRequestTooLarge) {
		writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge, "edge event too large; use artifact_ptrs", nil)
		return
	}
	if errors.Is(err, edgecore.ErrExecutionEventCapExceeded) {
		writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeEventCapExceeded, "edge execution event cap exceeded; end the execution or start a new session", nil)
		return
	}
	if isEdgeValidationError(err) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge event request", nil)
		return
	}
	errStr := err.Error()
	if strings.Contains(errStr, "invalid cursor") {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge event query", nil)
		return
	}
	if strings.Contains(errStr, "exceeds max") || strings.Contains(errStr, "too large") {
		writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge, "edge event too large; use artifact_ptrs", nil)
		return
	}
	writeEdgeInternalError(w, r, operation, err)
}

// edgeEventRequestCode classifies an edgeEventRequestError into a stable Edge
// API code. It looks at HTTP status first (broad bucket) then at the message
// (narrow case for raw/artifact rejections that are part of the request
// contract).
func edgeEventRequestCode(req edgeEventRequestError) string {
	switch req.status {
	case http.StatusRequestEntityTooLarge:
		return edgeErrCodeRequestTooLarge
	case http.StatusNotFound:
		return edgeErrCodeNotFound
	case http.StatusConflict:
		return edgeErrCodeConflict
	case http.StatusForbidden:
		return edgeErrCodeAccessDenied
	}
	msg := strings.ToLower(req.message)
	switch {
	case strings.Contains(msg, "raw "), strings.Contains(msg, "transcript"), strings.Contains(msg, "tool_input"):
		return edgeErrCodeRawPayloadRejected
	case strings.Contains(msg, "artifact"):
		return edgeErrCodeArtifactPointerInvalid
	case strings.Contains(msg, "tenant"):
		return edgeErrCodeTenantMismatch
	case strings.Contains(msg, "execution"):
		return edgeErrCodeExecutionMismatch
	}
	return edgeErrCodeInvalidRequest
}
