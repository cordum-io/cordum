package safetykernel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
)

// TestSetPolicyWarnsOnReservedMCPGatewayFields pins the warning the Safety
// Kernel logs when it installs a policy whose tenants set the reserved
// tenants.<tenant>.mcp gateway_enabled, upstream_servers or allowed_upstreams
// fields: one warning naming those tenants, and none for a policy that sets
// none of them. The policy is merged from a base and a bundle, as the policy
// loader installs it, so the fields must survive the merge and the clone.
func TestSetPolicyWarnsOnReservedMCPGatewayFields(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	const warning = "reserved and not enforced yet (EDGE-101)"

	base := &config.SafetyPolicy{DefaultDecision: "deny", Tenants: map[string]config.TenantPolicy{
		"alpha": {MCP: config.MCPPolicy{GatewayEnabled: true}},
		"gamma": {MCP: config.MCPPolicy{AllowedUpstreams: []string{"tenant-tools"}}},
		"plain": {MCP: config.MCPPolicy{AllowTools: []string{"read"}}},
	}}
	bundle := &config.SafetyPolicy{Tenants: map[string]config.TenantPolicy{
		"alpha": {AllowTopics: []string{"job.*"}},
		"beta":  {MCP: config.MCPPolicy{UpstreamServers: []config.UpstreamServerConfig{{Name: "tenant-tools"}}}},
	}}
	srv := &server{}
	if err := srv.setPolicyWithInvariants(context.Background(), mergePolicies(base, bundle), nil, "cfg:reserved", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}
	if got := buf.String(); strings.Count(got, warning) != 1 || !strings.Contains(got, `tenants="[alpha beta gamma]"`) {
		t.Fatalf("want one reserved-field warning naming tenants alpha, beta and gamma; got %q", got)
	}

	buf.Reset()
	plain := &config.SafetyPolicy{DefaultDecision: "deny", Tenants: map[string]config.TenantPolicy{"plain": {}}}
	if err := srv.setPolicyWithInvariants(context.Background(), plain, nil, "cfg:plain", 0); err != nil {
		t.Fatalf("setPolicyWithInvariants: %v", err)
	}
	if got := buf.String(); strings.Contains(got, warning) {
		t.Fatalf("a policy without reserved fields logged %q", got)
	}
}
