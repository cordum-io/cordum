package policyreplay

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestCompareDecisions(t *testing.T) {
	cases := []struct {
		name     string
		original string
		newd     string
		want     string
	}{
		{"same allow", "ALLOW", "ALLOW", "unchanged"},
		{"same deny", "DENY", "DENY", "unchanged"},
		{"allow to deny is escalated", "ALLOW", "DENY", "escalated"},
		{"deny to allow is relaxed", "DENY", "ALLOW", "relaxed"},
		{"allow to constrain is escalated", "ALLOW", "ALLOW_WITH_CONSTRAINTS", "escalated"},
		{"require-approval to throttle is escalated", "REQUIRE_APPROVAL", "THROTTLE", "escalated"},
		{"throttle to allow is relaxed", "THROTTLE", "ALLOW", "relaxed"},
		{"case insensitive", "allow", "deny", "escalated"},
		{"mixed case", "Allow", "Require_Approval", "escalated"},
		{"unknown original returns unchanged", "SOMETHING_ELSE", "DENY", "unchanged"},
		{"unknown new returns unchanged", "DENY", "WAT", "unchanged"},
		{"empty strings return unchanged", "", "", "unchanged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareDecisions(tc.original, tc.newd)
			if got != tc.want {
				t.Fatalf("CompareDecisions(%q, %q) = %q, want %q", tc.original, tc.newd, got, tc.want)
			}
		})
	}
}

func TestDecisionSeverityOrdering(t *testing.T) {
	// The ordering is load-bearing — CompareDecisions assumes a strict
	// hierarchy. If anyone reorders this map without thinking, severity
	// comparisons flip silently. This test pins the intended order.
	want := []string{
		"ALLOW",
		"ALLOW_WITH_CONSTRAINTS",
		"REQUIRE_APPROVAL",
		"THROTTLE",
		"DENY",
	}
	for i := 0; i < len(want)-1; i++ {
		lo, hi := want[i], want[i+1]
		if DecisionSeverity[lo] >= DecisionSeverity[hi] {
			t.Fatalf("DecisionSeverity[%q]=%d should be < DecisionSeverity[%q]=%d",
				lo, DecisionSeverity[lo], hi, DecisionSeverity[hi])
		}
	}
}

func TestProtoDecisionToString(t *testing.T) {
	cases := []struct {
		name  string
		input pb.DecisionType
		want  string
	}{
		{"allow", pb.DecisionType_DECISION_TYPE_ALLOW, "ALLOW"},
		{"deny", pb.DecisionType_DECISION_TYPE_DENY, "DENY"},
		{"require_human maps to REQUIRE_APPROVAL", pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, "REQUIRE_APPROVAL"},
		{"throttle", pb.DecisionType_DECISION_TYPE_THROTTLE, "THROTTLE"},
		{"allow_with_constraints", pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS, "ALLOW_WITH_CONSTRAINTS"},
		{"unspecified falls back to ALLOW", pb.DecisionType_DECISION_TYPE_UNSPECIFIED, "ALLOW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProtoDecisionToString(tc.input)
			if got != tc.want {
				t.Fatalf("ProtoDecisionToString(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestJobRequestToPolicyCheckRequest and the EvaluateJobRequest wrapper
// are deferred to the runner task (step 2 of task-42b98ec6). Shipping
// them here would couple this package to core/controlplane/gateway/policybundles,
// which transitively pulls in core/infra/store and core/audit — both of
// which currently have pre-existing sibling-task build failures that
// block verification. The 3 pure primitives above do not depend on any
// of that chain, so they are verifiable in isolation while the
// gateway-package breakage is fixed upstream.
