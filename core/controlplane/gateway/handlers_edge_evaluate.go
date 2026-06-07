package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
)

// edgeApprovalLookupMaxPages caps how many ListApprovals pages the per-action
// reusable-approval lookup will scan before giving up. With page Limit=50 and
// 20 pages, the function tolerates principals that have accumulated up to
// 1000 approvals in this session before silently missing matches. Above the
// cap we log a slog.Warn so an oncall operator notices the gap rather than
// having the lookup return nil and the handler enqueue a redundant approval.
//
// Sized for the long-tail of agent sessions (default ~10s of approvals) plus
// a 100x headroom; raise via const if a tenant pattern emerges that legitimately
// exceeds 1000 approvals per (session, principal).
const edgeApprovalLookupMaxPages = 20

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

	// ApprovalRef signals that this evaluate is the consume-once retry of a
	// previously approved EdgeApproval. When non-empty the gateway resolves the
	// approval via the EDGE-011 store, recomputes the action_hash against the
	// fresh safety snapshot, and lets the store CAS reject changed-command or
	// stale-snapshot mismatches. Default callers omit it.
	ApprovalRef string `json:"approval_ref"`

	// WaitForApproval is the local/demo opt-in that asks the gateway to inline-wait
	// for an approval to leave Pending before responding. The default is false:
	// the response returns REQUIRE_APPROVAL immediately and the caller polls or
	// reissues evaluate with approval_ref later. When true, ApprovalWaitTimeoutMS
	// bounds the wait; the gateway always uses a server-side cap.
	WaitForApproval       bool `json:"wait_for_approval"`
	ApprovalWaitTimeoutMS int  `json:"approval_wait_timeout_ms"`

	// ApprovalTTLSeconds overrides the default 20-minute approval TTL when set
	// (EDGE-059: enables e2e gates that need to exercise approval expiration
	// inside a bounded sleep window). Server-side cap: callers can ONLY
	// shorten the TTL, never extend it past the 20-minute default — preserves
	// the security floor. Min 1 second; values <= 0 use the default.
	ApprovalTTLSeconds int `json:"approval_ttl_seconds,omitempty"`
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
	Decision                 edgecore.EdgeDecision `json:"decision"`
	Reason                   string                `json:"reason,omitempty"`
	RuleID                   string                `json:"rule_id,omitempty"`
	RuleTier                 string                `json:"rule_tier,omitempty"`
	PolicySnapshot           string                `json:"policy_snapshot,omitempty"`
	WorkflowOverrideSnapshot string                `json:"workflow_override_snapshot,omitempty"`
	JobOverrideSnapshot      string                `json:"job_override_snapshot,omitempty"`
	ApprovalRef              string                `json:"approval_ref,omitempty"`
	ApprovalURL              string                `json:"approval_url,omitempty"`
	ActionHash               string                `json:"action_hash,omitempty"`
	InputHash                string                `json:"input_hash,omitempty"`
	Constraints              map[string]any        `json:"constraints,omitempty"`
	UpdatedInput             map[string]any        `json:"updated_input,omitempty"`
	EventID                  string                `json:"event_id,omitempty"`

	Degraded     bool   `json:"degraded,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	PermissionDecision       string `json:"permission_decision"`
	PermissionDecisionReason string `json:"permission_decision_reason,omitempty"`
	ExitCode                 int    `json:"exit_code"`
	TerminalTitle            string `json:"terminal_title,omitempty"`
	TerminalMessage          string `json:"terminal_message,omitempty"`
	WaitStrategy             string `json:"wait_strategy,omitempty"`
	WaitAfter                string `json:"wait_after,omitempty"`
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
		writeEdgeEventRequestError(w, r, err, "invalid edge evaluate request")
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
	outcome = s.decorateEdgeEvaluateTierEvidence(r.Context(), policyInput.event.Labels, outcome)
	actionHash, err := edgeEvaluateActionHash(policyInput.event, outcome.policySnapshot)
	if err != nil {
		writeEdgeEventRequestError(w, r, err, "invalid edge evaluate request")
		return
	}
	retryRef := strings.TrimSpace(evalCtx.req.ApprovalRef)
	if retryRef != "" {
		waitTimeout := boundEdgeEvaluateWaitTimeout(evalCtx.req.ApprovalWaitTimeoutMS)
		if evalCtx.req.WaitForApproval {
			resolved := s.waitForEdgeApprovalResolution(r.Context(), evalCtx.store, evalCtx.tenantID, retryRef, waitTimeout)
			if !resolved {
				timeoutOutcome, handled, timeoutErr := s.edgeEvaluateInlineWaitTimeoutOutcome(r.Context(), evalCtx.store, evalCtx.tenantID, outcome, retryRef, waitTimeout)
				if timeoutErr != nil {
					writeEdgeApprovalStoreError(w, r, timeoutErr, "load edge evaluate approval after wait timeout")
					return
				}
				if handled {
					appended, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, timeoutOutcome, edgeEvaluateDurationMS(started))
					if appendErr != nil {
						writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate retry timeout event")
						return
					}
					timeoutOutcome.response.EventID = appended.EventID
					writeJSON(w, timeoutOutcome.response)
					return
				}
			}
		}

		retryOutcome, retryErr := s.consumeEdgeEvaluateApproval(r.Context(), evalCtx.store, policyInput.event, outcome, retryRef, actionHash)
		if retryErr != nil {
			writeEdgeApprovalStoreError(w, r, retryErr, "consume edge evaluate approval")
			return
		}
		appended, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, retryOutcome, edgeEvaluateDurationMS(started))
		if appendErr != nil {
			writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate retry outcome event")
			return
		}
		retryOutcome.response.EventID = appended.EventID
		writeJSON(w, retryOutcome.response)
		return
	}

	// EDGE-039: when the agent retries an action with the same action_hash and
	// no explicit approval_ref, look up an existing approval for this
	// (tenant, session, action_hash). cordum-hook cannot carry an approval_ref
	// across Claude tool retries, so the gateway routes the retry through the
	// existing consume CAS — APPROVED approvals get consumed (ALLOW), CONSUMED
	// approvals surface "already consumed" deny, PENDING approvals echo the
	// existing reference. If no approval exists, fall through to the normal
	// enqueue path.
	if outcome.decision == edgecore.DecisionRequireApproval {
		existing, lookupErr := s.findReusableEdgeApprovalForAction(r.Context(), evalCtx.store, evalCtx.tenantID, policyInput.event, actionHash)
		if lookupErr != nil {
			writeEdgeApprovalStoreError(w, r, lookupErr, "look up reusable edge approval")
			return
		}
		if existing != nil {
			retryOutcome, retryErr := s.consumeEdgeEvaluateApproval(r.Context(), evalCtx.store, policyInput.event, outcome, existing.ApprovalRef, actionHash)
			if retryErr != nil {
				writeEdgeApprovalStoreError(w, r, retryErr, "consume reusable edge approval")
				return
			}
			appendedFinal, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, retryOutcome, edgeEvaluateDurationMS(started))
			if appendErr != nil {
				writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate auto-consume outcome event")
				return
			}
			retryOutcome.response.EventID = appendedFinal.EventID
			writeJSON(w, retryOutcome.response)
			return
		}
	}

	appended, err := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, policyInput.event, outcome, edgeEvaluateDurationMS(started))
	if err != nil {
		writeEdgeEventStoreError(w, r, err, "append edge evaluate decision event")
		return
	}
	outcome.response.EventID = appended.EventID
	switch outcome.decision {
	case edgecore.DecisionRequireApproval:
		approval, err := s.enqueueEdgeEvaluateApproval(r.Context(), evalCtx.store, appended, outcome, actionHash, evalCtx.requesterIdentity, evalCtx.req.ApprovalTTLSeconds)
		if err != nil {
			writeEdgeApprovalStoreError(w, r, err, "enqueue edge evaluate approval")
			return
		}
		outcome = outcome.withApprovalRetryMetadata(*approval)
		if evalCtx.req.WaitForApproval {
			waitTimeout := boundEdgeEvaluateWaitTimeout(evalCtx.req.ApprovalWaitTimeoutMS)
			resolved := s.waitForEdgeApprovalResolution(r.Context(), evalCtx.store, evalCtx.tenantID, approval.ApprovalRef, waitTimeout)
			if !resolved {
				timeoutOutcome, handled, timeoutErr := s.edgeEvaluateInlineWaitTimeoutOutcome(r.Context(), evalCtx.store, evalCtx.tenantID, outcome, approval.ApprovalRef, waitTimeout)
				if timeoutErr != nil {
					writeEdgeApprovalStoreError(w, r, timeoutErr, "load edge evaluate approval after wait timeout")
					return
				}
				if handled {
					appendedFinal, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, edgeEvaluateFollowupEvent(policyInput.event), timeoutOutcome, edgeEvaluateDurationMS(started))
					if appendErr != nil {
						writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate inline-wait timeout event")
						return
					}
					timeoutOutcome.response.EventID = appendedFinal.EventID
					outcome = timeoutOutcome
					break
				}
			}
			waitedOutcome, waitErr := s.consumeEdgeEvaluateApproval(r.Context(), evalCtx.store, appended, outcome, approval.ApprovalRef, actionHash)
			if waitErr != nil {
				writeEdgeApprovalStoreError(w, r, waitErr, "consume edge evaluate approval after wait")
				return
			}
			appendedFinal, appendErr := s.appendEdgeEvaluateOutcome(r.Context(), evalCtx.store, edgeEvaluateFollowupEvent(policyInput.event), waitedOutcome, edgeEvaluateDurationMS(started))
			if appendErr != nil {
				writeEdgeEventStoreError(w, r, appendErr, "append edge evaluate inline-wait outcome event")
				return
			}
			waitedOutcome.response.EventID = appendedFinal.EventID
			outcome = waitedOutcome
		}
	}
	writeJSON(w, outcome.response)
}

type edgeEvaluateContext struct {
	req               edgeEvaluateRequest
	store             edgecore.Store
	recorder          edgecore.Recorder
	tenantID          string
	principalID       string
	requesterIdentity string
	session           *edgecore.EdgeSession
	execution         *edgecore.AgentExecution
}

func (s *server) prepareEdgeEvaluateContext(w http.ResponseWriter, r *http.Request) (edgeEvaluateContext, bool) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsWrite, "admin", "user") {
		return edgeEvaluateContext{}, false
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return edgeEvaluateContext{}, false
	}

	var req edgeEvaluateRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid edge evaluate request")
		return edgeEvaluateContext{}, false
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, req.TenantID)
	if !ok {
		return edgeEvaluateContext{}, false
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return edgeEvaluateContext{}, false
	}
	if strings.TrimSpace(principalID) == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeMissingField, "principal_id is required", nil)
		return edgeEvaluateContext{}, false
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeMissingField, "session_id is required", nil)
		return edgeEvaluateContext{}, false
	}
	executionID := strings.TrimSpace(req.ExecutionID)
	if executionID == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeMissingField, "execution_id is required", nil)
		return edgeEvaluateContext{}, false
	}

	session, found, err := store.GetSession(r.Context(), tenantID, sessionID)
	if err != nil {
		writeEdgeInternalError(w, r, "get edge evaluate session", err)
		return edgeEvaluateContext{}, false
	}
	if !found || session == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge event parent not found", nil)
		return edgeEvaluateContext{}, false
	}
	if strings.TrimSpace(session.PrincipalID) != "" && strings.TrimSpace(session.PrincipalID) != principalID {
		writeEdgeForbidden(w, r, fmt.Errorf("edge session principal mismatch"))
		return edgeEvaluateContext{}, false
	}
	if isTerminalEdgeSessionStatus(session.Status) {
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeSessionTerminal, "edge session is not actionable", nil)
		return edgeEvaluateContext{}, false
	}

	execution, found, err := store.GetExecution(r.Context(), tenantID, executionID)
	if err != nil {
		writeEdgeInternalError(w, r, "get edge evaluate execution", err)
		return edgeEvaluateContext{}, false
	}
	if !found || execution == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge event parent not found", nil)
		return edgeEvaluateContext{}, false
	}
	if execution.SessionID != sessionID {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeExecutionMismatch, "execution does not belong to session", nil)
		return edgeEvaluateContext{}, false
	}
	if isTerminalEdgeExecutionStatus(execution.Status) {
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeExecutionTerminal, "edge execution is not actionable", nil)
		return edgeEvaluateContext{}, false
	}

	requesterIdentity := submitterIdentity(r)
	if strings.TrimSpace(requesterIdentity) == "" {
		requesterIdentity = "principal:" + principalID
	}

	req.SessionID = sessionID
	req.ExecutionID = executionID
	req.TenantID = tenantID
	req.PrincipalID = principalID
	return edgeEvaluateContext{
		req:               req,
		store:             store,
		recorder:          s.edgeRecorder,
		tenantID:          tenantID,
		principalID:       principalID,
		requesterIdentity: requesterIdentity,
		session:           session,
		execution:         execution,
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
	response                 edgeEvaluateResponse
	kind                     edgecore.EventKind
	decision                 edgecore.EdgeDecision
	status                   edgecore.ActionStatus
	reason                   string
	ruleID                   string
	ruleTier                 string
	policySnapshot           string
	workflowOverrideSnapshot string
	jobOverrideSnapshot      string
	approvalRef              string
	errorCode                string
	errorMessage             string
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

func (s *server) decorateEdgeEvaluateTierEvidence(ctx context.Context, labels edgecore.Labels, outcome edgeEvaluateDecisionOutcome) edgeEvaluateDecisionOutcome {
	tier := edgeNormalizeRuleTier(s.edgeEvaluateRuleTier(ctx, outcome.ruleID))
	workflowSnapshot, jobSnapshot := edgeEvaluateScopeSnapshots(outcome.policySnapshot, labels)
	outcome.ruleTier = tier
	outcome.workflowOverrideSnapshot = workflowSnapshot
	outcome.jobOverrideSnapshot = jobSnapshot
	outcome.response.RuleTier = tier
	outcome.response.WorkflowOverrideSnapshot = workflowSnapshot
	outcome.response.JobOverrideSnapshot = jobSnapshot
	return outcome
}

func (s *server) edgeEvaluateRuleTier(ctx context.Context, ruleID string) string {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return ""
	}
	if s == nil || s.configSvc == nil {
		return config.PolicyTierGlobal
	}
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return config.PolicyTierGlobal
	}
	policy, _, err := policybundles.BuildPolicyFromBundles(bundles)
	if err != nil || policy == nil {
		return config.PolicyTierGlobal
	}
	for _, rule := range policy.Rules {
		if strings.TrimSpace(rule.ID) == ruleID {
			return config.NormalizePolicyTier(rule.Tier)
		}
	}
	return config.PolicyTierGlobal
}

func edgeEvaluateScopeSnapshots(policySnapshot string, labels edgecore.Labels) (string, string) {
	workflowScope := edgeWorkflowPolicyScope(labels)
	jobScope := edgeJobPolicyScope(labels)
	return edgeTierSnapshot(policySnapshot, "workflow", workflowScope),
		edgeTierSnapshot(policySnapshot, "job", jobScope)
}

func edgeWorkflowPolicyScope(labels edgecore.Labels) string {
	return firstEdgeEvaluateNonEmpty(labels["workflow_id"], labels["workflow.id"],
		labels["workflow"], labels["workflow_run_id"], labels["workflow.run_id"])
}

func edgeJobPolicyScope(labels edgecore.Labels) string {
	return firstEdgeEvaluateNonEmpty(labels[edgecore.LabelPolicyAttachmentID], labels["job_id"],
		labels["job.id"], labels["edge.job_id"], labels["session_id"], labels["edge.session_id"])
}

func edgeTierSnapshot(policySnapshot, tier, scope string) string {
	policySnapshot = strings.TrimSpace(policySnapshot)
	tier = strings.TrimSpace(tier)
	scope = strings.TrimSpace(scope)
	if policySnapshot == "" || tier == "" || scope == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tier + "\x00" + policySnapshot + "\x00" + scope))
	return policySnapshot + ":" + tier + ":" + hex.EncodeToString(sum[:8])
}

func edgeNormalizeRuleTier(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case config.PolicyTierGlobal, config.PolicyTierWorkflow, config.PolicyTierJob:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
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

func (outcome edgeEvaluateDecisionOutcome) withApprovalRetryMetadata(approval edgecore.EdgeApproval) edgeEvaluateDecisionOutcome {
	approvalRef := strings.TrimSpace(approval.ApprovalRef)
	actionHash := strings.TrimSpace(approval.ActionHash)
	inputHash := strings.TrimSpace(approval.InputHash)
	message := edgeEvaluateApprovalRetryMessage(defaultEdgeEvaluateReason(outcome.reason, "approval required"), approvalRef)

	outcome.approvalRef = approvalRef
	outcome.response.ApprovalRef = approvalRef
	outcome.response.ApprovalURL = edgeEvaluateApprovalDashboardPath(approvalRef)
	outcome.response.ActionHash = actionHash
	outcome.response.InputHash = inputHash
	outcome.response.WaitAfter = "approve_then_retry"
	outcome.response.PermissionDecisionReason = message
	outcome.response.TerminalMessage = message
	return outcome
}

func edgeEvaluateApprovalRetryMessage(reason, approvalRef string) string {
	reason = defaultEdgeEvaluateReason(reason, "approval required")
	if strings.TrimSpace(approvalRef) == "" {
		return reason + ". This action was not run. Approve it in Cordum, then retry the command."
	}
	return fmt.Sprintf("%s. This action was not run. Approval: %s. Approve it in Cordum, then retry the command.", reason, approvalRef)
}

func edgeEvaluateApprovalDashboardPath(approvalRef string) string {
	approvalRef = strings.TrimSpace(approvalRef)
	if approvalRef == "" {
		return ""
	}
	return "/edge/approvals/" + approvalRef
}

func edgeEvaluateActionHash(event edgecore.AgentActionEvent, policySnapshot string) (string, error) {
	riskTags := append([]string(nil), event.RiskTags...)
	sort.Strings(riskTags)
	payload := struct {
		TenantID       string             `json:"tenant_id"`
		SessionID      string             `json:"session_id"`
		ExecutionID    string             `json:"execution_id"`
		PrincipalID    string             `json:"principal_id"`
		Layer          edgecore.Layer     `json:"layer"`
		Kind           edgecore.EventKind `json:"kind"`
		ToolName       string             `json:"tool_name"`
		ToolUseID      string             `json:"tool_use_id,omitempty"`
		ActionName     string             `json:"action_name"`
		Capability     string             `json:"capability"`
		RiskTags       []string           `json:"risk_tags,omitempty"`
		Labels         edgecore.Labels    `json:"labels,omitempty"`
		InputHash      string             `json:"input_hash"`
		PolicySnapshot string             `json:"policy_snapshot"`
	}{
		TenantID:       strings.TrimSpace(event.TenantID),
		SessionID:      strings.TrimSpace(event.SessionID),
		ExecutionID:    strings.TrimSpace(event.ExecutionID),
		PrincipalID:    strings.TrimSpace(event.PrincipalID),
		Layer:          event.Layer,
		Kind:           event.Kind,
		ToolName:       strings.TrimSpace(event.ToolName),
		ToolUseID:      strings.TrimSpace(event.ToolUseID),
		ActionName:     strings.TrimSpace(event.ActionName),
		Capability:     strings.TrimSpace(event.Capability),
		RiskTags:       riskTags,
		Labels:         cloneEdgeEvaluateLabels(event.Labels),
		InputHash:      strings.TrimSpace(event.InputHash),
		PolicySnapshot: strings.TrimSpace(policySnapshot),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal edge action hash: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func cloneEdgeEvaluateLabels(labels edgecore.Labels) edgecore.Labels {
	if len(labels) == 0 {
		return nil
	}
	out := make(edgecore.Labels, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

// Inline-wait constants. The poll interval is the upper bound on how long the
// caller waits past a resolution; the default and max timeouts cap caller
// requests so the handler never blocks indefinitely.
const (
	edgeEvaluateInlineWaitPollInterval     = 250 * time.Millisecond
	edgeEvaluateInlineWaitMaxTimeout       = 5 * time.Minute
	edgeEvaluateInlineWaitDefaultTimeoutMS = 30 * 1000
)

// boundEdgeEvaluateApprovalTTL clamps the caller-requested approval TTL to
// the [1s, 20min] window (EDGE-059). Values <= 0 return the 20-min default.
// Values > 20min also return the 20-min default — callers can ONLY shorten
// the TTL, preserving the security floor against malicious indefinite-hold
// requests. Mirror of boundEdgeEvaluateWaitTimeout for the approval-TTL
// field on edgeEvaluateRequest.
func boundEdgeEvaluateApprovalTTL(requestedSec int) time.Duration {
	const defaultApprovalTTL = 20 * time.Minute
	const minApprovalTTL = time.Second
	if requestedSec <= 0 {
		return defaultApprovalTTL
	}
	requested := time.Duration(requestedSec) * time.Second
	if requested < minApprovalTTL {
		return minApprovalTTL
	}
	if requested > defaultApprovalTTL {
		return defaultApprovalTTL
	}
	return requested
}

// boundEdgeEvaluateWaitTimeout clamps the caller-requested timeout to the
// server-side window. Zero or negative falls back to the default; values larger
// than the max are capped silently because inline wait is a demo affordance and
// a caller asking for an unbounded wait is asking for a hung handler.
func boundEdgeEvaluateWaitTimeout(requestedMS int) time.Duration {
	if requestedMS <= 0 {
		return time.Duration(edgeEvaluateInlineWaitDefaultTimeoutMS) * time.Millisecond
	}
	if requestedMS > int(edgeEvaluateInlineWaitMaxTimeout/time.Millisecond) {
		return edgeEvaluateInlineWaitMaxTimeout
	}
	return time.Duration(requestedMS) * time.Millisecond
}

// waitForEdgeApprovalResolution polls the EDGE-011 approval store at a capped
// interval until the approval is non-pending, the parent context is cancelled,
// or the bounded timeout elapses. It returns true only when a non-pending
// approval was observed; false means unresolved, missing, errored, or cancelled.
// The wait holds no locks and exits cleanly via deferred cancel + ticker stop,
// so neither timeout nor request cancellation leaks goroutines or tickers.
func (s *server) waitForEdgeApprovalResolution(ctx context.Context, store edgecore.Store, tenantID, approvalRef string, timeout time.Duration) bool {
	approvalRef = strings.TrimSpace(approvalRef)
	if approvalRef == "" {
		return false
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(edgeEvaluateInlineWaitPollInterval)
	defer ticker.Stop()
	for {
		approval, found, err := store.GetApproval(waitCtx, tenantID, approvalRef)
		if err != nil {
			return false
		}
		if !found || approval == nil {
			return false
		}
		if approval.Status != edgecore.ApprovalStatusPending {
			return true
		}
		select {
		case <-waitCtx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (s *server) edgeEvaluateInlineWaitTimeoutOutcome(ctx context.Context, store edgecore.Store, tenantID string, outcome edgeEvaluateDecisionOutcome, approvalRef string, timeout time.Duration) (edgeEvaluateDecisionOutcome, bool, error) {
	approval, found, err := store.GetApproval(ctx, tenantID, approvalRef)
	if err != nil {
		return outcome, false, err
	}
	if !found || approval == nil || approval.Status != edgecore.ApprovalStatusPending {
		return outcome, false, nil
	}
	return edgeEvaluateInlineWaitTimeoutDeny(outcome, *approval, timeout), true, nil
}

func edgeEvaluateInlineWaitTimeoutDeny(outcome edgeEvaluateDecisionOutcome, approval edgecore.EdgeApproval, timeout time.Duration) edgeEvaluateDecisionOutcome {
	approvalRef := strings.TrimSpace(approval.ApprovalRef)
	reason := edgeEvaluateApprovalWaitTimeoutMessage(approvalRef)
	out := outcome.edgeEvaluateDeny(reason)
	out.approvalRef = approvalRef
	if snapshot := strings.TrimSpace(approval.PolicySnapshot); snapshot != "" {
		out.policySnapshot = snapshot
		out.response.PolicySnapshot = snapshot
	}
	out.response.ApprovalRef = approvalRef
	out.response.ApprovalURL = edgeEvaluateApprovalDashboardPath(approvalRef)
	out.response.ActionHash = strings.TrimSpace(approval.ActionHash)
	out.response.InputHash = strings.TrimSpace(approval.InputHash)
	out.response.WaitStrategy = "manual_approval"
	out.response.WaitAfter = "approve_then_retry"
	out.response.TimeoutMS = int(timeout / time.Millisecond)
	out.response.TerminalTitle = "Cordum Edge approval timed out"
	return out
}

func edgeEvaluateApprovalWaitTimeoutMessage(approvalRef string) string {
	approvalRef = strings.TrimSpace(approvalRef)
	if approvalRef == "" {
		return "approval wait timeout; this action was not run. Approve it in Cordum, then retry the command."
	}
	return fmt.Sprintf("approval wait timeout for %s; this action was not run. Approve it in Cordum, then retry the command.", approvalRef)
}

func edgeEvaluateFollowupEvent(base edgecore.AgentActionEvent) edgecore.AgentActionEvent {
	base.EventID = uuid.NewString()
	base.Seq = 0
	base.Timestamp = time.Now().UTC()
	return base
}

// consumeEdgeEvaluateApproval resolves the retry case where the caller supplied
// approval_ref. It looks up the stored EDGE-011 approval, classifies the
// terminal outcome by status, and (only for status=approved with no prior
// consume) calls the store CAS primitive to atomically claim the approval. The
// CAS rejects mismatched action_hash or policy_snapshot — that is how stale
// snapshots and modified commands are forced back to a new approval cycle. The
// fresh action_hash and policy_snapshot are recomputed from the current evaluate
// event and the latest safety decision so the binding is server-authoritative.
//
// If the fresh safety decision is anything other than REQUIRE_APPROVAL, the
// approval is not consumed: a fresh DENY/THROTTLE must win over a stale approval,
// and a fresh ALLOW does not need one. The approval's lifecycle continues until
// it is explicitly resolved or expires.
func (s *server) consumeEdgeEvaluateApproval(ctx context.Context, store edgecore.Store, event edgecore.AgentActionEvent, outcome edgeEvaluateDecisionOutcome, approvalRef, actionHash string) (edgeEvaluateDecisionOutcome, error) {
	if outcome.decision != edgecore.DecisionRequireApproval {
		return outcome, nil
	}
	tenantID := strings.TrimSpace(event.TenantID)
	approval, found, err := store.GetApproval(ctx, tenantID, approvalRef)
	if err != nil {
		return outcome, err
	}
	if !found || approval == nil {
		return edgeEvaluateRetryDeny(outcome, approvalRef, "approval not found; request a new approval"), nil
	}
	switch approval.Status {
	case edgecore.ApprovalStatusRejected:
		reason := strings.TrimSpace(approval.ResolutionReason)
		if reason == "" {
			reason = strings.TrimSpace(approval.Reason)
		}
		if reason == "" {
			reason = "approval rejected"
		}
		return edgeEvaluateRetryDeny(outcome, approvalRef, reason), nil
	case edgecore.ApprovalStatusExpired, edgecore.ApprovalStatusInvalidated:
		return edgeEvaluateRetryDeny(outcome, approvalRef, "approval expired; request a new approval"), nil
	case edgecore.ApprovalStatusPending:
		return outcome.edgeEvaluateRequireApproval().withApprovalRetryMetadata(*approval), nil
	case edgecore.ApprovalStatusApproved:
		if approval.ConsumedAt != nil {
			return edgeEvaluateRetryDeny(outcome, approvalRef, "approval already consumed; request a new approval"), nil
		}
		// CAS uses the approval's stored session/execution/event tuple — the
		// retry's freshly-appended evidence event has a new event_id that the
		// approval was never bound to. action_hash and policy_snapshot are
		// recomputed against the *current* safety decision so a stale snapshot
		// or mutated command is forced into ErrApprovalConflict.
		consumed, ok, err := store.ClaimApproval(ctx, edgecore.ApprovalClaimRequest{
			TenantID:       tenantID,
			ApprovalRef:    approvalRef,
			SessionID:      strings.TrimSpace(approval.SessionID),
			ExecutionID:    strings.TrimSpace(approval.ExecutionID),
			EventID:        strings.TrimSpace(approval.EventID),
			ActionHash:     strings.TrimSpace(actionHash),
			InputHash:      strings.TrimSpace(event.InputHash),
			PolicySnapshot: strings.TrimSpace(outcome.policySnapshot),
			ConsumedAt:     time.Now().UTC(),
			CallerAgentID:  strings.TrimSpace(event.PrincipalID),
		})
		if err != nil {
			if errors.Is(err, edgecore.ErrApprovalConflict) {
				var conflict *edgecore.ApprovalConflictError
				if errors.As(err, &conflict) && conflict.Kind == edgecore.ApprovalConflictKindSelfApproval {
					s.auditEdgeApprovalSelfApprovalDenied(tenantID, *approval, "caller_is_approver")
					return edgeEvaluateRetryDeny(outcome, approvalRef, "approval self-approval denied; request a new approval"), nil
				}
				return edgeEvaluateRetryDeny(outcome, approvalRef, "approval action or policy snapshot mismatch; request a new approval"), nil
			}
			return outcome, err
		}
		if !ok || consumed == nil {
			return edgeEvaluateRetryDeny(outcome, approvalRef, "approval not consumable; request a new approval"), nil
		}
		return outcome.edgeEvaluateRetryAllow(*consumed), nil
	default:
		return edgeEvaluateRetryDeny(outcome, approvalRef, "approval not actionable"), nil
	}
}

// edgeEvaluateRetryDeny formats a deny outcome for a retry that could not
// consume its approval. The caller-supplied reason is user-facing copy; the
// approvalRef is echoed so hooks/agentd can correlate but a fresh approval is
// always required.
func edgeEvaluateRetryDeny(outcome edgeEvaluateDecisionOutcome, approvalRef, reason string) edgeEvaluateDecisionOutcome {
	approvalRef = strings.TrimSpace(approvalRef)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "approval not consumable"
	}
	out := outcome.edgeEvaluateDeny(reason)
	out.approvalRef = approvalRef
	out.response.ApprovalRef = approvalRef
	out.response.ApprovalURL = edgeEvaluateApprovalDashboardPath(approvalRef)
	out.response.WaitAfter = "request_new_approval"
	return out
}

// edgeEvaluateRetryAllow flips the outcome to ALLOW after a successful CAS
// consume. Wait/terminal fields are cleared because the action is permitted to
// run; ActionHash/InputHash echo the consumed approval's binding so callers can
// log audit-coherent retry coordinates.
func (outcome edgeEvaluateDecisionOutcome) edgeEvaluateRetryAllow(approval edgecore.EdgeApproval) edgeEvaluateDecisionOutcome {
	approvalRef := strings.TrimSpace(approval.ApprovalRef)
	resolver := strings.TrimSpace(approval.ResolverID)
	if resolver == "" {
		resolver = strings.TrimSpace(approval.ResolvedBy)
	}
	reason := "approval consumed"
	if resolver != "" {
		reason = fmt.Sprintf("approval %s consumed (approved by %s)", approvalRef, resolver)
	} else if approvalRef != "" {
		reason = fmt.Sprintf("approval %s consumed", approvalRef)
	}

	outcome.decision = edgecore.DecisionAllow
	outcome.status = edgecore.ActionStatusOK
	outcome.reason = reason
	outcome.approvalRef = approvalRef
	outcome.response.Decision = edgecore.DecisionAllow
	outcome.response.Reason = reason
	outcome.response.PermissionDecision = "allow"
	outcome.response.PermissionDecisionReason = reason
	outcome.response.ExitCode = 0
	outcome.response.TerminalTitle = ""
	outcome.response.TerminalMessage = ""
	outcome.response.WaitStrategy = ""
	outcome.response.WaitAfter = ""
	outcome.response.ApprovalRef = approvalRef
	outcome.response.ApprovalURL = ""
	outcome.response.ActionHash = strings.TrimSpace(approval.ActionHash)
	outcome.response.InputHash = strings.TrimSpace(approval.InputHash)
	return outcome
}

// findReusableEdgeApprovalForAction returns the most recent approval bound to
// the action_hash within the same (tenant, session, execution). Callers route
// the result through consumeEdgeEvaluateApproval which handles each
// ApprovalStatus (pending → echo, approved → CAS consume, consumed → "already
// consumed", rejected/expired → corresponding deny). Returns (nil, nil) when
// no approval exists, which is the signal to fall through to the normal
// enqueue path.
//
// Scoping by session+execution avoids cross-session/cross-execution approval
// reuse — two unrelated agent runs doing the same kind of action must each
// obtain their own approval. action_hash already incorporates the policy
// snapshot, so policy changes invalidate prior approvals automatically.
//
// We deliberately bypass the (session, execution, action_hash) tuple index
// because ClaimApproval SRems the consumed approval from that index — a
// terminal "already consumed" retry would fall through to enqueue a new
// approval otherwise. Instead the lookup paginates the principal-status index
// and post-filters by (session, execution, action_hash); the agent's own
// approval list per session is bounded so the scan stays cheap.
func (s *server) findReusableEdgeApprovalForAction(ctx context.Context, store edgecore.Store, tenantID string, event edgecore.AgentActionEvent, actionHash string) (*edgecore.EdgeApproval, error) {
	tenantID = strings.TrimSpace(tenantID)
	actionHash = strings.TrimSpace(actionHash)
	sessionID := strings.TrimSpace(event.SessionID)
	executionID := strings.TrimSpace(event.ExecutionID)
	principalID := strings.TrimSpace(event.PrincipalID)
	if tenantID == "" || actionHash == "" || sessionID == "" || executionID == "" {
		return nil, nil
	}
	// EDGE-043: track all five approval statuses so consumeEdgeEvaluateApproval
	// can emit the correct explicit deny shape for rejected/expired/invalidated
	// approvals. Pre-EDGE-043, only Approved/Pending were tracked here, so
	// admin's explicit reject decisions silently fell through to the
	// enqueueEdgeEvaluateApproval path and the agent was given a fresh
	// approval cycle — equivalent to ignoring the admin's reject. Returning
	// the rejected/expired/invalidated approval routes the retry through
	// consumeEdgeEvaluateApproval (handlers_edge_evaluate.go:775) which has
	// the correct enum coverage for those terminal statuses.
	var (
		consumed    *edgecore.EdgeApproval
		approved    *edgecore.EdgeApproval
		pending     *edgecore.EdgeApproval
		rejected    *edgecore.EdgeApproval
		expired     *edgecore.EdgeApproval
		invalidated *edgecore.EdgeApproval
	)
	cursor := ""
	pagesScanned := 0
	for pagesScanned < edgeApprovalLookupMaxPages {
		page, err := store.ListApprovals(ctx, edgecore.ListApprovalsQuery{
			TenantID:    tenantID,
			PrincipalID: principalID,
			Cursor:      cursor,
			Limit:       50,
		})
		if err != nil {
			return nil, err
		}
		pagesScanned++
		for i := range page.Items {
			item := &page.Items[i]
			if item.SessionID != sessionID || item.ExecutionID != executionID || item.ActionHash != actionHash {
				continue
			}
			switch item.Status {
			case edgecore.ApprovalStatusApproved:
				if item.ConsumedAt != nil {
					if consumed == nil || consumed.ConsumedAt.Before(*item.ConsumedAt) {
						consumed = item
					}
				} else if approved == nil || approved.CreatedAt.Before(item.CreatedAt) {
					approved = item
				}
			case edgecore.ApprovalStatusPending:
				if pending == nil || pending.CreatedAt.Before(item.CreatedAt) {
					pending = item
				}
			case edgecore.ApprovalStatusRejected:
				if rejected == nil || rejected.CreatedAt.Before(item.CreatedAt) {
					rejected = item
				}
			case edgecore.ApprovalStatusExpired:
				if expired == nil || expired.CreatedAt.Before(item.CreatedAt) {
					expired = item
				}
			case edgecore.ApprovalStatusInvalidated:
				if invalidated == nil || invalidated.CreatedAt.Before(item.CreatedAt) {
					invalidated = item
				}
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	// EDGE-043 Gap 2: when the bounded scan exhausts edgeApprovalLookupMaxPages
	// without exhausting the cursor AND has not found any matching approval,
	// log loud so the silent miss is observable. The handler still falls
	// through to enqueueEdgeEvaluateApproval, but the operator can see in
	// logs that pagination is now the bottleneck for a particular tenant.
	if pagesScanned >= edgeApprovalLookupMaxPages && cursor != "" &&
		approved == nil && pending == nil && consumed == nil &&
		rejected == nil && expired == nil && invalidated == nil {
		slog.Warn("edge approval lookup exceeded pagination cap without match",
			"component", "edge-evaluate",
			"tenant", tenantID,
			"session_id", sessionID,
			"execution_id", executionID,
			"action_hash", actionHash,
			"pages_scanned", pagesScanned,
			"page_limit", 50,
			"max_pages", edgeApprovalLookupMaxPages,
		)
	}
	// Priority: live approvals (approved, pending) > terminal-but-actionable
	// (consumed → "already consumed" deny) > terminal-from-admin (rejected →
	// admin's deny reason) > expired/invalidated (request-new). Approved and
	// pending win so a fresh approval always supersedes a stale terminal
	// status; rejected/expired/invalidated fall through to consume's
	// status-specific deny shapes when no live alternative exists.
	switch {
	case approved != nil:
		return approved, nil
	case pending != nil:
		return pending, nil
	case consumed != nil:
		return consumed, nil
	case rejected != nil:
		return rejected, nil
	case expired != nil:
		return expired, nil
	case invalidated != nil:
		return invalidated, nil
	}
	return nil, nil
}

func (s *server) enqueueEdgeEvaluateApproval(ctx context.Context, store edgecore.Store, event edgecore.AgentActionEvent, outcome edgeEvaluateDecisionOutcome, actionHash, requesterIdentity string, ttlSecondsHint int) (*edgecore.EdgeApproval, error) {
	policySnapshot := strings.TrimSpace(outcome.policySnapshot)
	if policySnapshot == "" {
		policySnapshot = strings.TrimSpace(event.PolicySnapshot)
	}
	return store.EnqueueApproval(ctx, edgecore.EdgeApprovalRequest{
		TenantID:       strings.TrimSpace(event.TenantID),
		SessionID:      strings.TrimSpace(event.SessionID),
		ExecutionID:    strings.TrimSpace(event.ExecutionID),
		EventID:        strings.TrimSpace(event.EventID),
		PrincipalID:    strings.TrimSpace(event.PrincipalID),
		Requester:      strings.TrimSpace(requesterIdentity),
		Reason:         defaultEdgeEvaluateReason(outcome.reason, "approval required"),
		RuleID:         strings.TrimSpace(outcome.ruleID),
		PolicySnapshot: policySnapshot,
		ActionHash:     strings.TrimSpace(actionHash),
		InputHash:      strings.TrimSpace(event.InputHash),
		// EDGE-059 — caller-shortened TTL via edgeEvaluateRequest.ApprovalTTLSeconds.
		// boundEdgeEvaluateApprovalTTL caps at the 20-min default.
		TTL:      boundEdgeEvaluateApprovalTTL(ttlSecondsHint),
		Labels:   edgecore.Labels{"source": "edge.evaluate"},
		Metadata: edgecore.Metadata{"source": "edge.evaluate"},
	})
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
	event.RuleTier = edgeNormalizeRuleTier(outcome.ruleTier)
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
	// EDGE-014 step-10: emit a single bounded audit event per persisted
	// evaluate decision. SIEMEventForAction maps decision -> EventType
	// (allow/recorded -> policy_decision, deny -> action_denied,
	// require_approval -> approval_requested, throttle -> action_denied
	// medium). Best-effort: nil-safe + panic-recovering, so audit
	// failures never change the response.
	edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForAction(appended))
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

	inputRedacted, inputHash, err := redactEdgeEventInput(req.redactedInput(), req.inputHash(), evalCtx.recorder)
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
		EventID:      eventID,
		SessionID:    strings.TrimSpace(req.SessionID),
		ExecutionID:  strings.TrimSpace(req.ExecutionID),
		TenantID:     strings.TrimSpace(evalCtx.tenantID),
		PrincipalID:  strings.TrimSpace(evalCtx.principalID),
		Timestamp:    time.Now().UTC(),
		Layer:        req.Layer,
		Kind:         edgecore.EventKind(strings.TrimSpace(string(req.Kind))),
		AgentProduct: mustRedactEdgeString(agentProduct),
		// Attribution labels are backfilled from the parent session (already
		// sanitized at session-create); SIEMEventForAction re-bounds them
		// defensively. task-c8d4b056.
		AgentName:            evalCtx.session.AgentName,
		PrincipalDisplayName: evalCtx.session.PrincipalDisplayName,
		ToolName:             mustRedactEdgeString(req.ToolName),
		ToolUseID:            mustRedactEdgeString(req.ToolUseID),
		ActionName:           mustRedactEdgeString(req.ActionName),
		Capability:           mustRedactEdgeString(req.Capability),
		RiskTags:             riskTags,
		InputRedacted:        inputRedacted,
		InputHash:            inputHash,
		Decision:             edgecore.DecisionRecorded,
		Status:               edgecore.ActionStatusOK,
		Labels:               labels,
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
	if session != nil {
		var err error
		labels, err = edgeEvaluateMergeSessionLabels(labels, session.Labels)
		if err != nil {
			return nil, err
		}
	}
	var sessionCWD, sessionRepo, sessionGitRemote, sessionGitBranch, sessionGitSHA string
	var sessionWorkflowRunID, sessionJobID string
	if session != nil {
		sessionCWD = session.CWD
		sessionRepo = session.Repo
		sessionGitRemote = session.GitRemote
		sessionGitBranch = session.GitBranch
		sessionGitSHA = session.GitSHA
		sessionWorkflowRunID = session.WorkflowRunID
		sessionJobID = session.JobID
	}
	contextFields := map[string]string{
		"cwd":             firstEdgeEvaluateNonEmpty(req.CWD, sessionCWD),
		"repo.path":       firstEdgeEvaluateNonEmpty(req.Repo, sessionRepo),
		"git.remote":      firstEdgeEvaluateNonEmpty(req.GitRemote, sessionGitRemote),
		"git.branch":      firstEdgeEvaluateNonEmpty(req.GitBranch, sessionGitBranch),
		"git.sha":         firstEdgeEvaluateNonEmpty(req.GitSHA, sessionGitSHA),
		"workflow_run_id": sessionWorkflowRunID,
		"job_id":          sessionJobID,
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

func edgeEvaluateMergeSessionLabels(labels, sessionLabels edgecore.Labels) (edgecore.Labels, error) {
	for key, value := range sessionLabels {
		redactedKey, err := redactEdgeString(key)
		if err != nil {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
		}
		redactedValue, err := redactEdgeString(value)
		if err != nil {
			return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
		}
		if strings.TrimSpace(redactedKey) != "" && strings.TrimSpace(redactedValue) != "" {
			labels[redactedKey] = redactedValue
		}
	}
	if len(labels) > edgecore.MaxLabelEntries {
		return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
	return labels, nil
}

func edgeEvaluateMergeLabels(base edgecore.Labels, trusted edgecore.Labels) (edgecore.Labels, error) {
	if len(base) > edgecore.MaxLabelEntries || len(trusted) > edgecore.MaxLabelEntries || len(base) > edgecore.MaxLabelEntries-len(trusted) {
		return nil, edgeEventRequestError{status: http.StatusBadRequest, message: "invalid edge evaluate request"}
	}
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
