package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestHandlerCreateMCPUpstreamLimitExceededReturns429(t *testing.T) {
	registry := &limitExceededMCPUpstreamRegistry{
		createErr: edgecore.ErrUpstreamLimitExceeded,
	}
	handler := newMCPUpstreamLimitHTTPHandler(t, registry)
	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"risk":"medium",
		"enabled":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertEdgeErrorShape(t, rec, http.StatusTooManyRequests, edgeErrCodeConflict)
	if registry.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", registry.createCalls)
	}
	response := rec.Body.String()
	if !strings.Contains(response, "mcp upstream tenant cap reached") {
		t.Fatalf("response message did not identify tenant cap: %s", response)
	}
	if strings.Contains(response, "registry error") || strings.Contains(response, edgeErrCodeInternalError) {
		t.Fatalf("limit response used internal-error shape: %s", response)
	}
}

func newMCPUpstreamLimitHTTPHandler(t *testing.T, registry edgecore.MCPUpstreamRegistry) http.Handler {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"` + mcpUpstreamTestAPIKey + `","tenant":"tenant-a","role":"admin","principal_id":"mcp-admin"}]`,
	})
	s.mcpUpstreamRegistry = registry
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
}

type limitExceededMCPUpstreamRegistry struct {
	createCalls int
	createErr   error
}

func (f *limitExceededMCPUpstreamRegistry) Create(_ context.Context, _ *edgecore.UpstreamServer) error {
	f.createCalls++
	return f.createErr
}

func (f *limitExceededMCPUpstreamRegistry) Get(_ context.Context, _, _ string) (*edgecore.UpstreamServer, bool, error) {
	return nil, false, nil
}

func (f *limitExceededMCPUpstreamRegistry) List(_ context.Context, _ string) ([]edgecore.UpstreamServer, error) {
	return nil, nil
}

func (f *limitExceededMCPUpstreamRegistry) Update(_ context.Context, _ *edgecore.UpstreamServer) error {
	return nil
}

func (f *limitExceededMCPUpstreamRegistry) Disable(_ context.Context, _, _ string) error {
	return nil
}

func (f *limitExceededMCPUpstreamRegistry) Enable(_ context.Context, _, _ string) error {
	return nil
}
