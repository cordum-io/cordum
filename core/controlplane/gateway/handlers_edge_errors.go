package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// Stable error codes for /api/v1/edge/* responses. These are part of the API
// contract: callers SHOULD switch on `code` rather than parse `message`.
//
// Per PRD_ROADMAP §7.10, every Edge error response uses the standard envelope:
//
//	{
//	  "code":       "<stable code>",
//	  "message":    "<sanitized human copy>",
//	  "request_id": "<trace correlation>",
//	  "details":    { ... }   // optional
//	}
//
// Codes are deliberately scoped to the Edge surface; non-Edge handlers continue
// to use the legacy `{error,status}` shape until a separate migration.
const (
	edgeErrCodeUnauthorized            = "unauthorized"
	edgeErrCodeAccessDenied            = "access_denied"
	edgeErrCodeTenantRequired          = "tenant_required"
	edgeErrCodeTenantMismatch          = "tenant_mismatch"
	edgeErrCodeTenantAccessDenied      = "tenant_access_denied"
	edgeErrCodeMissingPathParam        = "missing_path_param"
	edgeErrCodeInvalidRequest          = "invalid_request"
	edgeErrCodeInvalidJSON             = "invalid_json"
	edgeErrCodeMissingField            = "missing_required_field"
	edgeErrCodeNotFound                = "not_found"
	edgeErrCodeRequestTooLarge         = "request_too_large"
	edgeErrCodeServiceUnavailable      = "service_unavailable"
	edgeErrCodeStoreUnavailable        = "store_unavailable"
	edgeErrCodeInternalError           = "internal_error"
	edgeErrCodeUpstreamError           = "upstream_error"
	edgeErrCodeConflict                = "conflict"
	edgeErrCodeSessionTerminal         = "session_terminal"
	edgeErrCodeExecutionTerminal       = "execution_terminal"
	edgeErrCodeExecutionMismatch       = "execution_session_mismatch"
	edgeErrCodeRawPayloadRejected      = "raw_payload_rejected"
	edgeErrCodeArtifactPointerInvalid  = "artifact_pointer_invalid"
	edgeErrCodeApprovalConflict        = "approval_conflict"
	edgeErrCodeApprovalNotActionable   = "approval_not_actionable"
	edgeErrCodeSelfApprovalDenied      = "self_approval_denied"
	edgeErrCodeIdempotencyConflict     = "idempotency_conflict"
	edgeErrCodeIdempotencyKeyTooLong   = "idempotency_key_invalid"
)

// edgeErrorEnvelope is the on-the-wire shape of a /api/v1/edge/* error.
type edgeErrorEnvelope struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

// writeEdgeError emits the standard Edge error envelope. Edge handlers MUST
// route every error response through this helper (or one of the typed wrappers
// below) so the wire shape stays consistent and request_id/code/message are
// always populated. Messages and details must be sanitized by the caller —
// never echo raw tool input, API keys, signed URLs, or other secrets.
func writeEdgeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = edgeErrCodeInternalError
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = code
	}
	envelope := edgeErrorEnvelope{
		Code:      code,
		Message:   message,
		RequestID: edgeRequestID(r),
	}
	if len(details) > 0 {
		envelope.Details = details
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(envelope); err != nil {
		slog.Warn("json encode edge error response failed", "error", err)
	}
}

// edgeRequestID returns the request id middleware stamped onto the request
// context (or echoed via X-Request-Id). Empty string is acceptable: tests
// require the field to be present in the JSON, not non-empty for unrouted
// requests, but production traffic always has a request id from middleware.
func edgeRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id := requestIdFromContext(r.Context()); strings.TrimSpace(id) != "" {
		return id
	}
	return strings.TrimSpace(r.Header.Get("X-Request-Id"))
}

// writeEdgeForbidden mirrors writeForbidden but emits the Edge envelope.
// Use for 403 responses on Edge routes; the underlying error is logged
// server-side and never leaked to the client.
func writeEdgeForbidden(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("edge access denied", "method", r.Method, "path", r.URL.Path, "error", err)
	writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeAccessDenied, "access denied", nil)
}

// writeEdgeUnauthorized emits a sanitized 401 envelope for Edge routes whose
// auth middleware did not run (defense in depth) or whose handler explicitly
// rejects an unauthenticated caller.
func writeEdgeUnauthorized(w http.ResponseWriter, r *http.Request) {
	writeEdgeError(w, r, http.StatusUnauthorized, edgeErrCodeUnauthorized, "authentication required", nil)
}

// writeEdgeInternalError mirrors writeInternalError but emits the Edge envelope.
func writeEdgeInternalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeEdgeError(w, r, http.StatusInternalServerError, edgeErrCodeInternalError, "internal error", nil)
}

// writeEdgeServiceUnavailable mirrors writeServiceUnavailable but emits the
// Edge envelope. Use for transient store/dependency outages.
func writeEdgeServiceUnavailable(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" unavailable", "method", r.Method, "path", r.URL.Path, "error", err)
	writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable, "service unavailable", nil)
}

// writeEdgeBadGateway mirrors writeBadGateway but emits the Edge envelope.
func writeEdgeBadGateway(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" upstream failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeEdgeError(w, r, http.StatusBadGateway, edgeErrCodeUpstreamError, "upstream service error", nil)
}

// writeEdgeJSONDecodeError mirrors writeJSONDecodeError but emits the Edge envelope.
// It distinguishes oversize bodies from malformed JSON so callers can switch
// on `code` to triage retries.
func writeEdgeJSONDecodeError(w http.ResponseWriter, r *http.Request, err error, message string) {
	if errors.Is(err, errRequestBodyTooLarge) {
		writeEdgeError(w, r, http.StatusRequestEntityTooLarge, edgeErrCodeRequestTooLarge, "request body too large", nil)
		return
	}
	if strings.TrimSpace(message) == "" {
		message = "invalid request body"
	}
	writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidJSON, message, nil)
}

// requireEdgePermissionOrRole is the Edge analogue of requirePermissionOrRole.
// On allow, returns true. On deny, emits the Edge access_denied envelope and
// returns false; callers should bail immediately.
func (s *server) requireEdgePermissionOrRole(w http.ResponseWriter, r *http.Request, permission string, legacyRoles ...string) bool {
	if strings.TrimSpace(permission) == "" {
		if len(legacyRoles) == 0 {
			return true
		}
		if err := s.requireRole(r, legacyRoles...); err != nil {
			writeEdgeForbidden(w, r, err)
			return false
		}
		return true
	}
	if s != nil && s.auth != nil && s.permChecker != nil && auth.RBACEntitled(s.currentEntitlements()) {
		if err := s.permChecker.RequirePermission(r, permission); err != nil {
			writeEdgeForbidden(w, r, err)
			return false
		}
		if !s.requireLicensePermission(w, r, permission) {
			return false
		}
		return true
	}
	if len(legacyRoles) == 0 {
		return true
	}
	if err := s.requireRole(r, legacyRoles...); err != nil {
		writeEdgeForbidden(w, r, err)
		return false
	}
	return true
}

// requireEdgePathParam mirrors requirePathParam but emits the Edge envelope on
// missing param.
func requireEdgePathParam(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	val := r.PathValue(name)
	if val == "" {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeMissingPathParam, fmt.Sprintf("missing %s", name), nil)
		return "", false
	}
	return val, true
}
