package mcp

import (
	"context"
	"strings"
)

// copilotSessionIDCtxKey carries the optional Copilot session id (from the
// X-Copilot-Session-Id request header) so the tool-invocation auditor can stamp
// it on each emitted event and the submit handler can label spawned jobs with
// it. Package-private key; use the helpers below.
type copilotSessionIDCtxKey struct{}

// WithCopilotSessionID attaches a Copilot session id to ctx. A blank id is a
// no-op so callers can pass it unconditionally.
func WithCopilotSessionID(ctx context.Context, id string) context.Context {
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, copilotSessionIDCtxKey{}, strings.TrimSpace(id))
}

// CopilotSessionIDFromContext returns the Copilot session id, or "" when none
// was set.
func CopilotSessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(copilotSessionIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}
