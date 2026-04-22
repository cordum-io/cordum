package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
)

func TestScanOutbound_FiltersBySignatureStatus(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	statuses := []string{"verified", "unverified", "invalid"}
	for i, st := range statuses {
		seedMCPEvent(t, mr, key, audit.SIEMEvent{
			TenantID:  "tenant-acme",
			EventType: audit.EventMCPToolOutboundInvocation,
			AgentID:   "agent-1",
			Extra: map[string]string{
				"tool_name":     "search",
				"target_server": "github",
				"sig_status":    st,
				"key_id":        "k-" + st,
				"latency_ms":    "42",
				"result_type":   "ok",
			},
		}, base+int64(i))
	}

	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 1000).UTC()

	t.Run("all", func(t *testing.T) {
		out, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 100, "", "", "all")
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(out))
		}
	})
	t.Run("verified only", func(t *testing.T) {
		out, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 100, "", "", "verified")
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(out) != 1 || out[0].SignatureStatus != "verified" || out[0].SignatureKeyID != "k-verified" {
			t.Fatalf("verified filter: %+v", out)
		}
	})
	t.Run("invalid only", func(t *testing.T) {
		out, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 100, "", "", "invalid")
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(out) != 1 || out[0].SignatureStatus != "invalid" {
			t.Fatalf("invalid filter: %+v", out)
		}
	})
}

func TestScanOutbound_FiltersByAgentAndServer(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	for i, agent := range []string{"a", "b"} {
		for j, server := range []string{"github", "jira"} {
			seedMCPEvent(t, mr, key, audit.SIEMEvent{
				TenantID:  "tenant-acme",
				EventType: audit.EventMCPToolOutboundInvocation,
				AgentID:   agent,
				Extra: map[string]string{
					"tool_name":     "list",
					"target_server": server,
					"sig_status":    "verified",
				},
			}, base+int64(i*10+j))
		}
	}
	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 1000).UTC()

	out, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 100, "a", "", "all")
	if err != nil || len(out) != 2 {
		t.Fatalf("agent filter: %d %v", len(out), err)
	}
	for _, r := range out {
		if r.AgentID != "a" {
			t.Fatalf("agent leak: %+v", r)
		}
	}
	out, _, _, err = scanOutbound(context.Background(), rdb, key, from, to, "", 100, "", "jira", "all")
	if err != nil || len(out) != 2 {
		t.Fatalf("server filter: %d %v", len(out), err)
	}
	for _, r := range out {
		if r.TargetServer != "jira" {
			t.Fatalf("server leak: %+v", r)
		}
	}
}

func TestScanOutbound_PaginatesViaCursor(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	for i := 0; i < 15; i++ {
		seedMCPEvent(t, mr, key, audit.SIEMEvent{
			TenantID:  "tenant-acme",
			EventType: audit.EventMCPToolOutboundInvocation,
			AgentID:   "a",
			Extra: map[string]string{
				"tool_name":     "do",
				"target_server": "x",
				"sig_status":    "verified",
			},
		}, base+int64(i))
	}
	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 10000).UTC()

	page1, cursor, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 5, "", "", "all")
	if err != nil || len(page1) != 5 {
		t.Fatalf("page1: %d %v", len(page1), err)
	}
	if cursor == "" {
		t.Fatal("expected next_cursor on full page")
	}
	page2, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, cursor, 100, "", "", "all")
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 10 {
		t.Fatalf("expected 10 remaining entries, got %d", len(page2))
	}
	// Pages should not overlap.
	seen := map[string]bool{}
	for _, r := range page1 {
		seen[r.StreamID] = true
	}
	for _, r := range page2 {
		if seen[r.StreamID] {
			t.Fatalf("page2 re-emits %s from page1", r.StreamID)
		}
	}
}

func TestScanOutbound_EmptyOnNoMatches(t *testing.T) {
	t.Parallel()
	_, rdb, key := newAggregatorFixture(t)
	out, cursor, truncated, err := scanOutbound(context.Background(), rdb, key, time.Now().Add(-time.Hour).UTC(), time.Now().UTC(), "", 100, "", "", "all")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 0 || cursor != "" || truncated {
		t.Fatalf("expected empty pristine result, got %d/%q/%v", len(out), cursor, truncated)
	}
}

func TestScanOutbound_SkipsNonOutboundAndMissingServer(t *testing.T) {
	t.Parallel()
	mr, rdb, key := newAggregatorFixture(t)
	base := time.Now().UTC().UnixMilli()
	// Wrong event type — must be ignored.
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventMCPToolInvocation,
		AgentID: "a", Extra: map[string]string{"tool_name": "x", "target_server": "x"},
	}, base)
	// Outbound event WITHOUT target_server — must be ignored.
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventMCPToolOutboundInvocation,
		AgentID: "a", Extra: map[string]string{"tool_name": "x"},
	}, base+1)
	// Valid outbound event — must surface.
	seedMCPEvent(t, mr, key, audit.SIEMEvent{
		TenantID: "tenant-acme", EventType: audit.EventMCPToolOutboundInvocation,
		AgentID: "a", Extra: map[string]string{"tool_name": "x", "target_server": "github", "sig_status": "verified"},
	}, base+2)

	from := time.UnixMilli(base - 1000).UTC()
	to := time.UnixMilli(base + 1000).UTC()
	out, _, _, err := scanOutbound(context.Background(), rdb, key, from, to, "", 100, "", "", "all")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 1 || out[0].TargetServer != "github" {
		t.Fatalf("expected single github entry, got %+v", out)
	}
}

func TestNormaliseSigFilter(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":            "all",
		"all":         "all",
		"verified":    "verified",
		"unverified":  "unverified",
		"invalid":     "invalid",
		"VERIFIED":    "verified",
		"  invalid  ": "invalid",
		"bogus":       "invalid_value",
	}
	for in, want := range cases {
		if got := normaliseSigFilter(in); got != want {
			t.Fatalf("normaliseSigFilter(%q) = %q want %q", in, got, want)
		}
	}
}

func TestNormaliseSigStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"verified":   "verified",
		"invalid":    "invalid",
		"":           "unverified",
		"unverified": "unverified",
		"weird":      "unverified",
	}
	for in, want := range cases {
		if got := normaliseSigStatus(in); got != want {
			t.Fatalf("normaliseSigStatus(%q) = %q want %q", in, got, want)
		}
	}
}
