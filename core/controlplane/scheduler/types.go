package scheduler

import (
	"context"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// Bus abstracts the message bus so the scheduler can remain decoupled
// from concrete transport implementations.
type Bus = model.Bus

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
type SafetyDecision = model.SafetyDecision

const (
	SafetyAllow                = model.SafetyAllow
	SafetyDeny                 = model.SafetyDeny
	SafetyRequireApproval      = model.SafetyRequireApproval
	SafetyThrottle             = model.SafetyThrottle
	SafetyAllowWithConstraints = model.SafetyAllowWithConstraints
	SafetyUnavailable          = model.SafetyUnavailable
)

// SafetyChecker determines if a job request may proceed.
type SafetyChecker interface {
	Check(ctx context.Context, req *pb.JobRequest) (SafetyDecisionRecord, error)
}

// OutputDecision indicates the result of an output policy check.
type OutputDecision = model.OutputDecision

const (
	OutputAllow      = model.OutputAllow
	OutputDeny       = model.OutputDeny
	OutputQuarantine = model.OutputQuarantine
	OutputRedact     = model.OutputRedact
)

// OutputEvaluateRequest captures output content and original job context for policy checks.
type OutputEvaluateRequest = model.OutputEvaluateRequest

type OutputFinding = model.OutputFinding

// OutputSafetyRecord captures the output policy evaluation result.
type OutputSafetyRecord = model.OutputSafetyRecord

// OutputSafetyChecker evaluates job outputs against policy rules.
type OutputSafetyChecker = model.OutputSafetyChecker

// WorkerRegistry tracks worker heartbeats and handshakes.
type WorkerRegistry interface {
	UpdateHeartbeat(hb *pb.Heartbeat)
	UpdateHandshake(hs *pb.Handshake)
	Snapshot() map[string]*pb.Heartbeat
	ReadinessSnapshot() map[string]WorkerReadiness
	// IsAlive reports whether the worker has been seen within the TTL window.
	IsAlive(workerID string) bool
}

// WorkerReadiness captures the latest readiness state advertised by a worker.
type WorkerReadiness struct {
	Ready       bool
	ReadyTopics []string
}

// SchedulingStrategy selects the target subject for a job.
type SchedulingStrategy interface {
	PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat, readiness map[string]WorkerReadiness) (string, error)
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
	IncValidationRejections()
	IncInputFailOpen(topic string)
	IncJobLockAbandoned()
	IncResultPtrWriteFailure()
	IncDispatchRollback(topic string)
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
type JobState = model.JobState

const (
	JobStatePending     = model.JobStatePending
	JobStateApproval    = model.JobStateApproval
	JobStateScheduled   = model.JobStateScheduled
	JobStateDispatched  = model.JobStateDispatched
	JobStateRunning     = model.JobStateRunning
	JobStateSucceeded   = model.JobStateSucceeded
	JobStateFailed      = model.JobStateFailed
	JobStateCancelled   = model.JobStateCancelled
	JobStateTimeout     = model.JobStateTimeout
	JobStateDenied      = model.JobStateDenied
	JobStateQuarantined = model.JobStateQuarantined
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
type JobRecord = model.JobRecord

// JobStore tracks job state and result pointers.
type JobStore = model.JobStore

// SafetyDecisionRecord captures a policy decision and constraints for auditing.
type SafetyDecisionRecord = model.SafetyDecisionRecord
