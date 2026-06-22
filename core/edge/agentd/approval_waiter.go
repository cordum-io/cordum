package agentd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

const defaultInlineApprovalWaitTimeout = 30 * time.Second

type ApprovalDecisionConfig struct {
	InlineWaitEnabled bool
	InlineWaitTimeout time.Duration
	PolicyMode        edgecore.PolicyMode
}

type ApprovalWaitStatus string

const (
	ApprovalWaitApproved ApprovalWaitStatus = "approved"
	ApprovalWaitRejected ApprovalWaitStatus = "rejected"
	ApprovalWaitTimeout  ApprovalWaitStatus = "timeout"
	ApprovalWaitPending  ApprovalWaitStatus = "pending"
)

type ApprovalWaitRequest struct {
	ApprovalRef string
	Timeout     time.Duration
}

type ApprovalWaitResult struct {
	Status       ApprovalWaitStatus
	Reason       string
	UpdatedInput map[string]any
}

type ApprovalWaiter interface {
	WaitForApproval(context.Context, ApprovalWaitRequest) (ApprovalWaitResult, error)
}

// WaitForApproval calls Gateway's bounded EDGE-012 approval wait endpoint.
// The endpoint returns the current/final EdgeApproval record; agentd maps
// pending/timeouts to a DENY-at-hook decision instead of deferring execution.
func (c *GatewayClient) WaitForApproval(ctx context.Context, req ApprovalWaitRequest) (ApprovalWaitResult, error) {
	ref := strings.TrimSpace(req.ApprovalRef)
	if ref == "" {
		return ApprovalWaitResult{}, fmt.Errorf("approval_ref is required")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultInlineApprovalWaitTimeout
	}
	if timeout > maxAgentdDuration {
		timeout = maxAgentdDuration
	}
	timeoutMS := int(timeout / time.Millisecond)
	if timeoutMS <= 0 {
		timeoutMS = 1
	}
	var approval edgecore.EdgeApproval
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/edge/approvals/"+url.PathEscape(ref)+"/wait", map[string]int{"timeout_ms": timeoutMS}, &approval); err != nil {
		return ApprovalWaitResult{}, err
	}
	return approvalWaitResultFromEdgeApproval(approval), nil
}

// ApprovalConsumeFunc re-issues /api/v1/edge/evaluate carrying the approval_ref so
// the Gateway runs its single-use ClaimApproval CAS, returning the post-consume
// decision. Inline-wait ALLOW is gated on this: a passive "approved" poll must
// never authorize execution by itself. A nil consumer makes an approved inline
// wait fail closed (DENY) rather than allow without consuming.
type ApprovalConsumeFunc func(ctx context.Context, approvalRef string) (*EvaluateResponse, error)

// AgentdDecisionFromEvaluateResponse maps a fresh Gateway evaluate response to
// the compact hook-facing decision shape. Approval waits are opt-in and bounded;
// the default REQUIRE_APPROVAL behavior is immediate deny/retry guidance. On an
// approved inline wait, consume re-issues evaluate with the approval_ref so the
// single-use CAS runs before ALLOW (see approvalRequiredDecision).
func AgentdDecisionFromEvaluateResponse(ctx context.Context, resp EvaluateResponse, cfg ApprovalDecisionConfig, waiter ApprovalWaiter, consume ApprovalConsumeFunc) claude.AgentdDecision {
	switch strings.ToUpper(strings.TrimSpace(resp.Decision)) {
	case string(edgecore.DecisionAllow):
		return claude.AgentdDecision{Decision: claude.DecisionAllow}
	case string(edgecore.DecisionDeny):
		return claude.AgentdDecision{Decision: claude.DecisionDeny, Reason: decisionReason(resp)}
	case string(edgecore.DecisionRequireApproval):
		return approvalRequiredDecision(ctx, resp, cfg, waiter, consume)
	case string(edgecore.DecisionThrottle):
		reason := decisionReason(resp)
		if reason == "" {
			reason = "Cordum Edge throttled this action; retry later"
		}
		return claude.AgentdDecision{Decision: claude.DecisionDeny, Reason: boundDecisionText(reason)}
	case string(edgecore.DecisionConstrain):
		return claude.AgentdDecision{Decision: claude.DecisionAllow, UpdatedInput: cloneAnyMap(resp.UpdatedInput), Reason: boundDecisionText(resp.Reason)}
	default:
		return claude.AgentdDecision{Decision: claude.DecisionDeny, Reason: "Cordum Edge returned an invalid decision; action was not run"}
	}
}

func approvalRequiredDecision(ctx context.Context, resp EvaluateResponse, cfg ApprovalDecisionConfig, waiter ApprovalWaiter, consume ApprovalConsumeFunc) claude.AgentdDecision {
	ref := strings.TrimSpace(resp.ApprovalRef)
	if ref == "" {
		return claude.AgentdDecision{Decision: claude.DecisionDeny, Reason: "Cordum approval is required but no approval reference was returned; action was not run"}
	}
	if !cfg.InlineWaitEnabled || waiter == nil {
		return claude.AgentdDecision{
			Decision:          claude.DecisionRequireApproval,
			Reason:            approvalRetryReason(resp, "approval required"),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	}
	timeout := cfg.InlineWaitTimeout
	if timeout <= 0 {
		timeout = defaultInlineApprovalWaitTimeout
	}
	if timeout > maxAgentdDuration {
		timeout = maxAgentdDuration
	}
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := waiter.WaitForApproval(waitCtx, ApprovalWaitRequest{ApprovalRef: ref, Timeout: timeout})
	if err != nil {
		return claude.AgentdDecision{
			Decision:          claude.DecisionDeny,
			Reason:            boundDecisionText(fmt.Sprintf("approval wait unavailable for %s; approve then retry the tool call", ref)),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	}
	switch result.Status {
	case ApprovalWaitApproved:
		// EDGE consume-once: a polled "approved" status is NOT authorization.
		// Consume the approval via the Gateway single-use CAS (re-issue evaluate
		// with the approval_ref) and ALLOW only if THIS turn consumed it. Any
		// failure or non-ALLOW result fails closed, so one human approval
		// authorizes exactly one execution.
		return consumeApprovedInlineWait(ctx, resp, ref, result, consume)
	case ApprovalWaitRejected:
		reason := "approval rejected"
		if strings.TrimSpace(result.Reason) != "" {
			reason = "approval rejected: " + result.Reason
		}
		return claude.AgentdDecision{
			Decision:          claude.DecisionDeny,
			Reason:            boundDecisionText(fmt.Sprintf("%s for %s; action was not run", reason, ref)),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	case ApprovalWaitTimeout, ApprovalWaitPending, "":
		fallthrough
	default:
		return claude.AgentdDecision{
			Decision:          claude.DecisionDeny,
			Reason:            boundDecisionText(fmt.Sprintf("approval wait timed out for %s; approve then retry the tool call", ref)),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	}
}

// consumeApprovedInlineWait turns an approved inline wait into a terminal
// decision by performing the single-use consume CAS for THIS turn via consume
// (re-issue evaluate with the approval_ref). A passive "approved" status is not
// authorization; ALLOW is returned only when the Gateway confirms the consume.
func consumeApprovedInlineWait(ctx context.Context, resp EvaluateResponse, ref string, waitResult ApprovalWaitResult, consume ApprovalConsumeFunc) claude.AgentdDecision {
	if consume == nil {
		return claude.AgentdDecision{
			Decision:          claude.DecisionDeny,
			Reason:            boundDecisionText(fmt.Sprintf("approval %s approved but no consume path is configured; action was not run", ref)),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	}
	consumeResp, err := consume(ctx, ref)
	if err != nil || consumeResp == nil {
		return claude.AgentdDecision{
			Decision:          claude.DecisionDeny,
			Reason:            boundDecisionText(fmt.Sprintf("approval %s approved but could not be consumed; action was not run", ref)),
			ApprovalRef:       ref,
			AdditionalContext: approvalAdditionalContext(resp),
		}
	}
	return decisionFromConsumedEvaluate(*consumeResp, ref, waitResult)
}

// decisionFromConsumedEvaluate maps the consuming re-evaluate response to a
// terminal hook decision. ALLOW only when the Gateway confirms the single-use
// CAS succeeded (ALLOW/CONSTRAIN); every other decision (already-consumed,
// rejected, expired, error) fails closed. The reviewer's wait-result reason/
// updated input is preferred, falling back to the consume response.
func decisionFromConsumedEvaluate(resp EvaluateResponse, ref string, waitResult ApprovalWaitResult) claude.AgentdDecision {
	switch strings.ToUpper(strings.TrimSpace(resp.Decision)) {
	case string(edgecore.DecisionAllow), string(edgecore.DecisionConstrain):
		updated := cloneAnyMap(waitResult.UpdatedInput)
		if updated == nil {
			updated = cloneAnyMap(resp.UpdatedInput)
		}
		reason := boundDecisionText(waitResult.Reason)
		if reason == "" {
			reason = boundDecisionText(resp.Reason)
		}
		return claude.AgentdDecision{Decision: claude.DecisionAllow, Reason: reason, UpdatedInput: updated}
	default:
		reason := decisionReason(resp)
		if reason == "" {
			reason = fmt.Sprintf("approval %s could not be consumed; action was not run", ref)
		}
		return claude.AgentdDecision{
			Decision:    claude.DecisionDeny,
			Reason:      boundDecisionText(reason),
			ApprovalRef: ref,
		}
	}
}

func approvalRetryReason(resp EvaluateResponse, fallback string) string {
	reason := decisionReason(resp)
	if reason == "" {
		reason = fallback
	}
	ref := strings.TrimSpace(resp.ApprovalRef)
	if ref != "" {
		reason = fmt.Sprintf("%s; approval_ref=%s; approve then retry the tool call", reason, ref)
	}
	return boundDecisionText(reason)
}

func approvalAdditionalContext(resp EvaluateResponse) string {
	url := strings.TrimSpace(resp.ApprovalURL)
	if url == "" {
		return ""
	}
	return boundDecisionText("Cordum approval URL: " + url)
}

func approvalWaitResultFromEdgeApproval(approval edgecore.EdgeApproval) ApprovalWaitResult {
	reason := strings.TrimSpace(approval.ResolutionReason)
	if reason == "" {
		reason = strings.TrimSpace(approval.Reason)
	}
	result := ApprovalWaitResult{Reason: boundDecisionText(reason)}
	switch approval.Status {
	case edgecore.ApprovalStatusApproved:
		result.Status = ApprovalWaitApproved
	case edgecore.ApprovalStatusRejected, edgecore.ApprovalStatusExpired, edgecore.ApprovalStatusInvalidated:
		result.Status = ApprovalWaitRejected
	case edgecore.ApprovalStatusPending, "":
		result.Status = ApprovalWaitPending
	default:
		result.Status = ApprovalWaitPending
	}
	return result
}

func decisionReason(resp EvaluateResponse) string {
	for _, candidate := range []string{resp.TerminalMessage, resp.PermissionDecisionReason, resp.Reason, resp.ErrorMessage} {
		if strings.TrimSpace(candidate) != "" {
			return boundDecisionText(candidate)
		}
	}
	return ""
}

func boundDecisionText(value string) string {
	return boundMetadataString(redactSecretLike(strings.TrimSpace(value)))
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
