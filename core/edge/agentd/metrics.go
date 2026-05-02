package agentd

import (
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
)

const (
	agentdMetricsComponent      = "agentd"
	evidenceWriteFailedReason   = "evidence_write_failed"
	approvalWaitOutcomeApproved = "approved"
	approvalWaitOutcomeRejected = "rejected"
	approvalWaitOutcomeTimeout  = "timeout"
)

func (e *Evaluator) recordCacheLookup(req EvaluateRequest, result string) {
	if e == nil || e.recorder == nil {
		return
	}
	e.recorder.RecordCacheLookup(req.TenantID, req.Layer, req.Kind, result)
}

func (e *Evaluator) recordDecisionObservability(req claude.AgentdRequest, evalReq EvaluateRequest, resp EvaluateResponse, decision claude.AgentdDecision, startedAt time.Time) {
	if e == nil || e.recorder == nil {
		return
	}
	decisionLabel := decisionMetricValue(resp, decision)
	mode := string(nonEmptyPolicyMode(e.state.PolicyMode, edgecore.PolicyModeObserve))
	duration := evaluatorObservedDuration(req, startedAt)
	e.recorder.RecordActionDecision(evalReq.TenantID, evalReq.Layer, evalReq.Kind, decisionLabel, mode)
	if decision.Decision == claude.DecisionDeny {
		e.recorder.RecordActionDenied(evalReq.TenantID, evalReq.Layer, evalReq.Kind, decisionReasonCode(resp))
	}
	e.recorder.ObserveEvaluateLatency(evalReq.TenantID, evalReq.Layer, evalReq.Kind, decisionLabel, duration)
	e.recorder.ObserveHookLatency(evalReq.TenantID, req.EventName, decisionLabel, duration)
	if strings.EqualFold(strings.TrimSpace(resp.Decision), string(edgecore.DecisionRequireApproval)) {
		e.recorder.RecordApprovalRequested(evalReq.TenantID, evalReq.Layer, evalReq.Kind)
		if e.approvalConfig.InlineWaitEnabled && e.approvalWaiter != nil {
			e.recorder.RecordApprovalResolved(evalReq.TenantID, evalReq.Layer, evalReq.Kind, inlineApprovalOutcome(decision))
		}
	}
}

func (e *Evaluator) recordFailModeObservability(req claude.AgentdRequest, evalReq EvaluateRequest, outcome FailModeOutcome, category GatewayErrorCategory, startedAt time.Time) {
	if e == nil || e.recorder == nil {
		return
	}
	decisionLabel := string(outcome.Decision)
	mode := string(nonEmptyPolicyMode(e.state.PolicyMode, edgecore.PolicyModeObserve))
	reasonCode := string(category)
	if strings.TrimSpace(reasonCode) == "" {
		reasonCode = "unknown"
	}
	duration := evaluatorObservedDuration(req, startedAt)
	e.recorder.RecordActionDecision(evalReq.TenantID, evalReq.Layer, evalReq.Kind, decisionLabel, mode)
	if outcome.Decision == claude.DecisionDeny {
		e.recorder.RecordActionDenied(evalReq.TenantID, evalReq.Layer, evalReq.Kind, reasonCode)
	}
	if outcome.Degraded {
		e.recorder.RecordDegraded(evalReq.TenantID, mode, agentdMetricsComponent, reasonCode)
	}
	if outcome.FailClosed {
		e.recorder.RecordFailClosed(evalReq.TenantID, mode, reasonCode)
	}
	e.recorder.ObserveEvaluateLatency(evalReq.TenantID, evalReq.Layer, evalReq.Kind, decisionLabel, duration)
	e.recorder.ObserveHookLatency(evalReq.TenantID, req.EventName, decisionLabel, duration)
}

func (e *Evaluator) recordEvidenceFailure(req EvaluateRequest) {
	if e == nil || e.recorder == nil {
		return
	}
	mode := string(nonEmptyPolicyMode(e.state.PolicyMode, edgecore.PolicyModeObserve))
	e.recorder.RecordDegraded(req.TenantID, mode, agentdMetricsComponent, evidenceWriteFailedReason)
}

func decisionMetricValue(resp EvaluateResponse, decision claude.AgentdDecision) string {
	if strings.TrimSpace(string(decision.Decision)) != "" {
		return string(decision.Decision)
	}
	if strings.TrimSpace(resp.Decision) != "" {
		return strings.ToLower(strings.TrimSpace(resp.Decision))
	}
	return "unknown"
}

func decisionReasonCode(resp EvaluateResponse) string {
	for _, value := range []string{resp.ErrorCode, resp.RuleID, resp.WaitStrategy} {
		if strings.TrimSpace(value) != "" {
			return boundDecisionText(value)
		}
	}
	return "unknown"
}

func inlineApprovalOutcome(decision claude.AgentdDecision) string {
	switch decision.Decision {
	case claude.DecisionAllow:
		return approvalWaitOutcomeApproved
	case claude.DecisionDeny:
		reason := strings.ToLower(decision.Reason)
		if strings.Contains(reason, "reject") {
			return approvalWaitOutcomeRejected
		}
		return approvalWaitOutcomeTimeout
	default:
		return approvalWaitOutcomeTimeout
	}
}

func evaluatorObservedDuration(req claude.AgentdRequest, startedAt time.Time) time.Duration {
	if req.DurationMS > 0 {
		return time.Duration(req.DurationMS) * time.Millisecond
	}
	if startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(startedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
