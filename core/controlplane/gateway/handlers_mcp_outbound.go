package gateway

// MCP outbound call log endpoint.
//
// `GET /api/v1/mcp/outbound` walks the tenant's audit chain stream
// for `mcp.tool_outbound_invocation` events (emitted by the signed
// outbound MCP path in core/mcp/outbound) and returns a paginated,
// filterable feed for the dashboard's MCP governance page.
//
// Pagination: cursor-based via Redis Stream IDs. The caller passes
// `?cursor=<id>` to continue past the previous page; the response
// returns `next_cursor` set to the last entry ID seen (empty when the
// page exhausts the range).

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

const (
	// mcpOutboundDefaultLookback matches the heatmap default — operators
	// see "the last week" by default and adjust from there.
	mcpOutboundDefaultLookback = 7 * 24 * time.Hour

	// mcpOutboundDefaultPageSize is the default page size; capped at
	// mcpOutboundMaxPageSize so a misbehaving caller can't request a
	// 100k row payload in one shot.
	mcpOutboundDefaultPageSize = int64(100)
	mcpOutboundMaxPageSize     = int64(1000)
)

// MCPOutboundEntry is a single row in the outbound call log.
type MCPOutboundEntry struct {
	TimestampMs     int64  `json:"ts_ms"`
	StreamID        string `json:"stream_id"`
	AgentID         string `json:"agent_id"`
	ToolName        string `json:"tool_name"`
	TargetServer    string `json:"target_server"`
	SignatureStatus string `json:"signature_status"`
	SignatureKeyID  string `json:"signature_key_id,omitempty"`
	LatencyMs       int    `json:"latency_ms,omitempty"`
	ResultType      string `json:"result_type,omitempty"`
	EventHash       string `json:"event_hash,omitempty"`
}

// MCPOutboundResponse is the paginated outbound log payload.
type MCPOutboundResponse struct {
	Entries        []MCPOutboundEntry `json:"entries"`
	NextCursor     string             `json:"next_cursor,omitempty"`
	TruncatedAtMax bool               `json:"truncated_at_max"`
}

// handleMCPOutbound implements GET /api/v1/mcp/outbound.
//
// Query parameters:
//
//	since      (optional) — unix ms inclusive lower bound (default now-7d)
//	until      (optional) — unix ms inclusive upper bound (default now)
//	agent      (optional) — filter to one agent_id
//	server     (optional) — filter to one Extra.target_server
//	sig_status (optional) — verified | unverified | invalid | all (default all)
//	cursor     (optional) — Redis Stream ID to resume from (exclusive)
//	limit      (optional) — page size, default 100, max 1000
//
// Response: MCPOutboundResponse. Admin-gated + tenant-scoped.
func (s *server) handleMCPOutbound(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermMCPRead, []string{"admin"}, client) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	q := r.URL.Query()
	now := time.Now().UTC()
	since, until, herr := parseMCPRange(q, now, mcpOutboundDefaultLookback)
	if herr != nil {
		writeErrorJSON(w, herr.status, herr.message)
		return
	}
	agentFilter := strings.TrimSpace(q.Get("agent"))
	serverFilter := strings.TrimSpace(q.Get("server"))
	sigFilter := normaliseSigFilter(q.Get("sig_status"))
	if sigFilter == "invalid_value" {
		writeErrorJSON(w, http.StatusBadRequest, "sig_status must be one of verified|unverified|invalid|all")
		return
	}

	pageSize := mcpOutboundDefaultPageSize
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || v <= 0 {
			writeErrorJSON(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		if v > mcpOutboundMaxPageSize {
			v = mcpOutboundMaxPageSize
		}
		pageSize = v
	}

	cursor := strings.TrimSpace(q.Get("cursor"))

	chainer := audit.NewChainer(client, "")
	streamKey := chainer.StreamKey(tenant)

	entries, nextCursor, truncated, err := scanOutbound(r.Context(), client, streamKey, since, until, cursor, pageSize, agentFilter, serverFilter, sigFilter)
	if err != nil {
		writeInternalError(w, r, "mcp outbound: walk audit stream", err)
		return
	}
	writeJSON(w, MCPOutboundResponse{
		Entries:        entries,
		NextCursor:     nextCursor,
		TruncatedAtMax: truncated,
	})
}

// scanOutbound pages through the tenant audit stream collecting
// outbound MCP events that match the filter set. Reads in fixed-size
// chunks until either pageSize entries are accumulated or the range
// is exhausted. Returns (entries, next_cursor, truncated_at_max).
//
// truncated_at_max = true means the per-request walk budget was
// reached before the source range was exhausted; UI should warn that
// counts may be partial.
func scanOutbound(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	from, to time.Time,
	cursor string,
	pageSize int64,
	agentFilter, serverFilter, sigFilter string,
) ([]MCPOutboundEntry, string, bool, error) {
	minID := strconv.FormatInt(from.UnixMilli(), 10) + "-0"
	if cursor != "" {
		// Exclusive resume: prefix "(" tells Redis to skip the entry
		// at the cursor and continue with the next one.
		minID = "(" + cursor
	}
	maxID := strconv.FormatInt(to.UnixMilli(), 10) + "-18446744073709551615"
	out := make([]MCPOutboundEntry, 0, mcpOutboundMaxPageSize)
	const chunk = int64(500)
	const walkBudget = int64(50_000)
	scanned := int64(0)
	cur := minID
	for int64(len(out)) < pageSize {
		if err := ctx.Err(); err != nil {
			return nil, "", false, err
		}
		entries, err := client.XRangeN(ctx, streamKey, cur, maxID, chunk).Result()
		if err != nil {
			return nil, "", false, err
		}
		if len(entries) == 0 {
			return out, "", false, nil
		}
		for _, entry := range entries {
			scanned++
			if scanned > walkBudget {
				return out, entry.ID, true, nil
			}
			payload, ok := entry.Values[chainStreamEventField].(string)
			if !ok {
				continue
			}
			var ev audit.SIEMEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			if ev.EventType != audit.EventMCPToolOutboundInvocation {
				continue
			}
			row, ok := buildOutboundEntry(ev, entry.ID)
			if !ok {
				continue
			}
			if agentFilter != "" && row.AgentID != agentFilter {
				continue
			}
			if serverFilter != "" && row.TargetServer != serverFilter {
				continue
			}
			if sigFilter != "" && sigFilter != "all" && row.SignatureStatus != sigFilter {
				continue
			}
			out = append(out, row)
			if int64(len(out)) >= pageSize {
				return out, entry.ID, false, nil
			}
		}
		// Advance cursor exclusively past the last entry seen.
		cur = "(" + entries[len(entries)-1].ID
		if int64(len(entries)) < chunk {
			return out, "", false, nil
		}
	}
	return out, "", false, nil
}

// buildOutboundEntry projects a SIEMEvent into the wire row. Returns
// (entry, ok=true) on success; ok=false for events missing the
// minimum (target_server) — those would render as half-broken rows in
// the UI.
func buildOutboundEntry(ev audit.SIEMEvent, streamID string) (MCPOutboundEntry, bool) {
	server := strings.TrimSpace(extraField(ev, "target_server", "server", "server_id"))
	if server == "" {
		return MCPOutboundEntry{}, false
	}
	tool := strings.TrimSpace(extraField(ev, "tool_name", "tool"))
	agent := strings.TrimSpace(ev.AgentID)
	if agent == "" {
		agent = strings.TrimSpace(extraField(ev, "agent_id"))
	}
	row := MCPOutboundEntry{
		TimestampMs:     ev.Timestamp.UnixMilli(),
		StreamID:        streamID,
		AgentID:         agent,
		ToolName:        tool,
		TargetServer:    server,
		SignatureStatus: normaliseSigStatus(extraField(ev, "sig_status", "signature_status")),
		SignatureKeyID:  strings.TrimSpace(extraField(ev, "key_id", "signature_key_id")),
		ResultType:      strings.TrimSpace(extraField(ev, "result_type")),
		EventHash:       strings.TrimSpace(ev.EventHash),
	}
	if raw := extraField(ev, "latency_ms"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			row.LatencyMs = v
		}
	}
	return row, true
}

// normaliseSigFilter accepts the API-layer ?sig_status= value and maps
// it to the canonical wire form. "" is treated as "all". Unknown values
// surface as "invalid_value" so the caller can return 400 explicitly.
func normaliseSigFilter(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "all":
		return "all"
	case "verified", "unverified", "invalid":
		return v
	}
	return "invalid_value"
}

// normaliseSigStatus maps the SIEMEvent's Extra.sig_status value (free
// form, may be missing) to the dashboard's tri-state. Anything not
// "verified" or "invalid" collapses to "unverified" so the UI never
// sees a missing-status row.
func normaliseSigStatus(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "verified":
		return "verified"
	case "invalid":
		return "invalid"
	}
	return "unverified"
}
