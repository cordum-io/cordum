package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/policy"
)

func (s *server) emitUnifiedPolicyEvaluateAudit(ctx context.Context, req policyEvaluationRequest, decision policy.Decision) {
	if s == nil || s.auditExporter == nil {
		return
	}
	tenant := policyEvaluationTenant(req)
	legacy := audit.SIEMEvent{Timestamp: time.Now().UTC(), EventType: audit.EventSafetyDecision, Severity: audit.SeverityInfo, TenantID: tenant, Action: "policy.evaluate", Decision: decision.Type.String(), MatchedRule: decision.RuleID, Reason: policyDecisionReason(decision), PolicyVersion: decision.BundleVersion, Extra: policyDecisionAuditExtra(decision)}
	events, err := audit.DecisionEventsForMode(audit.UnifiedDecisionModeFromEnv(), legacy, decision)
	if err != nil {
		s.auditExporter.Send(legacy)
		return
	}
	for _, event := range events {
		s.auditExporter.Send(event)
	}
	_ = ctx
}

func policyEvaluationTenant(req policyEvaluationRequest) string {
	if req.JobContext != nil {
		return strings.TrimSpace(req.JobContext.TenantID)
	}
	if req.EdgeContext != nil {
		return strings.TrimSpace(req.EdgeContext.TenantID)
	}
	return ""
}

func policyDecisionReason(decision policy.Decision) string {
	for _, step := range decision.Trace {
		if reason := strings.TrimSpace(step.Reason); reason != "" {
			return reason
		}
	}
	return decision.Type.String()
}

func policyDecisionAuditExtra(decision policy.Decision) map[string]string {
	extra := map[string]string{"source": decision.Source.String()}
	if decision.BundleID != "" {
		extra["bundle_id"] = decision.BundleID
	}
	if decision.BundleVersion != "" {
		extra["bundle_version"] = decision.BundleVersion
	}
	return extra
}
