package audit

import (
	"testing"
	"time"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDecisionMode(t *testing.T) {
	require.Equal(t, UnifiedDecisionModeDual, ParseUnifiedDecisionMode(""))
	require.Equal(t, UnifiedDecisionModeDual, ParseUnifiedDecisionMode("DUAL"))
	require.Equal(t, UnifiedDecisionModeLegacy, ParseUnifiedDecisionMode("legacy"))
	require.Equal(t, UnifiedDecisionModeUnified, ParseUnifiedDecisionMode(" unified "))
	require.Equal(t, UnifiedDecisionModeDual, ParseUnifiedDecisionMode("bogus"))
}

func TestUnifiedDecisionModeFromEnvDefaultsInvalidValues(t *testing.T) {
	t.Setenv(EnvUnifiedDecisionMode, "definitely-invalid")

	require.Equal(t, UnifiedDecisionModeDual, UnifiedDecisionModeFromEnv())
}

func TestDecisionEventsForMode(t *testing.T) {
	legacy := SIEMEvent{
		Timestamp:     time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		EventType:     EventSafetyDecision,
		Severity:      SeverityHigh,
		TenantID:      "tenant-acme",
		JobID:         "job-123",
		Action:        "submit_denied",
		Decision:      "deny",
		MatchedRule:   "legacy-rule",
		Reason:        "legacy deny",
		PolicyVersion: "legacy-snap",
	}
	decision := policy.Decision{
		Source:        policy.DecisionSourceJob,
		RuleID:        "unified-rule",
		BundleID:      "bundle-main",
		BundleVersion: "v7",
		Type:          policy.DecisionDeny,
		InputRef:      "blob://input",
		AuditHash:     "sha256:audit",
		Timestamp:     legacy.Timestamp,
	}

	cases := []struct {
		name      string
		mode      UnifiedDecisionMode
		wantTypes []string
	}{
		{"dual", UnifiedDecisionModeDual, []string{EventSafetyDecision, EventPolicyDecisionV2}},
		{"legacy", UnifiedDecisionModeLegacy, []string{EventSafetyDecision}},
		{"unified", UnifiedDecisionModeUnified, []string{EventPolicyDecisionV2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecisionEventsForMode(tc.mode, legacy, decision)
			require.NoError(t, err)
			require.Len(t, got, len(tc.wantTypes))
			for i, wantType := range tc.wantTypes {
				require.Equal(t, wantType, got[i].EventType)
			}
			if tc.mode != UnifiedDecisionModeLegacy {
				v2 := got[len(got)-1]
				require.Equal(t, "deny", v2.Decision)
				require.Equal(t, "unified-rule", v2.MatchedRule)
				require.Equal(t, "v7", v2.PolicyVersion)
				require.Equal(t, "job", v2.Extra["source"])
				require.Equal(t, "bundle-main", v2.Extra["bundle_id"])
				require.Equal(t, "sha256:audit", v2.Extra["audit_hash"])
			}
		})
	}
}

func TestDecisionEventsForModeRequiresLegacyAndUnifiedInputs(t *testing.T) {
	_, err := DecisionEventsForMode(UnifiedDecisionModeDual, SIEMEvent{}, policy.Decision{})
	require.ErrorContains(t, err, "legacy event")

	_, err = DecisionEventsForMode(
		UnifiedDecisionModeUnified,
		SIEMEvent{TenantID: "tenant-acme", EventType: EventSafetyDecision},
		policy.Decision{},
	)
	require.ErrorContains(t, err, "policy decision")
}
