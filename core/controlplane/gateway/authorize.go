package gateway

import (
	"errors"
	"net/http"
	"strings"
)

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
