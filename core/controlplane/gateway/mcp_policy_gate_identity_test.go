package gateway

// HTTP no-identity coverage for task-f6d83860. With mcp.policy_gate_enabled
// on, a tools/call from a request without a bound agent identity must answer
// -32098 with sub_reason no_identity, as it does with the gate off, instead of
// the old -32603 (plain call) or -32097 (call carrying `_approval_ref`)
// missing_mcp_metadata. The three request shapes are the ones eb2d6f28's
// resolveMCPIdentity maps to an empty agent ID: no header and an unbound
// principal, a header that contradicts the bound identity, and an unbound
// principal that sends a header anyway.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/mcp"
)

// newGateIdentityServer serves the HTTP MCP routes for tenant "default" as
// principal "tester". With bind, tester is linked to agent-bound; agent-other
// is an active, unlinked identity in the same tenant.
func newGateIdentityServer(t *testing.T, gateOn, bind bool) *httptest.Server {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	if gateOn {
		configurePolicyGateBootDeps(t, s)
	}
	setMCPConfigForBoot(t, s, true, gateOn)
	seedMCPBindingAgent(t, s, "default", "agent-bound")
	seedMCPBindingAgent(t, s, "default", "agent-other")
	if bind {
		if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-bound", "tester"); err != nil {
			t.Fatalf("LinkWorker: %v", err)
		}
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	if got := s.getMCPRuntime().server.HasPolicyGate(); got != gateOn {
		t.Fatalf("HasPolicyGate() = %v, want %v", got, gateOn)
	}
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)
	return httpSrv
}

// requireNoIdentityResponse asserts HTTP 200 carrying -32098 no_identity.
func requireNoIdentityResponse(t *testing.T, code int, resp mcpBindingRPCResponse) {
	t.Helper()
	subReason, _ := resp.errorData()
	if code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32098 || subReason != "no_identity" {
		t.Fatalf("tools/call: status=%d error=%+v sub_reason=%q, want 200 with -32098 no_identity", code, resp.Error, subReason)
	}
}

func TestNoIdentity_HTTP(t *testing.T) {
	plain := `{"name":"` + mcp.ToolStatus + `","arguments":{}}`
	withRef := `{"name":"` + mcp.ToolStatus + `","arguments":{"_approval_ref":"edge_appr_noid_1"}}`
	cases := []struct {
		name   string
		bind   bool
		header string
	}{
		{"missing header, unbound principal", false, ""},
		{"mismatched header, bound principal", true, "agent-other"},
		{"header with unbound principal", false, "agent-other"},
	}
	for _, gate := range []bool{true, false} {
		gateName := map[bool]string{true: "gate on", false: "gate off"}[gate]
		for _, tc := range cases {
			t.Run(gateName+"/"+tc.name, func(t *testing.T) {
				srv := newGateIdentityServer(t, gate, tc.bind)
				code, resp := postMCPBindingRPC(t, srv.URL, tc.header, "tools/call", plain)
				requireNoIdentityResponse(t, code, resp)
				if !gate {
					return
				}
				// A ref-bearing call is refused before any claim is tried.
				code, resp = postMCPBindingRPC(t, srv.URL, tc.header, "tools/call", withRef)
				requireNoIdentityResponse(t, code, resp)
			})
		}
	}
	// Control: a bound principal that sends no header, or its own ID, is not
	// refused as unidentified under the gate.
	for _, header := range []string{"", "agent-bound"} {
		t.Run("gate on/bound principal header="+header, func(t *testing.T) {
			srv := newGateIdentityServer(t, true, true)
			code, resp := postMCPBindingRPC(t, srv.URL, header, "tools/call", plain)
			if code != http.StatusOK || resp.Error != nil {
				t.Fatalf("bound tools/call: status=%d error=%+v, want 200 and no error", code, resp.Error)
			}
		})
	}
}
