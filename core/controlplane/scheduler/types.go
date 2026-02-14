package scheduler

import (
	"context"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Bus abstracts the message bus so the scheduler can remain decoupled
// from concrete transport implementations.
type Bus interface {
	Publish(subject string, packet *pb.BusPacket) error
	Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error
}

// DLQEntry captures a scheduler-side dead-letter record.
type DLQEntry struct {
	JobID      string
	Topic      string
	Status     string
	Reason     string
	ReasonCode string
	LastState  string
	Attempts   int
	CreatedAt  time.Time
}

// DLQSink persists dead-letter entries to durable storage.
type DLQSink interface {
	Add(ctx context.Context, entry DLQEntry) error
}

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

// SafetyChecker determines if a job request may proceed.
type SafetyChecker interface {
	Check(req *pb.JobRequest) (SafetyDecisionRecord, error)
}

// OutputDecision indicates the result of an output policy check.
type OutputDecision string

const (
	OutputAllow      OutputDecision = "ALLOW"
	OutputDeny       OutputDecision = "DENY"
	OutputQuarantine OutputDecision = "QUARANTINE"
	OutputRedact     OutputDecision = "REDACT"
)

// OutputEvaluateRequest captures output content and original job context for policy checks.
type OutputEvaluateRequest struct {
	JobID           string
	Topic           string
	Tenant          string
	Labels          map[string]string
	ResultPtr       string
	ArtifactPtrs    []string
	ErrorMessage    string
	ErrorCode       string
	WorkerID        string
	ExecutionMs     int64
	OutputSizeBytes int64
	ContentHash     string
	WorkflowID      string
	StepID          string
	OutputContent   []byte
	Capabilities    []string
	RiskTags        []string
	PrincipalID     string
	PackID          string
	ContentType     string
	OriginalLabels  map[string]string
}

type OutputFinding struct {
	Type           string  `json:"type"`
	Severity       string  `json:"severity"`
	Detail         string  `json:"detail"`
	Scanner        string  `json:"scanner,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	MatchedPattern string  `json:"matched_pattern,omitempty"`
	Offset         int64   `json:"offset,omitempty"`
	Length         int64   `json:"length,omitempty"`
}

// OutputSafetyRecord captures the output policy evaluation result.
type OutputSafetyRecord struct {
	Decision        OutputDecision  `json:"decision"`
	Reason          string          `json:"reason,omitempty"`
	RuleID          string          `json:"rule_id,omitempty"`
	PolicySnapshot  string          `json:"policy_snapshot,omitempty"`
	Findings        []OutputFinding `json:"findings,omitempty"`
	RedactedPtr     string          `json:"redacted_ptr,omitempty"`
	OriginalPtr     string          `json:"original_ptr,omitempty"`
	CheckedAt       int64           `json:"checked_at,omitempty"`
	CheckDurationMs int64           `json:"check_duration_ms,omitempty"`
	Phase           string          `json:"phase,omitempty"` // "sync" or "async"
}

// OutputSafetyChecker evaluates job outputs against policy rules.
type OutputSafetyChecker interface {
	// EvaluateOutput runs output policy checks using dereferenced content and original request context.
	EvaluateOutput(ctx context.Context, req *OutputEvaluateRequest) (OutputSafetyRecord, error)
	// CheckOutputMeta runs fast sync checks on metadata only (~1ms).
	CheckOutputMeta(res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error)
	// CheckOutputContent runs deep async checks on actual content.
	CheckOutputContent(ctx context.Context, res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error)
}

// WorkerRegistry tracks worker heartbeats.
type WorkerRegistry interface {
	UpdateHeartbeat(hb *pb.Heartbeat)
	Snapshot() map[string]*pb.Heartbeat
}

// SchedulingStrategy selects the target subject for a job.
type SchedulingStrategy interface {
	PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error)
}

// ConfigProvider resolves effective configuration for a given context.
type ConfigProvider interface {
	Effective(ctx context.Context, orgID, teamID, workflowID, stepID string) (map[string]any, error)
}

// Metrics captures counters for scheduler events.
type Metrics interface {
	IncJobsReceived(topic string)
	IncJobsDispatched(topic string)
	IncJobsCompleted(topic, status string)
	IncSafetyDenied(topic string)
	IncSafetyUnavailable(topic string)
	IncOutputPolicyChecked(topic string)
	IncOutputPolicyQuarantined(topic string)
	IncOutputPolicySkipped(topic string)
	IncAsyncOutputTimeout(topic string)
	IncOutputEvaluations(topic string)
	IncOutputDenials(topic string)
	IncOutputRedactions(topic string)
	IncOrphanReplayed(topic string)
	ObserveJobLockWait(seconds float64)
	ObserveDispatchLatency(topic string, seconds float64)
	ObserveOutputCheckLatency(topic, phase string, seconds float64)
	ObserveOutputEvalDuration(topic string, seconds float64)
	SetActiveGoroutines(count int)
	SetStaleJobs(state string, count int)
	IncDLQEmitFailure(topic string)
	IncJobCancelFailures()
}

// SagaMetrics captures metrics for saga rollbacks and compensation handling.
type SagaMetrics interface {
	IncSagaRecorded()
	IncSagaRollbackTriggered()
	IncSagaCompensationDispatched()
	IncSagaCompensationFailed()
	ObserveSagaRollback(durationSeconds float64)
	IncSagaActive()
	DecSagaActive()
	IncSagaUnmarshalError()
}

// JobState captures lifecycle for a job as seen by the scheduler.
type JobState string

const (
	JobStatePending     JobState = "PENDING"
	JobStateApproval    JobState = "APPROVAL_REQUIRED"
	JobStateScheduled   JobState = "SCHEDULED"
	JobStateDispatched  JobState = "DISPATCHED"
	JobStateRunning     JobState = "RUNNING"
	JobStateSucceeded   JobState = "SUCCEEDED"
	JobStateFailed      JobState = "FAILED"
	JobStateCancelled   JobState = "CANCELLED"
	JobStateTimeout     JobState = "TIMEOUT"
	JobStateDenied      JobState = "DENIED"
	JobStateQuarantined JobState = "OUTPUT_QUARANTINED"
)

var terminalStates = map[JobState]bool{
	JobStateSucceeded:   true,
	JobStateFailed:      true,
	JobStateCancelled:   true,
	JobStateTimeout:     true,
	JobStateDenied:      true,
	JobStateQuarantined: true,
}

// JobRecord captures a lightweight view of job state for reconciliation.
type JobRecord struct {
	ID             string   `json:"id"`
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
	SafetyDecision string   `json:"safety_decision,omitempty"`
	SafetyReason   string   `json:"safety_reason,omitempty"`
	SafetyRuleID   string   `json:"safety_rule_id,omitempty"`
	SafetySnapshot string   `json:"safety_snapshot,omitempty"`
	DeadlineUnix   int64    `json:"deadline_unix,omitempty"`
	FailureReason  string   `json:"failure_reason,omitempty"`
}

// JobStore tracks job state and result pointers.
type JobStore interface {
	SetState(ctx context.Context, jobID string, state JobState) error
	GetState(ctx context.Context, jobID string) (JobState, error)
	SetResultPtr(ctx context.Context, jobID, resultPtr string) error
	GetResultPtr(ctx context.Context, jobID string) (string, error)
	SetJobMeta(ctx context.Context, req *pb.JobRequest) error
	SetDeadline(ctx context.Context, jobID string, deadline time.Time) error
	ListExpiredDeadlines(ctx context.Context, nowUnix int64, limit int64) ([]JobRecord, error)
	ListJobsByState(ctx context.Context, state JobState, updatedBeforeUnix int64, limit int64) ([]JobRecord, error)
	// New: Trace support
	AddJobToTrace(ctx context.Context, traceID, jobID string) error
	GetTraceJobs(ctx context.Context, traceID string) ([]JobRecord, error)
	// Metadata helpers
	SetTopic(ctx context.Context, jobID, topic string) error
	GetTopic(ctx context.Context, jobID string) (string, error)
	SetTenant(ctx context.Context, jobID, tenant string) error
	GetTenant(ctx context.Context, jobID string) (string, error)
	SetTeam(ctx context.Context, jobID, team string) error
	GetTeam(ctx context.Context, jobID string) (string, error)
	SetSafetyDecision(ctx context.Context, jobID string, record SafetyDecisionRecord) error
	GetSafetyDecision(ctx context.Context, jobID string) (SafetyDecisionRecord, error)
	GetAttempts(ctx context.Context, jobID string) (int, error)
	CountActiveByTenant(ctx context.Context, tenant string) (int, error)
	TryAcquireLock(ctx context.Context, key string, ttl time.Duration) (string, error)
	ReleaseLock(ctx context.Context, key string, token string) error
	CancelJob(ctx context.Context, jobID string) (JobState, error)
	SetFailureReason(ctx context.Context, jobID, reason string) error
	GetFailureReason(ctx context.Context, jobID string) (string, error)
	SetOutputDecision(ctx context.Context, jobID string, record OutputSafetyRecord) error
	GetOutputDecision(ctx context.Context, jobID string) (OutputSafetyRecord, error)
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
}
