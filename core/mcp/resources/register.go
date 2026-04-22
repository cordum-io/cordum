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
	Addr              string
	HTTPClient        *http.Client
	allowedHosts      []string
	allowPrivateHosts bool
	apiKey            string

	// outboundSigner is forwarded to the underlying HTTPDataBridge so
	// every read against the gateway carries the 6 X-Cordum-* headers
	// when enabled. Symmetric with tools.GatewayClient so a stdio MCP
	// server signs ALL outbound calls, not just the mutating ones.
	outboundSigner  mcp.OutboundSigner
	outboundAgentID string

	// outboundAuditor installs the invocation auditor forwarded to
	// the HTTPDataBridge. See tools.GatewayClient.WithOutboundAuditor
	// — same semantics, same wiring, so both halves of the stdio
	// MCP's outbound surface land events on mcp.tool_outbound_invocation.
	outboundAuditor mcp.OutboundInvocationAuditor
}

// NewGatewayClient creates a gateway API client used by MCP resource handlers.
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

// WithOutboundSigner installs the signer that Register forwards to the
// underlying HTTPDataBridge. Mirrors tools.GatewayClient — same field
// semantics so both halves of the stdio MCP's outbound surface stay
// symmetric. Nil signer is a noop.
func (c *GatewayClient) WithOutboundSigner(signer mcp.OutboundSigner, agentID string) *GatewayClient {
	if c == nil {
		return nil
	}
	c.outboundSigner = signer
	c.outboundAgentID = strings.TrimSpace(agentID)
	return c
}

// WithOutboundAuditor installs the invocation auditor that Register
// forwards to the underlying HTTPDataBridge. See
// tools.GatewayClient.WithOutboundAuditor for the full contract.
func (c *GatewayClient) WithOutboundAuditor(auditor mcp.OutboundInvocationAuditor) *GatewayClient {
	if c == nil {
		return nil
	}
	c.outboundAuditor = auditor
	return c
}

func (c *GatewayClient) authTokenValue() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.apiKey)
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
		BaseURL:           client.Addr,
		TenantID:          strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID")),
		HTTPClient:        client.HTTPClient,
		AllowedHosts:      append([]string{}, client.allowedHosts...),
		AllowPrivateHosts: client.allowPrivateHosts,
	}.WithAuthToken(client.authTokenValue()))
	if client.outboundSigner != nil {
		bridge.WithOutboundSigner(client.outboundSigner, client.outboundAgentID)
	}
	if client.outboundAuditor != nil {
		bridge.WithOutboundInvocationAuditor(client.outboundAuditor)
	}
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
