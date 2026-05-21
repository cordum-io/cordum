package config

// MCPPolicy captures allow/deny controls for Model Context Protocol servers,
// tools, resources, and action-layer requests. The gateway-specific fields in
// this struct are deliberately config-only contracts; EDGE-101 owns runtime
// registry persistence and validation for upstream server entries.
type MCPPolicy struct {
	AllowServers   []string `json:"allow_servers" yaml:"allow_servers"`
	DenyServers    []string `json:"deny_servers" yaml:"deny_servers"`
	AllowTools     []string `json:"allow_tools" yaml:"allow_tools"`
	DenyTools      []string `json:"deny_tools" yaml:"deny_tools"`
	AllowResources []string `json:"allow_resources" yaml:"allow_resources"`
	DenyResources  []string `json:"deny_resources" yaml:"deny_resources"`
	AllowActions   []string `json:"allow_actions" yaml:"allow_actions"`
	DenyActions    []string `json:"deny_actions" yaml:"deny_actions"`
	// GatewayEnabled gates the per-tenant EDGE-100 cross-agent MCP Gateway
	// upstream-forwarding family of routes (/api/v1/mcp/gateway/upstream/*).
	// Default false — gateway is disabled-by-default per EDGE-100 DoD #1;
	// the health and config routes remain reachable regardless so operators
	// can probe a disabled deployment. EDGE-101 populates the upstream
	// registry consumed when this flag is true.
	GatewayEnabled bool `json:"gateway_enabled" yaml:"gateway_enabled"`
	// UpstreamServers is the config-file bootstrap contract for approved MCP
	// upstreams. EDGE-101 imports/validates these into the runtime registry;
	// this layer never resolves secrets or performs forwarding.
	UpstreamServers []UpstreamServerConfig `json:"upstream_servers,omitempty" yaml:"upstream_servers,omitempty"`
	// AllowedUpstreams names the managed-settings allowlist enforced in
	// enterprise-strict mode by EDGE-101. Empty means no unmanaged upstreams
	// are approved in strict mode.
	AllowedUpstreams []string `json:"allowed_upstreams,omitempty" yaml:"allowed_upstreams,omitempty"`
}

// UpstreamServerConfig is the minimal config-layer contract for an approved
// upstream MCP server. Runtime registry metadata (tenant scope, timestamps,
// backup records, validation state) belongs to EDGE-101's edge registry.
type UpstreamServerConfig struct {
	Name          string            `json:"name" yaml:"name"`
	Transport     string            `json:"transport" yaml:"transport"`
	Endpoint      string            `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Command       []string          `json:"command,omitempty" yaml:"command,omitempty"`
	AuthSecretRef string            `json:"auth_secret_ref,omitempty" yaml:"auth_secret_ref,omitempty"`
	Labels        map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Risk          string            `json:"risk,omitempty" yaml:"risk,omitempty"`
	Enabled       bool              `json:"enabled" yaml:"enabled"`
}
