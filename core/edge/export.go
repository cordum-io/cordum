package edge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cordum/cordum/core/infra/artifacts"
)

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
	Truncation       ExportTruncation        `json:"truncation"`
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

// ExportSessionQuery names the session whose evidence the bundler should
// assemble. TenantID is the auth-resolved tenant from the Gateway — the
// bundler enforces tenant_id == session.TenantID before any read so a
// request with the wrong tenant short-circuits with ErrNotFound.
type ExportSessionQuery struct {
	TenantID  string
	SessionID string
}

// ArtifactStater is the metadata-only artifact-store contract the bundler
// needs. *artifacts.RedisStore satisfies it implicitly. Defining it here
// (instead of taking artifacts.Store directly) keeps tests free to inject
// fakes that don't require the full Redis store, and locks the bundler to
// metadata-only access — there is no Get on this interface, so a future
// bug cannot accidentally start exporting raw artifact bodies.
type ArtifactStater interface {
	Stat(ctx context.Context, ptr string) (artifacts.Metadata, error)
}

// SessionExportAssembler builds a SessionExportBundle from the final Edge
// store and artifact metadata store. Concurrency: an Assembler is safe for
// concurrent reads; it owns no internal mutable state. Time injection via
// Now is for testability — tests pin a deterministic GeneratedAt.
type SessionExportAssembler struct {
	Store         Store
	ArtifactStore ArtifactStater
	Now           func() time.Time
}

// defaultExportEventsCap is the assembler's safety ceiling on events when
// the caller did not specify ExportOptions.MaxEvents. Hit this and the
// bundle records EventsTruncated=true; the actual session-wide count is
// reported via EventCount so auditors know the bundle is partial. Sized to
// match maxStorePageLimit×N in the underlying store; the Gateway can tune
// down via ExportOptions.MaxEvents.
const defaultExportEventsCap = 5000

// safetyExecutionsCap bounds executions per session at assembly time.
// Sessions with more executions than this are extraordinary; the safety
// cap prevents an unbounded loop if the store somehow returns infinite
// pages. Truncation.ExecutionsTruncated surfaces the hit.
const safetyExecutionsCap = 5000

// Assemble runs the full Edge evidence-export pipeline:
//
//  1. Resolve and tenant-check the session (returns ErrNotFound on miss
//     or cross-tenant access — same wire shape so neither path leaks
//     existence to a mis-tenanted caller).
//  2. Page executions for the session via the bounded ListExecutions
//     cursor (PR #243 storage contract). Honors safetyExecutionsCap.
//  3. Page events via ListEvents, capped at opts.MaxEvents (or
//     defaultExportEventsCap if unset). Truncation.EventsTruncated and
//     Truncation.EventCount surface partial bundles to auditors.
//  4. Sort events deterministically by (Seq, Timestamp, EventID) so two
//     exports of the same data produce byte-identical bundles.
//  5. Page approvals via ListApprovals.
//  6. Walk artifact_ptrs from every event, dedupe by URI, and Stat each
//     against the artifact store. Cross-tenant pointers (a tenant-B URI
//     on a tenant-A event — should never happen but defended anyway)
//     and missing-artifact (TTL expiry / never-stored) pointers land in
//     MissingArtifacts with the Reason set, never as silent omissions.
//  7. Project executions' job_id/workflow_run_id/step_id into JobLinks
//     so auditors can pivot from the bundle to the scheduler's record
//     of the actual production work.
//  8. Compute the bundle-wide RedactionLevel as the max strictness
//     across present artifacts (strict wins over standard).
//
// Note that the Gateway route layered above this is responsible for
// enforcing CORDUM_EDGE_EXPORT_MAX_BYTES as a final cap on the serialized
// bundle (step-12 of EDGE-013); the assembler tracks but does not enforce
// MaxBundleSizeBytes here.
func (a *SessionExportAssembler) Assemble(ctx context.Context, q ExportSessionQuery, opts ExportOptions) (SessionExportBundle, error) {
	if a == nil || a.Store == nil {
		return SessionExportBundle{}, fmt.Errorf("export assembler not configured: store is required")
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	tenantID := q.TenantID
	sessionID := q.SessionID
	if tenantID == "" || sessionID == "" {
		return SessionExportBundle{}, fmt.Errorf("tenant_id and session_id are required")
	}

	session, ok, err := a.Store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return SessionExportBundle{}, fmt.Errorf("get session: %w", err)
	}
	if !ok || session == nil {
		return SessionExportBundle{}, ErrNotFound
	}

	executions, executionsTruncated, err := a.collectExecutions(ctx, tenantID, sessionID)
	if err != nil {
		return SessionExportBundle{}, err
	}

	maxEvents := opts.MaxEvents
	if maxEvents <= 0 {
		maxEvents = defaultExportEventsCap
	}
	events, totalEvents, eventsTruncated, err := a.collectEvents(ctx, tenantID, sessionID, maxEvents)
	if err != nil {
		return SessionExportBundle{}, err
	}

	approvals, err := a.collectApprovals(ctx, tenantID, sessionID)
	if err != nil {
		return SessionExportBundle{}, err
	}

	artifactsList, missing := a.collectArtifacts(ctx, tenantID, events)

	jobLinks := buildJobLinks(executions)
	bundleRedaction := overallRedactionLevel(artifactsList)

	bundle := SessionExportBundle{
		ManifestVersion:  ExportManifestVersion,
		GeneratedAt:      now().UTC(),
		TenantID:         tenantID,
		RedactionLevel:   bundleRedaction,
		Session:          *session,
		Executions:       executions,
		Events:           events,
		Approvals:        approvals,
		Artifacts:        artifactsList,
		MissingArtifacts: missing,
		JobLinks:         jobLinks,
		Truncation: ExportTruncation{
			EventsTruncated:     eventsTruncated,
			EventCount:          totalEvents,
			EventScanLimitHit:   eventsTruncated && totalEvents == maxEvents,
			ExecutionsTruncated: executionsTruncated,
		},
	}
	return bundle, nil
}

func (a *SessionExportAssembler) collectExecutions(ctx context.Context, tenantID, sessionID string) ([]AgentExecution, bool, error) {
	var executions []AgentExecution
	cursor := ""
	for {
		page, err := a.Store.ListExecutions(ctx, ListExecutionsQuery{
			TenantID:  tenantID,
			SessionID: sessionID,
			Cursor:    cursor,
			Limit:     maxStorePageLimit,
		})
		if err != nil {
			return nil, false, fmt.Errorf("list executions: %w", err)
		}
		executions = append(executions, page.Items...)
		if len(executions) >= safetyExecutionsCap {
			return executions[:safetyExecutionsCap], true, nil
		}
		if page.NextCursor == "" {
			return executions, false, nil
		}
		cursor = page.NextCursor
	}
}

func (a *SessionExportAssembler) collectEvents(ctx context.Context, tenantID, sessionID string, maxEvents int) ([]AgentActionEvent, int, bool, error) {
	var events []AgentActionEvent
	cursor := ""
	truncated := false
	totalSeen := 0
	for {
		page, err := a.Store.ListEvents(ctx, ListEventsQuery{
			TenantID:  tenantID,
			SessionID: sessionID,
			Cursor:    cursor,
			Limit:     maxStorePageLimit,
		})
		if err != nil {
			return nil, 0, false, fmt.Errorf("list events: %w", err)
		}
		for _, e := range page.Items {
			totalSeen++
			if len(events) >= maxEvents {
				truncated = true
				continue
			}
			events = append(events, e)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Seq != events[j].Seq {
			return events[i].Seq < events[j].Seq
		}
		if !events[i].Timestamp.Equal(events[j].Timestamp) {
			return events[i].Timestamp.Before(events[j].Timestamp)
		}
		return events[i].EventID < events[j].EventID
	})
	return events, totalSeen, truncated, nil
}

func (a *SessionExportAssembler) collectApprovals(ctx context.Context, tenantID, sessionID string) ([]EdgeApproval, error) {
	// EDGE-062 narrowed the tenant-wide approval ZSET index to active
	// approvals (Pending), so the audit-export must query each status
	// index separately and union the results to preserve the
	// export-bundle audit completeness contract: bundles include every
	// approval ever created for the session, regardless of terminal state.
	var approvals []EdgeApproval
	seen := make(map[string]struct{})
	for _, status := range []ApprovalStatus{
		ApprovalStatusPending,
		ApprovalStatusApproved,
		ApprovalStatusRejected,
		ApprovalStatusExpired,
	} {
		cursor := ""
		for {
			page, err := a.Store.ListApprovals(ctx, ListApprovalsQuery{
				TenantID:  tenantID,
				SessionID: sessionID,
				Status:    status,
				Cursor:    cursor,
				Limit:     maxStorePageLimit,
			})
			if err != nil {
				return nil, fmt.Errorf("list approvals (status=%s): %w", status, err)
			}
			for _, approval := range page.Items {
				ref := approval.ApprovalRef
				if _, dup := seen[ref]; dup {
					continue
				}
				seen[ref] = struct{}{}
				approvals = append(approvals, approval)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	return approvals, nil
}

func (a *SessionExportAssembler) collectArtifacts(ctx context.Context, tenantID string, events []AgentActionEvent) ([]ExportArtifactEntry, []ExportMissingArtifact) {
	var entries []ExportArtifactEntry
	var missing []ExportMissingArtifact
	seen := make(map[string]struct{})

	for _, e := range events {
		for _, ptr := range e.ArtifactPointers {
			if _, dup := seen[ptr.URI]; dup {
				continue
			}
			seen[ptr.URI] = struct{}{}

			// Defense in depth: the AttachArtifactPointer helper already
			// rejects cross-tenant pointers at write time, but an export
			// can run against historical data attached before the helper
			// existed. Refuse to fetch a pointer whose tenant_id disagrees
			// with the export tenant.
			if ptr.TenantID != tenantID {
				missing = append(missing, missingFromPointer(ptr, MissingArtifactReasonTenantMismatch))
				continue
			}
			if a.ArtifactStore == nil {
				// Without an artifact store wired in, every pointer is
				// effectively unresolvable. Surface that as not_found so
				// auditors see what is missing rather than getting a
				// silently empty bundle.
				missing = append(missing, missingFromPointer(ptr, MissingArtifactReasonNotFound))
				continue
			}
			meta, err := a.ArtifactStore.Stat(ctx, ptr.URI)
			if err != nil {
				reason := MissingArtifactReasonStoreError
				if errors.Is(err, artifacts.ErrArtifactNotFound) {
					reason = MissingArtifactReasonNotFound
				}
				missing = append(missing, missingFromPointer(ptr, reason))
				continue
			}
			// Defense in depth: do not trust the pointer's tenant scope alone.
			// The artifact-store metadata also carries identity labels written
			// at Put time. Mismatched labels mean either tampered evidence or
			// a buggy producer; in either case refusing to include the entry
			// is the only safe call. Empty labels are treated as "label not
			// asserted" — older artifacts may have been stored without the
			// edge-side identity convention and we accept those by pointer
			// alone, which is what the existing pointer cross-scope guard
			// above already protects.
			if !artifactStoreLabelsMatchPointer(meta.Labels, ptr) {
				missing = append(missing, missingFromPointer(ptr, MissingArtifactReasonTenantMismatch))
				continue
			}
			entries = append(entries, ExportArtifactEntry{
				SessionID:      ptr.SessionID,
				ExecutionID:    ptr.ExecutionID,
				EventID:        ptr.EventID,
				ArtifactType:   ptr.ArtifactType,
				RetentionClass: ptr.RetentionClass,
				RedactionLevel: ptr.RedactionLevel,
				SHA256:         ptr.SHA256,
				URI:            ptr.URI,
				SizeBytes:      meta.SizeBytes,
				ContentType:    meta.ContentType,
				CreatedAt:      ptr.CreatedAt,
			})
		}
	}
	return entries, missing
}

func missingFromPointer(ptr ArtifactPointer, reason MissingArtifactReason) ExportMissingArtifact {
	return ExportMissingArtifact{
		URI:          ptr.URI,
		SHA256:       ptr.SHA256,
		ArtifactType: ptr.ArtifactType,
		SessionID:    ptr.SessionID,
		ExecutionID:  ptr.ExecutionID,
		EventID:      ptr.EventID,
		Reason:       reason,
	}
}

// artifactStoreLabelsMatchPointer cross-checks the artifact-store metadata
// labels against the pointer's identity scope. The store's metadata is
// written at Put time by the producing pack and is treated as a second
// source of truth: if the pointer claims this artifact belongs to tenant-A
// but the store's labels say tenant-B, the artifact is either tampered or
// misattributed and must NOT appear in the export.
//
// A label that is absent in the metadata is treated as "not asserted" —
// older artifacts may have been stored without the Edge identity
// convention and the pointer-only path remains the fallback. A label that
// is present and disagrees with the pointer is a hard reject.
func artifactStoreLabelsMatchPointer(labels map[string]string, ptr ArtifactPointer) bool {
	checks := []struct {
		key  string
		want string
	}{
		{"tenant_id", ptr.TenantID},
		{"session_id", ptr.SessionID},
		{"execution_id", ptr.ExecutionID},
		{"event_id", ptr.EventID},
	}
	for _, c := range checks {
		got, ok := labels[c.key]
		if !ok {
			continue
		}
		if got != c.want {
			return false
		}
	}
	return true
}

func buildJobLinks(executions []AgentExecution) []ExportJobLink {
	var links []ExportJobLink
	for _, e := range executions {
		if e.JobID == "" && e.WorkflowRunID == "" && e.StepID == "" {
			continue
		}
		links = append(links, ExportJobLink{
			ExecutionID:   e.ExecutionID,
			JobID:         e.JobID,
			WorkflowRunID: e.WorkflowRunID,
			StepID:        e.StepID,
		})
	}
	return links
}

func overallRedactionLevel(entries []ExportArtifactEntry) RedactionLevel {
	level := RedactionLevelStandard
	for _, e := range entries {
		if e.RedactionLevel == RedactionLevelStrict {
			return RedactionLevelStrict
		}
	}
	return level
}
