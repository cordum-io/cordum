package policybundles

import (
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/stretchr/testify/require"
)

func TestAuditEntryToSIEMEventsDecisionModes(t *testing.T) {
	entry := PolicyAuditEntry{
		Action:        "submit",
		ResourceType:  "job",
		ResourceID:    "job-123",
		ResourceName:  "job.topic",
		ActorID:       "principal-1",
		Decision:      "deny",
		MatchedRule:   "rule-1",
		PolicyVersion: "snap-1",
		Reason:        "blocked by rule",
		Extra:         map[string]string{"bundle_id": "bundle-main"},
		CreatedAt:     "2026-05-09T12:00:00Z",
	}

	cases := []struct {
		name      string
		mode      audit.UnifiedDecisionMode
		wantTypes []string
	}{
		{"dual", audit.UnifiedDecisionModeDual, []string{audit.EventSafetyDecision, audit.EventPolicyDecisionV2}},
		{"legacy", audit.UnifiedDecisionModeLegacy, []string{audit.EventSafetyDecision}},
		{"unified", audit.UnifiedDecisionModeUnified, []string{audit.EventPolicyDecisionV2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AuditEntryToSIEMEvents(entry, "tenant-acme", tc.mode)
			require.NoError(t, err)
			require.Len(t, got, len(tc.wantTypes))
			for i, wantType := range tc.wantTypes {
				require.Equal(t, wantType, got[i].EventType)
			}

			if tc.mode == audit.UnifiedDecisionModeLegacy {
				return
			}
			v2 := got[len(got)-1]
			require.Equal(t, "tenant-acme", v2.TenantID)
			require.Equal(t, "job-123", v2.Extra["resource_id"])
			require.Equal(t, "deny", v2.Decision)
			require.Equal(t, "rule-1", v2.MatchedRule)
			require.Equal(t, "snap-1", v2.PolicyVersion)
			require.Equal(t, "job", v2.Extra["source"])
			require.Equal(t, "bundle-main", v2.Extra["bundle_id"])
			require.Equal(t, "snap-1", v2.Extra["bundle_version"])
			require.Equal(t, "blocked by rule", v2.Reason)
			require.Equal(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC), v2.Timestamp)
		})
	}
}

func TestAuditEntryToSIEMEventsSkipsUnifiedForNonSafetyOrNoRule(t *testing.T) {
	changeEvent, err := AuditEntryToSIEMEvents(PolicyAuditEntry{
		Action:    "publish",
		CreatedAt: "2026-05-09T12:00:00Z",
	}, "tenant-acme", audit.UnifiedDecisionModeDual)
	require.NoError(t, err)
	require.Len(t, changeEvent, 1)
	require.Equal(t, audit.EventPolicyChange, changeEvent[0].EventType)

	noRuleEvent, err := AuditEntryToSIEMEvents(PolicyAuditEntry{
		Action:    "submit",
		Decision:  "allow",
		CreatedAt: "2026-05-09T12:00:00Z",
	}, "tenant-acme", audit.UnifiedDecisionModeUnified)
	require.NoError(t, err)
	require.Len(t, noRuleEvent, 1)
	require.Equal(t, audit.EventSafetyDecision, noRuleEvent[0].EventType)
}
