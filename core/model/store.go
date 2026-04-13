package model

import (
	"context"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ApprovalRecord captures approval audit metadata plus explicit lifecycle state.
type ApprovalRecord struct {
	ApprovedBy     string                `json:"approved_by,omitempty"`
	ApprovedRole   string                `json:"approved_role,omitempty"`
	ApprovedAt     int64                 `json:"approved_at,omitempty"`
	Reason         string                `json:"reason,omitempty"`
	Note           string                `json:"note,omitempty"`
	PolicySnapshot string                `json:"policy_snapshot,omitempty"`
	JobHash        string                `json:"job_hash,omitempty"`
	Status         ApprovalStatus        `json:"status,omitempty"`
	Actionability  ApprovalActionability `json:"actionability,omitempty"`
	Revision       int64                 `json:"revision,omitempty"`
	Decision       ApprovalDecision      `json:"decision,omitempty"`
	PublishStatus  ApprovalPublishStatus `json:"publish_status,omitempty"`
	PublishTarget  ApprovalPublishTarget `json:"publish_target,omitempty"`
	PublishedAt    int64                 `json:"published_at,omitempty"`
}

// HasPendingPublish reports whether the approval still has durable side effects
// that need replay after the decision commit.
func (r ApprovalRecord) HasPendingPublish() bool {
	return r.PublishStatus == ApprovalPublishPending && r.PublishTarget != ""
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
	RenewLock(ctx context.Context, key, token string, ttl time.Duration) error
	CancelJob(ctx context.Context, jobID string) (JobState, error)
	SetFailureReason(ctx context.Context, jobID, reason string) error
	GetFailureReason(ctx context.Context, jobID string) (string, error)
	SetOutputDecision(ctx context.Context, jobID string, record OutputSafetyRecord) error
	GetOutputDecision(ctx context.Context, jobID string) (OutputSafetyRecord, error)
	// Worker tracking
	SetWorkerID(ctx context.Context, jobID, workerID string) error
}
