package model

import (
	"context"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ApprovalOwnerKind identifies which subsystem owns the approval. New
// values must be enumerated here; ApprovalRecord.OwnerKind is consumed
// by audit code, dashboard rendering, and the cordumctl `mcp` subcommand.
type ApprovalOwnerKind string

const (
	// ApprovalOwnerJob is the legacy default — approvals attached to jobs.
	// Records with OwnerKind == "" are interpreted as ApprovalOwnerJob for
	// backward compatibility with pre-MCP records already in Redis.
	ApprovalOwnerJob ApprovalOwnerKind = "job"

	// ApprovalOwnerMCPCall is the per-tool approval gate added for MCP.
	// OwnerID holds the MCP-call approval ID; the job-scoped fields
	// (JobHash, Decision tied to a JobRequest, etc.) are not populated.
	ApprovalOwnerMCPCall ApprovalOwnerKind = "mcp_call"
)

// ApprovalRecord captures approval audit metadata plus explicit lifecycle state.
//
// Owner discrimination:
//   - When OwnerKind is empty or "job", the approval is keyed by JobID
//     (the existing job-meta storage). All legacy fields apply.
//   - When OwnerKind == "mcp_call", OwnerID is the approval ID and
//     MCPCallID identifies the originating tools/call invocation. JobID
//     is NOT populated; consume the new fields instead.
//
// The owner discriminator is additive (omitempty) so existing serialised
// records decode unchanged.
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

	// OwnerKind identifies which subsystem the approval belongs to.
	// Empty means legacy job-scoped behaviour (treated as "job").
	OwnerKind ApprovalOwnerKind `json:"owner_kind,omitempty"`

	// OwnerID is the canonical ID of the approval target. For job
	// approvals this mirrors the existing JobID-based access path so
	// callers can move to a uniform key without changing semantics.
	OwnerID string `json:"owner_id,omitempty"`

	// MCPCallID is the MCP-call invocation ID that produced this approval
	// (set only when OwnerKind == "mcp_call"). Together with the args
	// hash on the request it lets the server short-circuit a re-issued
	// tools/call against an already-granted approval.
	MCPCallID string `json:"mcp_call_id,omitempty"`
}

// EffectiveOwnerKind returns the owner kind, defaulting to job for legacy
// records. Use this rather than reading OwnerKind directly so the
// "" → "job" backward-compat translation lives in exactly one place.
func (r ApprovalRecord) EffectiveOwnerKind() ApprovalOwnerKind {
	if r.OwnerKind == "" {
		return ApprovalOwnerJob
	}
	return r.OwnerKind
}

// HasPendingPublish reports whether the approval still has durable side effects
// that need replay after the decision commit.
func (r ApprovalRecord) HasPendingPublish() bool {
	return r.PublishStatus == ApprovalPublishPending && r.PublishTarget != ""
}

// JobStore tracks job state and result pointers.
type JobStore interface {
	SetState(ctx context.Context, jobID string, state JobState) error
	SetStateWithContext(ctx context.Context, jobID string, state JobState, evtCtx *StateEventContext) error
	GetJobEvents(ctx context.Context, jobID string) ([]JobEvent, error)
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
	SetDelegationLineage(ctx context.Context, jobID string, lineage DelegationLineage) error
	GetDelegationLineage(ctx context.Context, jobID string) (DelegationLineage, error)
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

// DecisionLogStore indexes governance decisions for the Policy Decision Log.
type DecisionLogStore interface {
	AppendDecision(ctx context.Context, record DecisionLogRecord) error
	QueryDecisions(ctx context.Context, query DecisionQuery) (DecisionPage, error)
}

// EvalDatasetStore persists curated policy-regression evaluation datasets.
//
// Datasets are immutable once created: the store does not expose an update
// method, and Create enforces uniqueness on the (tenant, name, version)
// triple. To evolve a dataset, callers Create a new version with the same
// (tenant, name) and an incremented Version. Destruction is an admin-only
// escape hatch and is modeled as a hard delete so auditors do not discover
// silently-hidden datasets.
type EvalDatasetStore interface {
	// CreateEvalDataset persists a new dataset. The ID, CreatedAt,
	// UpdatedAt, EntryCount, and ContentHash fields are assigned by the
	// store and will overwrite whatever the caller set. Returns
	// ErrEvalDatasetVersionExists when (tenant, name, version) already
	// exists.
	CreateEvalDataset(ctx context.Context, dataset EvalDataset) (EvalDataset, error)

	// GetEvalDataset returns the dataset by id within the tenant scope, or
	// (EvalDataset{}, ErrEvalDatasetNotFound) when absent.
	GetEvalDataset(ctx context.Context, tenant, id string) (EvalDataset, error)

	// ListEvalDatasets returns a page of datasets ordered by CreatedAt
	// descending, filtered by the supplied filter.
	ListEvalDatasets(ctx context.Context, tenant string, filter EvalDatasetFilter, cursor string, limit int) (EvalDatasetPage, error)

	// DeleteEvalDataset removes a dataset and every index entry pointing
	// at it. Because the store has no soft-delete it is the caller's job
	// (typically the gateway handler) to gate this on the explicit force
	// query flag plus admin RBAC.
	DeleteEvalDataset(ctx context.Context, tenant, id string) error

	// GetEvalDatasetByNameVersion returns the dataset uniquely identified
	// by (tenant, name, version), or ErrEvalDatasetNotFound when absent.
	GetEvalDatasetByNameVersion(ctx context.Context, tenant, name string, version int) (EvalDataset, error)

	// ListEvalDatasetVersions returns every version of a dataset name in
	// the tenant, newest-version-first.
	ListEvalDatasetVersions(ctx context.Context, tenant, name string) ([]EvalDataset, error)
}
