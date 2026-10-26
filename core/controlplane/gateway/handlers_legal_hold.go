package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

// initLegalHoldStore creates a legal hold store from a Redis URL.
// Returns nil on error (non-fatal — legal hold is optional).
func initLegalHoldStore(redisURL string) *audit.LegalHoldStore {
	if strings.TrimSpace(redisURL) == "" {
		return nil
	}
	store, err := audit.NewLegalHoldStore(redisURL)
	if err != nil {
		slog.Warn("legal hold store init failed", "error", err)
		return nil
	}
	return store
}

// requireLegalHoldEntitlement checks the LegalHold entitlement and writes
// a 403 response if not entitled. Returns true if the caller should return.
func (s *server) requireLegalHoldEntitlement(w http.ResponseWriter) bool {
	if err := audit.RequireLegalHoldEntitlement(s.entitlementResolver()); err != nil {
		writeLegalHoldTierLimit(w, legalHoldTierLimitHTTPError(err))
		return true
	}
	return false
}

func legalHoldTierLimitHTTPError(err error) licensing.TierLimitHTTPError {
	var tierErr *licensing.TierLimitError
	if errors.As(err, &tierErr) {
		return tierErr.ToHTTPError()
	}
	return licensing.TierLimitHTTPError{
		Code:       "tier_limit_exceeded",
		Message:    "legal hold requires Enterprise license",
		Limit:      "legal_hold",
		UpgradeURL: licensing.DefaultUpgradeURL,
	}
}

func writeLegalHoldTierLimit(w http.ResponseWriter, body licensing.TierLimitHTTPError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(body)
}

// handleCreateLegalHold creates a legal hold on a tenant's audit data.
// POST /api/v1/audit/legal-hold
func (s *server) handleCreateLegalHold(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermLegalHoldWrite, "admin") {
		return
	}
	if s.requireLegalHoldEntitlement(w) {
		return
	}
	if s.legalHoldStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "legal hold store unavailable")
		return
	}

	var req struct {
		TenantID string `json:"tenant_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Holds gate audit deletion, so the caller must have access to the
	// tenant it holds. An omitted tenant_id means the caller's own tenant.
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		resolved, err := s.resolveTenant(r, "")
		if err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
		tenant = resolved
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "reason required")
		return
	}

	// Resolve creator from auth context
	createdBy := "admin"
	if auth := auth.FromRequest(r); auth != nil && auth.PrincipalID != "" {
		createdBy = auth.PrincipalID
	}

	hold, err := s.legalHoldStore.CreateHold(r.Context(), tenant, req.Reason, createdBy)
	if err != nil {
		if errors.Is(err, audit.ErrHoldAlreadyExists) {
			writeErrorJSON(w, http.StatusConflict, "active legal hold already exists for this tenant")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to create legal hold")
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"hold": hold})
}

// handleListLegalHolds lists legal holds in the tenants the caller can
// access, optionally filtered by tenant.
// GET /api/v1/audit/legal-holds
func (s *server) handleListLegalHolds(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermLegalHoldRead, "admin") {
		return
	}
	if s.requireLegalHoldEntitlement(w) {
		return
	}
	if s.legalHoldStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "legal hold store unavailable")
		return
	}

	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant != "" && s.requireTenantAccess(r, tenant) != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	holds, err := s.legalHoldStore.ListHolds(r.Context(), tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list legal holds")
		return
	}

	// Without a tenant filter ListHolds returns every tenant's holds; keep
	// only the tenants the caller may access (all of them for a
	// cross-tenant principal).
	visible := make([]*audit.LegalHold, 0, len(holds))
	for _, hold := range holds {
		if s.requireTenantAccess(r, hold.TenantID) == nil {
			visible = append(visible, hold)
		}
	}

	writeJSON(w, map[string]any{"holds": visible})
}

// handleReleaseLegalHold releases a legal hold. Does NOT delete retained data.
// DELETE /api/v1/audit/legal-hold/{id}
func (s *server) handleReleaseLegalHold(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermLegalHoldWrite, "admin") {
		return
	}
	if s.requireLegalHoldEntitlement(w) {
		return
	}
	if s.legalHoldStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "legal hold store unavailable")
		return
	}

	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErrorJSON(w, http.StatusBadRequest, "hold id required")
		return
	}

	releasedBy := "admin"
	if auth := auth.FromRequest(r); auth != nil && auth.PrincipalID != "" {
		releasedBy = auth.PrincipalID
	}

	hold, err := s.legalHoldStore.GetHold(r.Context(), id)
	if err == nil && s.requireTenantAccess(r, hold.TenantID) != nil {
		// Answer exactly like an unknown ID so callers cannot probe
		// which hold IDs exist in tenants they cannot access.
		err = audit.ErrHoldNotFound
	}
	if err == nil {
		err = s.legalHoldStore.ReleaseHold(r.Context(), id, releasedBy)
	}
	if err != nil {
		if errors.Is(err, audit.ErrHoldNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "legal hold not found")
			return
		}
		if errors.Is(err, audit.ErrHoldAlreadyReleased) {
			writeErrorJSON(w, http.StatusConflict, "legal hold already released")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to release legal hold")
		return
	}

	writeJSON(w, map[string]any{"released": true, "id": id})
}
