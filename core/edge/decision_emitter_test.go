package edge

import (
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestEmitDecisionForEdgeEventMapsEveryEdgeDecision(t *testing.T) {
	cases := []struct {
		name         string
		edgeDecision EdgeDecision
		want         policy.DecisionType
		wantMarker   string
	}{
		{"allow", DecisionAllow, policy.DecisionAllow, ""},
		{"deny", DecisionDeny, policy.DecisionDeny, ""},
		{"require approval", DecisionRequireApproval, policy.DecisionRequireHuman, ""},
		{"throttle", DecisionThrottle, policy.DecisionThrottle, ""},
		{"constrain", DecisionConstrain, policy.DecisionRedact, ""},
		{"recorded", DecisionRecorded, policy.DecisionAllow, "recorded"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := unifiedDecisionTestEvent(tc.edgeDecision)
			got, err := EmitDecisionForEdgeEvent(event, EdgeDecisionEmitOptions{
				BundleID:      "bundle-edge-main",
				BundleVersion: "bundle-v3",
				InputRef:      "artifact://tenant-a/input",
				OutputRef:     "artifact://tenant-a/output",
				AuditHash:     "sha256:audit-edge",
				Classification: &ActionClassification{
					ActionName: "bash.exec",
					Capability: "exec.shell",
					RiskTags:   []string{"exec", "filesystem"},
				},
			})

			require.NoError(t, err)
			require.Equal(t, policy.DecisionSourceEdge, got.Source)
			require.Equal(t, event.RuleID, got.RuleID)
			require.Equal(t, "bundle-edge-main", got.BundleID)
			require.Equal(t, "bundle-v3", got.BundleVersion)
			require.Equal(t, tc.want, got.Type)
			require.Equal(t, "artifact://tenant-a/input", got.InputRef)
			require.Equal(t, "artifact://tenant-a/output", got.OutputRef)
			require.Equal(t, "sha256:audit-edge", got.AuditHash)
			require.False(t, got.Timestamp.IsZero())
			require.Equal(t, got.Timestamp.UTC(), got.Timestamp)
			require.Len(t, got.Trace, 1)
			require.Equal(t, event.RuleID, got.Trace[0].RuleID)
			require.Equal(t, tc.want, got.Trace[0].DecisionType)
			require.Contains(t, got.Trace[0].Reason, event.DecisionReason)
			if tc.wantMarker != "" {
				require.Contains(t, strings.ToLower(got.Trace[0].Reason), tc.wantMarker)
			}
		})
	}
}

func TestEmitDecisionForEdgeEventHandlesNilClassificationAndBundleContext(t *testing.T) {
	event := unifiedDecisionTestEvent(DecisionAllow)

	got, err := EmitDecisionForEdgeEvent(event, EdgeDecisionEmitOptions{})

	require.NoError(t, err)
	require.Equal(t, policy.DecisionSourceEdge, got.Source)
	require.Equal(t, policy.DecisionAllow, got.Type)
	require.Equal(t, event.RuleID, got.RuleID)
	require.Empty(t, got.BundleID)
	require.Empty(t, got.BundleVersion)
	require.Empty(t, got.InputRef)
	require.NotEmpty(t, got.Trace)
}

func TestEmitDecisionForEdgeEventRejectsMissingRuleAndUnknownDecision(t *testing.T) {
	missingRule := unifiedDecisionTestEvent(DecisionDeny)
	missingRule.RuleID = ""
	_, err := EmitDecisionForEdgeEvent(missingRule, EdgeDecisionEmitOptions{})
	require.ErrorContains(t, err, "rule")

	unknown := unifiedDecisionTestEvent(EdgeDecision("EXPLODE"))
	_, err = EmitDecisionForEdgeEvent(unknown, EdgeDecisionEmitOptions{})
	require.ErrorContains(t, err, "decision")
}

func unifiedDecisionTestEvent(decision EdgeDecision) AgentActionEvent {
	return AgentActionEvent{
		EventID:        "evt-unified-edge",
		SessionID:      "sess-unified-edge",
		ExecutionID:    "exec-unified-edge",
		TenantID:       "tenant-a",
		PrincipalID:    "principal-a",
		Timestamp:      time.Date(2026, 5, 9, 12, 30, 0, 0, time.UTC),
		Layer:          LayerHook,
		Kind:           EventKindHookPolicyDecision,
		ActionName:     "bash.exec",
		Capability:     "exec.shell",
		RiskTags:       []string{"exec"},
		Decision:       decision,
		DecisionReason: "matched edge rule",
		RuleID:         "edge.rule." + strings.ToLower(strings.ReplaceAll(string(decision), "_", "-")),
		RuleTier:       "edge",
		PolicySnapshot: "snap-edge",
		InputHash:      "sha256:input-edge",
	}
}
