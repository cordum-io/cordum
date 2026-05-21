package config

import (
	"encoding/json"
	"testing"
)

func TestMCPConfigUpstreamServers(t *testing.T) {
	t.Run("json round trip", func(t *testing.T) {
		raw := []byte(`{
			"gateway_enabled": true,
			"upstream_servers": [
				{
					"name": "tenant-tools",
					"transport": "http",
					"endpoint": "https://mcp.example.com/sse",
					"auth_secret_ref": "secret://vault/mcp/tenant-tools",
					"labels": {"team": "platform", "tier": "prod"},
					"risk": "high",
					"enabled": true
				},
				{
					"name": "local-indexer",
					"transport": "stdio",
					"command": ["cordum-mcp-indexer", "--stdio"],
					"risk": "medium",
					"enabled": false
				}
			],
			"allowed_upstreams": ["tenant-tools", "local-indexer"]
		}`)

		var policy MCPPolicy
		if err := json.Unmarshal(raw, &policy); err != nil {
			t.Fatalf("unmarshal MCPPolicy: %v", err)
		}

		if !policy.GatewayEnabled {
			t.Fatalf("GatewayEnabled = false, want true")
		}
		if got := len(policy.UpstreamServers); got != 2 {
			t.Fatalf("len(UpstreamServers) = %d, want 2", got)
		}
		httpUpstream := policy.UpstreamServers[0]
		if httpUpstream.Name != "tenant-tools" {
			t.Fatalf("http upstream Name = %q, want tenant-tools", httpUpstream.Name)
		}
		if httpUpstream.Transport != "http" {
			t.Fatalf("http upstream Transport = %q, want http", httpUpstream.Transport)
		}
		if httpUpstream.Endpoint != "https://mcp.example.com/sse" {
			t.Fatalf("http upstream Endpoint = %q", httpUpstream.Endpoint)
		}
		if httpUpstream.AuthSecretRef != "secret://vault/mcp/tenant-tools" {
			t.Fatalf("http upstream AuthSecretRef = %q", httpUpstream.AuthSecretRef)
		}
		if httpUpstream.Labels["team"] != "platform" || httpUpstream.Labels["tier"] != "prod" {
			t.Fatalf("http upstream Labels = %#v", httpUpstream.Labels)
		}
		if httpUpstream.Risk != "high" {
			t.Fatalf("http upstream Risk = %q, want high", httpUpstream.Risk)
		}
		if !httpUpstream.Enabled {
			t.Fatalf("http upstream Enabled = false, want true")
		}

		stdioUpstream := policy.UpstreamServers[1]
		if stdioUpstream.Name != "local-indexer" {
			t.Fatalf("stdio upstream Name = %q, want local-indexer", stdioUpstream.Name)
		}
		if stdioUpstream.Transport != "stdio" {
			t.Fatalf("stdio upstream Transport = %q, want stdio", stdioUpstream.Transport)
		}
		if len(stdioUpstream.Command) != 2 || stdioUpstream.Command[0] != "cordum-mcp-indexer" ||
			stdioUpstream.Command[1] != "--stdio" {
			t.Fatalf("stdio upstream Command = %#v", stdioUpstream.Command)
		}
		if stdioUpstream.Enabled {
			t.Fatalf("stdio upstream Enabled = true, want false")
		}
		if len(policy.AllowedUpstreams) != 2 ||
			policy.AllowedUpstreams[0] != "tenant-tools" ||
			policy.AllowedUpstreams[1] != "local-indexer" {
			t.Fatalf("AllowedUpstreams = %#v", policy.AllowedUpstreams)
		}
	})

	t.Run("defaults unset slices empty", func(t *testing.T) {
		var policy MCPPolicy
		if err := json.Unmarshal([]byte(`{}`), &policy); err != nil {
			t.Fatalf("unmarshal empty MCPPolicy: %v", err)
		}
		if len(policy.UpstreamServers) != 0 {
			t.Fatalf("default UpstreamServers len = %d, want 0", len(policy.UpstreamServers))
		}
		if len(policy.AllowedUpstreams) != 0 {
			t.Fatalf("default AllowedUpstreams len = %d, want 0", len(policy.AllowedUpstreams))
		}
	})
}
