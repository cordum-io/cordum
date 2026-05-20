package gateway

// Integration-style test for the outbound-log pipeline. Drives the
// actual production path:
//
//   HTTPServiceBridge.SubmitJob
//     → bridge.doJSON → POST body
//       → ToolInvocationOutboundAuditor.StartRequest / FinishRequest
//         → ToolInvocationAuditor.StartOutbound / FinishOutbound
//           → audit.SIEMEvent (mcp.tool_outbound_invocation)
//             → audit.Chainer.Append → tenant Redis Stream
//               → scanOutbound → MCPOutboundResponse
//
// The test proves every link: the auditor fires on a real outbound
// HTTP call, the event lands on the audit chain, and the outbound
// endpoint returns it. This addresses the QA reopen #1 gap ("tests
// seed synthetic events directly into the audit stream, missing the
// real integration gap").

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/mcp"
	"github.com/redis/go-redis/v9"
)

// chainSender bridges audit.SIEMEvent from the invocation auditor
// into the per-tenant audit chain. Production code uses NATS-backed
// publishing; this test-double hits the chainer directly so the
// whole pipeline is exercised without standing up a NATS server.
type chainSender struct {
	chainer *audit.Chainer
}

func (s *chainSender) Send(event audit.SIEMEvent) {
	if s == nil || s.chainer == nil {
		return
	}
	// Normalise: the chain requires timestamp + tenant + event_type
	// to resolve the per-tenant stream key. The invocation auditor
	// already fills these but defensively set them here.
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	_ = s.chainer.Append(context.Background(), &event)
}

func (s *chainSender) Close() error { return nil }

func TestMCPOutbound_EndToEndRealAuditorPipeline(t *testing.T) {
	t.Parallel()

	// --- Miniredis + audit chainer wiring ---------------------------
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(testRedisOptions(mr.Addr()))
	t.Cleanup(func() { _ = rdb.Close() })
	chainer := audit.NewChainer(rdb, "")

	// --- Real MCP invocation auditor + outbound adapter -------------
	sender := &chainSender{chainer: chainer}
	invAuditor := mcp.NewToolInvocationAuditor(sender, nil)
	bridgeAuditor := mcp.NewToolInvocationOutboundAuditor(invAuditor, "agent-int", "tenant-int", "github")

	// --- Fake upstream gateway the bridge talks to ------------------
	gatewayHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bridge SubmitJob hits POST /api/v1/jobs. Return a minimal
		// success payload so the bridge unmarshals without error.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-int-1",
				"status": "queued",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(gatewayHTTP.Close)

	// --- Real HTTPServiceBridge with the auditor installed ---------
	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           gatewayHTTP.URL,
		TenantID:          "tenant-int",
		HTTPClient:        &http.Client{Timeout: 2 * time.Second},
		AllowPrivateHosts: true, // httptest binds to 127.0.0.1
	}.WithAuthToken("test-key"))
	bridge.WithOutboundInvocationAuditor(bridgeAuditor)

	// --- Drive a real outbound call through the bridge -------------
	out, err := bridge.SubmitJob(context.Background(), mcp.SubmitJobInput{
		Topic:  "github.repo.list",
		Prompt: "list repos",
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if out == nil || out.JobID == "" {
		t.Fatalf("empty SubmitJob output: %+v", out)
	}

	// --- Wait briefly for the auditor's goroutine-free path to
	// finish (it's synchronous — Finish emits before returning, but
	// miniredis doesn't always see the write immediately in a hot
	// loop; a short poll avoids flake).
	streamKey := chainer.StreamKey("tenant-int")
	var found []redis.XMessage
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := rdb.XRangeN(context.Background(), streamKey, "-", "+", 10).Result()
		if err != nil {
			t.Fatalf("xrange: %v", err)
		}
		found = entries
		if len(found) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("audit chain empty — auditor pipeline did not reach the stream")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// --- Assert the stream carries an mcp.tool_outbound_invocation --
	var ev audit.SIEMEvent
	payload, ok := found[0].Values["event"].(string)
	if !ok {
		t.Fatalf("stream entry missing event field: %+v", found[0])
	}
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if ev.EventType != audit.EventMCPToolOutboundInvocation {
		t.Fatalf("expected %s, got %s", audit.EventMCPToolOutboundInvocation, ev.EventType)
	}
	if ev.TenantID != "tenant-int" {
		t.Fatalf("tenant mismatch: %q", ev.TenantID)
	}
	if ev.Extra == nil || ev.Extra["server_id"] != "github" {
		t.Fatalf("missing server_id in Extra: %+v", ev.Extra)
	}
	// tool_name is synthesised as "METHOD path" by the adapter.
	if ev.Extra["tool_name"] == "" {
		t.Fatalf("missing tool_name in Extra: %+v", ev.Extra)
	}

	// --- Finally, assert scanOutbound surfaces it in the dashboard
	// endpoint payload.
	// Pad the window generously so timing drift doesn't skip the row.
	from := time.Now().Add(-1 * time.Minute).UTC()
	to := time.Now().Add(1 * time.Minute).UTC()
	// Seed Extra.target_server (the dashboard's required field) — the
	// adapter populates server_id; map it over so the handler sees it
	// under its canonical key.
	// scanOutbound's buildOutboundEntry looks at target_server, server, or
	// server_id — server_id is already there, so no copy needed.
	entries, _, _, serr := scanOutbound(context.Background(), rdb, streamKey, from, to, "", 100, "", "", "all")
	if serr != nil {
		t.Fatalf("scanOutbound: %v", serr)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one outbound row, got %d: %+v", len(entries), entries)
	}
	if entries[0].TargetServer != "github" {
		t.Fatalf("target_server=%q", entries[0].TargetServer)
	}
	if entries[0].AgentID != "agent-int" {
		t.Fatalf("agent_id=%q", entries[0].AgentID)
	}
}
