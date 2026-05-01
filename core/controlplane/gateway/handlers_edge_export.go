package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/artifacts"
)

const (
	// defaultEdgeExportMaxBytes mirrors the existing artifact-store
	// 10 MiB convention. Bundles approaching this size are extraordinary;
	// the cap exists as a defense-in-depth guard against an unbounded
	// session pulling the gateway's process memory under sustained load.
	defaultEdgeExportMaxBytes int64 = 10 * 1024 * 1024

	// edgeExportMaxBytesCeiling is the absolute cap; CORDUM_EDGE_EXPORT_MAX_BYTES
	// can lower this but cannot raise above it. 64 MiB is enough room for
	// the most pathological allowed session while still bounding gateway
	// memory deterministically.
	edgeExportMaxBytesCeiling int64 = 64 * 1024 * 1024
)

// edgeExportMaxBytes resolves the active size cap for evidence exports.
// Reads CORDUM_EDGE_EXPORT_MAX_BYTES; clamps to [1KiB, ceiling] and falls
// back to defaultEdgeExportMaxBytes on missing/invalid input. The clamp
// is deliberately one-way (downward only) to match the architect's rail
// "clamp caller-requested limits downward only".
func edgeExportMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("CORDUM_EDGE_EXPORT_MAX_BYTES"))
	if raw == "" {
		return defaultEdgeExportMaxBytes
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1024 {
		return defaultEdgeExportMaxBytes
	}
	if n > edgeExportMaxBytesCeiling {
		return edgeExportMaxBytesCeiling
	}
	return n
}

// edgeExportRequest is the optional body for POST /api/v1/edge/sessions/{id}/export.
// All fields are optional; an empty body produces a default-bounded bundle.
// IncludeArtifactBodies is intentionally absent from the request struct —
// P0 hardcodes false at this layer. A future task may add an allowlisted
// opt-in path with stricter authentication.
type edgeExportRequest struct {
	MaxEvents int `json:"max_events,omitempty"`
}

// handleExportEdgeSession assembles a SessionExportBundle for the named
// session using the metadata-only artifact store path. Reads only — never
// mutates the session, executions, events, approvals, or artifact store.
//
// Auth: PermJobsRead + admin/user/viewer (matches the rest of the Edge
// read surface). The export contains identifying metadata about the
// session's principals and policy decisions, so the same role gate as
// other read-side handlers applies.
//
// Error mapping:
//   - 400 invalid path param / malformed body
//   - 401/403 enforced by requirePermissionOrRole and edgeTenantFromRequest
//   - 404 session not found OR cross-tenant access (same response so
//     existence does not leak)
//   - 503 edge store unavailable
//   - 500 unexpected store error
func (s *server) handleExportEdgeSession(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	sessionID, ok := requirePathParam(w, r, "session_id")
	if !ok {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "session_id is required")
		return
	}

	// Optional body: { max_events?: int }. Empty body is fine — defaults
	// in core/edge/export.go apply.
	var req edgeExportRequest
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeJSONDecodeError(w, err, "invalid edge export request")
			return
		}
	}
	opts := edgecore.ExportOptions{
		MaxEvents:             req.MaxEvents,
		IncludeArtifactBodies: false, // P0: never include artifact bodies in export response.
	}

	// Bundle the metadata-only artifact stater. If the artifact store is
	// not wired (development/local mode), the bundler will emit
	// missing_artifacts entries for every pointer rather than fail the
	// export — auditors then see what should have been there.
	var artifactStore edgecore.ArtifactStater
	if s.artifactStore != nil {
		if rs, ok := s.artifactStore.(*artifacts.RedisStore); ok {
			artifactStore = rs
		} else if stater, ok := s.artifactStore.(edgecore.ArtifactStater); ok {
			artifactStore = stater
		}
	}

	assembler := &edgecore.SessionExportAssembler{
		Store:         store,
		ArtifactStore: artifactStore,
		// Now defaults to time.Now() when nil; the assembler stamps
		// GeneratedAt with that value. Tests that need a deterministic
		// stamp inject Now via the assembler directly.
	}
	bundle, err := assembler.Assemble(r.Context(), edgecore.ExportSessionQuery{
		TenantID:  tenantID,
		SessionID: sessionID,
	}, opts)
	if err != nil {
		// Cross-tenant and missing-session both map to 404 by design so
		// the existence of a session in another tenant cannot be probed.
		if errors.Is(err, edgecore.ErrNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "edge session not found")
			return
		}
		writeInternalError(w, r, "assemble edge session export", err)
		return
	}

	// Marshal once so we can both check size and serve the same bytes —
	// re-marshaling would race against any time.Now()-touching field. If
	// the bundle exceeds CORDUM_EDGE_EXPORT_MAX_BYTES we surface the
	// limit in Truncation and fail with 413 so the caller can lower
	// max_events; we do NOT silently truncate trailing events because
	// auditors must not receive a partial bundle without an explicit
	// signal in the response status code.
	payload, marshalErr := json.Marshal(bundle)
	if marshalErr != nil {
		writeInternalError(w, r, "marshal edge session export", marshalErr)
		return
	}
	maxBytes := edgeExportMaxBytes()
	if int64(len(payload)) > maxBytes {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"edge export bundle is %d bytes, exceeds cap of %d bytes; reduce max_events", len(payload), maxBytes,
		))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	if _, err := w.Write(payload); err != nil {
		// Best-effort write; the headers are already flushed.
		return
	}
}
