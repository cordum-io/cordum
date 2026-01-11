package gateway

import (
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
