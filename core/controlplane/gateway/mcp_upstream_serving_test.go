package gateway

import (
	"context"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

// These tests cover the gateway's upstream-fronting seam (mcp_upstream_serving.go)
// without a live Monday or DNS. The endpoint is a public IP literal
// (203.0.113.10, TEST-NET-3) so both the edge use-time SSRF revalidation
// (RevalidateMCPUpstreamAtUse) and the core/mcp outbound guard accept it with no
// lookup, and ResolvedIPs is pinned to the same literal so revalidation sees no
// DNS drift. NewRemoteUpstream only CONSTRUCTS the proxy here — tools/list and
// tools/call would dial, and these tests never invoke them.
const (
	frontingTestSecretRef = "secret://test-fronting-token"
	frontingTestSecretEnv = "TEST_FRONTING_TOKEN" // EnvSecretResolver convention name for the ref above
	frontingTestPublicIP  = "203.0.113.10"
)

// frontingReadyUpstream returns an enabled http upstream that PASSES use-time
// revalidation (public IP literal pinned in ResolvedIPs) and carries a secret
// ref. Whether fronting actually builds then depends solely on the test's env
// setup for frontingTestSecretEnv and on Enabled — the knobs each case mutates.
func frontingReadyUpstream(tenantID, name string) edgecore.UpstreamServer {
	return edgecore.UpstreamServer{
		Name:          name,
		TenantID:      tenantID,
		Transport:     "http",
		Endpoint:      "https://" + frontingTestPublicIP + "/mcp",
		AuthSecretRef: frontingTestSecretRef,
		Risk:          "high",
		Enabled:       true,
		ResolvedIPs:   []string{frontingTestPublicIP},
	}
}

// TestMCPUpstreamServing_FrontsEnabledUpstream: a single enabled, revalidating
// upstream with a resolvable secret makes buildFrontedUpstreamToolService return
// a non-nil ToolService gated under the upstream's EXACT name (so the action-gate
// and audit attribute every decision to e.g. cordum.monday). selectGatewayMCPUpstream
// must select that same upstream.
func TestMCPUpstreamServing_FrontsEnabledUpstream(t *testing.T) {
	t.Setenv(frontingTestSecretEnv, "dummy-token-never-dialed")
	s, _, _ := newTestGateway(t)
	const tenant, name = "default", "cordum.monday"
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
		entries: []edgecore.UpstreamServer{frontingReadyUpstream(tenant, name)},
	}

	up, ok := s.selectGatewayMCPUpstream(context.Background(), tenant)
	if !ok || up == nil {
		t.Fatalf("selectGatewayMCPUpstream ok=%v up=%v, want the enabled upstream selected", ok, up)
	}
	if up.Name != name {
		t.Fatalf("selected upstream name = %q, want %q", up.Name, name)
	}

	ts, gotName := s.buildFrontedUpstreamToolService(context.Background(), tenant)
	if ts == nil {
		t.Fatalf("buildFrontedUpstreamToolService returned nil ToolService, want a fronted proxy")
	}
	if gotName != name {
		t.Fatalf("fronted gate server-name = %q, want %q", gotName, name)
	}
}

// TestMCPUpstreamServing_NoUpstreamFailsClosedToBuiltin: with no registered
// upstream for the tenant, fronting returns (nil,"") so the caller serves the
// built-in tool registry unchanged. The empty name is the signal the caller keys
// on to keep the built-in path.
func TestMCPUpstreamServing_NoUpstreamFailsClosedToBuiltin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{} // no entries

	ts, gotName := s.buildFrontedUpstreamToolService(context.Background(), "default")
	if ts != nil {
		t.Fatalf("buildFronted ToolService = %v, want nil (fail-closed to builtin)", ts)
	}
	if gotName != "" {
		t.Fatalf("fronted name = %q, want \"\" so the caller serves the builtin registry", gotName)
	}

	up, ok := s.selectGatewayMCPUpstream(context.Background(), "default")
	if ok || up != nil {
		t.Fatalf("selectGatewayMCPUpstream = (%v,%v), want (nil,false)", up, ok)
	}
}

// TestMCPUpstreamServing_DisabledUpstreamFailsClosed: a registered-but-disabled
// upstream (otherwise valid + revalidating) is never fronted — Enabled gates
// fronting, so the result is (nil,"") fail-closed-to-builtin.
func TestMCPUpstreamServing_DisabledUpstreamFailsClosed(t *testing.T) {
	t.Setenv(frontingTestSecretEnv, "dummy-token-never-dialed")
	s, _, _ := newTestGateway(t)
	up := frontingReadyUpstream("default", "cordum.monday")
	up.Enabled = false
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{entries: []edgecore.UpstreamServer{up}}

	ts, gotName := s.buildFrontedUpstreamToolService(context.Background(), "default")
	if ts != nil || gotName != "" {
		t.Fatalf("buildFronted = (%v,%q) for a disabled upstream, want (nil,\"\")", ts, gotName)
	}
}

// TestMCPUpstreamServing_UnresolvedSecretDisablesFronting: an enabled upstream
// whose auth secret cannot resolve (env empty) disables fronting (nil,"") rather
// than dialing with a blank credential, and must not panic. This locks the
// ResolveUpstreamConfig fail-closed branch (mcp_upstream_serving.go:62).
func TestMCPUpstreamServing_UnresolvedSecretDisablesFronting(t *testing.T) {
	t.Setenv(frontingTestSecretEnv, "") // explicitly empty -> EnvSecretResolver returns an error
	s, _, _ := newTestGateway(t)
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
		entries: []edgecore.UpstreamServer{frontingReadyUpstream("default", "cordum.monday")},
	}

	ts, gotName := s.buildFrontedUpstreamToolService(context.Background(), "default")
	if ts != nil || gotName != "" {
		t.Fatalf("buildFronted = (%v,%q) with an unresolved secret, want (nil,\"\") fail-closed", ts, gotName)
	}
}

// TestMCPUpstreamServing_SelectSkipsDisabledPicksEnabled: selection skips a
// leading disabled entry and returns the first ENABLED upstream. Locks the
// `!Enabled { continue }` skip in selectGatewayMCPUpstream — removing it would
// surface disabled-first instead.
func TestMCPUpstreamServing_SelectSkipsDisabledPicksEnabled(t *testing.T) {
	s, _, _ := newTestGateway(t)
	disabled := frontingReadyUpstream("default", "disabled-first")
	disabled.Enabled = false
	enabled := frontingReadyUpstream("default", "enabled-second")
	s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
		entries: []edgecore.UpstreamServer{disabled, enabled},
	}

	up, ok := s.selectGatewayMCPUpstream(context.Background(), "default")
	if !ok || up == nil {
		t.Fatalf("select ok=%v up=%v, want the enabled upstream", ok, up)
	}
	if up.Name != "enabled-second" {
		t.Fatalf("selected = %q, want enabled-second (disabled-first must be skipped)", up.Name)
	}
}
