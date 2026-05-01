package edge

import "time"

// ApprovalStatus captures the lifecycle state for a generic Edge approval.
type ApprovalStatus string

const (
	ApprovalStatusPending     ApprovalStatus = "pending"
	ApprovalStatusApproved    ApprovalStatus = "approved"
	ApprovalStatusRejected    ApprovalStatus = "rejected"
	ApprovalStatusExpired     ApprovalStatus = "expired"
	ApprovalStatusInvalidated ApprovalStatus = "invalidated"
)

// ApprovalDecision captures the resolver decision wire value for an Edge approval.
type ApprovalDecision string

const (
	ApprovalDecisionApprove    ApprovalDecision = "approve"
	ApprovalDecisionReject     ApprovalDecision = "reject"
	ApprovalDecisionExpire     ApprovalDecision = "expire"
	ApprovalDecisionInvalidate ApprovalDecision = "invalidate"
)

// EdgeApproval is a generic approval record for action-level governance across
// hook, MCP, LLM, runtime, and workflow layers.
type EdgeApproval struct {
	ApprovalRef      string           `json:"approval_ref"`
	TenantID         string           `json:"tenant_id"`
	SessionID        string           `json:"session_id"`
	ExecutionID      string           `json:"execution_id"`
	EventID          string           `json:"event_id"`
	PrincipalID      string           `json:"principal_id"`
	Requester        string           `json:"requester"`
	ResolverID       string           `json:"resolver_id"`
	ResolvedBy       string           `json:"resolved_by"`
	Status           ApprovalStatus   `json:"status"`
	Decision         ApprovalDecision `json:"decision"`
	Reason           string           `json:"reason"`
	ResolutionReason string           `json:"resolution_reason"`
	RuleID           string           `json:"rule_id"`
	PolicySnapshot   string           `json:"policy_snapshot"`
	ActionHash       string           `json:"action_hash"`
	InputHash        string           `json:"input_hash"`
	CreatedAt        time.Time        `json:"created_at"`
	ExpiresAt        *time.Time       `json:"expires_at"`
	ResolvedAt       *time.Time       `json:"resolved_at"`
	ConsumedAt       *time.Time       `json:"consumed_at"`
	Labels           Labels           `json:"labels"`
	Metadata         Metadata         `json:"metadata"`
}
