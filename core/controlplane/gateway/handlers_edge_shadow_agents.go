// EDGE-141 — Shadow agent finding lifecycle handlers.
//
// HTTP surface mounted under /api/v1/edge/shadow-agents/:
//
//   - POST   /api/v1/edge/shadow-agents               — create (ingest one finding)
//   - GET    /api/v1/edge/shadow-agents               — list (cursor-paginated)
//   - GET    /api/v1/edge/shadow-agents/{finding_id}  — get
//   - POST   /api/v1/edge/shadow-agents/{finding_id}/resolve   — resolve
//   - POST   /api/v1/edge/shadow-agents/{finding_id}/suppress  — suppress
//   - POST   /api/v1/edge/shadow-agents/{finding_id}/ignore    — alias for suppress (compat)
//
// All routes share auth + tenant resolution via existing Edge helpers:
// requireEdgePermissionOrRole + edgeTenantFromRequest +
// resolveEdgeAuthPrincipal. Errors flow through writeEdgeError so the
// {code,message,request_id,details} envelope is consistent with the
// rest of /api/v1/edge.
//
// Observe/warn ONLY (EDGE-141 task rail): handlers do not enqueue
// remediation jobs, do not flip enforcement, and do not call into the
// Safety Kernel. They persist + audit the lifecycle disposition; the
// dashboard surfaces remain deferred.
package gateway

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge/shadow"
)

// shadowQuerySignalRe gates per-entry shape of the repeated ?signal=
// query param: bounded to the same alphabet as the store's signal
// validation, so 400s land in the handler rather than 500s in the
// store.
var shadowQuerySignalRe = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// §10.2 byte caps mirror the §10.1 store-side caps; reject earlier.
const (
	shadowQueryClusterIDMaxBytes   = 64
	shadowQueryNamespaceMaxBytes   = 63
	shadowQueryRepoMaxBytes        = 256
	shadowQueryExceptionIDMaxBytes = 64
	shadowQuerySignalMaxEntries    = 16
)

// validShadowQuerySourceType / CIProvider mirror the store-side enum
// gates; centralised here so the handler returns 400 with a clean
// envelope instead of letting the store error bubble up.
var (
	validShadowQuerySourceType = map[string]struct{}{
		shadow.SourceTypeLocal:      {},
		shadow.SourceTypeKubernetes: {},
		shadow.SourceTypeCI:         {},
		shadow.SourceTypeNetwork:    {},
	}
	validShadowQueryCIProvider = map[string]struct{}{
		shadow.CIProviderGitHubActions: {},
		shadow.CIProviderGitLabCI:      {},
		shadow.CIProviderJenkins:       {},
		shadow.CIProviderBuildkite:     {},
		shadow.CIProviderCircleCI:      {},
		shadow.CIProviderOther:         {},
	}
)

type shadowAgentCreateRequest struct {
	FindingID        string                  `json:"finding_id,omitempty"`
	TenantID         string                  `json:"tenant_id,omitempty"`
	OwnerPrincipalID string                  `json:"owner_principal_id"`
	PrincipalID      string                  `json:"principal_id,omitempty"`
	AgentProduct     string                  `json:"agent_product"`
	AgentID          string                  `json:"agent_id,omitempty"`
	Hostname         string                  `json:"hostname,omitempty"`
	Risk             shadow.FindingRisk      `json:"risk"`
	EvidenceType     string                  `json:"evidence_type"`
	EvidenceSummary  string                  `json:"evidence_summary,omitempty"`
	EvidenceArtifact *shadow.EvidencePointer `json:"evidence_artifact_ptr,omitempty"`
	RedactedPath     string                  `json:"redacted_path,omitempty"`
	DetectedAt       time.Time               `json:"detected_at"`
	Metadata         map[string]string       `json:"metadata,omitempty"`

	// EDGE-143.5 — §10.1 wire fields. All omitempty so EDGE-141
	// clients without these fields continue to ingest cleanly.
	SourceType          string                             `json:"source_type,omitempty"`
	SourceID            string                             `json:"source_id,omitempty"`
	ClusterID           string                             `json:"cluster_id,omitempty"`
	Namespace           string                             `json:"namespace,omitempty"`
	WorkloadKind        string                             `json:"workload_kind,omitempty"`
	WorkloadName        string                             `json:"workload_name,omitempty"`
	PodUID              string                             `json:"pod_uid,omitempty"`
	CIProvider          string                             `json:"ci_provider,omitempty"`
	Repo                string                             `json:"repo,omitempty"`
	Ref                 string                             `json:"ref,omitempty"`
	WorkflowID          string                             `json:"workflow_id,omitempty"`
	JobID               string                             `json:"job_id,omitempty"`
	RunID               string                             `json:"run_id,omitempty"`
	RunnerID            string                             `json:"runner_id,omitempty"`
	TenantSource        string                             `json:"tenant_source,omitempty"`
	PrincipalSource     string                             `json:"principal_source,omitempty"`
	SignalSet           []string                           `json:"signal_set,omitempty"`
	Confidence          float64                            `json:"confidence,omitempty"`
	FirstSeen           *time.Time                         `json:"first_seen,omitempty"`
	LastSeen            *time.Time                         `json:"last_seen,omitempty"`
	FalsePositiveReason string                             `json:"false_positive_reason,omitempty"`
	ExceptionID         string                             `json:"exception_id,omitempty"`
	RetentionClass      shadow.ShadowFindingRetentionClass `json:"retention_class,omitempty"`
}

type shadowAgentResolveRequest struct {
	Reason string `json:"reason,omitempty"`
}

type shadowAgentSuppressRequest struct {
	Reason          string     `json:"reason,omitempty"`
	SuppressedUntil *time.Time `json:"suppressed_until,omitempty"`
}

// handleCreateShadowAgentFinding ingests one shadow finding.
// Returns 201 + the persisted record on success.
func (s *server) handleCreateShadowAgentFinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var body shadowAgentCreateRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid shadow finding request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, body.TenantID)
	if !ok {
		return
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	// Caller-supplied principal_id may identify the detector (e.g. the
	// scanner service principal). If omitted, fall back to the auth
	// principal so the audit row always has identity.
	detector := strings.TrimSpace(body.PrincipalID)
	if detector == "" {
		detector = principalID
	}

	created, err := store.CreateFinding(r.Context(), shadow.CreateFindingRequest{
		FindingID:        body.FindingID,
		TenantID:         tenantID,
		OwnerPrincipalID: body.OwnerPrincipalID,
		PrincipalID:      detector,
		AgentProduct:     body.AgentProduct,
		AgentID:          body.AgentID,
		Hostname:         body.Hostname,
		Risk:             body.Risk,
		EvidenceType:     body.EvidenceType,
		EvidenceSummary:  body.EvidenceSummary,
		EvidenceArtifact: body.EvidenceArtifact,
		RedactedPath:     body.RedactedPath,
		DetectedAt:       body.DetectedAt,
		Metadata:         body.Metadata,

		// EDGE-143.5 — §10.1 fields forwarded from wire body to store.
		SourceType:          body.SourceType,
		SourceID:            body.SourceID,
		ClusterID:           body.ClusterID,
		Namespace:           body.Namespace,
		WorkloadKind:        body.WorkloadKind,
		WorkloadName:        body.WorkloadName,
		PodUID:              body.PodUID,
		CIProvider:          body.CIProvider,
		Repo:                body.Repo,
		Ref:                 body.Ref,
		WorkflowID:          body.WorkflowID,
		JobID:               body.JobID,
		RunID:               body.RunID,
		RunnerID:            body.RunnerID,
		TenantSource:        body.TenantSource,
		PrincipalSource:     body.PrincipalSource,
		SignalSet:           body.SignalSet,
		Confidence:          body.Confidence,
		FirstSeen:           body.FirstSeen,
		LastSeen:            body.LastSeen,
		FalsePositiveReason: body.FalsePositiveReason,
		ExceptionID:         body.ExceptionID,
		RetentionClass:      body.RetentionClass,
	})
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "create shadow finding")
		return
	}
	s.emitShadowFindingAudit(r, audit.EventShadowAgentDetected, principalID, created, "")
	// EDGE-143.6 — if the store stamped an ExceptionID at emit time,
	// emit the per-finding shadow_agent.exception_applied audit event
	// carrying the exception's recorded step_up_factor.
	if created != nil && created.ExceptionID != "" {
		s.emitShadowExceptionAppliedAudit(r, principalID, created)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

// handleListShadowAgentFindings returns a bounded page of findings for
// the requested tenant. Query params: risk, status, agent (alias for
// agent_product), owner, limit, cursor.
func (s *server) handleListShadowAgentFindings(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	q, ok := parseShadowFindingListQuery(w, r, tenantID)
	if !ok {
		return
	}
	page, err := store.ListFindings(r.Context(), q)
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "list shadow findings")
		return
	}
	writeJSON(w, page)
}

// handleGetShadowAgentFinding returns a single finding scoped to the
// caller's tenant. Cross-tenant lookup returns 404 (never 403) to
// avoid leaking tuple existence.
func (s *server) handleGetShadowAgentFinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	findingID, ok := requireEdgePathParam(w, r, "finding_id")
	if !ok {
		return
	}
	f, err := store.GetFinding(r.Context(), tenantID, findingID)
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "get shadow finding")
		return
	}
	writeJSON(w, f)
}

// handleResolveShadowAgentFinding flips a detected finding to resolved.
func (s *server) handleResolveShadowAgentFinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	findingID, ok := requireEdgePathParam(w, r, "finding_id")
	if !ok {
		return
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	var body shadowAgentResolveRequest
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid shadow finding resolve request")
			return
		}
	}
	updated, err := store.ResolveFinding(r.Context(), tenantID, findingID, shadow.ResolveRequest{
		ResolvedBy: principalID,
		Reason:     body.Reason,
	})
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "resolve shadow finding")
		return
	}
	s.emitShadowFindingAudit(r, audit.EventShadowAgentResolved, principalID, updated, body.Reason)
	writeJSON(w, updated)
}

// handleSuppressShadowAgentFinding flips a detected finding to
// suppressed. Mounted at both /suppress (canonical) and /ignore
// (PRD-compat alias).
func (s *server) handleSuppressShadowAgentFinding(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	findingID, ok := requireEdgePathParam(w, r, "finding_id")
	if !ok {
		return
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}
	var body shadowAgentSuppressRequest
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid shadow finding suppress request")
			return
		}
	}
	updated, err := store.SuppressFinding(r.Context(), tenantID, findingID, shadow.SuppressRequest{
		SuppressedBy:    principalID,
		Reason:          body.Reason,
		SuppressedUntil: body.SuppressedUntil,
	})
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "suppress shadow finding")
		return
	}
	s.emitShadowFindingAudit(r, audit.EventShadowAgentSuppressed, principalID, updated, body.Reason)
	writeJSON(w, updated)
}

// shadowFindingStoreOrUnavailable returns the configured store or
// writes a 503 envelope and returns nil. Mirrors edgeStoreOrUnavailable.
func (s *server) shadowFindingStoreOrUnavailable(w http.ResponseWriter, r *http.Request) shadow.Store {
	if s == nil || s.shadowFindingStore == nil {
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "shadow finding store unavailable", nil)
		return nil
	}
	return s.shadowFindingStore
}

// parseShadowFindingListQuery extracts validated list filters from the
// URL. Returns ok=false after writing the appropriate 400 envelope.
func parseShadowFindingListQuery(w http.ResponseWriter, r *http.Request, tenantID string) (shadow.ListFindingsQuery, bool) {
	q := r.URL.Query()
	out := shadow.ListFindingsQuery{
		TenantID:         tenantID,
		Status:           shadow.FindingStatus(strings.ToLower(strings.TrimSpace(q.Get("status")))),
		Risk:             shadow.FindingRisk(strings.ToLower(strings.TrimSpace(q.Get("risk")))),
		AgentProduct:     strings.ToLower(strings.TrimSpace(firstNonEmpty(q.Get("agent_product"), q.Get("agent")))),
		OwnerPrincipalID: strings.TrimSpace(q.Get("owner")),
		Cursor:           strings.TrimSpace(q.Get("cursor")),
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n := edgeQueryLimit(r)
		if n <= 0 {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "limit must be a positive integer", nil)
			return shadow.ListFindingsQuery{}, false
		}
		// Boundary cap: clamp adversarial caller values at decode time so
		// the bound is visible at the request boundary and not solely
		// dependent on the shadow.RedisStore.clampListPageSize re-clamp.
		// This is the defense-in-depth gate DoD requires for the CodeQL
		// uncontrolled-allocation-size class (alerts #34-39 on PR #276).
		if n > shadow.MaxListPageSize {
			n = shadow.MaxListPageSize
		}
		out.Limit = n
	}
	if out.Status != "" && out.Status != shadow.FindingStatusDetected && out.Status != shadow.FindingStatusResolved && out.Status != shadow.FindingStatusSuppressed {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "status must be one of detected|resolved|suppressed", nil)
		return shadow.ListFindingsQuery{}, false
	}
	if out.Risk != "" && out.Risk != shadow.FindingRiskLow && out.Risk != shadow.FindingRiskMedium && out.Risk != shadow.FindingRiskHigh && out.Risk != shadow.FindingRiskCritical {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "risk must be one of low|medium|high|critical", nil)
		return shadow.ListFindingsQuery{}, false
	}

	// EDGE-143.5 — §10.2 query params.
	if v := strings.ToLower(strings.TrimSpace(q.Get("source_type"))); v != "" {
		if _, ok := validShadowQuerySourceType[v]; !ok {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "source_type must be one of local|kubernetes|ci|network", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.SourceType = v
	}
	if v := strings.ToLower(strings.TrimSpace(q.Get("ci_provider"))); v != "" {
		if _, ok := validShadowQueryCIProvider[v]; !ok {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "ci_provider must be one of github_actions|gitlab_ci|jenkins|buildkite|circleci|other", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.CIProvider = v
	}
	if v := strings.TrimSpace(q.Get("cluster_id")); v != "" {
		if len(v) > shadowQueryClusterIDMaxBytes {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "cluster_id exceeds 64 bytes", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.ClusterID = v
	}
	if v := strings.TrimSpace(q.Get("namespace")); v != "" {
		if len(v) > shadowQueryNamespaceMaxBytes {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "namespace exceeds 63 bytes", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.Namespace = v
	}
	if v := strings.TrimSpace(q.Get("repo")); v != "" {
		if len(v) > shadowQueryRepoMaxBytes {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "repo exceeds 256 bytes", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.Repo = v
	}
	// repo without ci_provider is a 400 (composite constraint).
	if out.Repo != "" && out.CIProvider == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "repo requires ci_provider", nil)
		return shadow.ListFindingsQuery{}, false
	}
	if v := strings.TrimSpace(q.Get("exception_id")); v != "" {
		if len(v) > shadowQueryExceptionIDMaxBytes {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "exception_id exceeds 64 bytes", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.ExceptionID = v
	}
	if raw := q["signal"]; len(raw) > 0 {
		if len(raw) > shadowQuerySignalMaxEntries {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "signal accepts at most 16 entries", nil)
			return shadow.ListFindingsQuery{}, false
		}
		signals := make([]string, 0, len(raw))
		for _, sig := range raw {
			s := strings.ToLower(strings.TrimSpace(sig))
			if !shadowQuerySignalRe.MatchString(s) {
				writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "signal entry must match [a-z0-9_]{1,32}", nil)
				return shadow.ListFindingsQuery{}, false
			}
			signals = append(signals, s)
		}
		out.Signals = signals
	}
	if raw := strings.TrimSpace(q.Get("confidence_min")); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		// NaN/Inf both satisfy !(f<0||f>1) so guard explicitly; otherwise
		// NaN bypasses validation and matches no findings silently.
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "confidence_min must be a float in [0, 1]", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.ConfidenceMin = f
	}
	if raw := strings.TrimSpace(q.Get("first_seen_after")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "first_seen_after must be RFC3339", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.FirstSeenAfter = &t
	}
	if raw := strings.TrimSpace(q.Get("last_seen_before")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "last_seen_before must be RFC3339", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.LastSeenBefore = &t
	}
	if raw := strings.TrimSpace(q.Get("include_managed_skip")); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "include_managed_skip must be true|false", nil)
			return shadow.ListFindingsQuery{}, false
		}
		out.IncludeManagedSkip = b
	}
	return out, true
}

// writeShadowFindingStoreError maps store-layer sentinel errors to HTTP
// envelopes. Anything unrecognised falls through to a sanitised 500 so
// raw Redis details never leak.
func writeShadowFindingStoreError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	switch {
	case errors.Is(err, shadow.ErrNotFound):
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "shadow finding not found", nil)
	case errors.Is(err, shadow.ErrAlreadyExists):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeConflict, "shadow finding already exists", nil)
	case errors.Is(err, shadow.ErrTerminalConflict):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeConflict, err.Error(), nil)
	case errors.Is(err, shadow.ErrValidation):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, shadow.ErrInvalidCursor):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid cursor", nil)
	case errors.Is(err, shadow.ErrStoreUnavailable):
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "shadow finding store unavailable", nil)
	default:
		writeEdgeInternalError(w, r, operation, err)
	}
}

// emitShadowFindingAudit emits the lifecycle audit event through the
// existing gateway exporter. Payloads carry already-redacted summary +
// artifact pointer metadata; raw evidence summaries are NEVER included.
func (s *server) emitShadowFindingAudit(_ *http.Request, eventType, actor string, f *shadow.ShadowAgentFinding, reason string) {
	if s == nil || s.auditExporter == nil || f == nil {
		return
	}
	severity := audit.SeverityInfo
	switch f.Risk {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		severity = audit.SeverityHigh
	case shadow.FindingRiskMedium:
		severity = audit.SeverityMedium
	}
	extra := map[string]string{
		"finding_id":    f.FindingID,
		"agent_product": f.AgentProduct,
		"risk":          string(f.Risk),
		"status":        string(f.Status),
		"evidence_type": f.EvidenceType,
	}
	if f.AgentID != "" {
		extra["agent_id"] = f.AgentID
	}
	if f.Hostname != "" {
		extra["hostname"] = f.Hostname
	}
	if f.RedactedPath != "" {
		extra["redacted_path"] = f.RedactedPath
	}
	if f.OwnerPrincipalID != "" {
		extra["owner_principal_id"] = f.OwnerPrincipalID
	}
	if f.EvidenceArtifact != nil {
		extra["evidence_artifact_uri"] = f.EvidenceArtifact.URI
		extra["evidence_artifact_sha256"] = f.EvidenceArtifact.SHA256
	}
	if reason != "" {
		// Cap reason at audit-payload size to avoid bloating SIEM rows.
		const auditReasonMaxBytes = 256
		clipped := reason
		if len(clipped) > auditReasonMaxBytes {
			clipped = clipped[:auditReasonMaxBytes]
		}
		extra["reason"] = clipped
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: eventType,
		Severity:  severity,
		TenantID:  f.TenantID,
		Action:    eventType,
		Identity:  actor,
		Extra:     extra,
	})
}
