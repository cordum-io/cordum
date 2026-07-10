package gateway

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/copilot"
)

// copilotIngestSender decorates an audit.AuditSender: it taps inbound MCP
// tool-invocation events that carry a copilot_session_id and appends a
// transcript message to the Copilot session store, then always forwards the
// event to the inner sender. Ingestion is best-effort — a store failure is
// logged and never affects the audit Merkle chain or the tool call.
type copilotIngestSender struct {
	inner audit.AuditSender
	store *copilot.RedisStore
}

func newCopilotIngestSender(inner audit.AuditSender, store *copilot.RedisStore) audit.AuditSender {
	if inner == nil || store == nil {
		return inner
	}
	return &copilotIngestSender{inner: inner, store: store}
}

func (c *copilotIngestSender) Send(event audit.SIEMEvent) {
	c.ingest(event)
	c.inner.Send(event)
}

func (c *copilotIngestSender) Close() error { return c.inner.Close() }

func (c *copilotIngestSender) ingest(event audit.SIEMEvent) {
	defer func() {
		// The store path must never crash the audit emit.
		if r := recover(); r != nil {
			slog.Warn("copilot ingest: recovered panic", "panic", r)
		}
	}()
	if event.EventType != audit.EventMCPToolInvocation {
		return // only inbound, Copilot-driven invocations form the transcript
	}
	sessionID := strings.TrimSpace(event.Extra["copilot_session_id"])
	if sessionID == "" {
		return
	}
	// The session id is client-supplied (X-Copilot-Session-Id, see
	// handlers_mcp.go). Enforce the same format the read path
	// (handleGetCopilotSession) requires so this write path can't be used to
	// smuggle oversized or otherwise malformed keys into the store — and so
	// a session written here is always addressable by the read path's regex.
	if !copilotSessionIDPattern.MatchString(sessionID) {
		slog.Warn("copilot ingest: rejected malformed session id",
			"tenant", strings.TrimSpace(event.TenantID),
			"session_id_preview", truncateContent(sessionID, 32),
			"session_id_len", len(sessionID))
		return
	}
	tenant := strings.TrimSpace(event.TenantID)
	if tenant == "" {
		return
	}
	msg := copilot.CopilotMessage{
		Role:      "tool",
		Content:   copilotMessageContent(event),
		Timestamp: event.Timestamp,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// agentID doubles as the session owner: the store binds the first
	// AppendMessage for a session id to this principal and rejects later
	// appends from a different one (copilot.ErrOwnerMismatch). That is what
	// stops a different agent from setting X-Copilot-Session-Id to another
	// user's (guessed) session id and injecting fabricated transcript
	// entries into it.
	agentID := strings.TrimSpace(event.AgentID)
	if err := c.store.AppendMessage(ctx, tenant, agentID, sessionID, msg); err != nil {
		if errors.Is(err, copilot.ErrOwnerMismatch) {
			slog.Warn("copilot ingest: rejected cross-principal append",
				"session_id", sessionID, "tenant", tenant, "agent_id", copilotLogPrincipal(agentID))
			return
		}
		slog.Warn("copilot ingest: append message failed",
			"session_id", sessionID, "tenant", tenant, "error", err)
	}
}

// copilotMessageContent renders a compact, already-redacted summary of the tool
// call for the transcript. args_redacted is produced by the invocation
// auditor's policy redactor, so no raw secrets are stored here.
func copilotMessageContent(event audit.SIEMEvent) string {
	var b strings.Builder
	b.WriteString("tool=")
	b.WriteString(orDash(event.Extra["tool_name"]))
	if rt := event.Extra["result_type"]; rt != "" {
		b.WriteString(" result=")
		b.WriteString(rt)
	}
	if d := strings.TrimSpace(event.Decision); d != "" {
		b.WriteString(" decision=")
		b.WriteString(d)
	}
	if as := event.Extra["approval_status"]; as != "" && as != "none" {
		b.WriteString(" approval=")
		b.WriteString(as)
	}
	if args := event.Extra["args_redacted"]; args != "" {
		b.WriteString(" args=")
		b.WriteString(truncateContent(args, 2000))
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func truncateContent(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}
