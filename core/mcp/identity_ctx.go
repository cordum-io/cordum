package mcp

import "context"

// identityContextKey is an unexported key type for stashing an
// *AgentIdentity on a context.Context. Using an unexported type
// prevents collision with keys defined in other packages.
type identityContextKey struct{}

// ContextWithIdentity returns a copy of ctx that carries id. A nil id
// is stored as-is so downstream IdentityFromContext reports no identity
// rather than synthesising one.
func ContextWithIdentity(ctx context.Context, id *AgentIdentity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, identityContextKey{}, id)
}

// IdentityFromContext retrieves the *AgentIdentity stored via
// ContextWithIdentity. Returns nil when absent, which the scope filter
// treats as fail-closed (no tools visible, no calls permitted).
func IdentityFromContext(ctx context.Context) *AgentIdentity {
	if ctx == nil {
		return nil
	}
	val, _ := ctx.Value(identityContextKey{}).(*AgentIdentity)
	return val
}
