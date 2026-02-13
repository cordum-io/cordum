package resources

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/mcp"
)

// GatewayClient provides a future extension point for resource handlers backed by gateway APIs.
type GatewayClient struct {
	Addr       string
	APIKey     string
	HTTPClient *http.Client
}

// NewGatewayClient creates a gateway API client used by MCP resource handlers.
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

// Register wires MCP resource handlers into the registry with an HTTP data bridge.
func Register(registry *mcp.ResourceRegistry, client *GatewayClient) error {
	if registry == nil {
		return nil
	}
	if client == nil {
		return nil
	}
	bridge := mcp.NewHTTPDataBridge(mcp.HTTPDataBridgeConfig{
		BaseURL:    client.Addr,
		APIKey:     client.APIKey,
		TenantID:   strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID")),
		HTTPClient: client.HTTPClient,
	})
	return mcp.RegisterAllResources(registry, bridge)
}

// RegisterWithBridge wires MCP resource handlers with a caller-provided bridge.
func RegisterWithBridge(registry *mcp.ResourceRegistry, bridge mcp.DataBridge) error {
	if registry == nil {
		return nil
	}
	if bridge == nil {
		return nil
	}
	return mcp.RegisterAllResources(registry, bridge)
}
