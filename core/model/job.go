package model

// JobState captures lifecycle for a job as seen by the scheduler.
type JobState string

const (
	JobStatePending    JobState = "PENDING"
	JobStateApproval   JobState = "APPROVAL_REQUIRED"
	JobStateScheduled  JobState = "SCHEDULED"
	JobStateDispatched JobState = "DISPATCHED"
	JobStateRunning    JobState = "RUNNING"
	// JobStateRetrying is a non-terminal "awaiting retry re-dispatch" state.
	// Returned by the scheduler when a worker reports JOB_STATUS_FAILED_RETRYABLE
	// — the workflow engine (or higher-level retry coordinator) is expected to
	// re-dispatch with a fresh attempt counter. Distinguishes retry-pending
	// failures from terminal JobStateFailed so retries are not silently dropped
	// by the terminal-state short-circuit in handleJobRequest.
	JobStateRetrying    JobState = "RETRYING"
	JobStateSucceeded   JobState = "SUCCEEDED"
	JobStateFailed      JobState = "FAILED"
	JobStateCancelled   JobState = "CANCELLED"
	JobStateTimeout     JobState = "TIMEOUT"
	JobStateDenied      JobState = "DENIED"
	JobStateQuarantined JobState = "OUTPUT_QUARANTINED"
)

// JobRecord captures a lightweight view of job state for reconciliation.
type JobRecord struct {
	ID             string   `json:"id"`
	WorkerID       string   `json:"worker_id,omitempty"`
	TraceID        string   `json:"trace_id,omitempty"`
	UpdatedAt      int64    `json:"updated_at"`
	State          JobState `json:"state"`
	Topic          string   `json:"topic,omitempty"`
	Tenant         string   `json:"tenant,omitempty"`
	Team           string   `json:"team,omitempty"`
	Principal      string   `json:"principal,omitempty"`
	ActorID        string   `json:"actor_id,omitempty"`
	ActorType      string   `json:"actor_type,omitempty"`
	IdempotencyKey string   `json:"idempotency_key,omitempty"`
	Capability     string   `json:"capability,omitempty"`
	RiskTags       []string `json:"risk_tags,omitempty"`
	Requires       []string `json:"requires,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	Attempts       int      `json:"attempts,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	AgentName      string   `json:"agent_name,omitempty"`
	AgentRiskTier  string   `json:"agent_risk_tier,omitempty"`
	SafetyDecision string   `json:"safety_decision,omitempty"`
	SafetyReason   string   `json:"safety_reason,omitempty"`
	SafetyRuleID   string   `json:"safety_rule_id,omitempty"`
	SafetySnapshot string   `json:"safety_snapshot,omitempty"`
	DeadlineUnix   int64    `json:"deadline_unix,omitempty"`
	FailureReason  string   `json:"failure_reason,omitempty"`
}
