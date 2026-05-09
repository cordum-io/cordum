package gateway

import (
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func edgeEventFromEvaluationContext(ctx edgeEvaluationContext) edgecore.AgentActionEvent {
	inputHash := strings.TrimSpace(ctx.ToolInputHash)
	if inputHash == "" {
		inputHash = strings.TrimSpace(ctx.InputHash)
	}
	return edgecore.AgentActionEvent{
		EventID:       "policy-evaluate-" + strings.TrimSpace(ctx.ExecutionID),
		TenantID:      strings.TrimSpace(ctx.TenantID),
		PrincipalID:   strings.TrimSpace(ctx.PrincipalID),
		SessionID:     strings.TrimSpace(ctx.SessionID),
		ExecutionID:   strings.TrimSpace(ctx.ExecutionID),
		Layer:         edgecore.LayerHook,
		Kind:          edgecore.EventKindHookPreToolUse,
		AgentProduct:  strings.TrimSpace(ctx.AgentProduct),
		ToolName:      strings.TrimSpace(ctx.ToolName),
		InputRedacted: clonePolicyEvalAnyMap(ctx.ToolInputRedacted),
		InputHash:     inputHash,
		RiskTags:      append([]string{}, ctx.RiskTags...),
		Labels:        edgecore.Labels(clonePolicyEvalStringMap(ctx.Labels)),
		Timestamp:     time.Now().UTC(),
	}
}

func evaluateAdaptedEdgeRule(target resolvedPolicyEvaluationTarget, rule config.PolicyRule, req *pb.PolicyCheckRequest) policy.Decision {
	input := config.PolicyInput{Tenant: req.GetTenant(), Topic: edgeInputTopic(rule, req), Labels: req.GetLabels(), Meta: policybundles.PolicyMetaFromRequest(req)}
	input.SecretsPresent = policybundles.SecretsPresent(input.Meta, req.GetLabels())
	policyDecision := (&config.SafetyPolicy{Rules: []config.PolicyRule{rule}, DefaultDecision: "allow"}).Evaluate(input)
	return decisionFromConfigPolicyDecision(policy.DecisionSourceEdge, target.rule, target.binding, policyDecision)
}

func edgeInputTopic(rule config.PolicyRule, req *pb.PolicyCheckRequest) string {
	for _, topic := range rule.Match.Topics {
		if trimmed := strings.TrimSpace(topic); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(req.GetTopic())
}
