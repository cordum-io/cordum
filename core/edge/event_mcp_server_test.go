package edge

import "testing"

// TestEventKindMCPServerConnectedConstant folds in the amendment-3 assertion
// (EDGE-100 governor-e932e549 comment-a305f5a3 2026-05-16): DoD #3 event-kind
// constants are pre-satisfied at event.go:51-52. This test pins the wire
// string so a rename or accidental edit is caught by `go test ./core/edge/...`
// rather than at runtime by an MCP gateway emitting a non-existent event kind.
func TestEventKindMCPServerConnectedConstant(t *testing.T) {
	if got, want := string(EventKindMCPServerConnected), "mcp.server.connected"; got != want {
		t.Fatalf("EventKindMCPServerConnected: got %q want %q", got, want)
	}
}

// TestEventKindMCPServerFailedConstant pins the failed-connect wire string.
// Same rationale as TestEventKindMCPServerConnectedConstant; together they
// document the on-the-wire contract EDGE-100 MCP Gateway emits on connect
// success / connect failure.
func TestEventKindMCPServerFailedConstant(t *testing.T) {
	if got, want := string(EventKindMCPServerFailed), "mcp.server.failed"; got != want {
		t.Fatalf("EventKindMCPServerFailed: got %q want %q", got, want)
	}
}
