package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
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
	edgeErrCodeUnauthorized             = "unauthorized"
	edgeErrCodeAccessDenied             = "access_denied"
	edgeErrCodeTenantRequired           = "tenant_required"
	edgeErrCodeTenantMismatch           = "tenant_mismatch"
	edgeErrCodeTenantAccessDenied       = "tenant_access_denied"
	edgeErrCodeMissingPathParam         = "missing_path_param"
	edgeErrCodeInvalidRequest           = "invalid_request"
	edgeErrCodeInvalidJSON              = "invalid_json"
	edgeErrCodeMissingField             = "missing_required_field"
	edgeErrCodeNotFound                 = "not_found"
	edgeErrCodeRequestTooLarge          = "request_too_large"
	edgeErrCodeServiceUnavailable       = "service_unavailable"
	edgeErrCodeStoreUnavailable         = "store_unavailable"
	edgeErrCodeInternalError            = "internal_error"
	edgeErrCodeConflict                 = "conflict"
	edgeErrCodeLimitExceeded            = "limit_exceeded"
	edgeErrCodeSessionTerminal          = "session_terminal"
	edgeErrCodeExecutionTerminal        = "execution_terminal"
	edgeErrCodeExecutionMismatch        = "execution_session_mismatch"
	edgeErrCodeRawPayloadRejected       = "raw_payload_rejected"
	edgeErrCodeArtifactPointerInvalid   = "artifact_pointer_invalid"
	edgeErrCodeApprovalConflict         = "approval_conflict"
	edgeErrCodeSelfApprovalDenied       = "self_approval_denied"
	edgeErrCodeIdempotencyConflict      = "idempotency_conflict"
	edgeErrCodeIdempotencyKeyTooLong    = "idempotency_key_invalid"
	edgeErrCodeIdempotencyWindowExpired = "idempotency_window_expired"
	edgeErrCodeMaxExecutionsExceeded    = "max_executions_exceeded"
	edgeErrCodeEventCapExceeded         = "event_cap_exceeded"
	edgeErrCodeReplayWindowFull         = "replay_window_full"
	// EDGE-065 — POST /api/v1/edge/sessions/{id}/export rejects max_events
	// values that exceed the per-request cap (handlers_edge_export.go
	// maxExportEventsRequest). Maps to HTTP 400 Bad Request.
	edgeErrCodeMaxEventsTooLarge = "max_events_too_large"
	// EDGE-058 — EnqueueApproval refused inline validation because the parent
	// execution's event list exceeded maxEventsPerApprovalValidation. Maps to
	// HTTP 422 Unprocessable Entity (request well-formed but execution state
	// precludes the operation).
	edgeErrCodeEventListTooLarge = "event_list_too_large"
	// EDGE-143.6 — POST /api/v1/edge/shadow/exception (and the matching
	// DELETE) refuses the operation because the scope risk is high and
	// the caller did not satisfy the Q8 step-up auth gate (admin role
	// or PermShadowExceptionHighRisk). Maps to HTTP 403 Forbidden so
	// SIEM correlation can distinguish step-up rejection from baseline
	// access denial.
	edgeErrCodeStepUpRequired = "step_up_required"
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
		envelope.Details = sanitizeEdgeErrorDetails(details)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(envelope); err != nil {
		slog.Warn("json encode edge error response failed", "error", err)
	}
}

func sanitizeEdgeErrorDetails(details map[string]any) map[string]any {
	if len(details) == 0 {
		return nil
	}
	result, err := edgecore.RedactValue(details, edgecore.RedactionOptions{
		HashMode:       edgecore.RedactionHashNone,
		MaxDepth:       8,
		MaxItems:       64,
		MaxStringBytes: 256,
		MaxTotalBytes:  8 << 10,
	})
	if err != nil {
		return map[string]any{"redacted": true}
	}
	out, ok := result.Value.(map[string]any)
	if !ok {
		return map[string]any{"redacted": true}
	}
	return out
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

// writeEdgeInternalError mirrors writeInternalError but emits the Edge envelope.
func writeEdgeInternalError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	slog.Error(operation+" failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeEdgeError(w, r, http.StatusInternalServerError, edgeErrCodeInternalError, "internal error", nil)
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

// resolveEdgeAuthPrincipal returns the authenticated principal stamped by the
// auth provider. Edge evidence is compliance evidence: clients may not spoof a
// different principal_id inside the JSON body. If an auth provider does not
// populate a principal, fall back to the provider's normal blank-request
// resolution path (for local header-based test/dev modes), never to a
// client-supplied body principal.
func (s *server) resolveEdgeAuthPrincipal(r *http.Request) (string, error) {
	if authn := auth.FromRequest(r); authn != nil && strings.TrimSpace(authn.PrincipalID) != "" {
		return strings.TrimSpace(authn.PrincipalID), nil
	}
	return s.resolvePrincipal(r, "")
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
