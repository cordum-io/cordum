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
	Addr       string
	APIKey     string
	HTTPClient *http.Client
}

// NewGatewayClient creates a gateway API client used by MCP tool handlers.
func NewGatewayClient(addr, apiKey string, httpClient *http.Client) *GatewayClient {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "http://localhost:8081"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &GatewayClient{
		Addr:       addr,
		APIKey:     strings.TrimSpace(apiKey),
		HTTPClient: httpClient,
	}
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
		BaseURL:    client.Addr,
		APIKey:     client.APIKey,
		TenantID:   strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID")),
		HTTPClient: client.HTTPClient,
	})
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
