package gateway

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/model"
	"google.golang.org/protobuf/encoding/protojson"
)

func (d submitPolicyDecision) auditVerdict() string {
	switch {
	case d.ApprovalRequired:
		return "require_approval"
	case d.Throttled:
		return "throttle"
	case d.Denied:
		return "deny"
	case d.Constraints != nil:
		return "constrain"
	default:
		return "allow"
	}
}

func (d submitPolicyDecision) auditExtra(topic string, labels map[string]string) map[string]string {
	base := map[string]string{}
	if topic = strings.TrimSpace(topic); topic != "" {
		base["topic"] = topic
	}
	if d.ApprovalRequired {
		base["approval_status"] = string(model.ApprovalStatusPending)
	}
	if d.Constraints != nil {
		if raw, err := protojson.Marshal(d.Constraints); err == nil && len(raw) > 0 && string(raw) != "null" {
			base["constraints"] = string(raw)
		}
	}
	return mergeStringMap(base, config.DelegationAuditExtras(config.DelegationContextFromLabels(labels)))
}

func (s *server) appendSubmitSafetyDecisionAudit(
	ctx context.Context,
	action string,
	jobID string,
	topic string,
	actorID string,
	role string,
	message string,
	decision submitPolicyDecision,
	labels map[string]string,
	agentID string,
	agentName string,
	agentRiskTier string,
) {
	_ = s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{
		Action:        action,
		ResourceType:  "job",
		ResourceID:    strings.TrimSpace(jobID),
		ResourceName:  strings.TrimSpace(topic),
		ActorID:       strings.TrimSpace(actorID),
		Role:          strings.TrimSpace(role),
		Message:       strings.TrimSpace(message),
		Reason:        strings.TrimSpace(decision.Reason),
		Decision:      decision.auditVerdict(),
		MatchedRule:   strings.TrimSpace(decision.RuleId),
		PolicyVersion: snapshotBase(strings.TrimSpace(decision.PolicySnapshot)),
		Extra:         decision.auditExtra(topic, labels),
		AgentID:       strings.TrimSpace(agentID),
		AgentName:     strings.TrimSpace(agentName),
		AgentRiskTier: strings.TrimSpace(agentRiskTier),
	})
}
