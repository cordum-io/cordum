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
	Subscribe(subject, queue string, handler func(*pb.BusPacket)) error
}

// SafetyDecision indicates whether a job is allowed to proceed.
type SafetyDecision int

const (
	SafetyAllow SafetyDecision = iota
	SafetyDeny
	SafetyRequireHuman
	SafetyThrottle
)

// SafetyChecker determines if a job request may proceed.
type SafetyChecker interface {
	Check(req *pb.JobRequest) (SafetyDecision, string)
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
	JobStateScheduled  JobState = "SCHEDULED"
	JobStateDispatched JobState = "DISPATCHED"
	JobStateRunning    JobState = "RUNNING"
	JobStateSucceeded  JobState = "SUCCEEDED"
	JobStateFailed     JobState = "FAILED"
	JobStateCancelled  JobState = "CANCELLED"
	JobStateTimeout    JobState = "TIMEOUT"
	JobStateDenied     JobState = "DENIED"
)

// JobRecord captures a lightweight view of job state for reconciliation.
type JobRecord struct {
	ID             string   `json:"id"`
	UpdatedAt      int64    `json:"updated_at"`
	State          JobState `json:"state"`
	Topic          string   `json:"topic,omitempty"`
	Tenant         string   `json:"tenant,omitempty"`
	Principal      string   `json:"principal,omitempty"`
	SafetyDecision string   `json:"safety_decision,omitempty"`
	SafetyReason   string   `json:"safety_reason,omitempty"`
	DeadlineUnix   int64    `json:"deadline_unix,omitempty"`
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
	SetSafetyDecision(ctx context.Context, jobID, decision, reason string) error
	GetSafetyDecision(ctx context.Context, jobID string) (decision string, reason string, err error)
}
