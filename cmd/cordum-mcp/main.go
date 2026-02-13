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
	if err := mcptools.Register(toolRegistry, mcptools.NewGatewayClient(*gatewayAddr, *apiKey, httpClient)); err != nil {
		log.Fatalf("register mcp tools: %v", err)
	}
	if err := mcpresources.Register(resourceRegistry, mcpresources.NewGatewayClient(*gatewayAddr, *apiKey, httpClient)); err != nil {
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
