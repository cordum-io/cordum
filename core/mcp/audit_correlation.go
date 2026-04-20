package mcp

import "context"

// approvalIDCtxKey carries the approval_id across the call once an
// approval has been consumed. The gateway's approval gate sets this
// on ctx before dispatching so the invocation auditor stamps
// Extra.approval_id for downstream SIEM correlation with
// mcp.tool_approval events.
type approvalIDCtxKey struct{}

// WithApprovalID attaches an approval_id to ctx. Callers pass empty
// id for the no-approval case; the auditor then omits the field.
func WithApprovalID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, approvalIDCtxKey{}, id)
}

// ApprovalIDFromContext retrieves the approval_id set by WithApprovalID,
// or empty when absent.
func ApprovalIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(approvalIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}
