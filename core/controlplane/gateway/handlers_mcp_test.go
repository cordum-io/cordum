package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
)

type mcpTestAuth struct {
	apiKey string
	tenant string
}

func (a mcpTestAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	if strings.TrimSpace(r.Header.Get("X-API-Key")) != a.apiKey {
		return nil, errors.New("unauthorized")
	}
	return &auth.AuthContext{
		APIKey:      a.apiKey,
		Tenant:      a.tenant,
		PrincipalID: "tester",
		Role:        "admin",
		AuthSource:  auth.AuthSourceAPIKey,
	}, nil
}

func (a mcpTestAuth) AuthenticateGRPC(context.Context) (*auth.AuthContext, error) {
	return &auth.AuthContext{Tenant: a.tenant}, nil
}

func (a mcpTestAuth) RequireRole(*http.Request, ...string) error { return nil }

func (a mcpTestAuth) ResolveTenant(_ *http.Request, requested, fallback string) (string, error) {
	if strings.TrimSpace(requested) != "" {
		return requested, nil
	}
	return fallback, nil
}

func (a mcpTestAuth) RequireTenantAccess(*http.Request, string) error { return nil }

func (a mcpTestAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

func TestRegisterMCPRoutesEnforcesAuthAndHandlesPing(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() {
		close(s.shutdownCh)
	})

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	unauthReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	unauthReq.Header.Set("Content-Type", "application/json")
	unauthReq.Header.Set("X-Tenant-ID", "default")
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatalf("unauthorized request failed: %v", err)
	}
	defer func() { _ = unauthResp.Body.Close() }()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthorized request, got %d", unauthResp.StatusCode)
	}

	authReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	authReq.Header.Set("Content-Type", "application/json")
	authReq.Header.Set("X-API-Key", "test-key")
	authReq.Header.Set("X-Tenant-ID", "default")
	authResp, err := http.DefaultClient.Do(authReq)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	defer func() { _ = authResp.Body.Close() }()
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for authorized request, got %d", authResp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(authResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if _, ok := payload["result"]; !ok {
		t.Fatalf("expected result field in response: %+v", payload)
	}

	removedAliasReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/api/v1/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	removedAliasReq.Header.Set("Content-Type", "application/json")
	removedAliasReq.Header.Set("X-API-Key", "test-key")
	removedAliasReq.Header.Set("X-Tenant-ID", "default")
	aliasResp, err := http.DefaultClient.Do(removedAliasReq)
	if err != nil {
		t.Fatalf("removed alias request failed: %v", err)
	}
	defer func() { _ = aliasResp.Body.Close() }()
	if aliasResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for removed /api/v1/mcp/message alias, got %d", aliasResp.StatusCode)
	}
}

func TestRegisterMCPRoutesStatusEndpoint(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() {
		close(s.shutdownCh)
	})

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	sseReq, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/mcp/sse", nil)
	sseReq.Header.Set("X-API-Key", "test-key")
	sseReq.Header.Set("X-Tenant-ID", "default")
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("open sse failed: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from sse endpoint, got %d", sseResp.StatusCode)
	}
	reader := bufio.NewReader(sseResp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read sse prelude line1: %v", err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read sse prelude line2: %v", err)
	}

	statusReq, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/mcp/status", nil)
	statusReq.Header.Set("X-API-Key", "test-key")
	statusReq.Header.Set("X-Tenant-ID", "default")
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer func() { _ = statusResp.Body.Close() }()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from status endpoint, got %d", statusResp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode status payload: %v", err)
	}
	if running, ok := payload["running"].(bool); !ok || !running {
		t.Fatalf("expected running=true in status payload, got %#v", payload["running"])
	}
	if clients, ok := payload["connected_clients"].(float64); !ok || clients < 1 {
		t.Fatalf("expected connected_clients >= 1, got %#v", payload["connected_clients"])
	}
	if transport, ok := payload["transport"].(string); !ok || transport != "http" {
		t.Fatalf("expected transport=http, got %#v", payload["transport"])
	}
	if _, ok := payload["enabled_tools"].(float64); !ok {
		t.Fatalf("expected enabled_tools field in status payload")
	}
	if _, ok := payload["enabled_resources"].(float64); !ok {
		t.Fatalf("expected enabled_resources field in status payload")
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/api/v1/mcp/sse"},
		{method: http.MethodPost, path: "/api/v1/mcp/message", body: `{"jsonrpc":"2.0","id":3,"method":"ping"}`},
		{method: http.MethodGet, path: "/api/v1/mcp/status"},
	} {
		var body *strings.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		} else {
			body = strings.NewReader("")
		}
		aliasReq, _ := http.NewRequest(tc.method, httpSrv.URL+tc.path, body)
		aliasReq.Header.Set("Content-Type", "application/json")
		aliasReq.Header.Set("X-API-Key", "test-key")
		aliasReq.Header.Set("X-Tenant-ID", "default")
		aliasResp, err := http.DefaultClient.Do(aliasReq)
		if err != nil {
			t.Fatalf("removed alias request %s %s failed: %v", tc.method, tc.path, err)
		}
		func() {
			defer func() { _ = aliasResp.Body.Close() }()
			if aliasResp.StatusCode != http.StatusNotFound {
				t.Fatalf("expected 404 from removed alias %s %s, got %d", tc.method, tc.path, aliasResp.StatusCode)
			}
		}()
	}
}

func TestRegisterMCPRoutesStatusEndpointWhenMCPDisabled(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	statusReq, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/mcp/status", nil)
	statusReq.Header.Set("X-API-Key", "test-key")
	statusReq.Header.Set("X-Tenant-ID", "default")
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer func() { _ = statusResp.Body.Close() }()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from status endpoint when disabled, got %d", statusResp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode status payload: %v", err)
	}
	if running, ok := payload["running"].(bool); !ok || running {
		t.Fatalf("expected running=false in disabled status payload, got %#v", payload["running"])
	}

	messageReq, _ := http.NewRequest(http.MethodPost, httpSrv.URL+"/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	messageReq.Header.Set("Content-Type", "application/json")
	messageReq.Header.Set("X-API-Key", "test-key")
	messageReq.Header.Set("X-Tenant-ID", "default")
	messageResp, err := http.DefaultClient.Do(messageReq)
	if err != nil {
		t.Fatalf("message request failed: %v", err)
	}
	defer func() { _ = messageResp.Body.Close() }()
	if messageResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 from message endpoint when disabled, got %d", messageResp.StatusCode)
	}
}

func TestHandleSetConfigReloadsMCPRuntimeConfig(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	tools := mcp.NewToolRegistry()
	if err := tools.Register(mcp.Tool{Name: "demo.tool"}, func(_ context.Context, _ json.RawMessage) (*mcp.ToolCallResult, error) {
		return &mcp.ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	resources := mcp.NewResourceRegistry()
	if err := resources.Register(mcp.Resource{URI: "cordum://demo", Name: "demo.resource"}, func(_ context.Context, uri string) (*mcp.ResourceContents, error) {
		return &mcp.ResourceContents{URI: uri}, nil
	}); err != nil {
		t.Fatalf("register resource: %v", err)
	}
	s.setMCPRuntime(&mcpRuntimeState{
		toolRegistry:     tools,
		resourceRegistry: resources,
		server:           mcp.NewServer(nil, tools, resources, mcp.ServerConfig{}),
	})
	t.Cleanup(func() { s.clearMCPRuntime() })

	if got := len(tools.List()); got != 1 {
		t.Fatalf("expected tool enabled before config patch, got %d", got)
	}
	if got := len(resources.List()); got != 1 {
		t.Fatalf("expected resource enabled before config patch, got %d", got)
	}

	payload := map[string]any{
		"mcp": map[string]any{
			"tools": map[string]any{
				"demo.tool": map[string]any{"enabled": false},
			},
			"resources": map[string]any{
				"demo.resource": map[string]any{"enabled": false},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleSetConfig(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("set config failed: %d %s", rr.Code, rr.Body.String())
	}

	if got := len(tools.List()); got != 0 {
		t.Fatalf("expected tool disabled after mcp reload, got %d", got)
	}
	if got := len(resources.List()); got != 0 {
		t.Fatalf("expected resource disabled after mcp reload, got %d", got)
	}
}

func TestHandleSetConfigStartsMCPRuntimeWhenEnabled(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() {
		close(s.shutdownCh)
	})

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	if runtime := s.getMCPRuntime(); runtime != nil {
		t.Fatalf("expected MCP runtime disabled before config patch, got %#v", runtime)
	}

	body := strings.NewReader(`{"mcp":{"enabled":true,"transport":"http","port":3001,"requireAuth":true,"tools":{},"resources":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleSetConfig(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("set config failed: %d %s", rr.Code, rr.Body.String())
	}

	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.approvalHandler == nil || runtime.httpTransport == nil {
		t.Fatalf("expected live MCP runtime with approvals after enable, got %#v", runtime)
	}

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()
	statusReq, _ := http.NewRequest(http.MethodGet, httpSrv.URL+"/mcp/status", nil)
	statusReq.Header.Set("X-API-Key", "test-key")
	statusReq.Header.Set("X-Tenant-ID", "default")
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer func() { _ = statusResp.Body.Close() }()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 after dynamic enable, got %d", statusResp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode status payload: %v", err)
	}
	if running, ok := payload["running"].(bool); !ok || !running {
		t.Fatalf("expected running=true after dynamic enable, got %#v", payload["running"])
	}
}

func TestHandleSetConfigStopsMCPRuntimeWhenDisabled(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() {
		close(s.shutdownCh)
	})
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	if runtime := s.getMCPRuntime(); runtime == nil {
		t.Fatal("expected MCP runtime before disable")
	}

	body := strings.NewReader(`{"mcp":{"enabled":false,"transport":"http","port":3001,"requireAuth":true,"tools":{},"resources":{}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()
	s.handleSetConfig(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("set config failed: %d %s", rr.Code, rr.Body.String())
	}
	if runtime := s.getMCPRuntime(); runtime != nil {
		t.Fatalf("expected MCP runtime stopped after disable, got %#v", runtime)
	}
}

func TestMCPAuthRejectsCrossTenantRequest(t *testing.T) {
	t.Parallel()
	s := &server{
		auth: mcpTestAuth{apiKey: "test-key", tenant: "tenant-a"},
	}
	handler := s.mcpAuth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "tenant-b")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant request, got %d", rr.Code)
	}
}

func TestLoadMCPConfigDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestGateway(t)

	cfg := s.loadMCPConfig(context.Background())
	if cfg.Enabled {
		t.Fatal("expected MCP disabled by default")
	}
	if cfg.Transport != "http" {
		t.Fatalf("expected default transport http, got %q", cfg.Transport)
	}

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"mcp": map[string]any{
				"enabled":   true,
				"transport": "http",
				"port":      8089,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg = s.loadMCPConfig(ctx)
	if !cfg.Enabled {
		t.Fatal("expected MCP enabled from config")
	}
	if cfg.Port != 8089 {
		t.Fatalf("expected mcp.port=8089, got %d", cfg.Port)
	}
}
