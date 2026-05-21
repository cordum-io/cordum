package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// mcpDenyAuditor implements mcp.DenyAuditor on top of the gateway's
// audit exporter. Every scope-filter rejection produces a SIEMEvent of
// type mcp.tool_denied so downstream SIEM rules can alert on
// privilege-escalation probes. The event is also mirrored into the
// gateway's in-memory deny ring so the dashboard's per-identity
// "recent denials" panel has a local source of truth.
type mcpDenyAuditor struct {
	sender audit.AuditSender
	tenant func(ctx context.Context) string
	record func(ev audit.SIEMEvent)
}

// newMCPDenyAuditor wires the registry's deny audit hook to the
// gateway's configured SIEM sender. Returns an auditor even when the
// SIEM sender is nil so the in-memory ring keeps getting populated
// (operators still see denials on the dashboard without SIEM export).
func (s *server) newMCPDenyAuditor() mcp.DenyAuditor {
	if s == nil {
		return nil
	}
	return &mcpDenyAuditor{
		sender: s.auditExporter,
		tenant: func(ctx context.Context) string { return s.mcpTenantFromContext(ctx) },
		record: s.recordDenyEvent,
	}
}

// ToolDenied forwards the scope-filter denial to the SIEM exporter.
// Severity is HIGH: a rejected call is either a misconfiguration worth
// investigating or an intentional probe, both of which operators want
// to see promptly. The Extra map carries the structured sub_reason so
// SIEM rules can filter by it without parsing the natural-language
// Reason field.
func (a *mcpDenyAuditor) ToolDenied(ctx context.Context, ev mcp.DenyEvent) {
	if a == nil {
		return
	}
	extra := map[string]string{
		"tool_name":  ev.ToolName,
		"sub_reason": string(ev.SubReason),
	}
	if strings.TrimSpace(ev.AgentID) != "" {
		extra["agent_id"] = ev.AgentID
	}
	tenant := ""
	if a.tenant != nil {
		tenant = a.tenant(ctx)
	}
	// Resolve via the canonical helper so a nil-lookup or a lookup that
	// returned "" (anonymous tool deny, dev deploy with no tenant
	// resolver wired) lands on the default tenant chain instead of
	// surfacing as a sink-level slog.Warn. task-3fad45d3.
	siem := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPToolDenied,
		Severity:  audit.SeverityHigh,
		TenantID:  model.ResolveTenantForAudit(tenant, ""),
		AgentID:   ev.AgentID,
		Action:    "deny",
		Decision:  "deny",
		Reason:    string(ev.SubReason),
		Extra:     extra,
	}
	if a.sender != nil {
		a.sender.Send(siem)
	}
	if a.record != nil {
		a.record(siem)
	}
}
