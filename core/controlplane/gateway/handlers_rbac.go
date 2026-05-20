package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

const (
	maxRolePermissions = 128
	maxRoleInherits    = 32
)

var assignableRBACPermissions = func() map[string]struct{} {
	allowed := make(map[string]struct{}, len(auth.AllPermissions))
	for _, permission := range auth.AllPermissions {
		if permission == auth.PermAdminAll {
			continue
		}
		allowed[permission] = struct{}{}
	}
	return allowed
}()

// handleListRoles returns all role definitions.
// GET /api/v1/auth/roles
func (s *server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesRead, "admin") {
		return
	}
	if !s.requireRBACEntitlement(w) {
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

	writeJSON(w, map[string]any{
		"roles":    roles,
		"entitled": true,
	})
}

// handleGetRole returns a single role definition with resolved permissions.
// GET /api/v1/auth/roles/{name}
func (s *server) handleGetRole(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesRead, "admin") {
		return
	}
	if !s.requireRBACEntitlement(w) {
		return
	}
	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name, ok := parseRolePathName(w, r.PathValue("name"))
	if !ok {
		return
	}

	role, err := s.rbacStore.GetRole(r.Context(), name)
	if err != nil {
		if errors.Is(err, auth.ErrRoleNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeRBACRoleNotFound, "role not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get role")
		return
	}

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
	if !s.requireRBACEntitlement(w) {
		return
	}
	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name, ok := parseRolePathName(w, r.PathValue("name"))
	if !ok {
		return
	}

	req, ok := decodeRoleRequest(w, r)
	if !ok {
		return
	}
	existing, exists, ok := s.existingRoleForPut(w, r, name)
	if !ok {
		return
	}
	if exists && existing.BuiltIn {
		handleBuiltInRoleUpdate(w, existing, req)
		return
	}
	saved, ok := s.saveRoleRequest(w, r, name, req, existing)
	if !ok {
		return
	}

	op := "create"
	if exists {
		op = "update"
	}
	s.emitRoleUpserted(r, saved, op)
	writeJSON(w, map[string]any{"role": saved})
}

func decodeRoleRequest(w http.ResponseWriter, r *http.Request) (roleRequest, bool) {
	var req roleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	if err := decoder.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeRBACRequestInvalid, "invalid request body")
		return roleRequest{}, false
	}
	if err := normalizeRoleRequest(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeRBACRequestInvalid, err.Error())
		return roleRequest{}, false
	}
	return req, true
}

func (s *server) existingRoleForPut(w http.ResponseWriter, r *http.Request, name string) (*auth.RoleDefinition, bool, bool) {
	existing, err := s.rbacStore.GetRole(r.Context(), name)
	if err == nil {
		return existing, true, true
	}
	if errors.Is(err, auth.ErrRoleNotFound) {
		return nil, false, true
	}
	writeErrorJSON(w, http.StatusInternalServerError, "failed to read role")
	return nil, false, false
}

func (s *server) saveRoleRequest(w http.ResponseWriter, r *http.Request, name string, req roleRequest, existing *auth.RoleDefinition) (*auth.RoleDefinition, bool) {
	if err := validateAssignablePermissions(req.Permissions); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeRBACPermissionInvalid, err.Error())
		return nil, false
	}
	role := &auth.RoleDefinition{
		Name:        name,
		Description: req.Description,
		Permissions: req.Permissions,
		Inherits:    req.Inherits,
	}
	if existing != nil {
		role.CreatedAt = existing.CreatedAt
	}
	if err := s.rbacStore.PutRole(r.Context(), role); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeRBACPermissionInvalid, err.Error())
		return nil, false
	}
	saved, err := s.rbacStore.GetRole(r.Context(), name)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to read saved role")
		return nil, false
	}
	return saved, true
}

// handleDeleteRole removes a custom role definition.
// DELETE /api/v1/auth/roles/{name}
func (s *server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermRolesWrite, "admin") {
		return
	}
	if !s.requireRBACEntitlement(w) {
		return
	}
	if s.rbacStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rbac store unavailable")
		return
	}

	name, ok := parseRolePathName(w, r.PathValue("name"))
	if !ok {
		return
	}

	if err := s.rbacStore.DeleteRole(r.Context(), name); err != nil {
		var roleInUse *auth.RoleInUseError
		if errors.As(err, &roleInUse) {
			writeRBACRoleInUseError(w, roleInUse)
			return
		}
		if errors.Is(err, auth.ErrRoleNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeRBACRoleNotFound, "role not found")
			return
		}
		if errors.Is(err, auth.ErrBuiltInRole) {
			writeJSONError(w, http.StatusBadRequest, errorCodeRBACRoleInUse, "cannot delete built-in role")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete role")
		return
	}

	s.emitRoleDeleted(r, name)
	writeJSON(w, map[string]any{"deleted": true, "name": name})
}

func (s *server) requireRBACEntitlement(w http.ResponseWriter) bool {
	if auth.RBACEntitled(s.currentEntitlements()) {
		return true
	}
	writeTierFeatureJSON(w, "rbac", "advanced RBAC requires an Enterprise license")
	return false
}

func parseRolePathName(w http.ResponseWriter, raw string) (string, bool) {
	name := auth.NormalizeRoleName(raw)
	if err := auth.ValidateRoleName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeRBACRequestInvalid, err.Error())
		return "", false
	}
	return name, true
}

func normalizeRoleRequest(req *roleRequest) error {
	req.Permissions = trimStringSlice(req.Permissions)
	req.Inherits = normalizeRoleNames(req.Inherits)
	if len(req.Permissions) > maxRolePermissions {
		return fmt.Errorf("permissions exceeds maximum of %d", maxRolePermissions)
	}
	if len(req.Inherits) > maxRoleInherits {
		return fmt.Errorf("inherits exceeds maximum of %d", maxRoleInherits)
	}
	for _, parent := range req.Inherits {
		if err := auth.ValidateRoleName(parent); err != nil {
			return err
		}
	}
	return nil
}

func normalizeRoleNames(values []string) []string {
	if len(values) == 0 {
		return values
	}
	normalized := make([]string, len(values))
	for i, value := range values {
		normalized[i] = auth.NormalizeRoleName(value)
	}
	return normalized
}

func validateAssignablePermissions(permissions []string) error {
	for _, permission := range permissions {
		if _, ok := assignableRBACPermissions[permission]; !ok {
			return fmt.Errorf("invalid permission %q", permission)
		}
	}
	return nil
}

func handleBuiltInRoleUpdate(w http.ResponseWriter, existing *auth.RoleDefinition, req roleRequest) {
	if roleSpecChanged(existing, req) {
		writeJSONError(w, http.StatusConflict, errorCodeRBACRoleProtected, "cannot modify built-in role")
		return
	}
	writeJSON(w, map[string]any{"role": existing})
}

func roleSpecChanged(existing *auth.RoleDefinition, req roleRequest) bool {
	return existing.Description != req.Description ||
		!slicesEqual(existing.Permissions, req.Permissions) ||
		!slicesEqual(existing.Inherits, req.Inherits)
}

func writeRBACRoleInUseError(w http.ResponseWriter, err *auth.RoleInUseError) {
	refs := []string(nil)
	message := "role is inherited by another role"
	if err != nil {
		refs = append(refs, err.Referencing...)
		message = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":             errorCodeRBACRoleInUse,
		"code":              errorCodeRBACRoleInUse,
		"status":            http.StatusConflict,
		"message":           message,
		"referencing_roles": refs,
	})
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
