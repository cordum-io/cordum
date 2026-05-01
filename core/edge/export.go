package edge

import "time"

// ExportManifestVersion identifies the wire shape of SessionExportBundle.
// Bumped when a backwards-incompatible field is added/removed/renamed; minor
// additive changes (new optional fields, new MissingArtifactReason values)
// stay on the same version. Auditors and re-import tooling pin against this
// string.
const ExportManifestVersion = "edge.export.v1"

// MissingArtifactReason enumerates why an artifact pointer present on an
// event did not produce a manifest entry. Surfacing the reason lets auditors
// distinguish "TTL expired" (operationally normal) from "tenant mismatch"
// (potential cross-tenant injection caught at export time).
type MissingArtifactReason string

const (
	MissingArtifactReasonNotFound       MissingArtifactReason = "not_found"
	MissingArtifactReasonTenantMismatch MissingArtifactReason = "tenant_mismatch"
	MissingArtifactReasonStoreError     MissingArtifactReason = "store_error"
)

// ExportArtifactEntry is the metadata-only manifest entry for one artifact
// pointer captured during export. Mirrors ArtifactPointer plus the bytes
// the artifact store reports for the body. Crucially the body itself is
// never embedded — that would defeat the entire "no large raw payloads in
// Redis events" rail and silently turn the export into an exfiltration
// vector for the same secrets the events redacted.
type ExportArtifactEntry struct {
	SessionID      string         `json:"session_id"`
	ExecutionID    string         `json:"execution_id"`
	EventID        string         `json:"event_id"`
	ArtifactType   ArtifactType   `json:"artifact_type"`
	RetentionClass RetentionClass `json:"retention_class"`
	RedactionLevel RedactionLevel `json:"redaction_level"`
	SHA256         string         `json:"sha256"`
	URI            string         `json:"uri"`
	SizeBytes      int64          `json:"size_bytes"`
	ContentType    string         `json:"content_type,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// ExportMissingArtifact records an artifact pointer that the bundler could
// not resolve — TTL-expired, never-written, cross-tenant probe, etc. The
// auditor sees the URI/sha256/artifact_type so they can investigate, but the
// (already-absent) body never leaks.
type ExportMissingArtifact struct {
	URI          string                `json:"uri"`
	SHA256       string                `json:"sha256"`
	ArtifactType ArtifactType          `json:"artifact_type"`
	SessionID    string                `json:"session_id"`
	ExecutionID  string                `json:"execution_id"`
	EventID      string                `json:"event_id"`
	Reason       MissingArtifactReason `json:"reason"`
}

// ExportTruncation describes how the bundler had to clip its inputs to fit
// safety bounds. Auditors must be able to tell "this export contains every
// event for the session" from "this is the most recent N events because the
// session has too many" — the Truncation struct is how we surface that.
type ExportTruncation struct {
	EventsTruncated     bool   `json:"events_truncated"`
	EventCount          int    `json:"event_count"`
	EventScanLimitHit   bool   `json:"event_scan_limit_hit"`
	ExecutionsTruncated bool   `json:"executions_truncated,omitempty"`
	SizeLimitHit        bool   `json:"size_limit_hit,omitempty"`
	SizeLimitBytes      int64  `json:"size_limit_bytes,omitempty"`
	BundleSizeBytes     int64  `json:"bundle_size_bytes,omitempty"`
	Reason              string `json:"reason,omitempty"`
}

// ExportJobLink is the only place SessionExportBundle references the
// scheduler Job / Workflow Run subsystem — and only as IDs, never as
// embedded job state. The Edge subsystem is intentionally NOT a parallel
// job lifecycle, per epic rail; a job link is metadata, not a join.
type ExportJobLink struct {
	ExecutionID   string `json:"execution_id"`
	JobID         string `json:"job_id,omitempty"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	StepID        string `json:"step_id,omitempty"`
}

// SessionExportBundle is the audit/evidence payload assembled for a single
// EdgeSession. It is metadata + manifest only — every artifact body stays
// in the artifact store, referenced by URI + sha256. Read by:
//
//   - external auditors / compliance tooling consuming POST
//     /api/v1/edge/sessions/{id}/export
//   - the dashboard's session detail page (when offered as a download)
//   - re-import tooling pinned on ManifestVersion
//
// The bundle is intentionally designed to be safe to share even when
// redaction is set to standard (not strict) — secrets must never reach
// this struct. The bundler is responsible for upholding that invariant;
// callers should treat receipt of a SessionExportBundle as the redacted
// truth, not as raw evidence to redact further.
type SessionExportBundle struct {
	ManifestVersion  string                  `json:"manifest_version"`
	GeneratedAt      time.Time               `json:"generated_at"`
	TenantID         string                  `json:"tenant_id"`
	RedactionLevel   RedactionLevel          `json:"redaction_level"`
	Session          EdgeSession             `json:"session"`
	Executions       []AgentExecution        `json:"executions"`
	Events           []AgentActionEvent      `json:"events"`
	Approvals        []EdgeApproval          `json:"approvals"`
	Artifacts        []ExportArtifactEntry   `json:"artifacts"`
	MissingArtifacts []ExportMissingArtifact `json:"missing_artifacts"`
	JobLinks         []ExportJobLink         `json:"job_links,omitempty"`
	Truncation       ExportTruncation       `json:"truncation"`
}

// ExportOptions tunes assembly. Callers pass it through from the Gateway
// route. Defaults are conservative (no artifact bodies; standard redaction
// posture); the route enforces the bound on MaxBundleSizeBytes downward
// from a server-side cap so a caller cannot request an unbounded bundle.
type ExportOptions struct {
	// MaxBundleSizeBytes is a soft cap the assembler tracks against the
	// running serialized size; if exceeded, additional events are dropped
	// and Truncation.SizeLimitHit is set. The Gateway clamps this to
	// CORDUM_EDGE_EXPORT_MAX_BYTES.
	MaxBundleSizeBytes int64

	// MaxEvents caps the number of AgentActionEvents the bundle carries.
	// When the session has more, Truncation.EventsTruncated is true and
	// Truncation.EventCount records the actual session-wide event total.
	MaxEvents int

	// IncludeArtifactBodies must default false. P0 never emits raw bodies
	// — explicit opt-in is reserved for a future enterprise/strict mode
	// task with stricter authentication. Today the Gateway hardcodes false.
	IncludeArtifactBodies bool
}
