package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

type edgeEvaluateRequest struct {
	EventID     string `json:"event_id"`
	TenantID    string `json:"tenant_id"`
	PrincipalID string `json:"principal_id"`

	SessionID   string `json:"session_id"`
	ExecutionID string `json:"execution_id"`

	AgentProduct string             `json:"agent_product"`
	Layer        edgecore.Layer     `json:"layer"`
	Kind         edgecore.EventKind `json:"kind"`
	ToolName     string             `json:"tool_name"`
	ToolUseID    string             `json:"tool_use_id"`

	InputRedacted     map[string]any `json:"input_redacted"`
	ToolInputRedacted map[string]any `json:"tool_input_redacted"`
	InputHash         string         `json:"input_hash"`
	ToolInputHash     string         `json:"tool_input_hash"`

	// Raw/transcript fields are accepted only so the handler can reject them
	// with a sanitized error and force callers onto redacted input/artifacts.
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResult    json.RawMessage `json:"tool_result"`
	RawInput      json.RawMessage `json:"raw_input"`
	RawTranscript json.RawMessage `json:"raw_transcript"`
	Transcript    json.RawMessage `json:"transcript"`

	CWD       string `json:"cwd"`
	Repo      string `json:"repo"`
	GitRemote string `json:"git_remote"`
	GitBranch string `json:"git_branch"`
	GitSHA    string `json:"git_sha"`

	ActionName string          `json:"action_name"`
	Capability string          `json:"capability"`
	RiskTags   []string        `json:"risk_tags"`
	Labels     edgecore.Labels `json:"labels"`

	ArtifactPointers []edgecore.ArtifactPointer `json:"artifact_ptrs"`
}

func (r edgeEvaluateRequest) redactedInput() map[string]any {
	if len(r.ToolInputRedacted) > 0 {
		return r.ToolInputRedacted
	}
	return r.InputRedacted
}

func (r edgeEvaluateRequest) inputHash() string {
	if r.ToolInputHash != "" {
		return r.ToolInputHash
	}
	return r.InputHash
}

type edgeEvaluateResponse struct {
	Decision       edgecore.EdgeDecision `json:"decision"`
	Reason         string                `json:"reason,omitempty"`
	RuleID         string                `json:"rule_id,omitempty"`
	PolicySnapshot string                `json:"policy_snapshot,omitempty"`
	ApprovalRef    string                `json:"approval_ref,omitempty"`
	Constraints    map[string]any        `json:"constraints,omitempty"`
	UpdatedInput   map[string]any        `json:"updated_input,omitempty"`
	EventID        string                `json:"event_id,omitempty"`

	Degraded     bool   `json:"degraded,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	PermissionDecision       string `json:"permission_decision"`
	PermissionDecisionReason string `json:"permission_decision_reason,omitempty"`
	ExitCode                 int    `json:"exit_code"`
	TerminalTitle            string `json:"terminal_title,omitempty"`
	TerminalMessage          string `json:"terminal_message,omitempty"`
	WaitStrategy             string `json:"wait_strategy,omitempty"`
	TimeoutMS                int    `json:"timeout_ms,omitempty"`
}

func (s *server) handleEdgeEvaluate(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	evalCtx, ok := s.prepareEdgeEvaluateContext(w, r)
	if !ok {
		return
	}
	policyInput, err := buildEdgeEvaluatePolicyInput(evalCtx)
	if err != nil {
		writeEdgeEventRequestError(w, err, "invalid edge evaluate request")
		return
	}
	safetyResp, err := s.evaluateEdgeSafety(r.Context(), policyInput.policyRequest)
	if err != nil {
		outcome := edgeEvaluateOutcomeFromSafetyUnavailable(policyInput.event.EventID, evalCtx.session.PolicyMode, policyInput.classification)
		appended, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, outcome, edgeEvaluateDurationMS(started))
		if appendErr != nil {
			writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate degraded event")
			return
		}
		outcome.response.EventID = appended.EventID
		writeJSON(w, outcome.response)
		return
	}
	outcome := edgeEvaluateOutcomeFromSafety(policyInput.event.EventID, safetyResp)
	appended, err := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, outcome, edgeEvaluateDurationMS(started))
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "append edge evaluate decision event")
		return
	}
	outcome.response.EventID = appended.EventID
	writeJSON(w, outcome.response)
}

type edgeEvaluateContext struct {
	req         edgeEvaluateRequest
	store       edgecore.Store
	tenantID    string
	principalID string
	session     *edgecore.EdgeSession
	execution   *edgecore.AgentExecution
}

func (s *server) prepareEdgeEvaluateContext(w http.ResponseWriter, r *http.Request) (edgeEvaluateContext, bool) {
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return edgeEvaluateContext{}, false
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return edgeEvaluateContext{}, false
	}

	var req edgeEvaluateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid edge evaluate request")
		return edgeEvaluateContext{}, false
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return edgeEvaluateContext{}, false
	}
	principalID, err := s.resolvePrincipal(r, req.PrincipalID)
	if err != nil {
		writeForbidden(w, r, err)
		return edgeEvaluateContext{}, false
	}
	if strings.TrimSpace(principalID) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "principal_id is required")
		return edgeEvaluateContext{}, false
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "session_id is required")
		return edgeEvaluateContext{}, false
	}
	executionID := strings.TrimSpace(req.ExecutionID)
	if executionID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "execution_id is required")
		return edgeEvaluateContext{}, false
	}

	session, found, err := store.GetSession(r.Context(), tenantID, sessionID)
	if err != nil {
		writeInternalError(w, r, "get edge evaluate session", err)
		return edgeEvaluateContext{}, false
	}
	if !found || session == nil {
		writeErrorJSON(w, http.StatusNotFound, "edge event parent not found")
		return edgeEvaluateContext{}, false
	}
	if strings.TrimSpace(session.PrincipalID) != "" && strings.TrimSpace(session.PrincipalID) != principalID {
		writeForbidden(w, r, fmt.Errorf("edge session principal mismatch"))
		return edgeEvaluateContext{}, false
	}
	if isTerminalEdgeSessionStatus(session.Status) {
		writeErrorJSON(w, http.StatusConflict, "edge session is not actionable")
		return edgeEvaluateContext{}, false
	}

	execution, found, err := store.GetExecution(r.Context(), tenantID, executionID)
	if err != nil {
		writeInternalError(w, r, "get edge evaluate execution", err)
		return edgeEvaluateContext{}, false
	}
	if !found || execution == nil {
		writeErrorJSON(w, http.StatusNotFound, "edge event parent not found")
		return edgeEvaluateContext{}, false
	}
	if execution.SessionID != sessionID {
		writeErrorJSON(w, http.StatusBadRequest, "execution does not belong to session")
		return edgeEvaluateContext{}, false
	}
	if isTerminalEdgeExecutionStatus(execution.Status) {
		writeErrorJSON(w, http.StatusConflict, "edge execution is not actionable")
		return edgeEvaluateContext{}, false
	}

	req.SessionID = sessionID
	req.ExecutionID = executionID
	req.TenantID = tenantID
	req.PrincipalID = principalID
	return edgeEvaluateContext{
		req:         req,
		store:       store,
		tenantID:    tenantID,
		principalID: principalID,
		session:     session,
		execution:   execution,
	}, true
}

func isTerminalEdgeSessionStatus(status edgecore.SessionStatus) bool {
	switch status {
	case edgecore.SessionStatusEnded, edgecore.SessionStatusFailed:
		return true
	default:
		return false
	}
}

type edgeEvaluatePolicyInput struct {
	event          edgecore.AgentActionEvent
	classification edgecore.ActionClassification
	policyRequest  *pb.PolicyCheckRequest
}

type edgeEvaluateDecisionOutcome struct {
	response       edgeEvaluateResponse
	kind           edgecore.EventKind
	decision       edgecore.EdgeDecision
	status         edgecore.ActionStatus
	reason         string
	ruleID         string
	policySnapshot string
	approvalRef    string
	errorCode      string
	errorMessage   string
}

func (s *server) evaluateEdgeSafety(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	if s.safetyClient == nil {
		return nil, fmt.Errorf("safety kernel unavailable")
	}
	evalCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.safetyClient.Evaluate(evalCtx, req)
}

func edgeEvaluateOutcomeFromSafety(eventID string, resp *pb.PolicyCheckResponse) edgeEvaluateDecisionOutcome {
	reason := ""
	ruleID := ""
	policySnapshot := ""
	approvalRef := ""
	if resp != nil {
		reason = mustRedactEdgeString(resp.GetReason())
		ruleID = mustRedactEdgeString(resp.GetRuleId())
		policySnapshot = mustRedactEdgeString(resp.GetPolicySnapshot())
		approvalRef = mustRedactEdgeString(resp.GetApprovalRef())
	}
	base := edgeEvaluateDecisionOutcome{
		kind:           edgecore.EventKindHookPolicyDecision,
		reason:         reason,
		ruleID:         ruleID,
		policySnapshot: policySnapshot,
		approvalRef:    approvalRef,
	}
	base.response = edgeEvaluateResponse{
		Reason:                   reason,
		RuleID:                   ruleID,
		PolicySnapshot:           policySnapshot,
		ApprovalRef:              approvalRef,
		Constraints:              edgeEvaluateConstraintsToMap(resp.GetConstraints()),
		EventID:                  eventID,
		PermissionDecisionReason: reason,
	}

	if resp == nil {
		return base.edgeEvaluateDeny("unknown policy decision")
	}
	if resp.GetApprovalRequired() || resp.GetDecision() == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN {
		return base.edgeEvaluateRequireApproval()
	}

	switch resp.GetDecision() {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		base.decision = edgecore.DecisionAllow
		base.status = edgecore.ActionStatusOK
		base.response.Decision = edgecore.DecisionAllow
		base.response.PermissionDecision = "allow"
		base.response.ExitCode = 0
		return base
	case pb.DecisionType_DECISION_TYPE_DENY:
		return base.edgeEvaluateDeny(defaultEdgeEvaluateReason(reason, "policy denied"))
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		base.decision = edgecore.DecisionThrottle
		base.status = edgecore.ActionStatusBlocked
		base.response.Decision = edgecore.DecisionThrottle
		base.response.PermissionDecision = "deny"
		base.response.ExitCode = 2
		base.response.TerminalTitle = "Cordum Edge throttled"
		base.response.TerminalMessage = defaultEdgeEvaluateReason(reason, "policy throttled")
		base.response.WaitStrategy = "backoff"
		base.response.TimeoutMS = 5000
		return base
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		base.decision = edgecore.DecisionConstrain
		base.status = edgecore.ActionStatusOK
		base.response.Decision = edgecore.DecisionConstrain
		base.response.PermissionDecision = "allow"
		base.response.ExitCode = 0
		return base
	default:
		return base.edgeEvaluateDeny(defaultEdgeEvaluateReason(reason, "unknown policy decision"))
	}
}

func edgeEvaluateOutcomeFromSafetyUnavailable(eventID string, policyMode edgecore.PolicyMode, classification edgecore.ActionClassification) edgeEvaluateDecisionOutcome {
	const errorCode = "safety_unavailable"
	const errorMessage = "safety kernel unavailable; retry after checking Cordum Edge health"

	reason := "safety kernel unavailable; degraded policy mode applied"
	outcome := edgeEvaluateDecisionOutcome{
		kind:         edgecore.EventKindPolicyDegraded,
		status:       edgecore.ActionStatusDegraded,
		decision:     edgecore.DecisionRecorded,
		reason:       reason,
		errorCode:    errorCode,
		errorMessage: errorMessage,
		response: edgeEvaluateResponse{
			Decision:                 edgecore.DecisionDeny,
			Reason:                   reason,
			EventID:                  eventID,
			Degraded:                 true,
			ErrorCode:                errorCode,
			ErrorMessage:             errorMessage,
			PermissionDecision:       "deny",
			PermissionDecisionReason: reason,
			ExitCode:                 2,
			TerminalTitle:            "Cordum Edge safety unavailable",
			TerminalMessage:          errorMessage,
			WaitStrategy:             "retry",
			TimeoutMS:                5000,
		},
	}

	if policyMode == edgecore.PolicyModeObserve {
		outcome.response.Decision = edgecore.DecisionAllow
		outcome.response.PermissionDecision = "allow"
		outcome.response.ExitCode = 0
		outcome.response.TerminalMessage = "Safety kernel unavailable; observe mode allowed this action and recorded degraded evidence."
		return outcome
	}

	if policyMode == edgecore.PolicyModeEnterpriseStrict || edgeEvaluateRequiresFreshFailClosed(classification) {
		outcome.response.Decision = edgecore.DecisionDeny
		outcome.response.PermissionDecision = "deny"
		outcome.response.ExitCode = 2
		outcome.response.TerminalMessage = "Safety kernel unavailable; Cordum Edge failed closed for this governed action."
		outcome.decision = edgecore.DecisionDeny
		outcome.status = edgecore.ActionStatusDegraded
		return outcome
	}

	// No final safe-action cache contract exists in P0 yet. Enforce mode must
	// fail closed rather than inventing a fail-open cache path.
	outcome.response.Decision = edgecore.DecisionDeny
	outcome.response.PermissionDecision = "deny"
	outcome.response.ExitCode = 2
	outcome.response.TerminalMessage = "Safety kernel unavailable; no cached-safe decision is available, so Cordum Edge failed closed."
	outcome.decision = edgecore.DecisionDeny
	return outcome
}

func edgeEvaluateRequiresFreshFailClosed(classification edgecore.ActionClassification) bool {
	if strings.TrimSpace(classification.ActionName) == "" ||
		strings.TrimSpace(classification.Capability) == "" {
		return true
	}
	for _, tag := range classification.RiskTags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "destructive", "unknown", "review_required", "network", "deploy", "secrets", "mutating", "write", "filesystem":
			return true
		}
	}
	return false
}

func (outcome edgeEvaluateDecisionOutcome) edgeEvaluateDeny(reason string) edgeEvaluateDecisionOutcome {
	outcome.decision = edgecore.DecisionDeny
	outcome.status = edgecore.ActionStatusBlocked
	outcome.reason = reason
	outcome.response.Decision = edgecore.DecisionDeny
	outcome.response.Reason = reason
	outcome.response.PermissionDecision = "deny"
	outcome.response.PermissionDecisionReason = reason
	outcome.response.ExitCode = 2
	outcome.response.TerminalTitle = "Cordum Edge blocked"
	outcome.response.TerminalMessage = reason
	return outcome
}

func (outcome edgeEvaluateDecisionOutcome) edgeEvaluateRequireApproval() edgeEvaluateDecisionOutcome {
	reason := defaultEdgeEvaluateReason(outcome.reason, "approval required")
	outcome.decision = edgecore.DecisionRequireApproval
	outcome.status = edgecore.ActionStatusBlocked
	outcome.reason = reason
	outcome.response.Decision = edgecore.DecisionRequireApproval
	outcome.response.Reason = reason
	outcome.response.PermissionDecision = "deny"
	outcome.response.PermissionDecisionReason = reason
	outcome.response.ExitCode = 2
	outcome.response.TerminalTitle = "Cordum Edge approval required"
	outcome.response.TerminalMessage = reason
	outcome.response.WaitStrategy = "manual_approval"
	return outcome
}

func edgeEvaluateConstraintsToMap(constraints *pb.PolicyConstraints) map[string]any {
	if constraints == nil {
		return nil
	}
	data, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(constraints)
	if err != nil || len(data) == 0 || string(data) == "{}" || string(data) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *server) appendEdgeEvaluateOutcome(ctx context.Context, store edgecore.Store, base edgecore.AgentActionEvent, outcome edgeEvaluateDecisionOutcome, durationMS int) (edgecore.AgentActionEvent, error) {
	event := base
	if strings.TrimSpace(string(outcome.kind)) != "" {
		event.Kind = outcome.kind
	}
	event.Decision = outcome.decision
	event.DecisionReason = mustRedactEdgeString(outcome.reason)
	event.RuleID = mustRedactEdgeString(outcome.ruleID)
	event.PolicySnapshot = mustRedactEdgeString(outcome.policySnapshot)
	event.ApprovalRef = mustRedactEdgeString(outcome.approvalRef)
	event.DurationMS = durationMS
	event.Status = outcome.status
	event.ErrorCode = mustRedactEdgeString(outcome.errorCode)
	event.ErrorMessage = mustRedactEdgeString(outcome.errorMessage)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	appended, err := store.AppendEvent(ctx, event)
	if err != nil {
		return edgecore.AgentActionEvent{}, err
	}
	s.forwardPersistedEdgeEvent(appended)
	return appended, nil
}

func edgeEvaluateDurationMS(started time.Time) int {
	if started.IsZero() {
		return 1
	}
	elapsed := time.Since(started).Milliseconds()
	if elapsed <= 0 {
		return 1
	}
	return int(elapsed)
}

func defaultEdgeEvaluateReason(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func buildEdgeEvaluatePolicyInput(evalCtx edgeEvaluateContext) (edgeEvaluatePolicyInput, error) {
	req := evalCtx.req
	if err := rejectRawEdgeEventPayload(edgeEventWriteRequest{
		ToolInput:     req.ToolInput,
		ToolResult:    req.ToolResult,
		RawInput:      req.RawInput,
		RawTranscript: req.RawTranscript,
		Transcript:    req.Transcript,
	}); err != nil {
		return edgeEvaluatePolicyInput{}, err
	}

	inputRedacted, inputHash, err := redactEdgeEventInput(req.redactedInput(), req.inputHash())
	if err != nil {
		return edgeEvaluatePolicyInput{}, err
	}
	labels, err := redactEdgeLabels(req.Labels)
	if err != nil {
		return edgeEvaluatePolicyInput{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
	labels, err = edgeEvaluateContextLabels(labels, req, evalCtx.session)
	if err != nil {
		return edgeEvaluatePolicyInput{}, err
	}
	riskTags, err := redactEdgeStringSlice(req.RiskTags)
	if err != nil {
		return edgeEvaluatePolicyInput{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}

	eventID := mustRedactEdgeString(req.EventID)
	if strings.TrimSpace(eventID) == "" {
		eventID = uuid.NewString()
	}
	agentProduct := firstEdgeEvaluateNonEmpty(req.AgentProduct, evalCtx.session.AgentProduct)
	event := edgecore.AgentActionEvent{
		EventID:       eventID,
		SessionID:     strings.TrimSpace(req.SessionID),
		ExecutionID:   strings.TrimSpace(req.ExecutionID),
		TenantID:      strings.TrimSpace(evalCtx.tenantID),
		PrincipalID:   strings.TrimSpace(evalCtx.principalID),
		Timestamp:     time.Now().UTC(),
		Layer:         req.Layer,
		Kind:          edgecore.EventKind(strings.TrimSpace(string(req.Kind))),
		AgentProduct:  mustRedactEdgeString(agentProduct),
		ToolName:      mustRedactEdgeString(req.ToolName),
		ToolUseID:     mustRedactEdgeString(req.ToolUseID),
		ActionName:    mustRedactEdgeString(req.ActionName),
		Capability:    mustRedactEdgeString(req.Capability),
		RiskTags:      riskTags,
		InputRedacted: inputRedacted,
		InputHash:     inputHash,
		Decision:      edgecore.DecisionRecorded,
		Status:        edgecore.ActionStatusOK,
		Labels:        labels,
	}
	artifactPointers, err := normalizeEdgeEventArtifactPointers(req.ArtifactPointers, event)
	if err != nil {
		return edgeEvaluatePolicyInput{}, err
	}
	event.ArtifactPointers = artifactPointers

	classification, err := edgecore.ClassifyEvent(event)
	if err != nil {
		return edgeEvaluatePolicyInput{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
	event.ActionName = classification.ActionName
	event.Capability = classification.Capability
	event.RiskTags = append([]string(nil), classification.RiskTags...)
	event.Labels, err = edgeEvaluateMergeLabels(event.Labels, classification.Labels)
	if err != nil {
		return edgeEvaluatePolicyInput{}, err
	}

	policyRequest, err := edgecore.MapEventToPolicyCheckRequest(event, classification, edgecore.PolicyMappingOptions{
		ActorID:   evalCtx.principalID,
		ActorType: edgeEvaluateActorType(evalCtx.session.PrincipalType),
	})
	if err != nil {
		return edgeEvaluatePolicyInput{}, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}

	return edgeEvaluatePolicyInput{
		event:          event,
		classification: classification,
		policyRequest:  policyRequest,
	}, nil
}

func edgeEvaluateContextLabels(labels edgecore.Labels, req edgeEvaluateRequest, session *edgecore.EdgeSession) (edgecore.Labels, error) {
	if labels == nil {
		labels = edgecore.Labels{}
	}
	var sessionCWD, sessionRepo, sessionGitRemote, sessionGitBranch, sessionGitSHA string
	if session != nil {
		sessionCWD = session.CWD
		sessionRepo = session.Repo
		sessionGitRemote = session.GitRemote
		sessionGitBranch = session.GitBranch
		sessionGitSHA = session.GitSHA
	}
	contextFields := map[string]string{
		"cwd":        firstEdgeEvaluateNonEmpty(req.CWD, sessionCWD),
		"repo.path":  firstEdgeEvaluateNonEmpty(req.Repo, sessionRepo),
		"git.remote": firstEdgeEvaluateNonEmpty(req.GitRemote, sessionGitRemote),
		"git.branch": firstEdgeEvaluateNonEmpty(req.GitBranch, sessionGitBranch),
		"git.sha":    firstEdgeEvaluateNonEmpty(req.GitSHA, sessionGitSHA),
	}
	for key, value := range contextFields {
		redacted, err := redactEdgeString(value)
		if err != nil {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
		}
		if strings.TrimSpace(redacted) != "" {
			labels[key] = redacted
		}
	}
	if len(labels) > edgecore.MaxLabelEntries {
		return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
	return labels, nil
}

func edgeEvaluateMergeLabels(base edgecore.Labels, trusted edgecore.Labels) (edgecore.Labels, error) {
	out := make(edgecore.Labels, len(base)+len(trusted))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range trusted {
		redactedKey, err := redactEdgeString(key)
		if err != nil {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
		}
		redactedValue, err := redactEdgeString(value)
		if err != nil {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
		}
		if strings.TrimSpace(redactedKey) != "" && strings.TrimSpace(redactedValue) != "" {
			out[redactedKey] = redactedValue
		}
	}
	if len(out) > edgecore.MaxLabelEntries {
		return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
	return out, nil
}

func edgeEvaluateActorType(principalType edgecore.PrincipalType) pb.ActorType {
	switch principalType {
	case edgecore.PrincipalTypeHuman:
		return pb.ActorType_ACTOR_TYPE_HUMAN
	case edgecore.PrincipalTypeService:
		return pb.ActorType_ACTOR_TYPE_SERVICE
	default:
		return pb.ActorType_ACTOR_TYPE_UNSPECIFIED
	}
}

func firstEdgeEvaluateNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
