package edge

import (
	"reflect"
	"testing"
	"time"

	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

func TestMapEventToPolicyCheckRequestWithDecisionPreservesLegacyRequestAndEmitsEdgeDecision(t *testing.T) {
	event := AgentActionEvent{
		EventID:        "evt-map-unified",
		SessionID:      "sess-map-unified",
		ExecutionID:    "exec-map-unified",
		TenantID:       "tenant-map",
		PrincipalID:    "principal-map",
		Timestamp:      time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC),
		Layer:          LayerHook,
		Kind:           EventKindHookPolicyDecision,
		ToolName:       "Bash",
		AgentProduct:   "claude-code",
		Decision:       DecisionDeny,
		DecisionReason: "blocked by edge rule",
		RuleID:         "edge.deny.shell",
		Status:         ActionStatusBlocked,
		Labels:         Labels{"custom.team": "platform"},
	}
	classification := ActionClassification{
		ActionName:       "bash.exec",
		Capability:       "exec.shell",
		RiskTags:         []string{"destructive", "exec"},
		Labels:           Labels{"command.class": "destructive"},
		InputContent:     []byte(`{"command":"rm -rf /tmp/demo"}`),
		InputContentType: "application/json",
		InputSizeBytes:   31,
	}
	mapping := PolicyMappingOptions{
		ActorID:   "actor-edge",
		ActorType: pb.ActorType_ACTOR_TYPE_HUMAN,
	}

	legacyOnly, err := MapEventToPolicyCheckRequest(event, classification, mapping)
	require.NoError(t, err)

	legacyWithDecision, decision, err := MapEventToPolicyCheckRequestWithDecision(
		event,
		classification,
		mapping,
		EdgeDecisionEmitOptions{BundleID: "bundle-edge-main", BundleVersion: "bundle-v4"},
	)

	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(legacyOnly, legacyWithDecision), "legacy request changed:\nwant=%#v\ngot=%#v", legacyOnly, legacyWithDecision)
	require.Equal(t, policy.DecisionSourceEdge, decision.Source)
	require.Equal(t, policy.DecisionDeny, decision.Type)
	require.Equal(t, event.RuleID, decision.RuleID)
	require.Equal(t, "bundle-edge-main", decision.BundleID)
	require.Equal(t, "bundle-v4", decision.BundleVersion)
}
