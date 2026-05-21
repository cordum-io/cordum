// EDGE-141 — Shadow agent finding model, store interface, validation.
//
// This file defines the persisted ShadowAgentFinding lifecycle record.
// The scanner-only Finding type in finding.go (status set
// {observed|unreadable|managed_skip|partial}) describes a single
// detection observation; ShadowAgentFinding is the long-lived
// lifecycle record that operators triage and dispose of via the
// /api/v1/edge/shadow-agents/* APIs.
//
// Lifecycle is observe/warn ONLY (epic rail "shadow detection defaults
// to opt-in observe mode only"): findings transition between
// {detected, resolved, suppressed}; no enforcement, no remediation
// execution, no Cordum Job creation. The store is responsible for
// validation, redaction, indexing, and retention TTLs only.
package shadow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

// FindingStatus is the lifecycle status of a persisted finding. Distinct
// from the scanner-only Status constants — this enum tracks operator
// disposition (detected → triaged into resolved/suppressed), not the
// detection observation.
type FindingStatus string

const (
	// FindingStatusDetected — finding is fresh, awaiting operator triage.
	FindingStatusDetected FindingStatus = "detected"
	// FindingStatusResolved — operator confirmed the shadow agent was
	// remediated (uninstalled, brought under management, false positive).
	// Terminal: writes never overwrite, retention TTL applies.
	FindingStatusResolved FindingStatus = "resolved"
	// FindingStatusSuppressed — operator deferred / accepted the finding
	// (e.g., approved exception, low-risk service account). Terminal:
	// writes never overwrite, retention TTL applies, may carry a
	// suppressed_until hint for time-bound exceptions.
	FindingStatusSuppressed FindingStatus = "suppressed"
)

// FindingRisk classifies the operator-visible severity of the finding.
// Mirrors the scanner Risk* constants (low/medium/high) but is its own
// type so a future divergence in lifecycle severity vs detection
// severity does not require a breaking rename.
type FindingRisk string

const (
	FindingRiskLow      FindingRisk = "low"
	FindingRiskMedium   FindingRisk = "medium"
	FindingRiskHigh     FindingRisk = "high"
	FindingRiskCritical FindingRisk = "critical"
)

// ShadowFindingRetentionClass drives per-finding terminal-retention TTL
// (§10.5). Distinct from edgecore.RetentionClass because shadow lifecycle
// TTLs (7d/90d/365d) are semantically different from edgecore's artifact
// retention classes (short/standard/audit).
type ShadowFindingRetentionClass string

const (
	ShadowRetentionShort   ShadowFindingRetentionClass = "shadow_short"
	ShadowRetentionDefault ShadowFindingRetentionClass = "shadow_default"
	ShadowRetentionLong    ShadowFindingRetentionClass = "shadow_long"
)

// SourceType identifies the detector family that emitted a finding
// (§10.1). Empty on legacy EDGE-141 records; defaults to "local" on read.
const (
	SourceTypeLocal      = "local"
	SourceTypeKubernetes = "kubernetes"
	SourceTypeCI         = "ci"
	SourceTypeNetwork    = "network"
)

// CI provider enum per §10.1.
const (
	CIProviderGitHubActions = "github_actions"
	CIProviderGitLabCI      = "gitlab_ci"
	CIProviderJenkins       = "jenkins"
	CIProviderBuildkite     = "buildkite"
	CIProviderCircleCI      = "circleci"
	CIProviderOther         = "other"
)

// findingIDPrefix is the stable opaque prefix for synthetic finding ids
// minted when the caller omits one. Stable so downstream SIEM and audit
// consumers can recognise shadow records without parsing the full id.
const findingIDPrefix = "edge_shadow_"

// MaxEvidenceSummaryBytes bounds the JSON-persisted evidence summary
// after redaction. Larger summaries are rejected at validation time so a
// malicious uploader can't bloat Redis with multi-megabyte blobs.
const MaxEvidenceSummaryBytes = 2048

// MaxResolutionReasonBytes bounds the human-readable reason attached to
// resolve/suppress lifecycle changes. Tight cap — operators get a
// sentence, not a forensic narrative.
const MaxResolutionReasonBytes = 512

// MaxMetadataEntries / MaxMetadataValueBytes / MaxMetadataKeyBytes
// bound the small free-form metadata map. Mirrors edge.validateMetadata
// shape but enforced inline so the shadow store stays self-contained.
const (
	MaxMetadataEntries    = 16
	MaxMetadataKeyBytes   = 64
	MaxMetadataValueBytes = 256
)

// MaxListPageSize caps cursor pagination per request. Operators wanting
// more findings must paginate via cursor.
const (
	DefaultListPageSize = 50
	MaxListPageSize     = 200
)

// DefaultTerminalRetention is the TTL applied to resolved/suppressed
// records. Detected records carry no TTL — they remain until the
// operator triages them. Configurable via WithTerminalRetention so
// operator policy can extend or shorten.
const DefaultTerminalRetention = 90 * 24 * time.Hour

// EvidencePointer references redacted evidence stored outside the
// finding record. Distinct from edgecore.ArtifactPointer because shadow
// findings are observed OUTSIDE of an EdgeSession/AgentExecution
// context — the existing ArtifactPointer requires session_id +
// execution_id + event_id, which a scanner finding does not have.
// Retention/redaction enums are reused from edgecore so SIEM/export
// pipelines can treat both pointer shapes uniformly.
type EvidencePointer struct {
	// TenantID MUST match the parent finding's TenantID; the store
	// rejects cross-tenant pointers at validation time so a misconfigured
	// uploader cannot stash evidence in another tenant's blob store.
	TenantID       string                  `json:"tenant_id"`
	URI            string                  `json:"uri"`
	SHA256         string                  `json:"sha256"`
	SizeBytes      int64                   `json:"size_bytes,omitempty"`
	RetentionClass edgecore.RetentionClass `json:"retention_class"`
	RedactionLevel edgecore.RedactionLevel `json:"redaction_level"`
	CreatedAt      time.Time               `json:"created_at"`
}

// ShadowAgentFinding is the lifecycle-tracked operator-visible record.
// Persisted at edge:shadow:finding:<finding_id>. Indexed by tenant +
// status + risk + agent_product + owner so list filters are O(narrowest-
// index) rather than O(tenant-fanout).
type ShadowAgentFinding struct {
	FindingID        string            `json:"finding_id"`
	TenantID         string            `json:"tenant_id"`
	OwnerPrincipalID string            `json:"owner_principal_id"`
	PrincipalID      string            `json:"principal_id"`
	AgentProduct     string            `json:"agent_product"`
	AgentID          string            `json:"agent_id,omitempty"`
	Hostname         string            `json:"hostname,omitempty"`
	Risk             FindingRisk       `json:"risk"`
	Status           FindingStatus     `json:"status"`
	EvidenceType     string            `json:"evidence_type"`
	EvidenceSummary  string            `json:"evidence_summary,omitempty"`
	EvidenceArtifact *EvidencePointer  `json:"evidence_artifact_ptr,omitempty"`
	RedactedPath     string            `json:"redacted_path,omitempty"`
	DetectedAt       time.Time         `json:"detected_at"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ResolvedAt       *time.Time        `json:"resolved_at,omitempty"`
	ResolvedBy       string            `json:"resolved_by,omitempty"`
	ResolutionReason string            `json:"resolution_reason,omitempty"`
	SuppressedUntil  *time.Time        `json:"suppressed_until,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`

	// EDGE-143.5 — §10.1 fields. All omitempty so EDGE-141 legacy
	// records continue to round-trip unchanged; default-on-read for
	// SourceType is applied in applyReadDefaults.
	SourceType          string                      `json:"source_type,omitempty"`
	SourceID            string                      `json:"source_id,omitempty"`
	ClusterID           string                      `json:"cluster_id,omitempty"`
	Namespace           string                      `json:"namespace,omitempty"`
	WorkloadKind        string                      `json:"workload_kind,omitempty"`
	WorkloadName        string                      `json:"workload_name,omitempty"`
	PodUID              string                      `json:"pod_uid,omitempty"`
	CIProvider          string                      `json:"ci_provider,omitempty"`
	Repo                string                      `json:"repo,omitempty"`
	Ref                 string                      `json:"ref,omitempty"`
	WorkflowID          string                      `json:"workflow_id,omitempty"`
	JobID               string                      `json:"job_id,omitempty"`
	RunID               string                      `json:"run_id,omitempty"`
	RunnerID            string                      `json:"runner_id,omitempty"`
	TenantSource        string                      `json:"tenant_source,omitempty"`
	PrincipalSource     string                      `json:"principal_source,omitempty"`
	SignalSet           []string                    `json:"signal_set,omitempty"`
	Confidence          float64                     `json:"confidence,omitempty"`
	FirstSeen           *time.Time                  `json:"first_seen,omitempty"`
	LastSeen            *time.Time                  `json:"last_seen,omitempty"`
	FalsePositiveReason string                      `json:"false_positive_reason,omitempty"`
	ExceptionID         string                      `json:"exception_id,omitempty"`
	RetentionClass      ShadowFindingRetentionClass `json:"retention_class,omitempty"`
}

// CreateFindingRequest is the validated input used to mint a new
// finding. The store generates FindingID/CreatedAt/UpdatedAt/Status when
// the caller omits them; the caller MUST supply tenant, owner,
// principal, agent_product, risk, evidence_type, detected_at, and at
// least one of (evidence_summary, evidence_artifact_ptr).
type CreateFindingRequest struct {
	FindingID        string
	TenantID         string
	OwnerPrincipalID string
	PrincipalID      string
	AgentProduct     string
	AgentID          string
	Hostname         string
	Risk             FindingRisk
	EvidenceType     string
	EvidenceSummary  string
	EvidenceArtifact *EvidencePointer
	RedactedPath     string
	DetectedAt       time.Time
	Metadata         map[string]string

	// EDGE-143.5 — §10.1 fields. See ShadowAgentFinding for semantics.
	SourceType          string
	SourceID            string
	ClusterID           string
	Namespace           string
	WorkloadKind        string
	WorkloadName        string
	PodUID              string
	CIProvider          string
	Repo                string
	Ref                 string
	WorkflowID          string
	JobID               string
	RunID               string
	RunnerID            string
	TenantSource        string
	PrincipalSource     string
	SignalSet           []string
	Confidence          float64
	FirstSeen           *time.Time
	LastSeen            *time.Time
	FalsePositiveReason string
	ExceptionID         string
	RetentionClass      ShadowFindingRetentionClass
}

// ListFindingsQuery is the bounded filter set accepted by ListFindings.
// Empty fields mean "no filter on this dimension". TenantID is REQUIRED
// — the store refuses cross-tenant scans.
type ListFindingsQuery struct {
	TenantID         string
	Status           FindingStatus
	Risk             FindingRisk
	AgentProduct     string
	OwnerPrincipalID string
	Limit            int
	Cursor           string

	// EDGE-143.5 — §10.2 query filters. Empty = no filter on that
	// dimension. Filters combine with AND semantics across dimensions
	// and IN semantics within a slice dimension (Signals).
	SourceType         string
	ClusterID          string
	Namespace          string
	CIProvider         string
	Repo               string
	Signals            []string
	ConfidenceMin      float64
	FirstSeenAfter     *time.Time
	LastSeenBefore     *time.Time
	ExceptionID        string
	IncludeManagedSkip bool
}

// FindingPage is the cursor-paginated list response. NextCursor is
// empty when the caller has consumed the index.
type FindingPage struct {
	Findings   []ShadowAgentFinding `json:"findings"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// ResolveRequest captures the lifecycle-change inputs the resolve API
// accepts. Only Status (implicit resolved), ResolvedBy, and
// ResolutionReason mutate; everything else is preserved verbatim.
type ResolveRequest struct {
	ResolvedBy string
	Reason     string
}

// SuppressRequest is the suppress-side analog of ResolveRequest. The
// optional SuppressedUntil hint records time-bound exceptions; the
// store does not auto-revert when the timestamp lapses (operators may
// re-trigger the scanner instead).
type SuppressRequest struct {
	SuppressedBy    string
	Reason          string
	SuppressedUntil *time.Time
}

// Store is the persistence contract for ShadowAgentFinding lifecycle
// records. All methods MUST tenant-scope reads/writes; cross-tenant
// access returns ErrNotFound rather than a typed cross-tenant error so
// callers cannot use the API surface to probe other tenants' findings.
type Store interface {
	// CreateFinding mints a new finding from a validated request. Idempotent
	// when the caller supplies an explicit FindingID and the existing
	// record is byte-equal; returns ErrAlreadyExists otherwise.
	CreateFinding(ctx context.Context, req CreateFindingRequest) (*ShadowAgentFinding, error)
	// GetFinding loads a finding by id within the given tenant; returns
	// ErrNotFound when the id is missing OR resolves to a different
	// tenant's record (cross-tenant probe defense).
	GetFinding(ctx context.Context, tenantID, findingID string) (*ShadowAgentFinding, error)
	// ListFindings returns a bounded page of findings matching the query.
	// Stale index entries (records expired by terminal-retention TTL) are
	// hidden from the response and opportunistically removed from the
	// secondary indexes.
	ListFindings(ctx context.Context, q ListFindingsQuery) (FindingPage, error)
	// ResolveFinding transitions a detected finding to resolved. Idempotent
	// when the finding is already in the resolved terminal state with the
	// same fields. Returns ErrTerminalConflict when transitioning from
	// suppressed → resolved (terminal states do not re-open in this
	// lifecycle).
	ResolveFinding(ctx context.Context, tenantID, findingID string, req ResolveRequest) (*ShadowAgentFinding, error)
	// SuppressFinding transitions a detected finding to suppressed. Same
	// idempotence/conflict rules as ResolveFinding apply.
	SuppressFinding(ctx context.Context, tenantID, findingID string, req SuppressRequest) (*ShadowAgentFinding, error)

	// EDGE-143.6 — exception API (§10.3).

	// CreateException persists a new operator-defined exception. Returns
	// ErrExceptionLimitExceeded when the tenant index is at cap.
	CreateException(ctx context.Context, req CreateExceptionRequest) (*Exception, error)
	// GetException loads an exception by id within the given tenant;
	// returns ErrNotFound on cross-tenant probe (parity with GetFinding).
	GetException(ctx context.Context, tenantID, exceptionID string) (*Exception, error)
	// ListExceptions returns a bounded page of exceptions matching the
	// query. Tenant-scoped; scope filters (source_type, risk) apply
	// in-memory because the index dimensionality is bounded.
	ListExceptions(ctx context.Context, q ListExceptionsQuery) (ExceptionPage, error)
	// RevokeException transitions an active exception to revoked.
	// Idempotent when already revoked with the same RevokedBy; returns
	// ErrTerminalConflict on conflicting double-revoke.
	RevokeException(ctx context.Context, tenantID, exceptionID string, req RevokeExceptionRequest) (*Exception, error)
	// MatchActiveExceptions returns the bounded set of active,
	// unexpired exceptions matching the given finding's scope. Used by
	// CreateFinding at emit time to stamp exception_id on suppressed
	// findings.
	MatchActiveExceptions(ctx context.Context, f *ShadowAgentFinding) ([]Exception, error)
}

// Sentinel errors. Handlers map these to specific HTTP status codes.
var (
	ErrNotFound         = errors.New("shadow finding: not found")
	ErrAlreadyExists    = errors.New("shadow finding: already exists")
	ErrTerminalConflict = errors.New("shadow finding: terminal status conflict")
	ErrInvalidCursor    = errors.New("shadow finding: invalid cursor")
	// ErrValidation is the umbrella for input-validation failures. Use
	// errors.Is to detect; the wrapped message carries the offending field.
	ErrValidation = errors.New("shadow finding: validation")
	// ErrStoreUnavailable is returned when the underlying Redis client
	// is nil or unreachable. Distinct from internal-error so handlers can
	// map to 503 rather than 500.
	ErrStoreUnavailable = errors.New("shadow finding: store unavailable")
)

// validFindingRisk gates enum inputs. (Status gating moved to inline
// switch in normalizeAndValidateCreate; the map form was dead code.)
var (
	validFindingRisk = map[FindingRisk]struct{}{
		FindingRiskLow:      {},
		FindingRiskMedium:   {},
		FindingRiskHigh:     {},
		FindingRiskCritical: {},
	}
	// validEvidenceType is intentionally loose — the scanner's
	// EvidenceConfigFile/EvidenceProcessName/EvidenceEnvironmentVar are
	// the well-known producers, but operators ingesting findings from
	// SIEM webhooks may supply their own short identifiers. Anything
	// non-empty and within byte cap passes; unknown values surface in
	// audit/dashboard as-is.
	validEvidenceType = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	// validShadowSourceType — §10.1 enum gate; empty defaults to "local".
	validShadowSourceType = map[string]struct{}{
		SourceTypeLocal:      {},
		SourceTypeKubernetes: {},
		SourceTypeCI:         {},
		SourceTypeNetwork:    {},
	}
	// validShadowCIProvider — §10.1 enum gate.
	validShadowCIProvider = map[string]struct{}{
		CIProviderGitHubActions: {},
		CIProviderGitLabCI:      {},
		CIProviderJenkins:       {},
		CIProviderBuildkite:     {},
		CIProviderCircleCI:      {},
		CIProviderOther:         {},
	}
	// validShadowRetentionClass — §10.5 enum gate; empty allowed (legacy).
	validShadowRetentionClass = map[ShadowFindingRetentionClass]struct{}{
		ShadowRetentionShort:   {},
		ShadowRetentionDefault: {},
		ShadowRetentionLong:    {},
	}
	// validShadowSignal mirrors validEvidenceType — bounded enum-shape
	// identifier per §10.1 signal_set.
	validShadowSignal = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
)

// §10.1 byte caps for the new string fields.
const (
	maxShadowSourceIDBytes     = 128
	maxShadowClusterIDBytes    = 64
	maxShadowNamespaceBytes    = 63
	maxShadowWorkloadKindBytes = 32
	maxShadowWorkloadNameBytes = 253
	maxShadowPodUIDBytes       = 36
	maxShadowRepoBytes         = 256
	maxShadowRefBytes          = 256
	maxShadowWorkflowIDBytes   = 128
	maxShadowJobIDBytes        = 128
	maxShadowRunIDBytes        = 128
	maxShadowRunnerIDBytes     = 128
	maxShadowTenantSourceBytes = 64
	maxShadowPrincipalSrcBytes = 64
	maxShadowFPReasonBytes     = 64
	maxShadowExceptionIDBytes  = 64
	maxShadowSignalSetEntries  = 16
)

// normalizeAndValidateCreate normalizes a CreateFindingRequest into a
// persistable ShadowAgentFinding and rejects invalid inputs. now is
// injected so tests can pin time without monkey-patching.
func normalizeAndValidateCreate(req CreateFindingRequest, now time.Time, idGen func() string) (*ShadowAgentFinding, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("%w: tenant_id is required", ErrValidation)
	}
	owner := strings.TrimSpace(req.OwnerPrincipalID)
	if owner == "" {
		return nil, fmt.Errorf("%w: owner_principal_id is required", ErrValidation)
	}
	principal := strings.TrimSpace(req.PrincipalID)
	if principal == "" {
		return nil, fmt.Errorf("%w: principal_id is required", ErrValidation)
	}
	product := strings.ToLower(strings.TrimSpace(req.AgentProduct))
	if product == "" {
		return nil, fmt.Errorf("%w: agent_product is required", ErrValidation)
	}

	risk := FindingRisk(strings.ToLower(strings.TrimSpace(string(req.Risk))))
	if _, ok := validFindingRisk[risk]; !ok {
		return nil, fmt.Errorf("%w: risk must be one of low|medium|high|critical, got %q", ErrValidation, req.Risk)
	}

	evType := strings.ToLower(strings.TrimSpace(req.EvidenceType))
	if !validEvidenceType.MatchString(evType) {
		return nil, fmt.Errorf("%w: evidence_type must match [a-z0-9_]{1,32}, got %q", ErrValidation, req.EvidenceType)
	}

	summary := strings.TrimSpace(req.EvidenceSummary)
	if summary != "" {
		summary = stripSecretMarkers(summary)
		if len(summary) > MaxEvidenceSummaryBytes {
			return nil, fmt.Errorf("%w: evidence_summary exceeds %d bytes", ErrValidation, MaxEvidenceSummaryBytes)
		}
	}
	if summary == "" && req.EvidenceArtifact == nil {
		return nil, fmt.Errorf("%w: at least one of evidence_summary or evidence_artifact_ptr is required", ErrValidation)
	}

	var pointer *EvidencePointer
	if req.EvidenceArtifact != nil {
		ptr := *req.EvidenceArtifact
		if err := validateEvidencePointer(ptr, tenantID, now); err != nil {
			return nil, err
		}
		pointer = &ptr
	}

	if req.DetectedAt.IsZero() {
		return nil, fmt.Errorf("%w: detected_at is required", ErrValidation)
	}

	metadata := sanitizeFindingMetadata(req.Metadata)
	if err := validateFindingMetadata(metadata); err != nil {
		return nil, err
	}

	ext, err := validateShadowExtensions(req)
	if err != nil {
		return nil, err
	}

	id := strings.TrimSpace(req.FindingID)
	if id == "" {
		id = idGen()
	}
	if !strings.HasPrefix(id, findingIDPrefix) {
		id = findingIDPrefix + id
	}

	finding := &ShadowAgentFinding{
		FindingID:        id,
		TenantID:         tenantID,
		OwnerPrincipalID: owner,
		PrincipalID:      principal,
		AgentProduct:     product,
		AgentID:          strings.TrimSpace(req.AgentID),
		Hostname:         strings.TrimSpace(req.Hostname),
		Risk:             risk,
		Status:           FindingStatusDetected,
		EvidenceType:     evType,
		EvidenceSummary:  summary,
		EvidenceArtifact: pointer,
		RedactedPath:     RedactPath(req.RedactedPath),
		DetectedAt:       req.DetectedAt.UTC(),
		CreatedAt:        now,
		UpdatedAt:        now,
		Metadata:         copyMetadata(metadata),

		SourceType:          ext.sourceType,
		SourceID:            ext.sourceID,
		ClusterID:           ext.clusterID,
		Namespace:           ext.namespace,
		WorkloadKind:        ext.workloadKind,
		WorkloadName:        ext.workloadName,
		PodUID:              ext.podUID,
		CIProvider:          ext.ciProvider,
		Repo:                ext.repo,
		Ref:                 ext.ref,
		WorkflowID:          ext.workflowID,
		JobID:               ext.jobID,
		RunID:               ext.runID,
		RunnerID:            ext.runnerID,
		TenantSource:        ext.tenantSource,
		PrincipalSource:     ext.principalSource,
		SignalSet:           ext.signalSet,
		Confidence:          ext.confidence,
		FirstSeen:           ext.firstSeen,
		LastSeen:            ext.lastSeen,
		FalsePositiveReason: ext.falsePositiveReason,
		ExceptionID:         ext.exceptionID,
		RetentionClass:      ext.retentionClass,
	}
	return finding, nil
}

// shadowExtensionFields is the normalized + validated form of the
// §10.1 extension fields. Kept separate from CreateFindingRequest so
// the calling normalizeAndValidateCreate stays linear and the
// validation logic is independently testable.
type shadowExtensionFields struct {
	sourceType          string
	sourceID            string
	clusterID           string
	namespace           string
	workloadKind        string
	workloadName        string
	podUID              string
	ciProvider          string
	repo                string
	ref                 string
	workflowID          string
	jobID               string
	runID               string
	runnerID            string
	tenantSource        string
	principalSource     string
	signalSet           []string
	confidence          float64
	firstSeen           *time.Time
	lastSeen            *time.Time
	falsePositiveReason string
	exceptionID         string
	retentionClass      ShadowFindingRetentionClass
}

func validateShadowExtensions(req CreateFindingRequest) (shadowExtensionFields, error) {
	var ext shadowExtensionFields

	ext.sourceType = strings.ToLower(strings.TrimSpace(req.SourceType))
	if ext.sourceType == "" {
		ext.sourceType = SourceTypeLocal
	}
	if _, ok := validShadowSourceType[ext.sourceType]; !ok {
		return ext, fmt.Errorf("%w: source_type must be one of local|kubernetes|ci|network, got %q", ErrValidation, req.SourceType)
	}

	caps := []struct {
		name  string
		value string
		max   int
		dst   *string
	}{
		{"source_id", req.SourceID, maxShadowSourceIDBytes, &ext.sourceID},
		{"cluster_id", req.ClusterID, maxShadowClusterIDBytes, &ext.clusterID},
		{"namespace", req.Namespace, maxShadowNamespaceBytes, &ext.namespace},
		{"workload_kind", req.WorkloadKind, maxShadowWorkloadKindBytes, &ext.workloadKind},
		{"workload_name", req.WorkloadName, maxShadowWorkloadNameBytes, &ext.workloadName},
		{"pod_uid", req.PodUID, maxShadowPodUIDBytes, &ext.podUID},
		{"repo", req.Repo, maxShadowRepoBytes, &ext.repo},
		{"ref", req.Ref, maxShadowRefBytes, &ext.ref},
		{"workflow_id", req.WorkflowID, maxShadowWorkflowIDBytes, &ext.workflowID},
		{"job_id", req.JobID, maxShadowJobIDBytes, &ext.jobID},
		{"run_id", req.RunID, maxShadowRunIDBytes, &ext.runID},
		{"runner_id", req.RunnerID, maxShadowRunnerIDBytes, &ext.runnerID},
		{"tenant_source", req.TenantSource, maxShadowTenantSourceBytes, &ext.tenantSource},
		{"principal_source", req.PrincipalSource, maxShadowPrincipalSrcBytes, &ext.principalSource},
		{"false_positive_reason", req.FalsePositiveReason, maxShadowFPReasonBytes, &ext.falsePositiveReason},
		{"exception_id", req.ExceptionID, maxShadowExceptionIDBytes, &ext.exceptionID},
	}
	for _, c := range caps {
		v := strings.TrimSpace(c.value)
		if len(v) > c.max {
			return ext, fmt.Errorf("%w: %s exceeds %d bytes", ErrValidation, c.name, c.max)
		}
		*c.dst = v
	}

	if ext.ciProvider = strings.ToLower(strings.TrimSpace(req.CIProvider)); ext.ciProvider != "" {
		if _, ok := validShadowCIProvider[ext.ciProvider]; !ok {
			return ext, fmt.Errorf("%w: ci_provider must be one of github_actions|gitlab_ci|jenkins|buildkite|circleci|other, got %q", ErrValidation, req.CIProvider)
		}
	}
	// ci_provider and repo are mutual: both empty or both populated.
	if (ext.ciProvider == "") != (ext.repo == "") {
		return ext, fmt.Errorf("%w: ci_provider and repo must be set together (both empty or both populated)", ErrValidation)
	}

	if len(req.SignalSet) > maxShadowSignalSetEntries {
		return ext, fmt.Errorf("%w: signal_set exceeds %d entries", ErrValidation, maxShadowSignalSetEntries)
	}
	if len(req.SignalSet) > 0 {
		seen := make(map[string]struct{}, len(req.SignalSet))
		signals := make([]string, 0, len(req.SignalSet))
		for _, raw := range req.SignalSet {
			sig := strings.ToLower(strings.TrimSpace(raw))
			if !validShadowSignal.MatchString(sig) {
				return ext, fmt.Errorf("%w: signal_set entry %q must match [a-z0-9_]{1,32}", ErrValidation, raw)
			}
			if _, dup := seen[sig]; dup {
				continue
			}
			seen[sig] = struct{}{}
			signals = append(signals, sig)
		}
		ext.signalSet = signals
	}

	if req.Confidence < 0 || req.Confidence > 1 {
		return ext, fmt.Errorf("%w: confidence must be in [0, 1], got %v", ErrValidation, req.Confidence)
	}
	ext.confidence = req.Confidence

	if req.FirstSeen != nil && !req.FirstSeen.IsZero() {
		t := req.FirstSeen.UTC()
		ext.firstSeen = &t
	}
	if req.LastSeen != nil && !req.LastSeen.IsZero() {
		t := req.LastSeen.UTC()
		ext.lastSeen = &t
	}
	if ext.firstSeen != nil && ext.lastSeen != nil && ext.firstSeen.After(*ext.lastSeen) {
		return ext, fmt.Errorf("%w: first_seen must be <= last_seen", ErrValidation)
	}

	if rc := ShadowFindingRetentionClass(strings.ToLower(strings.TrimSpace(string(req.RetentionClass)))); rc != "" {
		if _, ok := validShadowRetentionClass[rc]; !ok {
			return ext, fmt.Errorf("%w: retention_class must be one of shadow_short|shadow_default|shadow_long, got %q", ErrValidation, req.RetentionClass)
		}
		ext.retentionClass = rc
	}

	return ext, nil
}

// applyReadDefaults sets the §10.4 "defaults on read" — currently only
// source_type defaults to "local" for legacy EDGE-141 records. Called
// from every read path (GetFinding, ListFindings, transitionFinding).
func applyReadDefaults(f *ShadowAgentFinding) {
	if f == nil {
		return
	}
	if f.SourceType == "" {
		f.SourceType = SourceTypeLocal
	}
}

func validateEvidencePointer(ptr EvidencePointer, parentTenant string, now time.Time) error {
	if strings.TrimSpace(ptr.TenantID) == "" {
		return fmt.Errorf("%w: evidence_artifact_ptr.tenant_id is required", ErrValidation)
	}
	if ptr.TenantID != parentTenant {
		return fmt.Errorf("%w: evidence_artifact_ptr.tenant_id must match finding tenant_id", ErrValidation)
	}
	if strings.TrimSpace(ptr.URI) == "" {
		return fmt.Errorf("%w: evidence_artifact_ptr.uri is required", ErrValidation)
	}
	if strings.TrimSpace(ptr.SHA256) == "" {
		return fmt.Errorf("%w: evidence_artifact_ptr.sha256 is required", ErrValidation)
	}
	switch ptr.RetentionClass {
	case edgecore.RetentionClassShort, edgecore.RetentionClassStandard, edgecore.RetentionClassAudit:
	default:
		return fmt.Errorf("%w: evidence_artifact_ptr.retention_class invalid: %q", ErrValidation, ptr.RetentionClass)
	}
	switch ptr.RedactionLevel {
	case edgecore.RedactionLevelStandard, edgecore.RedactionLevelStrict:
	default:
		return fmt.Errorf("%w: evidence_artifact_ptr.redaction_level invalid: %q", ErrValidation, ptr.RedactionLevel)
	}
	if ptr.CreatedAt.IsZero() {
		return fmt.Errorf("%w: evidence_artifact_ptr.created_at is required", ErrValidation)
	}
	if ptr.SizeBytes < 0 {
		return fmt.Errorf("%w: evidence_artifact_ptr.size_bytes must be >= 0", ErrValidation)
	}
	if !now.IsZero() && ptr.CreatedAt.After(now.Add(time.Hour)) {
		return fmt.Errorf("%w: evidence_artifact_ptr.created_at is in the future", ErrValidation)
	}
	return nil
}

func validateFindingMetadata(m map[string]string) error {
	if len(m) == 0 {
		return nil
	}
	if len(m) > MaxMetadataEntries {
		return fmt.Errorf("%w: metadata exceeds %d entries", ErrValidation, MaxMetadataEntries)
	}
	for k, v := range m {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%w: metadata has empty key", ErrValidation)
		}
		if len(k) > MaxMetadataKeyBytes {
			return fmt.Errorf("%w: metadata key %q exceeds %d bytes", ErrValidation, k, MaxMetadataKeyBytes)
		}
		if len(v) > MaxMetadataValueBytes {
			return fmt.Errorf("%w: metadata value for %q exceeds %d bytes", ErrValidation, k, MaxMetadataValueBytes)
		}
	}
	return nil
}

func copyMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

// ResolveOrSuppress applies a lifecycle change to an existing finding
// in-place. Pure function — the store wraps it inside a Redis Tx to
// preserve atomicity. exposed for tests that exercise the transition
// logic without going through Redis.
func applyResolve(f *ShadowAgentFinding, req ResolveRequest, now time.Time) error {
	switch f.Status {
	case FindingStatusResolved:
		return nil // idempotent
	case FindingStatusSuppressed:
		return fmt.Errorf("%w: cannot resolve suppressed finding %s", ErrTerminalConflict, f.FindingID)
	case FindingStatusDetected:
		reason := strings.TrimSpace(req.Reason)
		if len(reason) > MaxResolutionReasonBytes {
			return fmt.Errorf("%w: reason exceeds %d bytes", ErrValidation, MaxResolutionReasonBytes)
		}
		f.Status = FindingStatusResolved
		f.ResolvedBy = strings.TrimSpace(req.ResolvedBy)
		f.ResolutionReason = stripSecretMarkers(reason)
		f.ResolvedAt = timePtr(now)
		f.UpdatedAt = now
		return nil
	default:
		return fmt.Errorf("%w: unknown status %q", ErrValidation, f.Status)
	}
}

func applySuppress(f *ShadowAgentFinding, req SuppressRequest, now time.Time) error {
	switch f.Status {
	case FindingStatusSuppressed:
		return nil // idempotent
	case FindingStatusResolved:
		return fmt.Errorf("%w: cannot suppress resolved finding %s", ErrTerminalConflict, f.FindingID)
	case FindingStatusDetected:
		reason := strings.TrimSpace(req.Reason)
		if len(reason) > MaxResolutionReasonBytes {
			return fmt.Errorf("%w: reason exceeds %d bytes", ErrValidation, MaxResolutionReasonBytes)
		}
		f.Status = FindingStatusSuppressed
		f.ResolvedBy = strings.TrimSpace(req.SuppressedBy)
		f.ResolutionReason = stripSecretMarkers(reason)
		f.ResolvedAt = timePtr(now)
		if req.SuppressedUntil != nil && !req.SuppressedUntil.IsZero() {
			until := req.SuppressedUntil.UTC()
			f.SuppressedUntil = &until
		}
		f.UpdatedAt = now
		return nil
	default:
		return fmt.Errorf("%w: unknown status %q", ErrValidation, f.Status)
	}
}

func timePtr(t time.Time) *time.Time {
	tt := t.UTC()
	return &tt
}
