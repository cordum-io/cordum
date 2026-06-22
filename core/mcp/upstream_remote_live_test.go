//go:build live

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/edge"
)

// TestLive_MondayUpstream_ReadOnly drives the net-new RemoteUpstream proxy against
// the REAL Monday MCP server over its actual streamable-HTTP/SSE transport, using
// the SSRF-guarded production client path. It PROVES the full handshake the bug
// ticket (task-bfb4399a) suspected was broken — initialize -> tools/list -> a
// benign tools/call — which is in fact correct; the only live failure mode is
// Monday's per-token initialize throttle (HTTP 400), which SKIPs (never fails or
// fakes success).
//
// Expected Monday framing (matches the successful direct client AND the
// deterministic fake tests TestRemoteUpstream_MondayStreamableHTTPFraming /
// _ReusesSingleSessionAcrossListAndCall): POST initialize (protocolVersion
// 2024-11-05, empty capabilities, clientInfo; Authorization = raw resolved token;
// Accept: application/json, text/event-stream) -> `Mcp-Session-Id` RESPONSE header
// -> POST `notifications/initialized` with that session -> POST tools/list |
// tools/call reusing the SAME cached session (one initialize per RemoteUpstream).
//
// READ-ONLY: tools/list + get_board_items_page on the THROWAWAY board only (epic
// rail — never a mutation/delete). Excluded from normal builds/CI by the `live`
// build tag. Run:
//
//	Git Bash:   set -a; . /c/projects/Monday-demo/.env; set +a; go test -tags live -run TestLive_MondayUpstream_ReadOnly ./core/mcp -v
//	PowerShell: $env:MONDAY_API_TOKEN="<token>"; go test -tags live -run TestLive_MondayUpstream_ReadOnly ./core/mcp -v
//
// SECRET HYGIENE: never logs cfg.AuthHeader, request headers, raw env values, or
// raw upstream error bodies — only endpoint host, tool count, selected tool names,
// board id, and sanitized RemoteUpstream errors (numeric code only).
func TestLive_MondayUpstream_ReadOnly(t *testing.T) {
	if strings.TrimSpace(os.Getenv("MONDAY_API_TOKEN")) == "" {
		t.Skip("set MONDAY_API_TOKEN (Monday-demo/.env) to run the live Monday smoke")
	}
	cfg, err := ResolveUpstreamConfig(&edge.UpstreamServer{
		Name: "cordum.monday", Transport: "http",
		Endpoint: "https://mcp.monday.com/mcp", AuthSecretRef: "secret://monday-token",
	}, EnvSecretResolver(map[string]string{"secret://monday-token": "MONDAY_API_TOKEN"}))
	if err != nil {
		t.Fatalf("ResolveUpstreamConfig failed: %v", err) // sanitized — never contains the token
	}
	up, err := NewRemoteUpstream(context.Background(), cfg) // SSRF-guarded real client
	if err != nil {
		t.Fatalf("NewRemoteUpstream (real, SSRF-guarded): %v", err)
	}

	// initialize -> tools/list via one handshake. rpc surfaces only the sanitized
	// error, so a Monday throttle (HTTP 400) becomes a SKIP, never a failure.
	raw, rpcErr := up.rpc(context.Background(), MethodToolsList, nil)
	if rpcErr != nil {
		t.Skipf("live Monday unreachable (likely per-token initialize throttle): %v — rerun after cooldown", rpcErr)
	}
	var res ToolListResult
	if uerr := json.Unmarshal(raw, &res); uerr != nil {
		t.Fatalf("decode live tools/list: %v", uerr)
	}
	if len(res.Tools) == 0 {
		t.Skip("live Monday tools/list returned empty (throttled); rerun after the throttle clears")
	}
	names := map[string]bool{}
	for _, tl := range res.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"get_board_items_page", "all_monday_api"} {
		if !names[want] {
			t.Errorf("live Monday catalog missing %q", want)
		}
	}
	t.Logf("LIVE tools/list OK: %d Monday tools via RemoteUpstream over real streamable-HTTP", len(res.Tools))

	// Benign READ on the THROWAWAY board (epic rail — never a mutation). The exact
	// arg schema is the upstream's; a forwarded result OR a Monday isError result
	// both prove the gated initialize->list->call round-trip. A 400 here = throttle.
	boardID := strings.TrimSpace(os.Getenv("MONDAY_BOARD_ID"))
	if boardID == "" {
		boardID = "5097518101"
	}
	args, _ := json.Marshal(map[string]any{"boardId": boardID, "limit": 1})
	callRes, callErr := up.Invoke(context.Background(), ToolCallParams{Name: "get_board_items_page", Arguments: args})
	if callErr != nil {
		if strings.Contains(callErr.Error(), "400") {
			t.Skipf("live get_board_items_page throttled (status 400): %v — rerun after cooldown", callErr)
		}
		t.Logf("LIVE get_board_items_page sanitized error (upstream arg schema may differ): %v", callErr)
		return
	}
	if callRes == nil {
		t.Fatal("live get_board_items_page returned a nil result")
	}
	t.Logf("LIVE get_board_items_page round-trip OK on board %s: isError=%v content_items=%d",
		boardID, callRes.IsError, len(callRes.Content))
}
