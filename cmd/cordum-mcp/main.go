package main

import (
	"flag"
	"fmt"
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
	defaultHTTPAddr    = ":8090"
)

func main() {
	buildinfo.Log("cordum-mcp")

	gatewayAddr := flag.String("addr", envOrDefault("CORDUM_GATEWAY_ADDR", defaultGatewayAddr), "Cordum API gateway address")
	apiKey := flag.String("api-key", strings.TrimSpace(os.Getenv("CORDUM_API_KEY")), "Cordum API key for gateway-backed handlers")
	gatewayAllowlist := flag.String("gateway-allowlist", strings.TrimSpace(os.Getenv("CORDUM_MCP_GATEWAY_ALLOWLIST")), "Comma-separated host/domain allowlist for outbound gateway calls")
	allowPrivateGateway := flag.Bool("allow-private-gateway", envBoolOrDefault("CORDUM_MCP_ALLOW_PRIVATE_GATEWAY", false), "Allow private/loopback gateway hosts (disabled by default)")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "per-request MCP handler timeout")
	flag.Parse()

	transportMode, httpAddr, cfgErr := resolveTransportConfig()
	if cfgErr != nil {
		log.Fatalf("transport config: %v", cfgErr)
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

	if transportMode == "http" {
		httpTransport := transport.(*mcp.HTTPTransport)
		mux := http.NewServeMux()
		mux.HandleFunc("/sse", httpTransport.HandleSSE)
		mux.HandleFunc("/message", httpTransport.HandleMessage)
		httpSrv := &http.Server{
			Addr:              httpAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("cordum-mcp listening on http %s (gateway=%s)", httpAddr, strings.TrimSpace(*gatewayAddr))
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("mcp http server failed: %v", err)
			}
		}()
	} else {
		log.Printf("cordum-mcp listening on stdio (gateway=%s)", strings.TrimSpace(*gatewayAddr))
	}

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
