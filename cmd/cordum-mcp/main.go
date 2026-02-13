package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/mcp"
	mcpresources "github.com/cordum/cordum/core/mcp/resources"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
)

const (
	defaultGatewayAddr = "http://localhost:8081"
)

func main() {
	buildinfo.Log("cordum-mcp")

	gatewayAddr := flag.String("addr", envOrDefault("CORDUM_GATEWAY_ADDR", defaultGatewayAddr), "Cordum API gateway address")
	apiKey := flag.String("api-key", strings.TrimSpace(os.Getenv("CORDUM_API_KEY")), "Cordum API key for gateway-backed handlers")
	gatewayAllowlist := flag.String("gateway-allowlist", strings.TrimSpace(os.Getenv("CORDUM_MCP_GATEWAY_ALLOWLIST")), "Comma-separated host/domain allowlist for outbound gateway calls")
	allowPrivateGateway := flag.Bool("allow-private-gateway", envBoolOrDefault("CORDUM_MCP_ALLOW_PRIVATE_GATEWAY", false), "Allow private/loopback gateway hosts (disabled by default)")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "per-request MCP handler timeout")
	flag.Parse()

	transport := mcp.NewStdioTransport()
	defer func() {
		if err := transport.Close(); err != nil {
			log.Printf("mcp transport close failed: %v", err)
		}
	}()

	toolRegistry := mcp.NewToolRegistry()
	resourceRegistry := mcp.NewResourceRegistry()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	allowedHosts := splitCSV(*gatewayAllowlist)
	toolClient := mcptools.NewGatewayClient(*gatewayAddr, *apiKey, httpClient).
		WithAllowedHosts(allowedHosts).
		WithAllowPrivateHosts(*allowPrivateGateway)
	if err := mcptools.Register(toolRegistry, toolClient); err != nil {
		log.Fatalf("register mcp tools: %v", err)
	}
	resourceClient := mcpresources.NewGatewayClient(*gatewayAddr, *apiKey, httpClient).
		WithAllowedHosts(allowedHosts).
		WithAllowPrivateHosts(*allowPrivateGateway)
	if err := mcpresources.Register(resourceRegistry, resourceClient); err != nil {
		log.Fatalf("register mcp resources: %v", err)
	}

	server := mcp.NewServer(transport, toolRegistry, resourceRegistry, mcp.ServerConfig{
		Name:            "cordum",
		Version:         buildinfo.Version,
		ProtocolVersion: mcp.DefaultProtocolVersion,
		RequestTimeout:  *requestTimeout,
	})
	log.Printf("cordum-mcp listening on stdio (gateway=%s)", strings.TrimSpace(*gatewayAddr))
	if err := server.Serve(); err != nil {
		log.Fatalf("mcp server failed: %v", err)
	}
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
