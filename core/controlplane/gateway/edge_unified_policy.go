package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (s *server) evaluateBoundUnifiedEdgeRules(
	ctx context.Context,
	evalCtx edgeEvaluateContext,
	event *edgecore.AgentActionEvent,
	req *pb.PolicyCheckRequest,
) *pb.PolicyCheckResponse {
	if s == nil || event == nil || req == nil {
		return nil
	}
	bundleID := edgeUnifiedBoundBundleID(evalCtx, event)
	if bundleID == "" || s.configSvc == nil {
		return nil
	}
	bundle, ok := s.loadUnifiedEdgeBundle(ctx, bundleID)
	if !ok {
		return nil
	}
	adapted, snapshot := edgeAdaptedRulesFromBundle(bundle, edgeUnifiedRuleScope(*event), evalCtx.policyMode)
	if len(adapted) == 0 {
		return nil
	}
	resp := edgeEvaluateAdaptedRules(adapted, snapshot, req)
	if strings.TrimSpace(resp.GetRuleId()) == "" {
		return nil
	}
	edgeAttachUnifiedBundleLabels(event, bundle, snapshot)
	return resp
}

func edgeUnifiedBoundBundleID(evalCtx edgeEvaluateContext, event *edgecore.AgentActionEvent) string {
	bundleID := edgeEvaluateBoundBundleID(event.Labels)
	if bundleID == "" && evalCtx.session != nil {
		bundleID = edgeEvaluateBoundBundleID(evalCtx.session.Labels)
	}
	return bundleID
}

func (s *server) loadUnifiedEdgeBundle(ctx context.Context, bundleID string) (policy.Bundle, bool) {
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		slog.Warn("edge unified rule lookup failed; using legacy safety path", "error", err, "bundle_id", bundleID)
		return policy.Bundle{}, false
	}
	bundle, ok, err := edgeUnifiedBundleFromRaw(bundleID, bundles[bundleID])
	if err != nil {
		slog.Warn("edge unified bundle decode failed; using legacy safety path", "error", err, "bundle_id", bundleID)
		return policy.Bundle{}, false
	}
	return bundle, ok
}

func edgeUnifiedRuleScope(event edgecore.AgentActionEvent) edgecore.EdgeRuleScopeContext {
	return edgecore.EdgeRuleScopeContext{
		TenantID:    event.TenantID,
		PrincipalID: event.PrincipalID,
		FleetID:     edgeUnifiedFleetID(event),
	}
}

func edgeEvaluateAdaptedRules(adapted []edgecore.AdaptedEdgeRule, snapshot string, req *pb.PolicyCheckRequest) *pb.PolicyCheckResponse {
	rules := make([]config.PolicyRule, 0, len(adapted))
	for _, rule := range adapted {
		rules = append(rules, rule.Rule)
	}
	return policybundles.EvaluatePolicyCheck(&config.SafetyPolicy{
		Rules:           rules,
		DefaultDecision: "allow",
	}, snapshot, req)
}

func edgeAttachUnifiedBundleLabels(event *edgecore.AgentActionEvent, bundle policy.Bundle, snapshot string) {
	if event.Labels == nil {
		event.Labels = edgecore.Labels{}
	}
	event.Labels["policy.bundle_id"] = strings.TrimSpace(bundle.ID)
	if strings.TrimSpace(snapshot) != "" {
		event.Labels["policy.bundle_version"] = strings.TrimSpace(snapshot)
	}
}

func edgeUnifiedBundleFromRaw(bundleID string, raw any) (policy.Bundle, bool, error) {
	bundleMap, ok := raw.(map[string]any)
	if !ok || bundleMap == nil || !policybundles.BundleEnabled(bundleMap) {
		return policy.Bundle{}, false, nil
	}
	if _, hasVersions := bundleMap["versions"]; !hasVersions {
		return policy.Bundle{}, false, nil
	}
	payload, err := json.Marshal(bundleMap)
	if err != nil {
		return policy.Bundle{}, false, err
	}
	var bundle policy.Bundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return policy.Bundle{}, false, err
	}
	if strings.TrimSpace(bundle.ID) == "" {
		bundle.ID = strings.TrimSpace(bundleID)
	}
	return bundle, true, nil
}

func edgeAdaptedRulesFromBundle(bundle policy.Bundle, ctx edgecore.EdgeRuleScopeContext, fallback edgecore.PolicyMode) ([]edgecore.AdaptedEdgeRule, string) {
	if len(bundle.Versions) == 0 {
		return nil, ""
	}
	version := bundle.Versions[len(bundle.Versions)-1]
	out := make([]edgecore.AdaptedEdgeRule, 0, len(version.RuleSnapshot))
	for _, rule := range version.RuleSnapshot {
		if rule.Type != policy.RuleTypeEdge || rule.Status != policy.RuleStatusPublished {
			continue
		}
		if !edgecore.RuleScopeMatchesEdge(rule.Scope, ctx) {
			continue
		}
		adapted, err := edgecore.AdaptUnifiedEdgeRule(rule, edgecore.EdgeRuleAdapterOptions{
			Bundle:       &bundle,
			FallbackMode: fallback,
		})
		if err != nil {
			slog.Warn("edge unified rule adapter rejected rule; skipping", "error", err, "rule_id", rule.ID, "bundle_id", bundle.ID)
			continue
		}
		out = append(out, adapted)
	}
	snapshot := strings.TrimSpace(version.Version)
	if snapshot == "" {
		snapshot = strings.TrimSpace(bundle.ID)
	}
	return out, snapshot
}

func edgeUnifiedFleetID(event edgecore.AgentActionEvent) string {
	for _, key := range []string{"edge.fleet_id", "fleet_id", "edge.fleet", "fleet"} {
		if value := strings.TrimSpace(event.Labels[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(event.AgentProduct)
}
