package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge"
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

func TestMCPAuthRejectsNonFrontedTenantWhenUpstreamIsDefaultScoped(t *testing.T) {
	t.Parallel()
	s := &server{
		auth: mcpTestAuth{apiKey: "test-key", tenant: "tenant-b"},
	}
	s.setMCPRuntime(&mcpRuntimeState{
		frontedUpstream: "cordum.monday",
		frontedTenant:   "default",
	})
	t.Cleanup(func() { gatewayMCPState.Delete(s) })

	handler := s.mcpAuth(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run for a tenant that would be proxied through default-tenant upstream credentials")
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "tenant-b")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-fronted tenant, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMCPAuthStashesCoreCallMetadataForHTTPTransport(t *testing.T) {
	t.Parallel()
	s := &server{
		auth: mcpTestAuth{apiKey: "test-key", tenant: "default"},
	}
	handler := s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
		meta, ok := mcp.CallMetadataFromContext(r.Context())
		if !ok {
			t.Fatal("mcp.CallMetadata missing from request context")
		}
		if meta.Tenant != "default" || meta.Principal != "tester" || meta.AgentID != "agent-alpha" {
			t.Fatalf("unexpected call metadata: %+v", meta)
		}
		// Session-less POST: a minted one-shot id. A client-supplied id is only
		// used once the transport vouches for it (see
		// TestMCPAuthUnverifiedSessionIDCreatesNoEdgeRecords).
		if !strings.HasPrefix(meta.SessionID, "direct-") || meta.ExecutionID != "mcp-"+meta.SessionID {
			t.Fatalf("unexpected session/execution metadata: %+v", meta)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
	req.Header.Set("X-API-Key", "test-key")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Agent-Id", "agent-alpha")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// A session-less POST (VS Code type:"http") gets a fresh one-shot Edge session
// every time but one Copilot session per caller and agent, unless the client
// names its own with X-Copilot-Session-Id.
func TestMCPAuthSessionlessCopilotSessionPerCaller(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key-alice","tenant":"default","principal_id":"alice","role":"admin"},` +
			`{"key":"key-bob","tenant":"default","principal_id":"bob","role":"admin"}]`,
	})
	ids := func(key, agent, copilotHeader string) (edgeSession, copilotSession string) {
		t.Helper()
		h := s.mcpAuth(func(w http.ResponseWriter, r *http.Request) {
			meta, _ := mcp.CallMetadataFromContext(r.Context())
			edgeSession, copilotSession = meta.SessionID, mcp.CopilotSessionIDFromContext(r.Context())
		})
		req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
		req.Header.Set("X-API-Key", key)
		req.Header.Set("X-Tenant-ID", "default")
		req.Header.Set(mcpAgentIDHeader, agent)
		if copilotHeader != "" {
			req.Header.Set(mcpCopilotSessionIDHeader, copilotHeader)
		}
		h(httptest.NewRecorder(), req)
		return edgeSession, copilotSession
	}
	edge1, copilot1 := ids("key-alice", "agent-a", "")
	edge2, copilot2 := ids("key-alice", "agent-a", "")
	if edge1 == "" || edge1 == edge2 {
		t.Fatalf("Edge sessions %q, %q: want a distinct one-shot session per POST", edge1, edge2)
	}
	if !copilotSessionIDPattern.MatchString(copilot1) || copilot1 != copilot2 || copilot1 == edge1 {
		t.Fatalf("Copilot sessions %q, %q: want one stable, valid id per caller and agent", copilot1, copilot2)
	}
	if _, other := ids("key-bob", "agent-a", ""); other == copilot1 {
		t.Fatalf("two callers share Copilot session %q", other)
	}
	if _, other := ids("key-alice", "agent-b", ""); other == copilot1 {
		t.Fatalf("two agents of one caller share Copilot session %q", other)
	}
	if _, named := ids("key-alice", "agent-a", "workspace-1"); named != "workspace-1" {
		t.Fatalf("X-Copilot-Session-Id ignored: got %q", named)
	}
}

func TestMCPSessionOwnerSeparatesCallers(t *testing.T) {
	t.Parallel()
	owner := func(tenant string, ac *auth.AuthContext) string {
		req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
		req.Header.Set("X-Tenant-ID", tenant)
		if ac != nil {
			req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, ac))
		}
		return mcpSessionOwner(req)
	}
	keyAlice := &auth.AuthContext{APIKey: "key-a", PrincipalID: "alice", AuthSource: auth.AuthSourceAPIKey}
	jwtAlice := &auth.AuthContext{PrincipalID: "alice", AuthSource: auth.AuthSourceJWT}
	if a, b := owner("t1", keyAlice), owner("t1", &auth.AuthContext{APIKey: "key-a", PrincipalID: "alice", AuthSource: auth.AuthSourceAPIKey}); a == "" || a != b {
		t.Fatalf("same caller must map to one non-empty owner key: %q vs %q", a, b)
	}
	for name, pair := range map[string][2]string{
		"same principal, other tenant":   {owner("t1", jwtAlice), owner("t2", jwtAlice)},
		"same principal, other api key":  {owner("t1", keyAlice), owner("t1", &auth.AuthContext{APIKey: "key-b", PrincipalID: "alice", AuthSource: auth.AuthSourceAPIKey})},
		"same api key, other principal":  {owner("t1", keyAlice), owner("t1", &auth.AuthContext{APIKey: "key-a", PrincipalID: "bob", AuthSource: auth.AuthSourceAPIKey})},
		"verified vs header-only (anon)": {owner("t1", jwtAlice), owner("t1", &auth.AuthContext{PrincipalID: "alice"})},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s: callers share owner key %q", name, pair[0])
		}
	}
	if got := owner("t1", nil); got != "" {
		t.Fatalf("no auth context: owner key %q, want the anonymous owner \"\"", got)
	}
}

func TestMCPSessionOwnerLoginRenewalKeepsCredentialBoundary(t *testing.T) {
	t.Parallel()
	owner := func(token string, source auth.AuthSource) string {
		req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
		req.Header.Set("X-Tenant-ID", "default")
		req.Header.Set("Authorization", "Bearer "+token)
		ac := &auth.AuthContext{PrincipalID: "alice", AuthSource: source}
		if source == auth.AuthSourceSession {
			// Session authentication stores the validated token in APIKey;
			// JWT authentication carries the verified principal without it.
			ac.APIKey = token
		}
		req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, ac))
		return mcpSessionOwner(req)
	}
	previous := owner("session-before-login", auth.AuthSourceSession)
	if got := owner("session-before-login", auth.AuthSourceSession); got == "" || got != previous {
		t.Fatalf("same login session changed owner: %q vs %q", previous, got)
	}
	if got := owner("session-after-login", auth.AuthSourceSession); got == previous {
		t.Fatal("new login token must not inherit another login session's MCP stream")
	}
	if a, b := owner("jwt-before-refresh", auth.AuthSourceJWT), owner("jwt-after-refresh", auth.AuthSourceJWT); a == "" || a != b {
		t.Fatal("JWT refresh with the same verified identity must preserve stream ownership")
	}
}

// newMCPTwoKeyGateway serves the real MCP routes behind static API keys for
// alice and bob (tenant default) with a Redis-backed edge store.
func newMCPTwoKeyGateway(t *testing.T) (*server, string) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key-alice","tenant":"default","principal_id":"alice","role":"admin"},` +
			`{"key":"key-bob","tenant":"default","principal_id":"bob","role":"admin"}]`,
	})
	s.edgeStore = edge.NewRedisStoreFromClient(newSharedTestRedisClient(t, newTestRedisBackend(t)))
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)
	return s, httpSrv.URL
}

// openMCPSSE opens /mcp/sse as key (query is an optional raw query string) and
// returns the server-assigned session id; the stream closes at test cleanup.
func openMCPSSE(t *testing.T, baseURL, key, query string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/mcp/sse?"+query, nil)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	sessionID := resp.Header.Get("X-MCP-Session-ID")
	if resp.StatusCode != http.StatusOK || sessionID == "" {
		t.Fatalf("open sse: status %d session %q", resp.StatusCode, sessionID)
	}
	return sessionID
}

// postMCPPing POSTs a ping to /mcp/message as key, naming sessionID.
func postMCPPing(t *testing.T, baseURL, key, sessionID string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp/message", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-MCP-Session-ID", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post message: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read message response: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// TestMCPHTTPSessionBoundToAuthenticatedCaller drives the real gateway wiring:
// a second API key naming the first key's SSE session id gets the same 404 an
// unknown session id gets, while the owner keeps using its session.
func TestMCPHTTPSessionBoundToAuthenticatedCaller(t *testing.T) {
	_, baseURL := newMCPTwoKeyGateway(t)
	aliceSession := openMCPSSE(t, baseURL, "key-alice", "")

	foreignStatus, foreign := postMCPPing(t, baseURL, "key-bob", aliceSession)
	if foreignStatus != http.StatusNotFound {
		t.Fatalf("bob naming alice's session: status %d body %s, want 404", foreignStatus, foreign)
	}
	if status, body := postMCPPing(t, baseURL, "key-bob", "no-such-session"); status != foreignStatus || body != foreign {
		t.Fatalf("foreign vs unknown session answers differ: %d %s / %d %s", foreignStatus, foreign, status, body)
	}
	if status, body := postMCPPing(t, baseURL, "key-alice", aliceSession); status != http.StatusOK || !strings.Contains(body, `"result"`) {
		t.Fatalf("alice using her own session: status %d body %s, want 200 with result", status, body)
	}
}

// TestMCPAuthUnverifiedSessionIDCreatesNoEdgeRecords: a session id the
// transport does not vouch for (foreign, unknown, or sent on the SSE GET)
// must never reach ensureMCPHTTPTraceParents, so a caller can neither create
// edge records for arbitrary ids nor pre-create a leaked session's record
// under its own principal.
func TestMCPAuthUnverifiedSessionIDCreatesNoEdgeRecords(t *testing.T) {
	s, baseURL := newMCPTwoKeyGateway(t)
	aliceSession := openMCPSSE(t, baseURL, "key-alice", "")
	openMCPSSE(t, baseURL, "key-bob", "session_id=spoofed-on-get")
	for _, id := range []string{aliceSession, "no-such-session"} {
		if status, body := postMCPPing(t, baseURL, "key-bob", id); status != http.StatusNotFound {
			t.Fatalf("bob naming session %q: status %d body %s, want 404", id, status, body)
		}
	}

	ctx := context.Background()
	for _, id := range []string{aliceSession, "no-such-session", "spoofed-on-get"} {
		if _, ok, err := s.edgeStore.GetSession(ctx, "default", id); err != nil || ok {
			t.Fatalf("edge session for unverified id %q: ok=%v err=%v, want none", id, ok, err)
		}
		if _, ok, err := s.edgeStore.GetExecution(ctx, "default", "mcp-"+id); err != nil || ok {
			t.Fatalf("edge execution for unverified id %q: ok=%v err=%v, want none", id, ok, err)
		}
	}

	// The owner's own first POST still creates the trace parents, as alice.
	if status, body := postMCPPing(t, baseURL, "key-alice", aliceSession); status != http.StatusOK {
		t.Fatalf("alice using her own session: status %d body %s, want 200", status, body)
	}
	got, ok, err := s.edgeStore.GetSession(ctx, "default", aliceSession)
	if err != nil || !ok || got.PrincipalID != "alice" {
		t.Fatalf("alice's edge session = %+v ok=%v err=%v, want principal alice", got, ok, err)
	}
}

func TestEnsureMCPHTTPTraceParentsCreatesEdgeParents(t *testing.T) {
	t.Parallel()
	backend := newTestRedisBackend(t)
	client := newSharedTestRedisClient(t, backend)
	s := &server{edgeStore: edge.NewRedisStoreFromClient(client)}

	if err := s.ensureMCPHTTPTraceParents(context.Background(), "default", "agent-alpha", "tester", "sess-alpha", "sess-alpha", "mcp-sess-alpha"); err != nil {
		t.Fatalf("ensureMCPHTTPTraceParents: %v", err)
	}
	if _, ok, err := s.edgeStore.GetSession(context.Background(), "default", "sess-alpha"); err != nil || !ok {
		t.Fatalf("GetSession ok=%v err=%v", ok, err)
	}
	if got, ok, err := s.edgeStore.GetExecution(context.Background(), "default", "mcp-sess-alpha"); err != nil || !ok || got.SessionID != "sess-alpha" {
		t.Fatalf("GetExecution got=%+v ok=%v err=%v", got, ok, err)
	}
}

// TestMCPEdgeTraceRotatesBeforeCaps: with lowered Edge caps (5 events per
// execution, 2 executions per session) an MCP session moves to a new
// execution once the current one holds 4 events, and to a new Edge session,
// labelled with the MCP session, once its session holds both executions.
func TestMCPEdgeTraceRotatesBeforeCaps(t *testing.T) {
	t.Parallel()
	client := newSharedTestRedisClient(t, newTestRedisBackend(t))
	s := &server{edgeStore: edge.NewRedisStoreFromClient(client,
		edge.WithMaxEventsPerExecution(5), edge.WithMaxExecutionsPerSession(2))}
	ctx := context.Background()
	trace := func() (string, string) {
		t.Helper()
		sessionID, executionID, err := s.mcpEdgeTrace(ctx, "default", "agent-alpha", "tester", "sse-1")
		if err != nil {
			t.Fatalf("mcpEdgeTrace: %v", err)
		}
		return sessionID, executionID
	}
	fill := func(sessionID, executionID string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if _, err := s.edgeStore.AppendEvent(ctx, edge.AgentActionEvent{
				EventID: fmt.Sprintf("%s-%d", executionID, i), SessionID: sessionID, ExecutionID: executionID,
				TenantID: "default", PrincipalID: "tester", Timestamp: time.Now().UTC(),
				Layer: edge.LayerMCP, Kind: edge.EventKindMCPToolPre, ToolName: "t", Decision: edge.DecisionAllow, Status: edge.ActionStatusOK,
			}); err != nil {
				t.Fatalf("append event %d to %s: %v", i, executionID, err)
			}
		}
	}
	for _, step := range []struct{ session, execution string }{
		{"sse-1", "mcp-sse-1"},
		{"sse-1", "mcp-sse-1.e1"},
		{"sse-1.s1", "mcp-sse-1.s1"},
		{"sse-1.s1", "mcp-sse-1.s1.e1"},
	} {
		sessionID, executionID := trace()
		if sessionID != step.session || executionID != step.execution {
			t.Fatalf("trace = %s/%s, want %s/%s", sessionID, executionID, step.session, step.execution)
		}
		fill(sessionID, executionID, 3)
		if again, againExec := trace(); again != sessionID || againExec != executionID {
			t.Fatalf("below the rotation point the trace moved to %s/%s", again, againExec)
		}
		fill(sessionID, executionID, 1)
	}
	for _, id := range []string{"sse-1", "sse-1.s1"} {
		got, ok, err := s.edgeStore.GetSession(ctx, "default", id)
		if err != nil || !ok || got.Labels[mcpSessionLabel] != "sse-1" || got.Status != edge.SessionStatusRunning {
			t.Fatalf("edge session %s = %+v ok=%v err=%v, want running and labelled with the MCP session", id, got, ok, err)
		}
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
