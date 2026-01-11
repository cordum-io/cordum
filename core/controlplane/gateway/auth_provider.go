package gateway

import (
	"context"
	"net/http"
)

// AuthContext captures request identity for auditing and tenant routing.
type AuthContext struct {
	APIKey           string
	Tenant           string
	PrincipalID      string
	Role             string
	AllowCrossTenant bool
}

type authContextKey struct{}

// AuthProvider injects auth context and enforces access control.
type AuthProvider interface {
	AuthenticateHTTP(r *http.Request) (*AuthContext, error)
	AuthenticateGRPC(ctx context.Context) (*AuthContext, error)
	RequireRole(r *http.Request, roles ...string) error
	ResolveTenant(r *http.Request, requested, fallback string) (string, error)
	RequireTenantAccess(r *http.Request, tenant string) error
	ResolvePrincipal(r *http.Request, requested string) (string, error)
}

func authFromContext(ctx context.Context) *AuthContext {
	if ctx == nil {
		return nil
	}
	if raw := ctx.Value(authContextKey{}); raw != nil {
		if auth, ok := raw.(*AuthContext); ok {
			return auth
		}
	}
	return nil
}

func authFromRequest(r *http.Request) *AuthContext {
	if r == nil {
		return nil
	}
	return authFromContext(r.Context())
}
