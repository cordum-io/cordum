package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
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
	cfg := AuthConfig{}
	found := false
	for _, p := range c.providers {
		if acp, ok := p.(AuthConfigProvider); ok {
			cfg = mergeAuthConfig(cfg, acp.AuthConfig())
			found = true
		}
	}
	if !found {
		return AuthConfig{}
	}
	return cfg
}

// RegisterRoutes delegates to any provider that implements RouteRegistrar.
func (c *CompositeAuthProvider) RegisterRoutes(mux *http.ServeMux, wrap func(string, http.HandlerFunc) http.HandlerFunc) {
	for _, p := range c.providers {
		if rr, ok := p.(RouteRegistrar); ok {
			rr.RegisterRoutes(mux, wrap)
		}
	}
}

func mergeAuthConfig(current, next AuthConfig) AuthConfig {
	current.PasswordEnabled = current.PasswordEnabled || next.PasswordEnabled
	current.UserAuthEnabled = current.UserAuthEnabled || next.UserAuthEnabled
	current.SAMLEnabled = current.SAMLEnabled || next.SAMLEnabled
	current.SAMLEnterprise = current.SAMLEnterprise || next.SAMLEnterprise
	current.RequireRBAC = current.RequireRBAC || next.RequireRBAC
	current.RequirePrincipal = current.RequirePrincipal || next.RequirePrincipal
	current.OIDCEnabled = current.OIDCEnabled || next.OIDCEnabled

	if strings.TrimSpace(next.SAMLLoginURL) != "" {
		current.SAMLLoginURL = next.SAMLLoginURL
	}
	if strings.TrimSpace(next.SAMLMetadataURL) != "" {
		current.SAMLMetadataURL = next.SAMLMetadataURL
	}
	if strings.TrimSpace(next.SessionTTL) != "" && current.SessionTTL == "" {
		current.SessionTTL = next.SessionTTL
	}
	if strings.TrimSpace(next.DefaultTenant) != "" && current.DefaultTenant == "" {
		current.DefaultTenant = next.DefaultTenant
	}
	if strings.TrimSpace(next.OIDCIssuer) != "" {
		current.OIDCIssuer = next.OIDCIssuer
	}
	if strings.TrimSpace(next.OIDCLoginURL) != "" {
		current.OIDCLoginURL = next.OIDCLoginURL
	}
	if strings.TrimSpace(next.OIDCClientID) != "" {
		current.OIDCClientID = next.OIDCClientID
	}
	if strings.TrimSpace(next.OIDCRedirectURI) != "" {
		current.OIDCRedirectURI = next.OIDCRedirectURI
	}
	if len(next.OIDCScopes) > 0 {
		current.OIDCScopes = append([]string(nil), next.OIDCScopes...)
	}
	if strings.TrimSpace(next.OIDCGroupsClaim) != "" {
		current.OIDCGroupsClaim = next.OIDCGroupsClaim
	}
	if len(next.OIDCGroupRoleMapping) > 0 {
		current.OIDCGroupRoleMapping = cloneStringMap(next.OIDCGroupRoleMapping)
	}
	if strings.TrimSpace(next.OIDCClientSecretMasked) != "" {
		current.OIDCClientSecretMasked = next.OIDCClientSecretMasked
	}
	return current
}

// UpdateOIDCGroupRoleMapping delegates to the first OIDC-capable provider and
// then returns the merged sanitized config for the whole auth chain.
func (c *CompositeAuthProvider) UpdateOIDCGroupRoleMapping(groupsClaim string, mapping map[string]string) (AuthConfig, error) {
	for _, p := range c.providers {
		updater, ok := p.(OIDCGroupRoleMappingUpdater)
		if !ok {
			continue
		}
		if _, err := updater.UpdateOIDCGroupRoleMapping(groupsClaim, mapping); err != nil {
			return AuthConfig{}, err
		}
		return c.AuthConfig(), nil
	}
	return AuthConfig{}, errors.New("oidc group-role mapping updater unavailable")
}

// BasicProvider returns the first BasicAuthProvider in the composite, or nil.
func (c *CompositeAuthProvider) BasicProvider() *BasicAuthProvider {
	for _, p := range c.providers {
		if bp, ok := p.(*BasicAuthProvider); ok {
			return bp
		}
	}
	return nil
}

// UserStore returns the UserStore from the first BasicAuthProvider, or nil.
func (c *CompositeAuthProvider) UserStore() UserStore {
	if bp := c.BasicProvider(); bp != nil {
		return bp.UserStore()
	}
	return nil
}
