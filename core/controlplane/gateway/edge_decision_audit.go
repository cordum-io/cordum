package gateway

import (
	"strings"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
)

func (s *server) emitEdgeDecisionAuditEvents(event edgecore.AgentActionEvent) {
	if event.Kind != edgecore.EventKindHookPolicyDecision {
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
		BundleVersion: strings.TrimSpace(event.PolicySnapshot),
		InputRef:      edgeDecisionInputRef(event),
	}
}

func edgeDecisionInputRef(event edgecore.AgentActionEvent) string {
	for _, ptr := range event.ArtifactPointers {
		if strings.TrimSpace(ptr.URI) != "" {
			return strings.TrimSpace(ptr.URI)
		}
	}
	return ""
}
