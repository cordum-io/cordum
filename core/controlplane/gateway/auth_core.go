package gateway

import (
	"errors"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// Server authorize helpers
// ---------------------------------------------------------------------------

func (s *server) requireRole(r *http.Request, roles ...string) error {
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireRole(r, roles...)
}

func (s *server) resolveTenant(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	headerTenant := headerValue(r, "X-Tenant-ID")
	// Fall back to auth context tenant (e.g. from session token)
	if headerTenant == "" {
		if authCtx := authFromRequest(r); authCtx != nil && authCtx.Tenant != "" {
			headerTenant = authCtx.Tenant
		}
	}
	if headerTenant == "" {
		return "", errors.New("tenant id required")
	}
	if requested == "" {
		requested = headerTenant
	} else if requested != headerTenant {
		return "", errors.New("tenant header mismatch")
	}
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolveTenant(r, requested, s.tenant)
}

func (s *server) requireTenantAccess(r *http.Request, tenant string) error {
	tenant = strings.TrimSpace(tenant)
	if s == nil || s.auth == nil {
		return nil
	}
	return s.auth.RequireTenantAccess(r, tenant)
}

func (s *server) resolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if s == nil || s.auth == nil {
		return requested, nil
	}
	return s.auth.ResolvePrincipal(r, requested)
}

// ---------------------------------------------------------------------------
// AuthConfig handler
// ---------------------------------------------------------------------------

func (s *server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	defaultTenant := strings.TrimSpace(s.tenant)
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	resp := AuthConfig{
		PasswordEnabled:  false,
		SAMLEnabled:      false,
		SessionTTL:       "0s",
		RequireRBAC:      false,
		RequirePrincipal: false,
		DefaultTenant:    defaultTenant,
	}
	if provider, ok := s.auth.(AuthConfigProvider); ok {
		resp = provider.AuthConfig()
	}
	if strings.TrimSpace(resp.DefaultTenant) == "" {
		resp.DefaultTenant = defaultTenant
	}
	if strings.TrimSpace(resp.SessionTTL) == "" {
		resp.SessionTTL = "0s"
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// writeErrorJSON and writeJSON are defined elsewhere in the gateway package.
// AuthConfig, AuthConfigProvider, authFromRequest, headerValue are available
// via auth_compat.go type aliases and function re-exports.
