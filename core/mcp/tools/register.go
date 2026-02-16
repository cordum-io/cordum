package tools

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/mcp"
)

// GatewayClient provides a future extension point for tool handlers backed by gateway APIs.
type GatewayClient struct {
	Addr              string
	HTTPClient        *http.Client
	allowedHosts      []string
	allowPrivateHosts bool
	apiKey            string
}

// NewGatewayClient creates a gateway API client used by MCP tool handlers.
func NewGatewayClient(addr, apiKey string, httpClient *http.Client) *GatewayClient {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "http://localhost:8081"
	}
	if httpClient == nil {
		httpClient = mcp.SafeHTTPClient(10 * time.Second)
	}
	return &GatewayClient{
		Addr:       addr,
		apiKey:     strings.TrimSpace(apiKey),
		HTTPClient: httpClient,
	}
}

// WithAllowedHosts sets an optional host/domain allowlist for gateway calls.
func (c *GatewayClient) WithAllowedHosts(hosts []string) *GatewayClient {
	if c == nil {
		return nil
	}
	c.allowedHosts = append([]string{}, hosts...)
	return c
}

// WithAllowPrivateHosts enables loopback/private/link-local gateway hosts.
func (c *GatewayClient) WithAllowPrivateHosts(allow bool) *GatewayClient {
	if c == nil {
		return nil
	}
	c.allowPrivateHosts = allow
	return c
}

func (c *GatewayClient) authTokenValue() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.apiKey)
}

// Register wires MCP tool handlers into the registry with an HTTP bridge.
func Register(registry *mcp.ToolRegistry, client *GatewayClient) error {
	if registry == nil {
		return nil
	}
	if client == nil {
		return nil
	}
	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           client.Addr,
		TenantID:          strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID")),
		HTTPClient:        client.HTTPClient,
		AllowedHosts:      append([]string{}, client.allowedHosts...),
		AllowPrivateHosts: client.allowPrivateHosts,
	}.WithAuthToken(client.authTokenValue()))
	return mcp.RegisterAllTools(registry, bridge)
}

// RegisterWithBridge wires MCP tool handlers with a caller-provided bridge.
func RegisterWithBridge(registry *mcp.ToolRegistry, bridge mcp.ServiceBridge) error {
	if registry == nil {
		return nil
	}
	if bridge == nil {
		return nil
	}
	return mcp.RegisterAllTools(registry, bridge)
}
