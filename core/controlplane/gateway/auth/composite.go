package auth

import (
	"context"
	"errors"
	"net/http"
)

// CompositeAuthProvider tries multiple AuthProvider implementations in order.
// Authentication succeeds if ANY provider accepts the request. Role checks,
// tenant resolution, and principal resolution delegate to the primary provider
// (first in the list) since they operate on the AuthContext already stored in
// the request context.
type CompositeAuthProvider struct {
	providers []AuthProvider
	primary   AuthProvider // first provider — used for non-auth methods
}

// NewCompositeAuthProvider creates a composite that tries providers in order.
// At least one provider is required.
func NewCompositeAuthProvider(providers ...AuthProvider) (*CompositeAuthProvider, error) {
	if len(providers) == 0 {
		return nil, errors.New("composite auth: at least one provider required")
	}
	return &CompositeAuthProvider{
		providers: providers,
		primary:   providers[0],
	}, nil
}

// AuthenticateHTTP tries each provider in order — returns the first success.
func (c *CompositeAuthProvider) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	var lastErr error
	for _, p := range c.providers {
		authCtx, err := p.AuthenticateHTTP(r)
		if err == nil {
			return authCtx, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// AuthenticateGRPC tries each provider in order — returns the first success.
func (c *CompositeAuthProvider) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	var lastErr error
	for _, p := range c.providers {
		authCtx, err := p.AuthenticateGRPC(ctx)
		if err == nil {
			return authCtx, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// RequireRole delegates to the primary provider.
func (c *CompositeAuthProvider) RequireRole(r *http.Request, roles ...string) error {
	return c.primary.RequireRole(r, roles...)
}

// ResolveTenant delegates to the primary provider.
func (c *CompositeAuthProvider) ResolveTenant(r *http.Request, requested, fallback string) (string, error) {
	return c.primary.ResolveTenant(r, requested, fallback)
}

// RequireTenantAccess delegates to the primary provider.
func (c *CompositeAuthProvider) RequireTenantAccess(r *http.Request, tenant string) error {
	return c.primary.RequireTenantAccess(r, tenant)
}

// ResolvePrincipal delegates to the primary provider.
func (c *CompositeAuthProvider) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	return c.primary.ResolvePrincipal(r, requested)
}

// IsPublicPath delegates to any provider that implements PublicPathProvider.
func (c *CompositeAuthProvider) IsPublicPath(path string) bool {
	for _, p := range c.providers {
		if pp, ok := p.(PublicPathProvider); ok && pp.IsPublicPath(path) {
			return true
		}
	}
	return false
}

// AuthConfig delegates to the first provider that implements AuthConfigProvider.
func (c *CompositeAuthProvider) AuthConfig() AuthConfig {
	for _, p := range c.providers {
		if acp, ok := p.(AuthConfigProvider); ok {
			cfg := acp.AuthConfig()
			// Enrich with OIDC info if any provider is OIDC
			for _, op := range c.providers {
				if oidc, ok := op.(*OIDCAuthAdapter); ok {
					cfg.OIDCEnabled = true
					cfg.OIDCIssuer = oidc.provider.cfg.IssuerURL
					break
				}
			}
			return cfg
		}
	}
	return AuthConfig{}
}

// RegisterRoutes delegates to any provider that implements RouteRegistrar.
func (c *CompositeAuthProvider) RegisterRoutes(mux *http.ServeMux, wrap func(string, http.HandlerFunc) http.HandlerFunc) {
	for _, p := range c.providers {
		if rr, ok := p.(RouteRegistrar); ok {
			rr.RegisterRoutes(mux, wrap)
		}
	}
}
