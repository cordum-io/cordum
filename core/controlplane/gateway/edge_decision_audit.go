package gateway

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policy"
)

func (s *server) emitEdgeDecisionAuditEvents(ctx context.Context, store edgecore.Store, event edgecore.AgentActionEvent) {
	if event.Kind != edgecore.EventKindHookPolicyDecision {
		return
	}
	if edgeDecisionAuditAlreadyEmittedByGateway(ctx, store, event) {
		return
	}
	legacy := edgecore.SIEMEventForAction(event)
	decision, err := edgecore.EmitDecisionForEdgeEvent(event, edgeDecisionEmitOptions(event))
	if err != nil {
		edgecore.SendSIEMEvent(s.auditExporter, legacy)
		return
	}

	// Persist the unified decision before any fan-out so a fast WebSocket
	// subscriber can never observe a Decision that isn't yet readable via
	// /api/v1/policy/decisions. Order-of-write per Backend 5b Phase 8 (f).
	if s.decisionLogStore != nil {
		record := decisionLogRecordForEdge(event, decision)
		if err := s.decisionLogStore.AppendDecision(ctx, record); err != nil {
			slog.Warn("edge decision append failed",
				"event_id", event.EventID,
				"rule_id", event.RuleID,
				"error", err)
		}
	}

	// Fan-out to in-process broker AFTER persistence so live subscribers
	// see the decision but never out-of-order with the store.
	if broker := s.policyDecisionBroker(); broker != nil {
		broker.Publish(ctx, decision)
	}

	events, err := audit.DecisionEventsForMode(audit.UnifiedDecisionModeFromEnv(), legacy, decision)
	if err != nil {
		edgecore.SendSIEMEvent(s.auditExporter, legacy)
		return
	}
	for _, item := range events {
		edgecore.SendSIEMEvent(s.auditExporter, item)
	}
}

// decisionLogRecordForEdge projects an edge AgentActionEvent + the unified
// policy.Decision (built by EmitDecisionForEdgeEvent) onto the extended
// DecisionLogRecord. Edge decisions don't have a JobID — Tenant + RuleID +
// Timestamp uniquely identify them. PrincipalID maps to AgentID so the
// existing /governance/decisions transform sees a populated agent_id.
func decisionLogRecordForEdge(event edgecore.AgentActionEvent, decision policy.Decision) model.DecisionLogRecord {
	timestamp := decision.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = event.Timestamp.UTC()
	}
	return model.DecisionLogRecord{
		Tenant:        strings.TrimSpace(event.TenantID),
		AgentID:       strings.TrimSpace(event.PrincipalID),
		RuleID:        strings.TrimSpace(event.RuleID),
		Reason:        strings.TrimSpace(event.DecisionReason),
		Verdict:       safetyVerdictForEdgeDecision(decision.Type),
		PolicyVersion: strings.TrimSpace(event.PolicySnapshot),
		Timestamp:     timestamp.UnixMilli(),

		Source:        decision.Source,
		Type:          decision.Type,
		BundleID:      decision.BundleID,
		BundleVersion: decision.BundleVersion,
		Trace:         decision.Trace,
		AuditHash:     decision.AuditHash,
		InputRef:      decision.InputRef,
		OutputRef:     decision.OutputRef,
	}
}

// safetyVerdictForEdgeDecision maps the unified DecisionType onto the legacy
// SafetyDecision so /governance/decisions transforms still produce a valid
// verdict for edge-source rows. quarantine + redact map to SafetyAllow
// because the legacy edge view never recorded those outcomes; the unified
// Type field carries the truth.
func safetyVerdictForEdgeDecision(t policy.DecisionType) model.SafetyDecision {
	switch t {
	case policy.DecisionAllow, policy.DecisionQuarantine, policy.DecisionRedact:
		return model.SafetyAllow
	case policy.DecisionDeny:
		return model.SafetyDeny
	case policy.DecisionRequireHuman:
		return model.SafetyRequireApproval
	case policy.DecisionThrottle:
		return model.SafetyThrottle
	case policy.DecisionAllowWithConstraints:
		return model.SafetyAllowWithConstraints
	default:
		return model.SafetyAllow
	}
}

func edgeDecisionEmitOptions(event edgecore.AgentActionEvent) edgecore.EdgeDecisionEmitOptions {
	return edgecore.EdgeDecisionEmitOptions{
		BundleID:      edgeDecisionBundleID(event),
		BundleVersion: edgeDecisionBundleVersion(event),
		InputRef:      edgeDecisionInputRef(event),
	}
}

func edgeDecisionAuditAlreadyEmittedByGateway(ctx context.Context, store edgecore.Store, event edgecore.AgentActionEvent) bool {
	if store == nil || event.Labels == nil {
		return false
	}
	if strings.TrimSpace(event.AgentProduct) != "cordum-agentd" {
		return false
	}
	if strings.TrimSpace(event.Labels[edgecore.LabelDecisionAuditEmittedBy]) != edgecore.LabelDecisionAuditEmittedByGateway {
		return false
	}
	gatewayEventID := strings.TrimSpace(event.Labels[edgecore.LabelGatewayDecisionEventID])
	if gatewayEventID == "" {
		return false
	}
	page, err := store.ListEvents(ctx, edgecore.ListEventsQuery{
		TenantID:    strings.TrimSpace(event.TenantID),
		ExecutionID: strings.TrimSpace(event.ExecutionID),
		Kind:        edgecore.EventKindHookPolicyDecision,
		Limit:       200,
	})
	if err != nil {
		return false
	}
	for _, prior := range page.Items {
		if prior.EventID != gatewayEventID {
			continue
		}
		return prior.Kind == edgecore.EventKindHookPolicyDecision &&
			strings.TrimSpace(prior.AgentProduct) != "cordum-agentd" &&
			strings.TrimSpace(prior.RuleID) == strings.TrimSpace(event.RuleID) &&
			strings.TrimSpace(prior.InputHash) == strings.TrimSpace(event.InputHash)
	}
	return false
}

func edgeDecisionBundleID(event edgecore.AgentActionEvent) string {
	for _, key := range []string{"policy.bundle_id", "policy_bundle_id", "bundle_id"} {
		if value := strings.TrimSpace(event.Labels[key]); value != "" {
			return value
		}
	}
	return ""
}

func edgeDecisionBundleVersion(event edgecore.AgentActionEvent) string {
	for _, key := range []string{"policy.bundle_version", "policy_bundle_version", "bundle_version"} {
		if value := strings.TrimSpace(event.Labels[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(event.PolicySnapshot)
}

func edgeDecisionInputRef(event edgecore.AgentActionEvent) string {
	for _, ptr := range event.ArtifactPointers {
		if strings.TrimSpace(ptr.URI) != "" {
			return strings.TrimSpace(ptr.URI)
		}
	}
	return ""
}
