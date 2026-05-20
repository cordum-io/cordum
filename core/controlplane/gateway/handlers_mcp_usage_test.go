package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/redis/go-redis/v9"
)

// seedMCPEvent appends an event to a miniredis-backed audit chain
// stream the same way the production chainer would. Returns the
// XADD'd timestamp millis so the caller can sequence assertions.
func seedMCPEvent(t *testing.T, mr *miniredis.Miniredis, streamKey string, ev audit.SIEMEvent, tsMs int64) {
	t.Helper()
	ev.Timestamp = time.UnixMilli(tsMs).UTC()
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	id := strconv.FormatInt(tsMs, 10) + "-*"
	if _, err := mr.XAdd(streamKey, id, []string{"event", string(body), "seq", "1"}); err != nil {
		t.Fatalf("xadd: %v", err)
	}
}

func newAggregatorFixture(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient, string) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = rdb.Close() })
	chainer := audit.NewChainer(rdb, "")
	return mr, rdb, chainer.StreamKey("tenant-acme")
}

func TestMCPUsageAggregator_BucketsByAgentTool(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC).UnixMilli()

	type seed struct {
		agent, tool, decision, eventType string
		latencyMs                        string
		tickMs                           int64
	}
	seeds := []seed{
		// agent-1 / tool-A — 2 invocations, 1 allow + 1 deny, latencies 10/20
		{"agent-1", "tool-A", "allow", audit.EventMCPToolInvocation, "10", 0},
		{"agent-1", "tool-A", "deny", audit.EventMCPToolInvocation, "20", 1},
		// agent-1 / tool-B — 1 invocation allowed
		{"agent-1", "tool-B", "allow", audit.EventMCPToolInvocation, "5", 2},
		// agent-2 / tool-A — 3 invocations, all denied via mcp.tool_denied
		{"agent-2", "tool-A", "", audit.EventMCPToolDenied, "30", 3},
		{"agent-2", "tool-A", "", audit.EventMCPToolDenied, "40", 4},
		{"agent-2", "tool-A", "", audit.EventMCPToolDenied, "50", 5},
		// agent-3 / tool-C — approval required
		{"agent-3", "tool-C", "", audit.EventMCPToolApproval, "", 6},
	}
	for _, s := range seeds {
		ev := audit.SIEMEvent{
			TenantID:  "tenant-acme",
			EventType: s.eventType,
			AgentID:   s.agent,
			Decision:  s.decision,
			Extra: map[string]string{
				"tool_name": s.tool,
			},
		}
		if s.latencyMs != "" {
			ev.Extra["latency_ms"] = s.latencyMs
		}
		if s.eventType == audit.EventMCPToolApproval {
			ev.Extra["approval_status"] = "required"
		}
		seedMCPEvent(t, mr, key, ev, base+s.tickMs)
	}

	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 10000).UTC()
	agg := newMCPUsageAggregator("", "")
	truncated, err := walkMCPEvents(context.Background(), rdb, key, from, to, mcpUsageMaxEvents, agg.consume)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	cells := agg.cells()
	if len(cells) != 4 {
		t.Fatalf("expected 4 cells, got %d", len(cells))
	}

	byKey := map[string]MCPUsageCell{}
	for _, c := range cells {
		byKey[c.AgentID+"/"+c.ToolName] = c
	}

	c1A := byKey["agent-1/tool-A"]
	if c1A.Count != 2 || c1A.AllowCount != 1 || c1A.DenyCount != 1 {
		t.Fatalf("agent-1/tool-A: %+v", c1A)
	}
	if c1A.P50LatencyMs == 0 || c1A.P99LatencyMs == 0 {
		t.Fatalf("agent-1/tool-A latencies: p50=%v p99=%v", c1A.P50LatencyMs, c1A.P99LatencyMs)
	}

	c1B := byKey["agent-1/tool-B"]
	if c1B.Count != 1 || c1B.AllowCount != 1 || c1B.DenyCount != 0 {
		t.Fatalf("agent-1/tool-B: %+v", c1B)
	}

	c2A := byKey["agent-2/tool-A"]
	if c2A.Count != 3 || c2A.DenyCount != 3 {
		t.Fatalf("agent-2/tool-A: %+v", c2A)
	}

	c3C := byKey["agent-3/tool-C"]
	if c3C.Count != 1 || c3C.ApprovalRequiredCount != 1 {
		t.Fatalf("agent-3/tool-C: %+v", c3C)
	}
	if c3C.LastInvokedAtMs <= 0 {
		t.Fatalf("agent-3/tool-C missing last_invoked_at_ms: %+v", c3C)
	}

	// Total calls equals every counted event.
	if agg.totalCalls != 7 {
		t.Fatalf("totalCalls=%d", agg.totalCalls)
	}
}

func TestMCPUsageAggregator_FiltersByAgentAndTool(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	for i, agent := range []string{"a", "b"} {
		for j, tool := range []string{"x", "y"} {
			ev := audit.SIEMEvent{
				TenantID:  "tenant-acme",
				EventType: audit.EventMCPToolInvocation,
				AgentID:   agent,
				Decision:  "allow",
				Extra:     map[string]string{"tool_name": tool},
			}
			seedMCPEvent(t, mr, key, ev, base+int64(i*10+j))
		}
	}
	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 1000).UTC()

	t.Run("agent filter", func(t *testing.T) {
		agg := newMCPUsageAggregator("a", "")
		_, _ = walkMCPEvents(context.Background(), rdb, key, from, to, mcpUsageMaxEvents, agg.consume)
		cells := agg.cells()
		if len(cells) != 2 {
			t.Fatalf("expected 2 cells for agent=a, got %d", len(cells))
		}
		for _, c := range cells {
			if c.AgentID != "a" {
				t.Fatalf("filter leaked agent=%q", c.AgentID)
			}
		}
	})
	t.Run("tool filter", func(t *testing.T) {
		agg := newMCPUsageAggregator("", "x")
		_, _ = walkMCPEvents(context.Background(), rdb, key, from, to, mcpUsageMaxEvents, agg.consume)
		cells := agg.cells()
		if len(cells) != 2 {
			t.Fatalf("expected 2 cells for tool=x, got %d", len(cells))
		}
		for _, c := range cells {
			if c.ToolName != "x" {
				t.Fatalf("filter leaked tool=%q", c.ToolName)
			}
		}
	})
	t.Run("both filters", func(t *testing.T) {
		agg := newMCPUsageAggregator("b", "y")
		_, _ = walkMCPEvents(context.Background(), rdb, key, from, to, mcpUsageMaxEvents, agg.consume)
		cells := agg.cells()
		if len(cells) != 1 || cells[0].AgentID != "b" || cells[0].ToolName != "y" {
			t.Fatalf("expected single b/y cell, got %+v", cells)
		}
	})
}

func TestMCPUsageAggregator_IgnoresNonMCPEvents(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	// Mix: one MCP event, one safety.decision (irrelevant), one mcp event w/o tool name.
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventMCPToolInvocation,
		AgentID: "a", Decision: "allow",
		Extra: map[string]string{"tool_name": "tool-x"},
	}, base)
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventSafetyDecision,
		AgentID: "a",
	}, base+1)
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventMCPToolInvocation,
		AgentID: "a", Decision: "allow",
		// missing tool_name → must be skipped, not a panic
	}, base+2)

	agg := newMCPUsageAggregator("", "")
	_, _ = walkMCPEvents(context.Background(), rdb, key, time.UnixMilli(base-1000).UTC(), time.UnixMilli(base+10000).UTC(), mcpUsageMaxEvents, agg.consume)
	cells := agg.cells()
	if len(cells) != 1 {
		t.Fatalf("expected single cell, got %+v", cells)
	}
	if agg.totalCalls != 1 {
		t.Fatalf("totalCalls=%d", agg.totalCalls)
	}
}

func TestMCPUsageWalk_TruncatesAtMax(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	for i := 0; i < 25; i++ {
		seedMCPEvent(t, mr, key, audit.SIEMEvent{
			TenantID: "tenant-acme", EventType: audit.EventMCPToolInvocation,
			AgentID: "a", Decision: "allow",
			Extra: map[string]string{"tool_name": "tool-x"},
		}, base+int64(i))
	}
	agg := newMCPUsageAggregator("", "")
	truncated, err := walkMCPEvents(context.Background(), rdb, key, time.UnixMilli(base-1000).UTC(), time.UnixMilli(base+10000).UTC(), 10, agg.consume)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true when cap < total events")
	}
	if agg.totalCalls != 10 {
		t.Fatalf("expected 10 totalCalls, got %d", agg.totalCalls)
	}
}

func TestMCPUsageRangeParser(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	defaultLookback := 24 * time.Hour

	t.Run("defaults", func(t *testing.T) {
		since, until, err := parseMCPRange(map[string][]string{}, now, defaultLookback)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !until.Equal(now) {
			t.Fatalf("until=%v want %v", until, now)
		}
		if !since.Equal(now.Add(-defaultLookback)) {
			t.Fatalf("since=%v", since)
		}
	})
	t.Run("explicit since/until", func(t *testing.T) {
		since, until, err := parseMCPRange(map[string][]string{
			"since": {"1000"},
			"until": {"2000"},
		}, now, defaultLookback)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if since.UnixMilli() != 1000 || until.UnixMilli() != 2000 {
			t.Fatalf("range: since=%d until=%d", since.UnixMilli(), until.UnixMilli())
		}
	})
	t.Run("rejects until <= since", func(t *testing.T) {
		_, _, err := parseMCPRange(map[string][]string{
			"since": {"2000"},
			"until": {"1000"},
		}, now, defaultLookback)
		if err == nil || err.status != 400 {
			t.Fatalf("expected 400, got %+v", err)
		}
	})
	t.Run("rejects window over 30 days", func(t *testing.T) {
		_, _, err := parseMCPRange(map[string][]string{
			"since": {"0"},
			"until": {strconv.FormatInt((31 * 24 * time.Hour).Milliseconds(), 10)},
		}, now, defaultLookback)
		if err == nil || err.status != 400 {
			t.Fatalf("expected 400, got %+v", err)
		}
	})
	t.Run("rejects malformed since", func(t *testing.T) {
		_, _, err := parseMCPRange(map[string][]string{
			"since": {"abc"},
		}, now, defaultLookback)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
