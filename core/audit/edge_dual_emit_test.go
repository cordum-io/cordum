package audit

import (
	"testing"
	"time"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestDecisionEventsForModeDualEmitsLegacyEdgeAndUnifiedV2(t *testing.T) {
	ts := time.Date(2026, 5, 9, 14, 0, 0, 0, time.UTC)
	legacy := SIEMEvent{
		Timestamp:     ts,
		EventType:     EventEdgePolicyDecision,
		Severity:      SeverityHigh,
		TenantID:      "tenant-edge",
		Action:        "bash.exec",
		Decision:      "deny",
		MatchedRule:   "edge.deny.shell",
		Reason:        "destructive shell",
		PolicyVersion: "bundle-v4",
		Extra:         map[string]string{"session_id": "edge_sess_1"},
	}
	decision := policy.Decision{
		Source:        policy.DecisionSourceEdge,
		RuleID:        "edge.deny.shell",
		BundleID:      "bundle-edge-main",
		BundleVersion: "bundle-v4",
		Type:          policy.DecisionDeny,
		InputRef:      "artifact://edge/input",
		AuditHash:     "sha256:edge-audit",
		Timestamp:     ts,
	}

	got, err := DecisionEventsForMode(UnifiedDecisionModeDual, legacy, decision)

	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, EventEdgePolicyDecision, got[0].EventType)
	require.Equal(t, EventPolicyDecisionV2, got[1].EventType)
	require.Equal(t, "edge", got[1].Extra["source"])
	require.Equal(t, "bundle-edge-main", got[1].Extra["bundle_id"])
	require.Equal(t, "artifact://edge/input", got[1].Extra["input_ref"])
	require.Equal(t, "sha256:edge-audit", got[1].Extra["audit_hash"])
	require.Equal(t, "deny", got[1].Decision)
	require.Equal(t, "edge.deny.shell", got[1].MatchedRule)
}

func TestFoldPolicyDecisionEventsIncludesJobAndEdgeSources(t *testing.T) {
	events := []SIEMEvent{
		{
			EventType:   EventPolicyDecisionV2,
			TenantID:    "tenant-a",
			Decision:    "allow",
			MatchedRule: "job.allow",
			Extra:       map[string]string{"source": "job"},
		},
		{
			EventType:   EventPolicyDecisionV2,
			TenantID:    "tenant-a",
			Decision:    "redact",
			MatchedRule: "edge.redact",
			Extra:       map[string]string{"source": "edge"},
		},
	}

	got, err := FoldPolicyDecisionEvents(events)

	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, policy.DecisionSourceJob, got[0].Source)
	require.Equal(t, policy.DecisionAllow, got[0].Decision)
	require.Equal(t, "job.allow", got[0].RuleID)
	require.Equal(t, policy.DecisionSourceEdge, got[1].Source)
	require.Equal(t, policy.DecisionRedact, got[1].Decision)
	require.Equal(t, "edge.redact", got[1].RuleID)
}
