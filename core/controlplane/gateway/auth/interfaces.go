package auth

import (
	"context"
	"net/http"
)

// AuthProvider injects auth context and enforces access control.
type AuthProvider interface {
	AuthenticateHTTP(r *http.Request) (*AuthContext, error)
	AuthenticateGRPC(ctx context.Context) (*AuthContext, error)
	RequireRole(r *http.Request, roles ...string) error
	ResolveTenant(r *http.Request, requested, fallback string) (string, error)
	RequireTenantAccess(r *http.Request, tenant string) error
	ResolvePrincipal(r *http.Request, requested string) (string, error)
}

// UserStoreProvider is implemented by auth providers that hold a UserStore.
type UserStoreProvider interface {
	UserStore() UserStore
}

// RouteRegistrar allows auth providers to attach additional HTTP routes.
type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux, wrap func(route string, fn http.HandlerFunc) http.HandlerFunc)
}

// AuthConfigProvider allows auth providers to supply UI auth configuration.
type AuthConfigProvider interface {
	AuthConfig() AuthConfig
}

// PublicPathProvider allows auth providers to skip auth for specific paths.
type PublicPathProvider interface {
	IsPublicPath(path string) bool
}
