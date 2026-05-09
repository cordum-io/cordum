package edge

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestAdaptUnifiedEdgeRuleMapsRuleToLegacyPolicyRule(t *testing.T) {
	rule := policy.Rule{
		ID:      "edge.deny.shell",
		Name:    "Deny destructive shell",
		Type:    policy.RuleTypeEdge,
		Scope:   policy.RuleScope{Kind: policy.RuleScopeEdgeFleet, Value: "claude-code"},
		Status:  policy.RuleStatusPublished,
		Version: "rule-v7",
		Match: edgeRuleRawJSON(t, map[string]any{
			"capabilities": []string{"exec.shell"},
			"risk_tags":    []string{"destructive", "filesystem"},
			"labels":       map[string]string{"command.class": "destructive"},
			"label_allowlist": map[string][]string{
				"agent.product": {"claude-code"},
			},
		}),
		Decide: edgeRuleRawJSON(t, map[string]any{
			"decision": "deny",
			"reason":   "destructive shell command",
			"constraints": map[string]any{
				"redaction_level": "strict",
			},
		}),
	}
	bundle := &policy.Bundle{
		ID:       "bundle-edge-main",
		Metadata: policy.BundleMetadata{EdgeMode: policy.EdgeModeEnforce},
		Versions: []policy.BundleVersion{{Version: "bundle-v3"}},
	}

	got, err := AdaptUnifiedEdgeRule(rule, EdgeRuleAdapterOptions{
		Bundle:       bundle,
		FallbackMode: PolicyModeObserve,
	})

	require.NoError(t, err)
	require.Equal(t, PolicyModeEnforce, got.PolicyMode)
	require.Equal(t, "bundle-edge-main", got.BundleID)
	require.Equal(t, "bundle-v3", got.BundleVersion)

	legacy := got.Rule
	require.Equal(t, rule.ID, legacy.ID)
	require.Equal(t, config.PolicyTierGlobal, legacy.Tier)
	require.Equal(t, "deny", legacy.Decision)
	require.Equal(t, "destructive shell command", legacy.Reason)
	require.Equal(t, []string{EdgePolicyTopic}, legacy.Match.Topics)
	require.Equal(t, []string{"exec.shell"}, legacy.Match.Capabilities)
	require.Equal(t, []string{"destructive", "filesystem"}, legacy.Match.RiskTags)
	require.Equal(t, map[string]string{"command.class": "destructive"}, legacy.Match.Labels)
	require.Equal(t, map[string][]string{"agent.product": {"claude-code"}}, legacy.Match.LabelAllowlist)
	require.Equal(t, "strict", legacy.Constraints.RedactionLevel)
}

func TestAdaptUnifiedEdgeRuleRejectsWrongTypeMissingAndCorruptPayloads(t *testing.T) {
	base := policy.Rule{
		ID:     "edge.bad",
		Type:   policy.RuleTypeEdge,
		Scope:  policy.RuleScope{Kind: policy.RuleScopeGlobal},
		Status: policy.RuleStatusPublished,
		Match:  edgeRuleRawJSON(t, map[string]any{"capabilities": []string{"exec.shell"}}),
		Decide: edgeRuleRawJSON(t, map[string]any{"decision": "deny"}),
	}

	for _, tc := range []struct {
		name       string
		mutate     func(*policy.Rule)
		wantErrSub string
	}{
		{
			name: "wrong type",
			mutate: func(rule *policy.Rule) {
				rule.Type = policy.RuleTypeInput
			},
			wantErrSub: "type edge",
		},
		{
			name: "missing match",
			mutate: func(rule *policy.Rule) {
				rule.Match = nil
			},
			wantErrSub: "match",
		},
		{
			name: "corrupt match",
			mutate: func(rule *policy.Rule) {
				rule.Match = json.RawMessage(`{"capabilities":`)
			},
			wantErrSub: "match",
		},
		{
			name: "missing decide",
			mutate: func(rule *policy.Rule) {
				rule.Decide = nil
			},
			wantErrSub: "decide",
		},
		{
			name: "unsupported decision",
			mutate: func(rule *policy.Rule) {
				rule.Decide = edgeRuleRawJSON(t, map[string]any{"decision": "explode"})
			},
			wantErrSub: "decision",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := base
			tc.mutate(&rule)

			_, err := AdaptUnifiedEdgeRule(rule, EdgeRuleAdapterOptions{})

			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), tc.wantErrSub)
		})
	}
}

func TestRuleScopeMatchesEdge(t *testing.T) {
	ctx := EdgeRuleScopeContext{
		TenantID:    "tenant-a",
		PrincipalID: "principal-7",
		FleetID:     "claude-code",
	}

	for _, tc := range []struct {
		name  string
		scope policy.RuleScope
		want  bool
	}{
		{"global", policy.RuleScope{Kind: policy.RuleScopeGlobal}, true},
		{"tenant match", policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-a"}, true},
		{"tenant mismatch", policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-b"}, false},
		{"fleet match", policy.RuleScope{Kind: policy.RuleScopeEdgeFleet, Value: "claude-code"}, true},
		{"fleet mismatch", policy.RuleScope{Kind: policy.RuleScopeEdgeFleet, Value: "other-fleet"}, false},
		{"user match", policy.RuleScope{Kind: policy.RuleScopeEdgeUser, Value: "principal-7"}, true},
		{"user mismatch", policy.RuleScope{Kind: policy.RuleScopeEdgeUser, Value: "principal-8"}, false},
		{"job workflow scope never matches edge", policy.RuleScope{Kind: policy.RuleScopeWorkflow, Value: "wf-1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, RuleScopeMatchesEdge(tc.scope, ctx))
		})
	}
}

func TestPolicyModeFromBundleMetadataFallsBackToLegacyMode(t *testing.T) {
	require.Equal(t, PolicyModeEnterpriseStrict, PolicyModeFromBundleMetadata(
		policy.Bundle{Metadata: policy.BundleMetadata{EdgeMode: policy.EdgeModeEnterpriseStrict}},
		PolicyModeObserve,
	))
	require.Equal(t, PolicyModeObserve, PolicyModeFromBundleMetadata(policy.Bundle{}, PolicyModeObserve))
	require.Equal(t, PolicyModeEnforce, PolicyModeFromBundleMetadata(
		policy.Bundle{Metadata: policy.BundleMetadata{EdgeMode: policy.EdgeModeEnforce}},
		PolicyModeEnterpriseStrict,
	))
}

func edgeRuleRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}
