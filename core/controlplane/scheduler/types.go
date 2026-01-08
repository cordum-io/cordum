package scheduler

import (
	"context"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// Bus abstracts the message bus so the scheduler can remain decoupled
// from concrete transport implementations.
type Bus interface {
	Publish(subject string, packet *pb.BusPacket) error
	Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error
}

// SafetyDecision indicates whether a job is allowed to proceed.
type SafetyDecision string

const (
	SafetyAllow              SafetyDecision = "ALLOW"
	SafetyDeny               SafetyDecision = "DENY"
	SafetyRequireApproval    SafetyDecision = "REQUIRE_APPROVAL"
	SafetyThrottle           SafetyDecision = "THROTTLE"
	SafetyAllowWithConstraints SafetyDecision = "ALLOW_WITH_CONSTRAINTS"
)

// SafetyChecker determines if a job request may proceed.
type SafetyChecker interface {
	Check(req *pb.JobRequest) (SafetyDecisionRecord, error)
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
}

// JobState captures lifecycle for a job as seen by the scheduler.
type JobState string

const (
	JobStatePending    JobState = "PENDING"
	JobStateApproval   JobState = "APPROVAL_REQUIRED"
	JobStateScheduled  JobState = "SCHEDULED"
	JobStateDispatched JobState = "DISPATCHED"
	JobStateRunning    JobState = "RUNNING"
	JobStateSucceeded  JobState = "SUCCEEDED"
	JobStateFailed     JobState = "FAILED"
	JobStateCancelled  JobState = "CANCELLED"
	JobStateTimeout    JobState = "TIMEOUT"
	JobStateDenied     JobState = "DENIED"
)

var terminalStates = map[JobState]bool{
	JobStateSucceeded: true,
	JobStateFailed:    true,
	JobStateCancelled: true,
	JobStateTimeout:   true,
	JobStateDenied:    true,
}

// JobRecord captures a lightweight view of job state for reconciliation.
type JobRecord struct {
	ID               string   `json:"id"`
	TraceID          string   `json:"trace_id,omitempty"`
	UpdatedAt        int64    `json:"updated_at"`
	State            JobState `json:"state"`
	Topic            string   `json:"topic,omitempty"`
	Tenant           string   `json:"tenant,omitempty"`
	Team             string   `json:"team,omitempty"`
	Principal        string   `json:"principal,omitempty"`
	ActorID          string   `json:"actor_id,omitempty"`
	ActorType        string   `json:"actor_type,omitempty"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty"`
	Capability       string   `json:"capability,omitempty"`
	RiskTags         []string `json:"risk_tags,omitempty"`
	Requires         []string `json:"requires,omitempty"`
	PackID           string   `json:"pack_id,omitempty"`
	Attempts         int      `json:"attempts,omitempty"`
	SafetyDecision   string   `json:"safety_decision,omitempty"`
	SafetyReason     string   `json:"safety_reason,omitempty"`
	SafetyRuleID     string   `json:"safety_rule_id,omitempty"`
	SafetySnapshot   string   `json:"safety_snapshot,omitempty"`
	DeadlineUnix     int64    `json:"deadline_unix,omitempty"`
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
	TryAcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, key string) error
	CancelJob(ctx context.Context, jobID string) (JobState, error)
}

// SafetyDecisionRecord captures a policy decision and constraints for auditing.
type SafetyDecisionRecord struct {
	Decision         SafetyDecision        `json:"decision,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	RuleID           string                `json:"rule_id,omitempty"`
	PolicySnapshot   string                `json:"policy_snapshot,omitempty"`
	Constraints      *pb.PolicyConstraints `json:"constraints,omitempty"`
	ApprovalRequired bool                  `json:"approval_required,omitempty"`
	ApprovalRef      string                `json:"approval_ref,omitempty"`
	JobHash          string                `json:"job_hash,omitempty"`
	CheckedAt        int64                 `json:"checked_at,omitempty"`
}
