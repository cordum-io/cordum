package scheduler

import (
	"context"

	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
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
	ID        string
	UpdatedAt int64
	State     JobState
}

// JobStore tracks job state and result pointers.
type JobStore interface {
	SetState(ctx context.Context, jobID string, state JobState) error
	GetState(ctx context.Context, jobID string) (JobState, error)
	SetResultPtr(ctx context.Context, jobID, resultPtr string) error
	GetResultPtr(ctx context.Context, jobID string) (string, error)
	ListJobsByState(ctx context.Context, state JobState, updatedBeforeUnix int64, limit int64) ([]JobRecord, error)
	// New: Trace support
	AddJobToTrace(ctx context.Context, traceID, jobID string) error
	GetTraceJobs(ctx context.Context, traceID string) ([]JobRecord, error)
	// Metadata helpers
	SetTopic(ctx context.Context, jobID, topic string) error
	GetTopic(ctx context.Context, jobID string) (string, error)
}
