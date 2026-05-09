package agentd

import (
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestAgentdDecisionEvidenceCanEmitUnifiedEdgeDecision(t *testing.T) {
	event, err := BuildDecisionEvidenceEvent(DecisionEvidence{
		State: evidenceTestState(edgecore.PolicyModeEnterpriseStrict),
		Request: claude.AgentdRequest{
			EventName:     "PreToolUse",
			SessionID:     "edge_sess_unified",
			ExecutionID:   "edge_exec_unified",
			TenantID:      "tenant-unified",
			PrincipalID:   "principal-unified",
			ToolName:      "Bash",
			InputRedacted: map[string]any{"command": "rm -rf /tmp/project"},
			InputHash:     "sha256:input-unified",
			ActionHash:    "sha256:action-unified",
			RiskTags:      []string{"destructive", "filesystem"},
		},
		Response: edgecoreDecisionResponse(edgecore.DecisionDeny),
		Degraded: true,
	})
	require.NoError(t, err)

	got, err := edgecore.EmitDecisionForEdgeEvent(event, edgecore.EdgeDecisionEmitOptions{
		BundleID:      "agentd-local-cache",
		BundleVersion: "degraded",
	})

	require.NoError(t, err)
	require.Equal(t, policy.DecisionSourceEdge, got.Source)
	require.Equal(t, policy.DecisionDeny, got.Type)
	require.Equal(t, event.RuleID, got.RuleID)
	require.Equal(t, "agentd-local-cache", got.BundleID)
	require.Equal(t, "degraded", got.BundleVersion)
}

func edgecoreDecisionResponse(decision edgecore.EdgeDecision) EvaluateResponse {
	return EvaluateResponse{
		Decision:       string(decision),
		EventID:        "evt-agentd-unified",
		Reason:         "agentd degraded fallback",
		RuleID:         "edge.agentd.degraded",
		PolicySnapshot: "snap-agentd",
	}
}
