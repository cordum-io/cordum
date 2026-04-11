package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/licensing"
)

// PermissionChecker resolves whether a role has a given permission.
// It wraps the RBACStore and entitlement state so that callers don't need
// to thread those dependencies through every handler.
type PermissionChecker struct {
	store        *RBACStore
	entitlements func() licensing.Entitlements
}

// NewPermissionChecker creates a PermissionChecker. The entitlements function
// is called on each check to get the current entitlement state (licenses can
// be reloaded at runtime).
func NewPermissionChecker(store *RBACStore, entitlementsFn func() licensing.Entitlements) *PermissionChecker {
	return &PermissionChecker{
		store:        store,
		entitlements: entitlementsFn,
	}
}

// RequirePermission returns an error if the request's authenticated role does
// not hold the required permission. When the RBAC entitlement is disabled,
// the basic role mapping is used (admin=all, operator=read+write, viewer=read).
func (pc *PermissionChecker) RequirePermission(r *http.Request, permission string) error {
	if pc == nil {
		return nil
	}
	auth := FromRequest(r)
	if auth == nil {
		return fmt.Errorf("authentication required")
	}
	role := strings.ToLower(strings.TrimSpace(auth.Role))
	if role == "" {
		return fmt.Errorf("role required")
	}
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return nil
	}

	entitled := false
	if pc.entitlements != nil {
		entitled = RBACEntitled(pc.entitlements())
	}

	if HasPermission(r.Context(), pc.store, role, permission, entitled) {
		return nil
	}
	return fmt.Errorf("permission %s denied for role %s", permission, role)
}

// RequirePermissionGRPC checks permission for a gRPC context.
func (pc *PermissionChecker) RequirePermissionGRPC(ctx context.Context, role, permission string) error {
	if pc == nil {
		return nil
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return fmt.Errorf("role required")
	}
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return nil
	}

	entitled := false
	if pc.entitlements != nil {
		entitled = RBACEntitled(pc.entitlements())
	}

	if HasPermission(ctx, pc.store, role, permission, entitled) {
		return nil
	}
	return fmt.Errorf("permission %s denied for role %s", permission, role)
}

// RequirePermissionMiddleware returns an HTTP middleware that enforces the
// given permission before calling the next handler. This is useful for
// route-level permission declarations.
func (pc *PermissionChecker) RequirePermissionMiddleware(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := pc.RequirePermission(r, permission); err != nil {
				http.Error(w, `{"error":"forbidden","message":"`+permission+` required"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CheckRole verifies a role string against the required roles using the
// current RBAC state. This is the RBAC-aware replacement for basic role
// string matching. When RBAC is not entitled, it falls back to the legacy
// behavior where the role name must match one of the allowed role names.
func (pc *PermissionChecker) CheckRole(r *http.Request, roles ...string) error {
	if pc == nil {
		return nil
	}
	auth := FromRequest(r)
	if auth == nil {
		return fmt.Errorf("authentication required")
	}
	role := strings.ToLower(strings.TrimSpace(auth.Role))
	if role == "" {
		return fmt.Errorf("role required")
	}

	entitled := false
	if pc.entitlements != nil {
		entitled = RBACEntitled(pc.entitlements())
	}

	// When RBAC is not entitled, use basic role name matching (existing behavior).
	if !entitled || pc.store == nil {
		for _, allowed := range roles {
			if NormalizeRole(allowed) == role {
				return nil
			}
		}
		return fmt.Errorf("role %s not permitted", role)
	}

	// When RBAC is entitled, check if the user's role has permissions that
	// encompass any of the required roles. For backward compatibility, if the
	// required roles match by name, that's also accepted.
	for _, allowed := range roles {
		if NormalizeRole(allowed) == role {
			return nil
		}
	}

	// Check if the user's role inherits from any of the required roles
	perms, err := pc.store.ResolvePermissions(r.Context(), role)
	if err != nil {
		// Fall back to name matching on resolution failure
		return fmt.Errorf("role %s not permitted", role)
	}

	// Admin-level permissions grant access to any role check
	if matchPermission(perms, PermAdminAll) {
		return nil
	}

	return fmt.Errorf("role %s not permitted", role)
}
