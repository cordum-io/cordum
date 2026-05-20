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
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
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

type mcpReauthTestAuth struct {
	calls atomic.Int32
}

func (a *mcpReauthTestAuth) AuthenticateHTTP(*http.Request) (*auth.AuthContext, error) {
	if call := a.calls.Add(1); call > 1 {
		return nil, errors.New("credential revoked")
	}
	return &auth.AuthContext{
		APIKey:      "test-key",
		Tenant:      "default",
		PrincipalID: "tester",
		Role:        "admin",
		AuthSource:  auth.AuthSourceAPIKey,
	}, nil
}

func (a *mcpReauthTestAuth) AuthenticateGRPC(context.Context) (*auth.AuthContext, error) {
	return &auth.AuthContext{Tenant: "default"}, nil
}

func (a *mcpReauthTestAuth) RequireRole(*http.Request, ...string) error { return nil }

func (a *mcpReauthTestAuth) ResolveTenant(_ *http.Request, requested, fallback string) (string, error) {
	if strings.TrimSpace(requested) != "" {
		return requested, nil
	}
	return fallback, nil
}

func (a *mcpReauthTestAuth) RequireTenantAccess(*http.Request, string) error { return nil }

func (a *mcpReauthTestAuth) ResolvePrincipal(_ *http.Request, requested string) (string, error) {
	return requested, nil
}

// TestMCPToolCallAuditHookDefaultsEmptyTenant asserts the producer-
// side fallback contract for the gateway's tool-call audit forwarder
// (handlers_mcp.go::mcpToolCallAuditHook): an event arriving with
// empty TenantID (upstream producer bug or new producer site not yet
// wired to ResolveTenantForAudit) is rewritten to model.DefaultTenant
// before reaching the downstream sender. Defense-in-depth at the
// gateway boundary so the sink-level slog.Warn (Phase 4) only fires
// on genuinely-novel producer paths.
func TestMCPToolCallAuditHookDefaultsEmptyTenant(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	s := &server{auditExporter: cap}
	hook := s.mcpToolCallAuditHook()
	if hook == nil {
		t.Fatal("mcpToolCallAuditHook returned nil; expected non-nil hook when auditExporter is wired")
	}
	hook(audit.SIEMEvent{
		EventType: audit.EventMCPToolInvocation,
		TenantID:  "", // upstream producer left it empty
		Action:    "invoke",
	})
	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].TenantID != "default" {
		t.Fatalf("TenantID = %q, want %q (hook must default empty TenantID)",
			events[0].TenantID, "default")
	}
}

// TestMCPApprovalAuditHookDefaultsEmptyTenant mirrors the previous
// test for the approval-lifecycle hook. MCPApprovalStore writes
// SIEMEvents from MCPApprovalRecord.Tenant; if a future enqueue path
// slips through with an empty Tenant, the hook catches the gap before
// the chain sender's slog.Warn fires.
func TestMCPApprovalAuditHookDefaultsEmptyTenant(t *testing.T) {
	t.Parallel()
	cap := &captureAuditSender{}
	s := &server{auditExporter: cap}
	hook := s.mcpApprovalAuditHook()
	if hook == nil {
		t.Fatal("mcpApprovalAuditHook returned nil; expected non-nil hook when auditExporter is wired")
	}
	hook(audit.SIEMEvent{
		EventType: audit.EventMCPToolApproval,
		TenantID:  "",
		Action:    "approved",
	})
	events := cap.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].TenantID != "default" {
		t.Fatalf("TenantID = %q, want %q (hook must default empty TenantID)",
			events[0].TenantID, "default")
	}
}

func TestMcpTenantFromContext_FailsClosedOnMissingAuth(t *testing.T) {
	t.Parallel()
	s := &server{}
	handlerCalled := false

	status, _, raw, err := s.invokeMCPJSONHandler(
		context.Background(),
		http.MethodPost,
		"/api/v1/policy/simulate",
		nil,
		nil,
		map[string]any{"topic": "job.default"},
		func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			if got := r.Header.Get("X-Tenant-ID"); got != "" {
				t.Fatalf("handler saw fallback tenant header %q; want fail-closed before dispatch", got)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)
	if err != nil {
		t.Fatalf("invokeMCPJSONHandler returned unexpected err: %v", err)
	}
	if handlerCalled {
		t.Fatal("handler was called without auth/server tenant context; want fail-closed")
	}
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("status = %d body=%s, want 401 or 403", status, string(raw))
	}
	if strings.Contains(string(raw), `"default"`) {
		t.Fatalf("missing-tenant response should not stamp or mention default tenant: %s", raw)
	}
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

func TestMCPSSEReauthFirstTickRevokedCredentialDisconnectsWithinTwoSeconds(t *testing.T) {
	authProvider := &mcpReauthTestAuth{}
	transport := mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, mcp.DefaultHTTPResponseTimeout)
	s := &server{auth: authProvider}
	s.setMCPRuntime(&mcpRuntimeState{transport: "http", httpTransport: transport})
	t.Cleanup(func() {
		_ = transport.Close()
		s.clearMCPRuntime()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil).WithContext(ctx)
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "default")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.mcpAuth(s.handleMCPSSE)(rr, req)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatalf("SSE handler did not disconnect within 2s after revoked re-auth; auth calls=%d", authProvider.calls.Load())
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("SSE status = %d, want %d", rr.Code, http.StatusOK)
	}
	if calls := authProvider.calls.Load(); calls < 2 {
		t.Fatalf("AuthenticateHTTP calls = %d, want initial auth plus first re-auth", calls)
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
