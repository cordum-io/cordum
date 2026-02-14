package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/mcp"
	mcpresources "github.com/cordum/cordum/core/mcp/resources"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
)

type mcpGatewayConfig struct {
	Enabled   bool
	Transport string
	Port      int
	Raw       map[string]any
}

type mcpRuntimeState struct {
	startedAt        time.Time
	transport        string
	httpTransport    *mcp.HTTPTransport
	toolRegistry     *mcp.ToolRegistry
	resourceRegistry *mcp.ResourceRegistry
	server           *mcp.MCPServer
}

var gatewayMCPState sync.Map // map[*server]*mcpRuntimeState

func (s *server) registerMCPRoutes(mux *http.ServeMux) error {
	if s == nil || mux == nil {
		return nil
	}

	// Always expose MCP routes so clients get explicit disabled/unavailable responses
	// instead of startup-time 404s when MCP config loads after route registration.
	mux.HandleFunc("GET /mcp/sse", s.instrumented("/mcp/sse", s.mcpAuth(s.handleMCPSSE)))
	mux.HandleFunc("POST /mcp/message", s.instrumented("/mcp/message", s.mcpAuth(s.handleMCPMessage)))
	mux.HandleFunc("GET /mcp/status", s.instrumented("/mcp/status", s.mcpAuth(s.handleMCPStatus)))
	mux.HandleFunc("GET /api/v1/mcp/sse", s.instrumented("/api/v1/mcp/sse", s.mcpAuth(s.handleMCPSSE)))
	mux.HandleFunc("POST /api/v1/mcp/message", s.instrumented("/api/v1/mcp/message", s.mcpAuth(s.handleMCPMessage)))
	mux.HandleFunc("GET /api/v1/mcp/status", s.instrumented("/api/v1/mcp/status", s.mcpAuth(s.handleMCPStatus)))

	cfg := s.loadMCPConfig(context.Background())
	if !cfg.Enabled {
		logging.Info("api-gateway", "mcp runtime disabled by config")
		return nil
	}
	if cfg.Transport != "http" {
		logging.Info("api-gateway", "mcp http runtime disabled", "transport", cfg.Transport)
		return nil
	}

	transport := mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, mcp.DefaultHTTPResponseTimeout)
	toolRegistry := mcp.NewToolRegistry()
	resourceRegistry := mcp.NewResourceRegistry()
	toolRegistry.SetConfig(cfg.Raw)
	resourceRegistry.SetConfig(cfg.Raw)

	if err := mcptools.RegisterWithBridge(toolRegistry, s.newMCPServiceBridge()); err != nil {
		return fmt.Errorf("register mcp tools: %w", err)
	}
	if err := mcpresources.RegisterWithBridge(resourceRegistry, s.newMCPDataBridge()); err != nil {
		return fmt.Errorf("register mcp resources: %w", err)
	}

	mcpServer := mcp.NewServer(transport, toolRegistry, resourceRegistry, mcp.ServerConfig{
		Name:            "cordum",
		Version:         buildinfo.Version,
		ProtocolVersion: mcp.DefaultProtocolVersion,
		RequestTimeout:  30 * time.Second,
	})
	s.setMCPRuntime(&mcpRuntimeState{
		startedAt:        time.Now().UTC(),
		transport:        cfg.Transport,
		httpTransport:    transport,
		toolRegistry:     toolRegistry,
		resourceRegistry: resourceRegistry,
		server:           mcpServer,
	})
	go func() {
		if err := mcpServer.Serve(); err != nil {
			logging.Error("api-gateway", "mcp server loop failed", "error", err)
		}
	}()
	if s.shutdownCh != nil {
		go func() {
			<-s.shutdownCh
			if err := transport.Close(); err != nil {
				logging.Warn("api-gateway", "mcp transport close failed", "error", err)
			}
			s.clearMCPRuntime()
		}()
	}

	logging.Info(
		"api-gateway",
		"mcp routes enabled",
		"transport", cfg.Transport,
		"port", cfg.Port,
	)
	return nil
}

func (s *server) mcpAuth(next http.HandlerFunc) http.HandlerFunc {
	if next == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.auth == nil {
			next(w, r)
			return
		}
		authCtx, err := s.auth.AuthenticateHTTP(r)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, authCtx)
		r = r.WithContext(ctx)

		tenantID := tenantFromRequest(r)
		if tenantID == "" {
			writeErrorJSON(w, http.StatusForbidden, "tenant id required")
			return
		}
		if authCtx.Tenant != "" && !authCtx.AllowCrossTenant {
			if strings.TrimSpace(authCtx.Tenant) != tenantID {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
		next(w, r)
	}
}

func (s *server) loadMCPConfig(ctx context.Context) mcpGatewayConfig {
	cfg := mcpGatewayConfig{
		Enabled:   false,
		Transport: "http",
		Port:      0,
		Raw:       nil,
	}
	if s == nil || s.configSvc == nil {
		return cfg
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	effective, err := s.configSvc.Effective(cctx, "", "", "", "")
	if err != nil || effective == nil {
		return cfg
	}
	cfg.Raw = effective
	if enabled, ok := lookupBoolPath(effective, "mcp", "enabled"); ok {
		cfg.Enabled = enabled
	}
	if transport, ok := lookupStringPath(effective, "mcp", "transport"); ok && transport != "" {
		cfg.Transport = transport
	}
	if port := lookupIntPath(effective, "mcp", "port"); port > 0 {
		cfg.Port = port
	}
	return cfg
}

func (s *server) mcpHTTPTransport() *mcp.HTTPTransport {
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.transport != "http" || runtime.httpTransport == nil || runtime.httpTransport.IsClosed() {
		return nil
	}
	return runtime.httpTransport
}

func (s *server) handleMCPSSE(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "mcp http transport unavailable")
		return
	}
	transport.HandleSSE(w, r)
}

func (s *server) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "mcp http transport unavailable")
		return
	}
	transport.HandleMessage(w, r)
}

func (s *server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadMCPConfig(r.Context())
	resp := map[string]any{
		"running":           false,
		"connected_clients": 0,
		"uptime_seconds":    int64(0),
		"transport":         cfg.Transport,
		"enabled_tools":     0,
		"enabled_resources": 0,
	}
	if runtime := s.getMCPRuntime(); runtime != nil {
		running := runtime.server != nil
		if runtime.httpTransport != nil {
			running = running && !runtime.httpTransport.IsClosed()
			resp["connected_clients"] = runtime.httpTransport.ActiveSessionCount()
		}
		if !runtime.startedAt.IsZero() && running {
			resp["uptime_seconds"] = int64(time.Since(runtime.startedAt).Seconds())
		}
		if runtime.transport != "" {
			resp["transport"] = runtime.transport
		}
		if runtime.toolRegistry != nil {
			resp["enabled_tools"] = len(runtime.toolRegistry.List())
		}
		if runtime.resourceRegistry != nil {
			resp["enabled_resources"] = len(runtime.resourceRegistry.List()) + len(runtime.resourceRegistry.ListTemplates())
		}
		resp["running"] = running
	}
	writeJSON(w, resp)
}

func (s *server) setMCPRuntime(state *mcpRuntimeState) {
	if s == nil {
		return
	}
	if state == nil {
		gatewayMCPState.Delete(s)
		return
	}
	gatewayMCPState.Store(s, state)
}

func (s *server) getMCPRuntime() *mcpRuntimeState {
	if s == nil {
		return nil
	}
	raw, ok := gatewayMCPState.Load(s)
	if !ok {
		return nil
	}
	state, _ := raw.(*mcpRuntimeState)
	return state
}

func (s *server) clearMCPRuntime() {
	if s == nil {
		return
	}
	gatewayMCPState.Delete(s)
}

func mcpConfigTouched(data map[string]any) bool {
	if len(data) == 0 {
		return false
	}
	if _, ok := data["mcp"]; ok {
		return true
	}
	for key := range data {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "mcp.") {
			return true
		}
	}
	return false
}

func (s *server) reloadMCPConfig(ctx context.Context) {
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		return
	}
	cfg := s.loadMCPConfig(ctx)
	if runtime.toolRegistry != nil {
		runtime.toolRegistry.SetConfig(cfg.Raw)
	}
	if runtime.resourceRegistry != nil {
		runtime.resourceRegistry.SetConfig(cfg.Raw)
	}
	runtime.server.ReloadConfig(cfg.Raw)
}

func lookupBoolPath(data map[string]any, keys ...string) (bool, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	}
	return false, false
}

func lookupStringPath(data map[string]any, keys ...string) (string, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), true
	case []byte:
		return strings.TrimSpace(string(v)), true
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", raw)), true
	}
}

func lookupAnyPath(data map[string]any, keys ...string) (any, bool) {
	if data == nil || len(keys) == 0 {
		return nil, false
	}
	var cur any = data
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
