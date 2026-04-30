package edge

// EdgeDecision is the action-level policy decision wire enum used by Edge.
type EdgeDecision string

const (
	DecisionAllow           EdgeDecision = "ALLOW"
	DecisionDeny            EdgeDecision = "DENY"
	DecisionRequireApproval EdgeDecision = "REQUIRE_APPROVAL"
	DecisionThrottle        EdgeDecision = "THROTTLE"
	DecisionConstrain       EdgeDecision = "CONSTRAIN"
	DecisionRecorded        EdgeDecision = "RECORDED"
)

// ActionStatus captures the observed outcome for an agent action event.
type ActionStatus string

const (
	ActionStatusOK       ActionStatus = "ok"
	ActionStatusBlocked  ActionStatus = "blocked"
	ActionStatusFailed   ActionStatus = "failed"
	ActionStatusDegraded ActionStatus = "degraded"
)
