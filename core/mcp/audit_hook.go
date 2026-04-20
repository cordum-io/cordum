package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cordum/cordum/core/audit"
)

// Audit hook for the MCP tool surface (task-466b6a6a).
//
// Every tools/call invocation lands on the audit chain as a
// `mcp.tool_called` SIEMEvent carrying tool_name, agent_id, tenant,
// duration_ms, and result_size. The ToolRegistry already has hooks for
// denied tool calls via DenyAuditor; this file adds the success-path
// counterpart, gated behind a lightweight functional-options wrapper so
// tests and stdio deploys can leave it off without touching the call
// site.
//
// Performance: the hook sits between decode and handler execution.
// The only work on the hot path is time.Now() (twice) and the audit
// hook send — which itself is nil-checked. With the hook disabled the
// per-call overhead is one bool branch, well under the 5% ceiling.

// ToolCallAuditHook receives one SIEMEvent per completed tools/call.
// Implementations send the event to the gateway's audit chainer.
// A nil hook means "no audit" — the wrapper short-circuits without
// allocating.
type ToolCallAuditHook func(audit.SIEMEvent)

// ToolCallAuditConfig captures what the hook knows about the caller
// before the handler runs. Fields are read from request-scoped context
// values set by the transport middleware (see AgentIdentityFromContext).
type ToolCallAuditConfig struct {
	Hook ToolCallAuditHook
}

// WithToolCallAudit attaches the hook to the registry so every
// successful tools/call emits a SIEMEvent. Returns the registry for
// fluent configuration. Passing a nil hook leaves the registry as-is.
func (r *ToolRegistry) WithToolCallAudit(hook ToolCallAuditHook) *ToolRegistry {
	if r == nil || hook == nil {
		return r
	}
	r.auditHook = hook
	return r
}

// emitToolCallAudit is invoked by Registry.Call after a handler returns
// successfully. Never called on denied or handler-error paths — those
// have their own emission sites (DenyAuditor / the -32099 approval
// required branch).
func (r *ToolRegistry) emitToolCallAudit(ctx context.Context, tool Tool, started time.Time, result *ToolCallResult) {
	if r == nil || r.auditHook == nil {
		return
	}
	identity := IdentityFromContext(ctx)
	agentID := ""
	if identity != nil {
		agentID = identity.ID
	}
	tenant := TenantFromContext(ctx)
	event := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: EventMCPToolCalled,
		Severity:  audit.SeverityInfo,
		TenantID:  tenant,
		AgentID:   agentID,
		Action:    "call",
		Extra: map[string]string{
			"tool_name":   tool.Name,
			"agent_id":    agentID,
			"tenant":      tenant,
			"duration_ms": itoaDuration(time.Since(started)),
			"result_size": itoaResultSize(result),
		},
	}
	r.auditHook(event)
}

// EventMCPToolCalled is the canonical SIEMEvent type for every
// successful tools/call. Matches the naming of the existing
// audit.EventMCPToolApproval / EventMCPToolDenied constants.
const EventMCPToolCalled = "mcp.tool_called"

func itoaDuration(d time.Duration) string {
	return itoa(int64(d / time.Millisecond))
}

func itoaResultSize(result *ToolCallResult) string {
	if result == nil {
		return "0"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "0"
	}
	return itoa(int64(len(data)))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	buf := make([]byte, 0, 20)
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// TenantFromContext is a small helper exposed so the audit hook sees
// whatever tenant the transport stashed in ctx. Falls back to empty.
func TenantFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(tenantCtxKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// WithTenant attaches tenant to ctx. Exported so gateway middleware
// can consistently propagate it for audit-hook emission.
func WithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, tenant)
}

type tenantCtxKey struct{}
