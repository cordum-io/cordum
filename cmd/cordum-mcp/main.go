package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/mcp"
	mcpoutbound "github.com/cordum/cordum/core/mcp/outbound"
	mcpresources "github.com/cordum/cordum/core/mcp/resources"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
)

const (
	defaultGatewayAddr = "http://localhost:8081"
	// defaultHTTPAddr is loopback-only: HTTP mode acts with this process's
	// gateway API key, so exposing it beyond the host takes an explicit
	// MCP_HTTP_ADDR plus --http-auth-token (or --allow-unauthenticated-http).
	defaultHTTPAddr = "127.0.0.1:8090"
)

func main() {
	logging.Init("mcp-server")
	buildinfo.Log("cordum-mcp")

	gatewayAddr := flag.String("addr", envOrDefault("CORDUM_GATEWAY_ADDR", defaultGatewayAddr), "Cordum API gateway address")
	apiKey := flag.String("api-key", strings.TrimSpace(os.Getenv("CORDUM_API_KEY")), "Cordum API key for gateway-backed handlers")
	gatewayAllowlist := flag.String("gateway-allowlist", strings.TrimSpace(os.Getenv("CORDUM_MCP_GATEWAY_ALLOWLIST")), "Comma-separated host/domain allowlist for outbound gateway calls")
	allowPrivateGateway := flag.Bool("allow-private-gateway", envBoolOrDefault("CORDUM_MCP_ALLOW_PRIVATE_GATEWAY", false), "Allow private/loopback gateway hosts (disabled by default)")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "per-request MCP handler timeout")
	agentID := flag.String("agent-id", strings.TrimSpace(os.Getenv("CORDUM_MCP_AGENT_ID")), "Agent identity ID to scope this MCP session (resolved at boot)")
	httpAuthToken := flag.String("http-auth-token", strings.TrimSpace(os.Getenv("CORDUM_MCP_HTTP_AUTH_TOKEN")), "HTTP mode: bearer token every /sse and /message request must send (prefer the CORDUM_MCP_HTTP_AUTH_TOKEN env)")
	allowUnauthenticatedHTTP := flag.Bool("allow-unauthenticated-http", false, "HTTP mode: serve a non-loopback MCP_HTTP_ADDR without --http-auth-token (anyone who can reach it acts with this server's API key)")
	flag.Parse()

	transportMode, httpAddr, cfgErr := resolveTransportConfig()
	if cfgErr == nil && transportMode == "http" {
		cfgErr = checkHTTPExposure(httpAddr, strings.TrimSpace(*httpAuthToken), *allowUnauthenticatedHTTP)
	}
	if cfgErr != nil {
		slog.Error("transport config failed", "error", cfgErr)
		os.Exit(1)
	}

	var transport mcp.Transport
	switch transportMode {
	case "stdio":
		transport = mcp.NewStdioTransport()
	case "http":
		transport = mcp.NewHTTPTransport(0, *requestTimeout)
	}
	defer func() {
		if err := transport.Close(); err != nil {
			slog.Error("mcp transport close failed", "error", err)
		}
	}()

	toolRegistry := mcp.NewToolRegistry()
	resourceRegistry := mcp.NewResourceRegistry()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	allowedHosts := splitCSV(*gatewayAllowlist)

	// Outbound-signing wiring per epic rail "Every MCP tool call signed,
	// scoped, and auditable". Load the ECDSA P-256 signer from env;
	// when absent emit one boot WARN and continue with unsigned calls
	// so legacy deployments don't instantly break on upgrade. The env
	// variables + trust-store format are documented in
	// docs/mcp/outbound-signing.md.
	outboundSigner := loadOutboundSigner()
	signerAgentID := strings.TrimSpace(os.Getenv("CORDUM_MCP_OUTBOUND_AGENT_ID"))
	if signerAgentID == "" {
		signerAgentID = strings.TrimSpace(*agentID)
	}

	// Outbound invocation auditor — brackets every gateway round-trip
	// with mcp.tool_outbound_invocation SIEMEvents including terminal
	// status, latency, and redacted body. The stdio process has no
	// SIEM exporter of its own (the gateway IS the SIEM terminus), so
	// we emit via slog as structured JSON; operators pipe stderr to
	// the same log-aggregation target that collects the gateway's
	// audit chain. Agent + tenant + serverID come from the same env
	// the signer reads so signed + audited events are always
	// identity-consistent.
	outboundTenantID := strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID"))
	outboundServerID := strings.TrimSpace(*gatewayAddr)
	// Prefer a NATS-backed audit sender when CORDUM_NATS_URL is set so
	// mcp.tool_outbound_invocation events flow to the gateway's
	// NATSAuditConsumer → tenant audit chain → /api/v1/mcp/outbound.
	// Fallback to the stderr sender for dev deploys + CI where NATS
	// is out of reach. See handlers_mcp_outbound_integration_test.go
	// for the end-to-end wiring proof.
	auditSender := resolveAuditSender(os.Getenv("CORDUM_NATS_URL"))
	invocationAuditor := mcp.NewToolInvocationAuditor(auditSender, mcp.DefaultRedactor())
	outboundAuditor := mcp.NewToolInvocationOutboundAuditor(
		invocationAuditor,
		signerAgentID,
		outboundTenantID,
		outboundServerID,
	)

	toolClient := mcptools.NewGatewayClient(*gatewayAddr, *apiKey, httpClient).
		WithAllowedHosts(allowedHosts).
		WithAllowPrivateHosts(*allowPrivateGateway).
		WithOutboundSigner(outboundSigner, signerAgentID).
		WithOutboundAuditor(outboundAuditor)
	if err := mcptools.Register(toolRegistry, toolClient); err != nil {
		slog.Error("register mcp tools failed", "error", err)
		os.Exit(1)
	}
	resourceClient := mcpresources.NewGatewayClient(*gatewayAddr, *apiKey, httpClient).
		WithAllowedHosts(allowedHosts).
		WithAllowPrivateHosts(*allowPrivateGateway).
		WithOutboundSigner(outboundSigner, signerAgentID).
		WithOutboundAuditor(outboundAuditor)
	if err := mcpresources.Register(resourceRegistry, resourceClient); err != nil {
		slog.Error("register mcp resources failed", "error", err)
		os.Exit(1)
	}
	slog.Info("mcp outbound invocation auditor enabled",
		"event_type", "mcp.tool_outbound_invocation",
		"server_id", outboundServerID,
		"agent_id", signerAgentID,
	)

	if id := strings.TrimSpace(*agentID); id != "" {
		// Scope enforcement only makes sense when we actually resolved an
		// identity to enforce against. Keeps the legacy dev-mode path
		// working for users running stdio without --agent-id.
		toolRegistry.SetScopeEnforcement(true)
		stdio, ok := transport.(*mcp.StdioTransport)
		if ok {
			bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
			identity, err := toolClient.FetchAgentIdentity(bootCtx, id)
			bootCancel()
			if err != nil {
				slog.Error("cordum-mcp: resolve agent identity failed, refusing to start",
					"agent_id", id, "error", err)
				os.Exit(1)
			}
			if identity == nil {
				slog.Error("cordum-mcp: agent identity not found or revoked, refusing to start",
					"agent_id", id)
				os.Exit(1)
			}
			stdio.SetDefaultIdentity(identity)
			slog.Info("cordum-mcp: bound to agent identity",
				"agent_id", identity.ID,
				"risk_tier", identity.RiskTier,
				"allowed_tools", len(identity.AllowedTools),
			)
		}
	}

	// Register first-party prompts (draft_safety_rule, explain_denial,
	// summarize_approvals, policy_migration_helper) so the stdio MCP
	// server exposes them in lockstep with the HTTP gateway. Without
	// this, a Claude Code / Cursor client connected via stdio would
	// see prompts/list empty even though the backend has them.
	promptRegistry := mcp.NewPromptRegistry()
	if err := mcp.RegisterAllPrompts(promptRegistry); err != nil {
		slog.Error("cordum-mcp: prompt registration failed; prompts/list will be empty", "error", err)
	}
	server := mcp.NewServer(transport, toolRegistry, resourceRegistry, mcp.ServerConfig{
		Name:            "cordum",
		Version:         buildinfo.Version,
		ProtocolVersion: mcp.DefaultProtocolVersion,
		RequestTimeout:  *requestTimeout,
	}).WithPrompts(promptRegistry)

	if transportMode == "http" {
		httpTransport := transport.(*mcp.HTTPTransport)
		mux := http.NewServeMux()
		token := strings.TrimSpace(*httpAuthToken)
		mux.HandleFunc("/sse", requireBearer(token, httpTransport.HandleSSE))
		mux.HandleFunc("/message", requireBearer(token, httpTransport.HandleMessage))
		httpSrv := &http.Server{
			Addr:              httpAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			slog.Info("cordum-mcp listening", "transport", "http", "addr", httpAddr, "gateway", strings.TrimSpace(*gatewayAddr), "inbound_auth", token != "")
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("mcp http server failed", "error", err)
				os.Exit(1)
			}
		}()
	} else {
		slog.Info("cordum-mcp listening", "transport", "stdio", "gateway", strings.TrimSpace(*gatewayAddr))
	}

	if err := server.Serve(); err != nil {
		slog.Error("mcp server failed", "error", err)
		os.Exit(1)
	}
}

// loadOutboundSigner reads CORDUM_MCP_OUTBOUND_SIGNING_KEY(_PATH) +
// KEY_ID from env and returns a configured signer. Returns nil with
// one WARN log when no key is configured so legacy deployments keep
// working through the upgrade window. A misconfigured key (wrong
// curve, malformed PEM) is a hard error — better to refuse to boot
// than silently issue unsigned calls that operators think are signed.
func loadOutboundSigner() mcp.OutboundSigner {
	key, keyID, err := mcpoutbound.LoadPrivateKeyFromEnv()
	if err != nil {
		if errors.Is(err, mcpoutbound.ErrSigningKeyNotConfigured) {
			slog.Warn("cordum-mcp: outbound signing DISABLED — no signing key configured. " +
				"Set CORDUM_MCP_OUTBOUND_SIGNING_KEY(_PATH) + CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID to enable. " +
				"See docs/mcp/outbound-signing.md")
			return nil
		}
		// Hard refuse on parse errors — QA rejection flagged the
		// fail-open risk where a malformed signer would silently strip
		// signatures. Fail-closed at boot is safer.
		slog.Error("cordum-mcp: outbound signing key misconfigured, refusing to start", "error", err)
		os.Exit(1)
	}
	signer, err := mcpoutbound.NewSigner(key, keyID)
	if err != nil {
		slog.Error("cordum-mcp: signer construction failed, refusing to start", "error", err)
		os.Exit(1)
	}
	slog.Info("cordum-mcp: outbound signing enabled", "key_id", signer.KeyID())
	return signer
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// resolveTransportConfig reads MCP_TRANSPORT and MCP_HTTP_ADDR from env vars
// and returns the validated transport mode and HTTP listen address.
func resolveTransportConfig() (mode string, httpAddr string, err error) {
	mode = strings.ToLower(strings.TrimSpace(envOrDefault("MCP_TRANSPORT", "stdio")))
	httpAddr = envOrDefault("MCP_HTTP_ADDR", defaultHTTPAddr)
	switch mode {
	case "stdio", "", "http":
		if mode == "" {
			mode = "stdio"
		}
		return mode, httpAddr, nil
	default:
		return "", "", fmt.Errorf("unsupported MCP_TRANSPORT=%q (valid: stdio, http)", mode)
	}
}

// checkHTTPExposure refuses HTTP mode on a non-loopback address without an
// inbound token: every caller would act with this server's gateway API key.
// allowUnauthenticated overrides that with a loud warning.
func checkHTTPExposure(addr, token string, allowUnauthenticated bool) error {
	if token != "" || isLoopbackListenAddr(addr) {
		return nil
	}
	if !allowUnauthenticated {
		return fmt.Errorf("refusing to serve MCP over HTTP on non-loopback address %q without --http-auth-token (or CORDUM_MCP_HTTP_AUTH_TOKEN); pass --allow-unauthenticated-http to override", addr)
	}
	slog.Warn("cordum-mcp: SERVING MCP OVER HTTP WITHOUT AUTHENTICATION on a non-loopback address; anyone who can reach it acts with this server's Cordum API key",
		"addr", addr, "flag", "--allow-unauthenticated-http")
	return nil
}

// isLoopbackListenAddr reports whether a host:port listen address binds only
// loopback. An empty host binds every interface, and hostnames other than
// localhost may resolve anywhere, so both count as exposed.
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireBearer wraps h so each request must carry "Authorization: Bearer
// <token>". Digests are compared in constant time, so neither the token's
// bytes nor its length leak. An empty token disables the check.
func requireBearer(token string, h http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return h
	}
	want := sha256.Sum256([]byte(token))
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, got, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		sum := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cordum-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// stderrAuditSender is the stdio MCP's minimal audit.AuditSender. The
// stdio process has no SIEM exporter of its own — the gateway IS the
// SIEM terminus for inbound audit — but outbound MCP calls originate
// HERE and need a terminal event. Writing structured JSON to stderr
// lets operators pipe this through journald / Fluent Bit / Vector to
// the same aggregation target as the gateway's audit hash chain without
// needing a second audit transport in this process.
type stderrAuditSender struct{}

func newStderrAuditSender() audit.AuditSender {
	return stderrAuditSender{}
}

// resolveAuditSender picks the best available sender for the process.
// When CORDUM_NATS_URL is set and the NATS dial succeeds, events flow
// via audit.NewNATSAuditPublisher → sys.audit.export → gateway
// NATSAuditConsumer → tenant audit chain → /api/v1/mcp/outbound.
// When NATS is unavailable or the env var is unset, the stderr
// placeholder stays in place so dev/CI deploys continue to work.
func resolveAuditSender(natsURL string) audit.AuditSender {
	natsURL = strings.TrimSpace(natsURL)
	if natsURL == "" {
		return newStderrAuditSender()
	}
	natsBus, err := bus.NewNatsBus(natsURL)
	if err != nil {
		slog.Warn("cordum-mcp: NATS audit sender unavailable, falling back to stderr",
			"error", err, "nats_url", natsURL)
		return newStderrAuditSender()
	}
	// Fallback buffers events when NATS publish fails mid-run so the
	// audit trail degrades gracefully rather than silently dropping.
	fallback, _ := audit.NewExporterFromEnv()
	if fallback == nil {
		fallback = audit.NewBufferedExporter(nil)
	}
	slog.Info("cordum-mcp: NATS audit sender enabled",
		"subject", "sys.audit.export", "nats_url", natsURL)
	return audit.NewNATSAuditPublisher(natsBus, fallback)
}

func (stderrAuditSender) Send(event audit.SIEMEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Warn("audit event marshal failed", "error", err, "event_type", event.EventType)
		return
	}
	slog.Info("audit.event", "event", json.RawMessage(payload))
}

func (stderrAuditSender) Close() error { return nil }

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if entry := strings.TrimSpace(part); entry != "" {
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
