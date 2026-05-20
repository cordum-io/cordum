package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	edgecore "github.com/cordum/cordum/core/edge"
)

// TestMCPUpstreamPolicyInputs_DefaultsToStrictWhenAllowlistExists locks the
// PR #276 audit fix: if a tenant has configured an allowlist but did not
// explicitly set mcp.policy_mode, the policy MUST default to
// enterprise-strict so the validator enforces allowlist + HTTPS-only +
// DNS-fail-closed. Pre-fix the function returned "" so the strict gates
// were silently bypassed despite the operator declaring an allowlist.
func TestMCPUpstreamPolicyInputs_DefaultsToStrictWhenAllowlistExists(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeOrg,
		ScopeID: "tenant-a",
		Data: map[string]any{
			"safety": map[string]any{
				"mcp": map[string]any{
					// policy_mode intentionally omitted — allowlist alone
					// must imply strict.
					"allowed_upstreams": []any{"approved-server"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("set tenant config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
	mode, allow := s.mcpUpstreamPolicyInputs(req, "tenant-a")
	if mode != string(edgecore.PolicyModeEnterpriseStrict) {
		t.Fatalf("policy mode = %q, want %q (allowlist present must default to strict)",
			mode, edgecore.PolicyModeEnterpriseStrict)
	}
	if len(allow) != 1 || allow[0] != "approved-server" {
		t.Fatalf("allowlist = %v, want [approved-server]", allow)
	}
}

// TestRegisterMCPUpstream_HTTPAllowedWithTenantOptIn proves the plain-HTTP
// exception is an explicit tenant config setting, not a caller-controlled
// downgrade. The tenant still has an allowlist, so the strict name gate
// remains active while only the scheme requirement is relaxed.
func TestRegisterMCPUpstream_HTTPAllowedWithTenantOptIn(t *testing.T) {
	prevLookup := edgecore.MCPHostLookup
	t.Cleanup(func() { edgecore.MCPHostLookup = prevLookup })
	edgecore.MCPHostLookup = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "mcp.example.com" {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host}
	}

	registry := &fakeMCPUpstreamRegistry{}
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"` + mcpUpstreamTestAPIKey + `","tenant":"tenant-a","role":"admin","principal_id":"mcp-admin"}]`,
	})
	s.mcpUpstreamRegistry = registry

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeOrg,
		ScopeID: "tenant-a",
		Data: map[string]any{
			"safety": map[string]any{
				"mcp": map[string]any{
					"allowed_upstreams": []any{"tenant-tools"},
					"allow_plain_http":  true,
					"untrusted_ignored": "policy stays server-side",
				},
			},
		},
	}); err != nil {
		t.Fatalf("set tenant config: %v", err)
	}

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	handler := apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))

	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"http://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"risk":"medium",
		"enabled":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201 with tenant allow_plain_http opt-in", rec.Code, rec.Body.String())
	}
	if registry.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", registry.createCalls)
	}
	var got edgecore.UpstreamServer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, rec.Body.String())
	}
	if got.Endpoint != "http://mcp.example.com/tools" || len(got.ResolvedIPs) != 1 {
		t.Fatalf("response upstream = %#v, want plain HTTP endpoint with pinned IP", got)
	}
}

// TestRegisterMCPUpstream_HTTPRejectedWhenAllowlistConfigured exercises
// the full HTTP path: with a tenant allowlist configured, registering a
// plain-HTTP upstream MUST be rejected with 400 invalid_request. The
// strict-by-allowlist default closes the SSRF/credential-leak vector
// from accepting cleartext http:// upstreams that an admin may have
// believed were gated by the allowlist.
func TestRegisterMCPUpstream_HTTPRejectedWhenAllowlistConfigured(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{}
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"` + mcpUpstreamTestAPIKey + `","tenant":"tenant-a","role":"admin","principal_id":"mcp-admin"}]`,
	})
	s.mcpUpstreamRegistry = registry

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeOrg,
		ScopeID: "tenant-a",
		Data: map[string]any{
			"safety": map[string]any{
				"mcp": map[string]any{
					"allowed_upstreams": []any{"tenant-tools"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("set tenant config: %v", err)
	}

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	handler := apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))

	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"http://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"risk":"medium",
		"enabled":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400 (plain HTTP must be rejected when allowlist exists)",
			rec.Code, rec.Body.String())
	}
	if registry.createCalls != 0 {
		t.Fatalf("create called %d times despite reject, want 0", registry.createCalls)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("response did not include invalid_request code: %s", rec.Body.String())
	}
}
