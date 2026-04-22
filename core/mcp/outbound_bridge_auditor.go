package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ToolInvocationOutboundAuditor adapts a ToolInvocationAuditor into the
// bridge-level OutboundInvocationAuditor contract so the stdio
// cordum-mcp process can wrap every HTTPServiceBridge /
// HTTPDataBridge request in a terminal mcp.tool_outbound_invocation
// SIEMEvent.
//
// The adapter is the bridge between the narrow bridge callback surface
// (method, path, body bytes) and the richer invocation-audit payload
// the SIEM consumer expects (agent_id, tenant, tool_name, args_redacted,
// result_summary, latency_ms). It synthesises a "tool name" from the
// HTTP verb + path so the event has a stable grouping key; the bridge
// URL never includes secrets so path itself is safe to stamp.
type ToolInvocationOutboundAuditor struct {
	auditor  ToolInvocationAuditor
	agentID  string
	tenantID string
	serverID string
}

// NewToolInvocationOutboundAuditor binds the invocation auditor to a
// specific outbound identity. agentID/tenantID populate the SIEM event;
// serverID identifies the remote endpoint family (defaults to the
// bridge's baseURL host — callers pass that in).
func NewToolInvocationOutboundAuditor(auditor ToolInvocationAuditor, agentID, tenantID, serverID string) *ToolInvocationOutboundAuditor {
	return &ToolInvocationOutboundAuditor{
		auditor:  auditor,
		agentID:  strings.TrimSpace(agentID),
		tenantID: strings.TrimSpace(tenantID),
		serverID: strings.TrimSpace(serverID),
	}
}

// outboundRequestHandle is the concrete per-request token the adapter
// hands back through the bridge's OutboundRequestHandle alias. Holds
// the InvocationHandle the auditor built at Start time so Finish can
// feed it back in with the terminal result.
type outboundRequestHandle struct {
	inner     *InvocationHandle
	startedAt time.Time
}

// StartRequest is called by the bridge BEFORE client.Do. The method +
// path become the audit event's tool_name (e.g. "GET /api/v1/jobs/abc")
// and the body bytes become the redactable args payload.
func (a *ToolInvocationOutboundAuditor) StartRequest(ctx context.Context, method, path string, body []byte) OutboundRequestHandle {
	if a == nil || a.auditor == nil {
		return nil
	}
	toolName := strings.TrimSpace(method) + " " + strings.TrimSpace(path)
	// Promote the body bytes into the auditor's argsRaw slot. The body
	// is already-marshalled JSON for every gateway API call (content
	// type is application/json) so passing it as json.RawMessage works
	// even for GET requests that have no body (argsRaw is nil-safe).
	var argsRaw json.RawMessage
	if len(body) > 0 {
		argsRaw = append(json.RawMessage(nil), body...)
	}
	_, handle := a.auditor.StartOutbound(ctx, a.agentID, a.tenantID, a.serverID, toolName, argsRaw)
	return &outboundRequestHandle{inner: handle, startedAt: time.Now()}
}

// FinishRequest is called AFTER client.Do returns (or fails). The
// terminal status + response body + error drive the SIEM event's
// result_type, result_hash, and error_code fields. Response body goes
// through the redactor on its way out so an embedded secret from a
// misbehaving gateway doesn't leak into the audit record.
func (a *ToolInvocationOutboundAuditor) FinishRequest(h OutboundRequestHandle, statusCode int, responseBody []byte, err error) {
	if a == nil || a.auditor == nil {
		return
	}
	outboundHandle, ok := h.(*outboundRequestHandle)
	if !ok || outboundHandle == nil {
		return
	}
	var callErr error
	var result *ToolCallResult
	if err != nil {
		callErr = err
	} else {
		// Synthesise a ToolCallResult from the HTTP response so the
		// generic Finish path captures status + body_size + a content
		// block for result_hash. The raw body text is wrapped in a
		// text content block rather than set on Extra — the redactor
		// walks content blocks, so any secret that slipped through the
		// gateway is still scrubbed before landing on the SIEM chain.
		contents := []ContentItem{}
		if len(responseBody) > 0 {
			contents = append(contents, ContentItem{
				Type: "text",
				Text: string(responseBody),
			})
		}
		result = &ToolCallResult{
			Content: contents,
			IsError: statusCode >= 400,
		}
	}
	a.auditor.FinishOutbound(outboundHandle.inner, result, callErr)
}
