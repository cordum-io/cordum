package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// A fronted MCP upstream serves only a caller with an active bound agent
// identity, exactly like the built-in ToolRegistry: no identity lists no tools
// and refuses tools/call with -32098 no_identity (threads PRRT_kwDOQ30nx863accT
// and PRRT_kwDOQ30nx863accW).
const frontedTestUpstream = "cordum.monday"
const frontedTestTool = "monday_list_boards"

// The fronted service the gateway builds, called with a canceled context: the
// upstream proxy then fails before it dials, so no case reaches the network.
func TestFrontedUpstreamToolService_RequiresIdentity(t *testing.T) {
	t.Setenv(frontingTestSecretEnv, "dummy-token-never-dialed")
	s, _, _ := newTestGateway(t)
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
		entries: []edgecore.UpstreamServer{frontingReadyUpstream("default", frontedTestUpstream)},
	}
	ts, name := s.buildFrontedUpstreamToolService(context.Background(), "default")
	if ts == nil || name != frontedTestUpstream {
		t.Fatalf("buildFrontedUpstreamToolService = (%v,%q), want the fronted %s", ts, name, frontedTestUpstream)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if tools := ts.ListTools(ctx); len(tools) != 0 {
		t.Fatalf("ListTools without identity = %v, want none", tools)
	}
	var denied *mcp.NotAuthorized
	_, err := ts.Call(ctx, frontedTestTool, json.RawMessage(`{}`))
	if !errors.As(err, &denied) || denied.SubReason != mcp.DenyReasonNoIdentity || denied.Tool != frontedTestTool {
		t.Fatalf("Call without identity: err=%v, want NotAuthorized no_identity for %s", err, frontedTestTool)
	}
	_, err = ts.Call(mcp.ContextWithIdentity(ctx, &mcp.AgentIdentity{ID: mcpAssertedAgent}), frontedTestTool, json.RawMessage(`{}`))
	if err == nil || errors.As(err, &denied) || !strings.Contains(err.Error(), "upstream request failed") {
		t.Fatalf("Call with identity: err=%v, want the upstream proxy's own request failure", err)
	}
}

// The real gateway with mcp.policy_gate_enabled off serves the fronted upstream.
// A caller with no bound identity, or with a suspended or revoked binding (a
// non-empty agent id with a nil identity), sees no tools and is refused with
// -32098 no_identity before the upstream is dialed.
func TestMCPFrontedUpstream_GateOffRefusesCallerWithoutIdentity(t *testing.T) {
	cases := []struct {
		name, key, header, status string
	}{
		{name: "no bound identity", key: mcpAssertedUnboundKey, header: mcpAssertedWorker},
		{name: "suspended binding", key: mcpAssertedBoundKey, status: "suspended"},
		{name: "revoked binding", key: mcpAssertedBoundKey, status: "revoked"},
	}
	call := `{"name":"` + frontedTestTool + `","arguments":{}}`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(frontingTestSecretEnv, "dummy-token-never-dialed")
			s, _ := mcpAssertedGateway(t, map[string]string{"CORDUM_ENV": "development"}, false)
			s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
				entries: []edgecore.UpstreamServer{frontingReadyUpstream("default", frontedTestUpstream)},
			}
			if tc.status != "" {
				if _, err := s.agentIdentityStore.Update(context.Background(), "default", mcpAssertedAgent, store.AgentIdentity{Status: tc.status}); err != nil {
					t.Fatalf("set %s status: %v", tc.status, err)
				}
			}
			srv := mcpAssertedServe(t, s)
			if rt := s.getMCPRuntime(); rt == nil || rt.frontedUpstream != frontedTestUpstream || rt.server.HasPolicyGate() {
				t.Fatalf("runtime does not front %s with the policy gate off", frontedTestUpstream)
			}
			code, resp := mcpAssertedRPC(t, srv.URL, tc.key, tc.header, "tools/call", call)
			if sub, _ := resp.errorData(); code != 200 || resp.Error == nil || resp.Error.Code != -32098 || sub != "no_identity" {
				t.Fatalf("tools/call: status=%d error=%+v sub_reason=%q, want -32098 no_identity", code, resp.Error, sub)
			}
			code, resp = mcpAssertedRPC(t, srv.URL, tc.key, tc.header, "tools/list", `{}`)
			if code != 200 || resp.Error != nil || len(resp.Result.Tools) != 0 {
				t.Fatalf("tools/list: status=%d error=%+v tools=%v, want 200 and no tools", code, resp.Error, resp.Result.Tools)
			}
		})
	}
}
