package model

import "context"

// JobEventApplyDisposition reports the durable fence decision.
type JobEventApplyDisposition uint8

const (
	JobEventRejected JobEventApplyDisposition = iota
	JobEventApplied
	JobEventDuplicate
)

// JobResultApply is the authenticated input to the atomic result commit.
type JobResultApply struct {
	JobID, DispatchID, WorkerID, Tenant string
	Attempt                             int
	MessageID, Digest, Effect           []byte
	State                               JobState
	ResultPtr                           string
}

// JobEffect is a durable post-commit action awaiting replay.
type JobEffect struct {
	JobID, EventID  string
	Digest, Payload []byte
}

// DurableJobEventStore extends JobStore with cluster-safe event application.
type DurableJobEventStore interface {
	AcceptSignedJobEvent(context.Context, string, string, int, string, string, []byte, []byte) (JobEventApplyDisposition, error)
	ApplyJobResult(context.Context, JobResultApply) (JobEventApplyDisposition, error)
	PendingJobEffects(context.Context, int64) ([]JobEffect, error)
	AckJobEffect(context.Context, JobEffect) (bool, error)
	ProjectJobResult(context.Context, string, JobState, string, string) error
	RollbackDispatch(context.Context, string, string, int) (bool, error)
	CancelAllJobAttempts(context.Context, string) (JobState, error)
}
