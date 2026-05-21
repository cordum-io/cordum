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

// ApprovalStatus captures the explicit lifecycle status for a human approval.
type ApprovalStatus string

const (
	ApprovalStatusPending     ApprovalStatus = "pending"
	ApprovalStatusApproved    ApprovalStatus = "approved"
	ApprovalStatusRejected    ApprovalStatus = "rejected"
	ApprovalStatusExpired     ApprovalStatus = "expired"
	ApprovalStatusInvalidated ApprovalStatus = "invalidated"
	ApprovalStatusRepaired    ApprovalStatus = "repaired"
)

// ApprovalActionability describes whether an approval can still be acted on.
type ApprovalActionability string

const (
	ApprovalActionabilityActionable  ApprovalActionability = "actionable"
	ApprovalActionabilityResolved    ApprovalActionability = "resolved"
	ApprovalActionabilityExpired     ApprovalActionability = "expired"
	ApprovalActionabilityInvalidated ApprovalActionability = "invalidated"
	ApprovalActionabilityRepaired    ApprovalActionability = "repaired"
)

// ApprovalDecision captures the last lifecycle transition applied to an approval.
type ApprovalDecision string

const (
	ApprovalDecisionApprove    ApprovalDecision = "approve"
	ApprovalDecisionReject     ApprovalDecision = "reject"
	ApprovalDecisionExpire     ApprovalDecision = "expire"
	ApprovalDecisionInvalidate ApprovalDecision = "invalidate"
	ApprovalDecisionRepair     ApprovalDecision = "repair"
)

// ApprovalPublishStatus tracks durable outbox-like replay intent for approval side effects.
type ApprovalPublishStatus string

const (
	ApprovalPublishPending   ApprovalPublishStatus = "pending"
	ApprovalPublishPublished ApprovalPublishStatus = "published"
)

// ApprovalPublishTarget describes which publish path must be replayed.
type ApprovalPublishTarget string

const (
	ApprovalPublishTargetSubmit        ApprovalPublishTarget = "submit"
	ApprovalPublishTargetDLQ           ApprovalPublishTarget = "dlq"
	ApprovalPublishTargetDLQAndResult  ApprovalPublishTarget = "dlq_and_result"
)

// ApprovalConflictCode provides machine-readable approval failure semantics.
type ApprovalConflictCode string

const (
	ApprovalConflictAlreadyResolved ApprovalConflictCode = "approval_already_resolved"
	ApprovalConflictRetryableLock   ApprovalConflictCode = "approval_retryable_lock"
	ApprovalConflictTerminalRun     ApprovalConflictCode = "approval_terminal_run"
	ApprovalConflictStaleSnapshot   ApprovalConflictCode = "approval_stale_snapshot"
	ApprovalConflictStaleRequest    ApprovalConflictCode = "approval_stale_request"
	ApprovalConflictNotActionable   ApprovalConflictCode = "approval_not_actionable"
)

func (s ApprovalStatus) DefaultActionability() ApprovalActionability {
	switch s {
	case ApprovalStatusPending:
		return ApprovalActionabilityActionable
	case ApprovalStatusApproved, ApprovalStatusRejected:
		return ApprovalActionabilityResolved
	case ApprovalStatusExpired:
		return ApprovalActionabilityExpired
	case ApprovalStatusInvalidated:
		return ApprovalActionabilityInvalidated
	case ApprovalStatusRepaired:
		return ApprovalActionabilityRepaired
	default:
		return ""
	}
}

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
	ApprovalStatus   ApprovalStatus          `json:"approval_status,omitempty"`
	Actionability    ApprovalActionability   `json:"approval_actionability,omitempty"`
	ApprovalRevision int64                   `json:"approval_revision,omitempty"`
	ApprovalDecision ApprovalDecision        `json:"approval_decision,omitempty"`
	// Governance carries compact multi-agent governance evidence when
	// the decision was produced by core/governance/evaluator. Always
	// nil for purely rule-based decisions. Cardinality-bounded:
	// IDs/hashes/refs only, never raw tokens / prompts / payloads.
	Governance *GovernanceDecisionEvidence `json:"governance,omitempty"`
}

// GovernanceDecisionEvidence is the compact non-secret audit record
// produced by core/governance/evaluator. It is intentionally
// cardinality-bounded — every field is an ID, a stable rule constant,
// a sanitized reason string, or a small, enumerated value. Raw tokens,
// private prompts, secrets, and full shared-memory payloads are NEVER
// copied here.
type GovernanceDecisionEvidence struct {
	Operation             string   `json:"operation,omitempty"`
	RuleID                string   `json:"rule_id,omitempty"`
	Decision              string   `json:"decision,omitempty"`
	Reason                string   `json:"reason,omitempty"`
	SubReason             string   `json:"sub_reason,omitempty"`
	ParentAgentID         string   `json:"parent_agent_id,omitempty"`
	ChildAgentID          string   `json:"child_agent_id,omitempty"`
	IssuerRoot            string   `json:"issuer_root,omitempty"`
	ParentIssuer          string   `json:"parent_issuer,omitempty"`
	JTI                   string   `json:"jti,omitempty"`
	ProvenanceRef         string   `json:"provenance_ref,omitempty"`
	ProvenanceVerified    bool     `json:"provenance_verified,omitempty"`
	ApprovalRef           string   `json:"approval_ref,omitempty"`
	ApprovalStatus        string   `json:"approval_status,omitempty"`
	RequestedCapabilities []string `json:"requested_capabilities,omitempty"`
	ResourceScopes        []string `json:"resource_scopes,omitempty"`
}
