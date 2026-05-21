// EDGE-143.6 — operator-exception API handlers. Implements §10.3 of
// docs/edge/kubernetes-ci-shadow-detector-design.md, gated by the Q8
// step-up auth rule (comment-a17f4f1c on task-de50a293).
//
// HTTP surface:
//
//   - POST   /api/v1/edge/shadow/exception                 — create
//   - GET    /api/v1/edge/shadow/exception/{exception_id}  — read
//   - DELETE /api/v1/edge/shadow/exception/{exception_id}  — revoke
//   - GET    /api/v1/edge/shadow/exceptions                — list
//
// Baseline auth: requireEdgePermissionOrRole(auth.PermAuditExport,
// "admin", "user"). When scope.risk_level == high/critical (CREATE) or
// the referenced exception was created with risk_level == high/critical
// (REVOKE), the handler additionally requires the step-up gate
// requirePermissionOrRole(auth.PermShadowExceptionHighRisk, "admin").
// Gate failure returns 403 with code "step_up_required". The Exception
// record persists the StepUpFactor that satisfied the gate
// ("signed_admin_token" via admin role, "mfa_recent" via explicit
// permission, "none" when the gate was not required) so audit
// consumers can pivot on auth-tier-at-time-of-action without joining
// against a live RBAC snapshot.
package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge/shadow"
)

// shadowExceptionCreateRequest is the wire body for POST. TenantID is
// taken from the X-Tenant-ID header (existing edge convention), NOT
// from the body — a client cannot create an exception in another
// tenant by spoofing the body. CreatedBy + StepUpFactor are stamped by
// the handler from the authenticated principal + auth-gate outcome.
type shadowExceptionCreateRequest struct {
	ExpiresAt       time.Time          `json:"expires_at"`
	Reason          string             `json:"reason,omitempty"`
	ScopeSourceType string             `json:"scope_source_type"`
	ScopeSourceID   string             `json:"scope_source_id,omitempty"`
	ScopeSignalSet  []string           `json:"scope_signal_set,omitempty"`
	ScopeRiskLevel  shadow.FindingRisk `json:"scope_risk_level"`
}

// shadowExceptionRevokeRequest is the optional wire body for DELETE.
// Empty body is allowed; the auth principal is the canonical
// revoked_by identity.
type shadowExceptionRevokeRequest struct {
	Reason string `json:"reason,omitempty"`
}

const maxExceptionsListLimit = 1000

// shadowExceptionRequiresStepUp is the single create/revoke severity
// gate. High and critical exception scopes require the elevated
// operator authorization path; lower severities keep the baseline
// audit-export permission behavior.
func shadowExceptionRequiresStepUp(risk shadow.FindingRisk) bool {
	switch risk {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		return true
	default:
		return false
	}
}

// requireExceptionStepUp returns ok=true on pass, ok=false after
// writing the 403 envelope on denial. factor is the StepUpFactor that
// satisfied the gate ("signed_admin_token" when the admin legacy role
// matched; "mfa_recent" when the explicit permission matched). When
// the gate is not required (risk != high/critical), the caller passes
// required=false and gets back factor="none" without invoking the
// permission checker.
func (s *server) requireExceptionStepUp(w http.ResponseWriter, r *http.Request, required bool) (shadow.StepUpFactor, bool) {
	if !required {
		return shadow.StepUpFactorNone, true
	}
	authCtx := auth.FromRequest(r)
	role := ""
	if authCtx != nil {
		role = strings.ToLower(strings.TrimSpace(authCtx.Role))
	}
	if role == "admin" {
		return shadow.StepUpFactorSignedAdminToken, true
	}
	// RBAC permission check. When the entitlement is off, the basic
	// role mapping is used (admin-only); a non-admin role with the
	// permission only matters under an entitled deployment.
	if s != nil && s.auth != nil && s.permChecker != nil && auth.RBACEntitled(s.currentEntitlements()) {
		if err := s.permChecker.RequirePermission(r, auth.PermShadowExceptionHighRisk); err == nil {
			return shadow.StepUpFactorMFARecent, true
		}
	}
	writeEdgeError(w, r, http.StatusForbidden, edgeErrCodeStepUpRequired,
		"high or critical shadow exception requires recent MFA or signed admin token",
		map[string]any{"required": "mfa_recent|signed_admin_token"})
	return "", false
}

// handleCreateShadowException ingests one operator-defined exception.
// On 201, the response carries the persisted record including the
// StepUpFactor that satisfied the create gate.
func (s *server) handleCreateShadowException(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin", "user") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	var body shadowExceptionCreateRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid shadow exception request")
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}

	// Q8 step-up gate: required when the exception scopes high or
	// critical-risk findings. Returns the StepUpFactor that satisfied
	// the gate, or writes the 403 envelope itself.
	risk := shadow.FindingRisk(strings.ToLower(strings.TrimSpace(string(body.ScopeRiskLevel))))
	factor, ok := s.requireExceptionStepUp(w, r, shadowExceptionRequiresStepUp(risk))
	if !ok {
		return
	}

	created, err := store.CreateException(r.Context(), shadow.CreateExceptionRequest{
		TenantID:        tenantID,
		CreatedBy:       principalID,
		ExpiresAt:       body.ExpiresAt,
		Reason:          body.Reason,
		ScopeSourceType: body.ScopeSourceType,
		ScopeSourceID:   body.ScopeSourceID,
		ScopeSignalSet:  body.ScopeSignalSet,
		ScopeRiskLevel:  risk,
		StepUpFactor:    factor,
	})
	if err != nil {
		writeShadowExceptionStoreError(w, r, err, "create shadow exception")
		return
	}
	s.emitShadowExceptionAudit(r, audit.EventShadowAgentExceptionCreated, principalID, created, "")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, created)
}

// handleGetShadowException returns a single exception scoped to the
// caller's tenant. Cross-tenant probe returns 404.
func (s *server) handleGetShadowException(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin", "user") {
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
	exceptionID, ok := requireEdgePathParam(w, r, "exception_id")
	if !ok {
		return
	}
	exc, err := store.GetException(r.Context(), tenantID, exceptionID)
	if err != nil {
		writeShadowExceptionStoreError(w, r, err, "get shadow exception")
		return
	}
	writeJSON(w, exc)
}

// handleListShadowExceptions returns a bounded page of exceptions for
// the requested tenant. Filters: status, source_type, risk, limit,
// cursor.
func (s *server) handleListShadowExceptions(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin", "user") {
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
	q, ok := parseShadowExceptionListQuery(w, r, tenantID)
	if !ok {
		return
	}
	page, err := store.ListExceptions(r.Context(), q)
	if err != nil {
		writeShadowExceptionStoreError(w, r, err, "list shadow exceptions")
		return
	}
	writeJSON(w, page)
}

// handleRevokeShadowException revokes an active exception. Step-up
// gate matches the create-time gate: if the exception's
// ScopeRiskLevel is high, the caller must satisfy the step-up auth
// requirement. 204 on success; 404 on missing/cross-tenant; 403 on
// step-up failure; 409 on terminal conflict.
func (s *server) handleRevokeShadowException(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditExport, "admin", "user") {
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
	exceptionID, ok := requireEdgePathParam(w, r, "exception_id")
	if !ok {
		return
	}
	principalID, err := s.resolveEdgeAuthPrincipal(r)
	if err != nil {
		writeEdgeForbidden(w, r, err)
		return
	}

	// Read the exception FIRST so we can mirror the original
	// create-time gate on revoke (Q8 governor ruling: revoke uses same
	// auth level as the original create).
	existing, err := store.GetException(r.Context(), tenantID, exceptionID)
	if err != nil {
		writeShadowExceptionStoreError(w, r, err, "get shadow exception")
		return
	}
	if _, ok := s.requireExceptionStepUp(w, r, shadowExceptionRequiresStepUp(existing.ScopeRiskLevel)); !ok {
		return
	}

	body := shadowExceptionRevokeRequest{}
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid shadow exception revoke request")
			return
		}
	}
	revoked, err := store.RevokeException(r.Context(), tenantID, exceptionID, shadow.RevokeExceptionRequest{
		RevokedBy: principalID,
		Reason:    body.Reason,
	})
	if err != nil {
		writeShadowExceptionStoreError(w, r, err, "revoke shadow exception")
		return
	}
	s.emitShadowExceptionAudit(r, audit.EventShadowAgentExceptionRevoked, principalID, revoked, body.Reason)
	w.WriteHeader(http.StatusNoContent)
}

// parseShadowExceptionListQuery extracts validated list filters. mirrors
// parseShadowFindingListQuery's envelope conventions for consistency.
func parseShadowExceptionListQuery(w http.ResponseWriter, r *http.Request, tenantID string) (shadow.ListExceptionsQuery, bool) {
	q := r.URL.Query()
	out := shadow.ListExceptionsQuery{
		TenantID:        tenantID,
		Status:          shadow.ExceptionStatus(strings.ToLower(strings.TrimSpace(q.Get("status")))),
		ScopeSourceType: strings.ToLower(strings.TrimSpace(q.Get("source_type"))),
		ScopeRiskLevel:  shadow.FindingRisk(strings.ToLower(strings.TrimSpace(q.Get("risk")))),
		Cursor:          strings.TrimSpace(q.Get("cursor")),
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxExceptionsListLimit {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "limit must be between 1 and "+strconv.Itoa(maxExceptionsListLimit), nil)
			return shadow.ListExceptionsQuery{}, false
		}
		out.Limit = n
	}
	if v := out.ScopeSourceType; v != "" {
		if _, ok := validShadowQuerySourceType[v]; !ok {
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "source_type must be one of local|kubernetes|ci|network", nil)
			return shadow.ListExceptionsQuery{}, false
		}
	}
	if v := out.ScopeRiskLevel; v != "" && v != shadow.FindingRiskLow && v != shadow.FindingRiskMedium && v != shadow.FindingRiskHigh && v != shadow.FindingRiskCritical {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "risk must be one of low|medium|high|critical", nil)
		return shadow.ListExceptionsQuery{}, false
	}
	if v := out.Status; v != "" && v != shadow.ExceptionStatusActive && v != shadow.ExceptionStatusRevoked && v != shadow.ExceptionStatusExpired {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "status must be one of active|revoked|expired", nil)
		return shadow.ListExceptionsQuery{}, false
	}
	return out, true
}

// writeShadowExceptionStoreError maps store-layer sentinel errors to
// HTTP envelopes. Mirrors writeShadowFindingStoreError so callers can
// switch on `code` consistently across the shadow surface.
func writeShadowExceptionStoreError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	switch {
	case errors.Is(err, shadow.ErrNotFound):
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "shadow exception not found", nil)
	case errors.Is(err, shadow.ErrAlreadyExists):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeConflict, "shadow exception already exists", nil)
	case errors.Is(err, shadow.ErrTerminalConflict):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeConflict, err.Error(), nil)
	case errors.Is(err, shadow.ErrValidation):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, shadow.ErrInvalidCursor):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid cursor", nil)
	case errors.Is(err, shadow.ErrExceptionLimitExceeded):
		writeEdgeError(w, r, http.StatusTooManyRequests, edgeErrCodeLimitExceeded, "per-tenant exception cap reached", nil)
	case errors.Is(err, shadow.ErrStoreUnavailable):
		writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "shadow finding store unavailable", nil)
	default:
		writeEdgeInternalError(w, r, operation, err)
	}
}

// emitShadowExceptionAudit emits a lifecycle audit event for an
// exception action. Extra carries exception_id, scope_source_type,
// scope_risk_level, expires_at, and step_up_factor so SIEM rules can
// pivot on the auth tier that authorised the action.
func (s *server) emitShadowExceptionAudit(r *http.Request, eventType, actor string, e *shadow.Exception, reason string) {
	if s == nil || s.auditExporter == nil || e == nil {
		return
	}
	severity := audit.SeverityInfo
	switch e.ScopeRiskLevel {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		severity = audit.SeverityHigh
	case shadow.FindingRiskMedium:
		severity = audit.SeverityMedium
	}
	extra := map[string]string{
		"exception_id":      e.ExceptionID,
		"scope_source_type": e.ScopeSourceType,
		"scope_risk_level":  string(e.ScopeRiskLevel),
		"step_up_factor":    string(e.StepUpFactor),
		"status":            string(e.Status),
	}
	if !e.ExpiresAt.IsZero() {
		extra["expires_at"] = e.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if e.ScopeSourceID != "" {
		extra["scope_source_id"] = e.ScopeSourceID
	}
	if reason != "" {
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
		TenantID:  e.TenantID,
		Action:    eventType,
		Identity:  actor,
		Extra:     extra,
	})
}

// emitShadowExceptionAppliedAudit is invoked by handleCreateShadowAgentFinding
// after CreateFinding returns a finding with ExceptionID set. The
// store stamps the suppression but does not own the audit exporter;
// the gateway handler does. We GET the exception to read the
// step_up_factor recorded at create time so the audit event carries
// the authoritative auth-tier-at-time-of-create value.
func (s *server) emitShadowExceptionAppliedAudit(r *http.Request, actor string, f *shadow.ShadowAgentFinding) {
	if s == nil || s.auditExporter == nil || f == nil || f.ExceptionID == "" {
		return
	}
	store := s.shadowFindingStore
	if store == nil {
		return
	}
	exc, err := store.GetException(r.Context(), f.TenantID, f.ExceptionID)
	if err != nil {
		// Suppression already happened in the store; best-effort audit.
		return
	}
	severity := audit.SeverityInfo
	switch exc.ScopeRiskLevel {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		severity = audit.SeverityHigh
	case shadow.FindingRiskMedium:
		severity = audit.SeverityMedium
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventShadowAgentExceptionApplied,
		Severity:  severity,
		TenantID:  f.TenantID,
		Action:    audit.EventShadowAgentExceptionApplied,
		Identity:  actor,
		Extra: map[string]string{
			"exception_id":      exc.ExceptionID,
			"finding_id":        f.FindingID,
			"scope_source_type": exc.ScopeSourceType,
			"scope_risk_level":  string(exc.ScopeRiskLevel),
			"step_up_factor":    string(exc.StepUpFactor),
		},
	})
}
