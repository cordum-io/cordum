package safetykernel

import (
	"fmt"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

func TestInputDecisionTypesTable(t *testing.T) {
	cases := []struct {
		raw         string
		wantPB      pb.DecisionType
		wantUnified policy.DecisionType
	}{
		{"deny", pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionDeny},
		{"require_approval", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, policy.DecisionRequireHuman},
		{"require-approval", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, policy.DecisionRequireHuman},
		{"require_human", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, policy.DecisionRequireHuman},
		{"unknown", pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionDeny},
		{"", pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionDeny},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("input=%q", tc.raw), func(t *testing.T) {
			gotPB, gotUnified := inputDecisionTypes(tc.raw)
			require.Equal(t, tc.wantPB, gotPB)
			require.Equal(t, tc.wantUnified, gotUnified)
		})
	}
}

func TestOutputDecisionTypesTable(t *testing.T) {
	cases := []struct {
		raw         pb.OutputDecision
		wantPB      pb.DecisionType
		wantUnified policy.DecisionType
	}{
		{pb.OutputDecision_OUTPUT_DECISION_REDACT, pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionRedact},
		{pb.OutputDecision_OUTPUT_DECISION_QUARANTINE, pb.DecisionType_DECISION_TYPE_DENY, policy.DecisionQuarantine},
		{pb.OutputDecision_OUTPUT_DECISION_ALLOW, pb.DecisionType_DECISION_TYPE_ALLOW, policy.DecisionAllow},
		{pb.OutputDecision(99), pb.DecisionType_DECISION_TYPE_ALLOW, policy.DecisionAllow},
	}
	for _, tc := range cases {
		t.Run(tc.raw.String(), func(t *testing.T) {
			gotPB, gotUnified := outputDecisionTypes(tc.raw)
			require.Equal(t, tc.wantPB, gotPB)
			require.Equal(t, tc.wantUnified, gotUnified)
		})
	}
}

func TestUnifiedDecisionFromLegacyTable(t *testing.T) {
	cases := []struct {
		raw  string
		want policy.DecisionType
	}{
		{"deny", policy.DecisionDeny},
		{"require_approval", policy.DecisionRequireHuman},
		{"require_human", policy.DecisionRequireHuman},
		{"throttle", policy.DecisionThrottle},
		{"allow_with_constraints", policy.DecisionAllowWithConstraints},
		{"allow", policy.DecisionAllow},
		{"", policy.DecisionAllow},
		{"garbage", policy.DecisionAllow},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("legacy=%q", tc.raw), func(t *testing.T) {
			require.Equal(t, tc.want, unifiedDecisionFromLegacy(tc.raw))
		})
	}
}

func TestResponseFromPolicyDecisionTable(t *testing.T) {
	cases := []struct {
		decision config.PolicyDecision
		wantPB   pb.DecisionType
		wantRule string
	}{
		{config.PolicyDecision{Decision: "deny", Reason: "blocked", RuleID: "r1"}, pb.DecisionType_DECISION_TYPE_DENY, "r1"},
		{config.PolicyDecision{Decision: "require_approval", Reason: "ask"}, pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, ""},
		{config.PolicyDecision{Decision: "throttle", Reason: "rate"}, pb.DecisionType_DECISION_TYPE_THROTTLE, ""},
		{config.PolicyDecision{Decision: "allow_with_constraints"}, pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS, ""},
		{config.PolicyDecision{Decision: "allow"}, pb.DecisionType_DECISION_TYPE_ALLOW, ""},
		{config.PolicyDecision{Decision: ""}, pb.DecisionType_DECISION_TYPE_ALLOW, ""},
	}
	for _, tc := range cases {
		t.Run(tc.decision.Decision, func(t *testing.T) {
			resp := responseFromPolicyDecision(tc.decision)
			require.Equal(t, tc.wantPB, resp.GetDecision())
			require.Equal(t, tc.wantRule, resp.GetRuleId())
		})
	}
}

func TestPolicyInputFromRequestPropagatesFields(t *testing.T) {
	req := &pb.PolicyCheckRequest{
		Tenant: "tenant-acme",
		Topic:  "job.acme.velocity",
		Labels: map[string]string{"session_id": "sess-1", "actor_type": "service"},
	}
	got := policyInputFromRequest(req)
	require.Equal(t, "tenant-acme", got.Tenant)
	require.Equal(t, "job.acme.velocity", got.Topic)
	require.Equal(t, "service", got.Labels["actor_type"])
}

func TestOutputEvalRequestFromPolicyDereferencesContent(t *testing.T) {
	req := &pb.PolicyCheckRequest{
		JobId:            "job-out-deref",
		Tenant:           "tenant-acme",
		Topic:            "job.acme.evaluate",
		InputContent:     []byte("payload"),
		InputContentType: "application/json",
		Labels:           map[string]string{"k": "v"},
		InputSizeBytes:   42,
	}
	got := outputEvalRequestFromPolicy(req)
	require.Equal(t, "job-out-deref", got.JobID)
	require.Equal(t, "job.acme.evaluate", got.Topic)
	require.Equal(t, "tenant-acme", got.Tenant)
	require.Equal(t, []byte("payload"), got.OutputContent)
	require.Equal(t, "application/json", got.ContentType)
	require.Equal(t, int64(42), got.OutputSizeBytes)
	require.Equal(t, "v", got.Labels["k"])
}
