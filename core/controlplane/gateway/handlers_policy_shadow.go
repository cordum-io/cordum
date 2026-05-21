package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policyshadow"
)

// policyShadowUpsertRequest is the POST body for shadow activation.
// Keeping the field set minimal (just `content` + optional Metadata)
// matches the active-bundle upsert shape so operator muscle memory
// works across the two endpoints.
type policyShadowUpsertRequest struct {
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// handlePutPolicyShadow activates or replaces the shadow policy for a
// given active bundle. Tenant is resolved from ctx (session), NEVER
// from query/path, to prevent a misconfigured client from flipping a
// shadow in someone else's tenant.
func (s *server) handlePutPolicyShadow(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}
	if s.policyShadowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "policy shadow store unavailable")
		return
	}
	bundleID := policybundles.BundleIDFromRequest(r)
	if bundleID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "bundle id required")
		return
	}
	tenantID := strings.TrimSpace(tenantFromRequest(r))
	if tenantID == "" {
		writeErrorJSON(w, http.StatusForbidden, "tenant id required")
		return
	}

	var body policyShadowUpsertRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	content := policybundles.SanitizePolicyBundleYAML(strings.TrimSpace(body.Content))
	if content == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "content required")
		return
	}
	// Validate via the same path handlePutPolicyBundle uses — a malformed
	// shadow would panic the kernel's dual-eval. Keep the error body
	// identical so existing clients handle both endpoints the same way.
	if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, fmt.Sprintf("invalid policy content: %v", err))
		return
	}

	sp := policyshadow.ShadowPolicy{
		BundleID:  bundleID,
		TenantID:  tenantID,
		Content:   content,
		CreatedBy: policybundles.PolicyActorID(r),
		Metadata:  body.Metadata,
	}
	stored, err := s.policyShadowStore.Put(r.Context(), sp)
	if err != nil {
		if errors.Is(err, contextCanceled(r.Context())) {
			writeErrorJSON(w, http.StatusServiceUnavailable, "request canceled")
			return
		}
		if strings.Contains(err.Error(), "exhausted retries") {
			writeJSONError(w, http.StatusConflict, errorCodePolicyShadowConflict, "shadow write conflict — retry")
			return
		}
		writeInternalError(w, r, "policy shadow activate", err)
		return
	}
	s.emitShadowAuditEvent(r, tenantID, stored, "shadow_activate")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	writeJSON(w, stored)
}

// handleGetPolicyShadow returns the shadow policy for the given bundle
// or 404 when none is active.
func (s *server) handleGetPolicyShadow(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, s.configSvc) {
		return
	}
	if s.policyShadowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "policy shadow store unavailable")
		return
	}
	bundleID := policybundles.BundleIDFromRequest(r)
	if bundleID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "bundle id required")
		return
	}
	tenantID := strings.TrimSpace(tenantFromRequest(r))
	if tenantID == "" {
		writeErrorJSON(w, http.StatusForbidden, "tenant id required")
		return
	}
	sp, err := s.policyShadowStore.Get(r.Context(), tenantID, bundleID)
	if err != nil {
		writeInternalError(w, r, "policy shadow get", err)
		return
	}
	if sp == nil {
		writeErrorJSON(w, http.StatusNotFound, "shadow policy not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, sp)
}

// handleDeletePolicyShadow removes the shadow policy for the given
// bundle. 204 on successful removal, 404 if none was active.
func (s *server) handleDeletePolicyShadow(w http.ResponseWriter, r *http.Request) {
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyWrite, []string{"admin"}, s.configSvc) {
		return
	}
	if s.policyShadowStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "policy shadow store unavailable")
		return
	}
	bundleID := policybundles.BundleIDFromRequest(r)
	if bundleID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyValidationFailed, "bundle id required")
		return
	}
	tenantID := strings.TrimSpace(tenantFromRequest(r))
	if tenantID == "" {
		writeErrorJSON(w, http.StatusForbidden, "tenant id required")
		return
	}

	// Fetch the existing shadow BEFORE delete so the audit event can
	// carry its ShadowBundleID. Deactivation without the id is hard to
	// correlate with the matching activate event.
	existing, err := s.policyShadowStore.Get(r.Context(), tenantID, bundleID)
	if err != nil {
		writeInternalError(w, r, "policy shadow get", err)
		return
	}
	if existing == nil {
		writeErrorJSON(w, http.StatusNotFound, "shadow policy not found")
		return
	}
	removed, err := s.policyShadowStore.Delete(r.Context(), tenantID, bundleID)
	if err != nil {
		if strings.Contains(err.Error(), "exhausted retries") {
			writeJSONError(w, http.StatusConflict, errorCodePolicyShadowConflict, "shadow write conflict — retry")
			return
		}
		writeInternalError(w, r, "policy shadow delete", err)
		return
	}
	if !removed {
		// Raced with another deactivation — treat as 404 so callers
		// don't interpret idempotent no-op as success.
		writeErrorJSON(w, http.StatusNotFound, "shadow policy not found")
		return
	}
	s.emitShadowAuditEvent(r, tenantID, existing, "shadow_deactivate")
	w.WriteHeader(http.StatusNoContent)
}

// emitShadowAuditEvent records the activate/deactivate action in the
// SIEM chain. Uses safety.policy_change so downstream rules can
// correlate shadow activity alongside active-policy edits; the
// sub-event is carried in Action + Extra['shadow_bundle_id'].
func (s *server) emitShadowAuditEvent(r *http.Request, tenantID string, sp *policyshadow.ShadowPolicy, action string) {
	if s == nil || s.auditExporter == nil || sp == nil {
		return
	}
	extra := map[string]string{
		"shadow_bundle_id": sp.ShadowBundleID,
		"bundle_id":        sp.BundleID,
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventPolicyChange,
		Severity:  audit.SeverityInfo,
		TenantID:  tenantID,
		Action:    action,
		Identity:  policybundles.PolicyActorID(r),
		Extra:     extra,
	})
}

// contextCanceled returns the context's cancellation error if it has
// been cancelled. Used by handlers to map cancelled Put/Delete calls
// to a 503 instead of a 500.
func contextCanceled(ctx interface{ Err() error }) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
