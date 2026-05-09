package gateway

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
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
	events, err := audit.DecisionEventsForMode(audit.UnifiedDecisionModeFromEnv(), legacy, decision)
	if err != nil {
		edgecore.SendSIEMEvent(s.auditExporter, legacy)
		return
	}
	for _, item := range events {
		edgecore.SendSIEMEvent(s.auditExporter, item)
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
