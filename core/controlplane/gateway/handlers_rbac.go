package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

// handleListRoles returns all role definitions.
// GET /api/v1/auth/roles
func (s *server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesRead, "admin") {
		return
	}
	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	roles, err := s.rbacStore.ListRoles(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list roles")
		return
	}

	// Annotate with RBAC entitlement status so the dashboard knows
	// whether custom roles are editable.
	entitled := auth.RBACEntitled(s.currentEntitlements())
	writeJSON(w, map[string]any{
		"roles":    roles,
		"entitled": entitled,
	})
}

// handleGetRole returns a single role definition with resolved permissions.
// GET /api/v1/auth/roles/{name}
func (s *server) handleGetRole(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesRead, "admin") {
		return
	}
	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "role name required")
		return
	}

	role, err := s.rbacStore.GetRole(r.Context(), name)
	if err != nil {
		if errors.Is(err, auth.ErrRoleNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "role not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get role")
		return
	}

	// Resolve flattened permissions for display
	resolved, resolveErr := s.rbacStore.ResolvePermissions(r.Context(), name)
	if resolveErr != nil {
		resolved = role.Permissions
	}

	writeJSON(w, map[string]any{
		"role":                 role,
		"resolved_permissions": resolved,
	})
}

// roleRequest is the JSON body for creating/updating a role.
type roleRequest struct {
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	Inherits    []string `json:"inherits"`
}

// handlePutRole creates or updates a role definition.
// PUT /api/v1/auth/roles/{name}
func (s *server) handlePutRole(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesWrite, "admin") {
		return
	}

	// Check RBAC entitlement — custom role management requires it
	if !auth.RBACEntitled(s.currentEntitlements()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(licensing.TierLimitHTTPError{
			Code:       "tier_limit_exceeded",
			Message:    "advanced RBAC requires an Enterprise license",
			Limit:      "rbac",
			UpgradeURL: licensing.DefaultUpgradeURL,
		})
		return
	}

	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "role name required")
		return
	}

	var req roleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Check if updating a built-in role — only description and permissions can change
	existing, existErr := s.rbacStore.GetRole(r.Context(), name)
	if existErr == nil && existing.BuiltIn {
		// Built-in roles can have their description updated but not inheritance
		if len(req.Inherits) > 0 && !slicesEqual(req.Inherits, existing.Inherits) {
			writeErrorJSON(w, http.StatusBadRequest, "cannot change inheritance of built-in role")
			return
		}
	}

	// Validate inheritance — no cycles, no unknown parents
	if len(req.Inherits) > 0 {
		if err := s.rbacStore.ValidateInheritance(r.Context(), name, req.Inherits); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	role := &auth.RoleDefinition{
		Name:        name,
		Description: req.Description,
		Permissions: req.Permissions,
		Inherits:    req.Inherits,
	}

	// Preserve built-in flag if role already exists
	if existErr == nil {
		role.BuiltIn = existing.BuiltIn
		role.CreatedAt = existing.CreatedAt
	}

	if err := s.rbacStore.PutRole(r.Context(), role); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to save role")
		return
	}

	// Re-read to get the final state
	saved, err := s.rbacStore.GetRole(r.Context(), name)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to read saved role")
		return
	}

	op := "create"
	if existErr == nil {
		op = "update"
	}
	s.emitRoleUpserted(r, saved, op)

	writeJSON(w, map[string]any{"role": saved})
}

// handleDeleteRole removes a custom role definition.
// DELETE /api/v1/auth/roles/{name}
func (s *server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesWrite, "admin") {
		return
	}

	// Check RBAC entitlement
	if !auth.RBACEntitled(s.currentEntitlements()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(licensing.TierLimitHTTPError{
			Code:       "tier_limit_exceeded",
			Message:    "advanced RBAC requires an Enterprise license",
			Limit:      "rbac",
			UpgradeURL: licensing.DefaultUpgradeURL,
		})
		return
	}

	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == "" {
		writeErrorJSON(w, http.StatusBadRequest, "role name required")
		return
	}

	if err := s.rbacStore.DeleteRole(r.Context(), name); err != nil {
		if errors.Is(err, auth.ErrRoleNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "role not found")
			return
		}
		if errors.Is(err, auth.ErrBuiltInRole) {
			writeErrorJSON(w, http.StatusBadRequest, "cannot delete built-in role")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete role")
		return
	}

	s.emitRoleDeleted(r, name)

	writeJSON(w, map[string]any{"deleted": true, "name": name})
}

// slicesEqual compares two string slices for equality.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
