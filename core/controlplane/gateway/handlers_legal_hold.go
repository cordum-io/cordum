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

	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = s.tenant
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

	hold, err := s.legalHoldStore.CreateHold(r.Context(), req.TenantID, req.Reason, createdBy)
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

// handleListLegalHolds lists legal holds, optionally filtered by tenant.
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
	holds, err := s.legalHoldStore.ListHolds(r.Context(), tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list legal holds")
		return
	}

	writeJSON(w, map[string]any{"holds": holds})
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

	if err := s.legalHoldStore.ReleaseHold(r.Context(), id, releasedBy); err != nil {
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
