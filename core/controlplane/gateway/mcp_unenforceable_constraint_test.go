package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/mcp"
)

func TestMCPConstraintApplication(t *testing.T) {
	for _, tc := range []struct {
		name       string
		constraint bool
		wantCalls  int
	}{
		{name: "plain_allow", wantCalls: 1},
		{name: "unapplied_sandbox_is_refused", constraint: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, ctx, _, _, _ := bootMCPPolicyGateE2E(t)
			deps.Pipeline = passingDispatcher{}
			if tc.constraint {
				deps.Pipeline = fakeAWCDispatcher{constraints: map[string]any{"sandbox": "required"}}
			}
			upstream := &fakeUpstreamToolCaller{result: "ok"}
			deps.Upstream = upstreamCallerAdapter{upstream: upstream}
			transport := mcpConstraintTransport(t, deps)
			req := httptest.NewRequest(http.MethodPost, "/mcp/message", strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fs.read_file","arguments":{"path":"fixture.txt"}}}`)).WithContext(ctx)
			rr := httptest.NewRecorder()
			transport.HandleMessage(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("tools/call status = %d, want 200", rr.Code)
			}
			var reply agrReply
			if err := json.Unmarshal(rr.Body.Bytes(), &reply); err != nil {
				t.Fatalf("decode tools/call: %v", err)
			}
			if upstream.calls != tc.wantCalls {
				t.Errorf("upstream calls = %d, want %d", upstream.calls, tc.wantCalls)
			}
			if tc.constraint && reply.Error == nil {
				t.Error("unapplied constraint must return a JSON-RPC error")
			}
			if !tc.constraint && !reply.succeeded("ok") {
				t.Errorf("plain ALLOW = %s, want successful tool result", reply)
			}
		})
	}
}

func mcpConstraintTransport(t *testing.T, deps mcp.ToolCallDeps) *mcp.HTTPTransport {
	t.Helper()
	transport := mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, 5*time.Second)
	srv := mcp.NewServer(transport, mcp.NewToolRegistry(), mcp.NewResourceRegistry(), mcp.ServerConfig{}).
		WithPolicyGate("cordum.builtin", deps)
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Errorf("close transport: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve tools/call: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP server did not stop")
		}
	})
	return transport
}
