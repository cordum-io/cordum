package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

	// outboundSigner, when non-nil, is forwarded to the underlying
	// HTTPServiceBridge so every gateway call carries ECDSA-P256
	// X-Cordum-* headers. See core/mcp/outbound. Wire via
	// WithOutboundSigner at stdio boot.
	outboundSigner mcp.OutboundSigner
	// outboundAgentID is stamped into the signed request. Defaults to
	// the resolved tenant in the bridge when empty.
	outboundAgentID string

	// outboundAuditor, when non-nil, is forwarded to the underlying
	// HTTPServiceBridge so every outbound gateway call produces a
	// terminal mcp.tool_outbound_invocation SIEMEvent. Wire via
	// WithOutboundAuditor at stdio boot. Paired with the signer but
	// independently nilable — dev deploys without SIEM still work.
	outboundAuditor mcp.OutboundInvocationAuditor
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

// WithOutboundSigner installs the signer that Register forwards to the
// underlying HTTPServiceBridge. When non-nil, every tools/call the
// stdio MCP issues against the gateway carries the 6 X-Cordum-*
// signature headers. Nil is accepted (noop — unsigned calls).
// agentID is stamped into the signed request's Agent-Id header; empty
// falls back to the bridge's tenant.
func (c *GatewayClient) WithOutboundSigner(signer mcp.OutboundSigner, agentID string) *GatewayClient {
	if c == nil {
		return nil
	}
	c.outboundSigner = signer
	c.outboundAgentID = strings.TrimSpace(agentID)
	return c
}

// WithOutboundAuditor installs the invocation auditor that Register
// forwards to the underlying HTTPServiceBridge. Every gateway call
// brackets client.Do with StartRequest/FinishRequest so the terminal
// status, latency, and redacted body land on a
// mcp.tool_outbound_invocation SIEMEvent. Nil is accepted (noop — no
// outbound audit emission). cmd/cordum-mcp wires an adapter over the
// shared ToolInvocationAuditor.
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
	// Wire the optional outbound signer before registering handlers —
	// every subsequent tools/call issued through the bridge picks up
	// the 6 X-Cordum-* headers. Absent-signer deployments keep the
	// pre-signing behaviour (unsigned calls + one boot WARN emitted by
	// the caller wiring in cmd/cordum-mcp).
	if client.outboundSigner != nil {
		bridge.WithOutboundSigner(client.outboundSigner, client.outboundAgentID)
	}
	if client.outboundAuditor != nil {
		bridge.WithOutboundInvocationAuditor(client.outboundAuditor)
	}
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

// gatewayAgentResponse mirrors the agent identity payload the gateway
// returns on GET /api/v1/agents/{id}. Only fields the filter needs.
type gatewayAgentResponse struct {
	ID                  string   `json:"id"`
	AllowedTools        []string `json:"allowed_tools"`
	RiskTier            string   `json:"risk_tier"`
	DataClassifications []string `json:"data_classifications"`
	Status              string   `json:"status"`
}

// FetchAgentIdentity resolves an agent identity by ID via the gateway's
// /api/v1/agents/{id} endpoint. Returns nil when the identity is
// absent or revoked/suspended so callers can fail-closed uniformly.
func (c *GatewayClient) FetchAgentIdentity(ctx context.Context, agentID string) (*mcp.AgentIdentity, error) {
	if c == nil {
		return nil, fmt.Errorf("gateway client unavailable")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id required")
	}
	base := strings.TrimRight(c.Addr, "/")
	target, err := url.Parse(base + "/api/v1/agents/" + url.PathEscape(agentID))
	if err != nil {
		return nil, fmt.Errorf("build agent url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build agent request: %w", err)
	}
	if token := c.authTokenValue(); token != "" {
		req.Header.Set("X-API-Key", token)
	}
	if tenant := strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID")); tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch agent identity: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch agent identity: http %d", resp.StatusCode)
	}
	var payload gatewayAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode agent identity: %w", err)
	}
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if status == "revoked" || status == "suspended" {
		return nil, nil
	}
	return &mcp.AgentIdentity{
		ID:                  payload.ID,
		AllowedTools:        append([]string{}, payload.AllowedTools...),
		RiskTier:            payload.RiskTier,
		DataClassifications: append([]string{}, payload.DataClassifications...),
	}, nil
}
