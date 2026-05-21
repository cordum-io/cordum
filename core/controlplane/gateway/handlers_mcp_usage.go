package gateway

// MCP tool-usage aggregation endpoint.
//
// `GET /api/v1/mcp/usage` walks the tenant's audit chain and buckets
// MCP-related events into a per-(agent_id, tool_name) heatmap. Backs
// the dashboard's MCP governance page.
//
// Aggregation source: SIEMEvents emitted to the per-tenant audit
// stream by the MCP tool registry (mcp.tool_invocation,
// mcp.tool_denied, mcp.tool_approval). The handler does not look at
// raw MCP traffic — every emission flows through the same audit chain
// the verify endpoint attests, so heatmap counts and integrity
// reports stay reconcilable from a single source of truth.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

const (
	// mcpUsageMaxEvents bounds the walk per request. Matches
	// audit.MaxVerifyLimit so operators have one mental model for the
	// "how big a window can I scan" budget.
	mcpUsageMaxEvents = int64(100_000)

	// mcpUsageChunkSize bounds memory by capping each XRangeN page.
	mcpUsageChunkSize = int64(10_000)

	// mcpUsageDefaultLookback is the default since= when the caller
	// omits it. Seven days lines up with the default audit retention
	// window so the empty case is sensible.
	mcpUsageDefaultLookback = 7 * 24 * time.Hour

	// chainStreamEventField mirrors the unexported field used by
	// core/audit/chain.go to store the canonical event JSON. Held as a
	// constant here rather than via cross-package access so the audit
	// package's wire format stays a private contract.
	chainStreamEventField = "event"
)

// MCPUsageCell is a single bucket in the heatmap.
type MCPUsageCell struct {
	AgentID               string  `json:"agent_id"`
	ToolName              string  `json:"tool_name"`
	Count                 int     `json:"count"`
	AllowCount            int     `json:"allow_count"`
	DenyCount             int     `json:"deny_count"`
	ApprovalRequiredCount int     `json:"approval_required_count"`
	P50LatencyMs          float64 `json:"p50_latency_ms"`
	P99LatencyMs          float64 `json:"p99_latency_ms"`
	LastInvokedAtMs       int64   `json:"last_invoked_at_ms"`
}

// MCPUsageResponse is the heatmap payload.
type MCPUsageResponse struct {
	Cells          []MCPUsageCell `json:"cells"`
	TotalCalls     int            `json:"total_calls"`
	WindowMs       int64          `json:"window_ms"`
	TruncatedAtMax bool           `json:"truncated_at_max"`
}

// handleMCPUsage implements GET /api/v1/mcp/usage.
//
// Query parameters:
//
//	since (optional) — unix ms inclusive lower bound (defaults to now-7d)
//	until (optional) — unix ms inclusive upper bound (defaults to now)
//	agent (optional) — filter to one agent_id
//	tool  (optional) — filter to one tool_name
//
// Response: MCPUsageResponse. Admin-gated, tenant-scoped.
func (s *server) handleMCPUsage(w http.ResponseWriter, r *http.Request) {
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
	since, until, herr := parseMCPRange(q, now, mcpUsageDefaultLookback)
	if herr != nil {
		writeJSONError(w, herr.status, herr.code, herr.message)
		return
	}
	agentFilter := strings.TrimSpace(q.Get("agent"))
	toolFilter := strings.TrimSpace(q.Get("tool"))

	chainer := audit.NewChainer(client, "")
	streamKey := chainer.StreamKey(tenant)

	agg := newMCPUsageAggregator(agentFilter, toolFilter)
	truncated, err := walkMCPEvents(r.Context(), client, streamKey, since, until, mcpUsageMaxEvents, agg.consume)
	if err != nil {
		writeInternalError(w, r, "mcp usage: walk audit stream", err)
		return
	}

	resp := MCPUsageResponse{
		Cells:          agg.cells(),
		TotalCalls:     agg.totalCalls,
		WindowMs:       until.UnixMilli() - since.UnixMilli(),
		TruncatedAtMax: truncated,
	}
	writeJSON(w, resp)
}

// ---------------------------------------------------------------------------
// Aggregator
// ---------------------------------------------------------------------------

type mcpUsageAggregator struct {
	agentFilter string
	toolFilter  string
	totalCalls  int
	buckets     map[string]*mcpUsageBucket
}

type mcpUsageBucket struct {
	cell      MCPUsageCell
	latencies []float64
}

func newMCPUsageAggregator(agentFilter, toolFilter string) *mcpUsageAggregator {
	return &mcpUsageAggregator{
		agentFilter: agentFilter,
		toolFilter:  toolFilter,
		buckets:     map[string]*mcpUsageBucket{},
	}
}

func (a *mcpUsageAggregator) consume(ev audit.SIEMEvent) {
	if !isMCPUsageEvent(ev.EventType) {
		return
	}
	tool := strings.TrimSpace(extraField(ev, "tool_name", "tool"))
	if tool == "" {
		return
	}
	agentID := strings.TrimSpace(ev.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(extraField(ev, "agent_id"))
	}
	if a.agentFilter != "" && a.agentFilter != agentID {
		return
	}
	if a.toolFilter != "" && a.toolFilter != tool {
		return
	}
	key := agentID + "\x00" + tool
	b, ok := a.buckets[key]
	if !ok {
		b = &mcpUsageBucket{cell: MCPUsageCell{AgentID: agentID, ToolName: tool}}
		a.buckets[key] = b
	}
	b.cell.Count++
	a.totalCalls++

	switch {
	case ev.EventType == audit.EventMCPToolDenied:
		b.cell.DenyCount++
	case strings.EqualFold(ev.Decision, "deny"):
		b.cell.DenyCount++
	case strings.EqualFold(ev.Decision, "allow"):
		b.cell.AllowCount++
	}

	if status := strings.ToLower(extraField(ev, "approval_status")); status == "required" || status == "pending" {
		b.cell.ApprovalRequiredCount++
	}

	if raw := extraField(ev, "latency_ms"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 {
			b.latencies = append(b.latencies, v)
		}
	}

	tsMs := ev.Timestamp.UnixMilli()
	if tsMs > b.cell.LastInvokedAtMs {
		b.cell.LastInvokedAtMs = tsMs
	}
}

func (a *mcpUsageAggregator) cells() []MCPUsageCell {
	out := make([]MCPUsageCell, 0, len(a.buckets))
	for _, b := range a.buckets {
		if len(b.latencies) > 0 {
			b.cell.P50LatencyMs = percentile(b.latencies, 50)
			b.cell.P99LatencyMs = percentile(b.latencies, 99)
		}
		out = append(out, b.cell)
	}
	// Stable order: highest count first, then by agent+tool — keeps
	// downstream snapshot tests deterministic.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].AgentID != out[j].AgentID {
			return out[i].AgentID < out[j].AgentID
		}
		return out[i].ToolName < out[j].ToolName
	})
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isMCPUsageEvent(eventType string) bool {
	switch eventType {
	case audit.EventMCPToolInvocation,
		audit.EventMCPToolDenied,
		audit.EventMCPToolApproval:
		return true
	}
	return false
}

func extraField(ev audit.SIEMEvent, keys ...string) string {
	if ev.Extra == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := ev.Extra[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// percentile returns the nearest-rank percentile of vals (vals is mutated by sort).
func percentile(vals []float64, p int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := (p * len(sorted)) / 100
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// walkMCPEvents pages over the tenant's audit stream in chunks, capped at maxEvents.
// Returns (truncated, error). truncated=true means the cap was hit before the range was exhausted.
func walkMCPEvents(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	from, to time.Time,
	maxEvents int64,
	fn func(audit.SIEMEvent),
) (bool, error) {
	minID := strconv.FormatInt(from.UnixMilli(), 10) + "-0"
	maxID := strconv.FormatInt(to.UnixMilli(), 10) + "-18446744073709551615"
	emitted := int64(0)
	cursor := minID
	for emitted < maxEvents {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := maxEvents - emitted
		pageSize := mcpUsageChunkSize
		if remaining < pageSize {
			pageSize = remaining
		}
		entries, err := client.XRangeN(ctx, streamKey, cursor, maxID, pageSize).Result()
		if err != nil {
			return false, err
		}
		if len(entries) == 0 {
			return false, nil
		}
		for _, entry := range entries {
			payload, ok := entry.Values[chainStreamEventField].(string)
			if !ok {
				continue
			}
			var ev audit.SIEMEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			fn(ev)
			emitted++
		}
		// Exclusive cursor advance — never re-process the last row.
		cursor = "(" + entries[len(entries)-1].ID
		if int64(len(entries)) < pageSize {
			return false, nil
		}
	}
	return true, nil
}

type mcpHTTPError struct {
	status  int
	code    string
	message string
}

// parseMCPRange returns (since, until, error) for ?since= / ?until= unix-ms params.
// When both are omitted the window is the trailing defaultLookback ending at now.
func parseMCPRange(q map[string][]string, now time.Time, defaultLookback time.Duration) (time.Time, time.Time, *mcpHTTPError) {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	until := now
	since := now.Add(-defaultLookback)
	if raw := get("until"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return time.Time{}, time.Time{}, &mcpHTTPError{http.StatusBadRequest, errorCodeMCPRangeInvalid, "until must be a non-negative unix millisecond"}
		}
		until = time.UnixMilli(v).UTC()
	}
	if raw := get("since"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return time.Time{}, time.Time{}, &mcpHTTPError{http.StatusBadRequest, errorCodeMCPRangeInvalid, "since must be a non-negative unix millisecond"}
		}
		since = time.UnixMilli(v).UTC()
	}
	if !until.After(since) {
		return time.Time{}, time.Time{}, &mcpHTTPError{http.StatusBadRequest, errorCodeMCPRangeInvalid, "until must be > since"}
	}
	if until.Sub(since) > maxVerifySinceUntilSpread {
		return time.Time{}, time.Time{}, &mcpHTTPError{http.StatusBadRequest, errorCodeMCPRangeInvalid, "since/until range exceeds 30 days"}
	}
	return since, until, nil
}
