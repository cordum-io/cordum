package model

import pb "github.com/cordum/cordum/core/protocol/pb/v1"

// SafetyDecision indicates whether a job is allowed to proceed.
type SafetyDecision string

const (
	SafetyAllow                SafetyDecision = "ALLOW"
	SafetyDeny                 SafetyDecision = "DENY"
	SafetyRequireApproval      SafetyDecision = "REQUIRE_APPROVAL"
	SafetyThrottle             SafetyDecision = "THROTTLE"
	SafetyAllowWithConstraints SafetyDecision = "ALLOW_WITH_CONSTRAINTS"
	SafetyUnavailable          SafetyDecision = "UNAVAILABLE"
)

// SafetyDecisionRecord captures a policy decision and constraints for auditing.
type SafetyDecisionRecord struct {
	Decision         SafetyDecision          `json:"decision,omitempty"`
	Reason           string                  `json:"reason,omitempty"`
	RuleID           string                  `json:"rule_id,omitempty"`
	PolicySnapshot   string                  `json:"policy_snapshot,omitempty"`
	Constraints      *pb.PolicyConstraints   `json:"constraints,omitempty"`
	ApprovalRequired bool                    `json:"approval_required,omitempty"`
	ApprovalRef      string                  `json:"approval_ref,omitempty"`
	JobHash          string                  `json:"job_hash,omitempty"`
	Remediations     []*pb.PolicyRemediation `json:"remediations,omitempty"`
	CheckedAt        int64                   `json:"checked_at,omitempty"`
}
