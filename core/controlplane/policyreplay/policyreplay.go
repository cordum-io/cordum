package policyreplay

import (
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// DecisionSeverity is the canonical lookup table used to compare two
// decisions and answer the question "did the new decision get stricter,
// looser, or stay the same?". The ordering matches the safety-kernel
// policy envelope: ALLOW (0) < ALLOW_WITH_CONSTRAINTS (1) <
// REQUIRE_APPROVAL (2) < THROTTLE (3) < DENY (4). Unknown values are not
// present in the map so CompareDecisions treats them as "unchanged".
var DecisionSeverity = map[string]int{
	"ALLOW":                  0,
	"ALLOW_WITH_CONSTRAINTS": 1,
	"REQUIRE_APPROVAL":       2,
	"THROTTLE":               3,
	"DENY":                   4,
}

// CompareDecisions returns the drift direction from `original` to
// `newDecision` as one of "escalated" (new decision is stricter),
// "relaxed" (new decision is looser), or "unchanged" (same severity, or
// either value is not a recognized SafetyDecision). The comparison is
// case-insensitive on the canonical SCREAMING_SNAKE enum — both
// lowercase wire values and uppercase internal forms land in the same
// severity bucket.
func CompareDecisions(original, newDecision string) string {
	origSev, origOK := DecisionSeverity[strings.ToUpper(original)]
	newSev, newOK := DecisionSeverity[strings.ToUpper(newDecision)]
	if !origOK || !newOK {
		return "unchanged"
	}
	switch {
	case newSev > origSev:
		return "escalated"
	case newSev < origSev:
		return "relaxed"
	default:
		return "unchanged"
	}
}

// ProtoDecisionToString maps a pb.DecisionType enum value to its
// canonical SCREAMING_SNAKE string form. Unknown enum values fall back
// to "ALLOW" — matching the long-standing gateway behavior; callers
// that need to distinguish "unknown" from "allow" should inspect the
// enum directly.
func ProtoDecisionToString(d pb.DecisionType) string {
	switch d {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return "ALLOW"
	case pb.DecisionType_DECISION_TYPE_DENY:
		return "DENY"
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return "REQUIRE_APPROVAL"
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return "THROTTLE"
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return "ALLOW_WITH_CONSTRAINTS"
	default:
		return "ALLOW"
	}
}
