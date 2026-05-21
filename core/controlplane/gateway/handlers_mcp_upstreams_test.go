package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

const mcpUpstreamTestAPIKey = "mcp-upstream-test-key"

func TestHandlerListAuthRequired(t *testing.T) {
	handler := newMCPUpstreamHTTPHandler(t, &fakeMCPUpstreamRegistry{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
	}
}

func TestHandlerListTenantHeaderRequired(t *testing.T) {
	handler := newMCPUpstreamHTTPHandler(t, &fakeMCPUpstreamRegistry{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/mcp/upstreams", nil)
	req.Header.Set("X-API-Key", mcpUpstreamTestAPIKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestHandlerListEnabledFilter(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{entries: []edgecore.UpstreamServer{
		{Name: "enabled-tools", TenantID: "tenant-a", Transport: "http", Risk: "medium", Enabled: true},
		{Name: "disabled-tools", TenantID: "tenant-a", Transport: "http", Risk: "medium", Enabled: false},
	}}
	handler := newMCPUpstreamHTTPHandler(t, registry)

	all := requestMCPUpstreamList(t, handler, "/api/v1/edge/mcp/upstreams")
	if len(all.Items) != 2 {
		t.Fatalf("default list returned %d items, want enabled and disabled records", len(all.Items))
	}

	enabled := requestMCPUpstreamList(t, handler, "/api/v1/edge/mcp/upstreams?enabled=true")
	if len(enabled.Items) != 1 || enabled.Items[0].Name != "enabled-tools" || !enabled.Items[0].Enabled {
		t.Fatalf("enabled=true list = %#v, want only enabled-tools", enabled.Items)
	}
}

func requestMCPUpstreamList(t *testing.T, handler http.Handler, target string) mcpUpstreamListResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %s status = %d body=%s, want 200", target, rec.Code, rec.Body.String())
	}
	var got mcpUpstreamListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal list response: %v body=%s", err, rec.Body.String())
	}
	return got
}

func TestHandlerCreateRejectsCrossTenantBody(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{}
	handler := newMCPUpstreamHTTPHandler(t, registry)
	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"tenant_id":"tenant-b",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"risk":"medium",
		"enabled":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
	if registry.createCalls != 0 {
		t.Fatalf("cross-tenant body created %d record(s)", registry.createCalls)
	}
}

func TestHandlerCreateRejectsRawSecretRef(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{}
	handler := newMCPUpstreamHTTPHandler(t, registry)
	body := []byte(`{
		"name":"raw-secret-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"auth_secret_ref":"sk-test-raw-token",
		"risk":"medium",
		"enabled":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if registry.createCalls != 0 {
		t.Fatalf("raw-secret body created %d record(s)", registry.createCalls)
	}
	if strings.Contains(rec.Body.String(), "sk-test-raw-token") {
		t.Fatalf("response leaked raw secret: %s", rec.Body.String())
	}
}

func TestHandlerCreateRedactedResponse(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{}
	handler := newMCPUpstreamHTTPHandler(t, registry)
	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"labels":{"team":"platform"},
		"risk":"high",
		"enabled":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	if registry.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", registry.createCalls)
	}
	payload := rec.Body.String()
	if !strings.Contains(payload, "secret://vault/mcp/tenant-tools") {
		t.Fatalf("response omitted secret ref: %s", payload)
	}
	if strings.Contains(payload, "sk-test-raw-token") {
		t.Fatalf("response leaked raw token: %s", payload)
	}
	var got edgecore.UpstreamServer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, payload)
	}
	if got.TenantID != "tenant-a" || got.Name != "tenant-tools" || got.Risk != "high" {
		t.Fatalf("response upstream = %#v", got)
	}
}

// Policy must come from trusted tenant config only. Caller-supplied
// `policy_mode` / `allowed_upstream(s)` query params MUST be rejected with
// 400 invalid_request — not silently ignored — to prevent confused-deputy
// where the caller believes they overrode strict mode.
func TestHandlerCreateRejectsPolicyModeQueryParam(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"policy_mode_observe", "policy_mode=observe"},
		{"policy_mode_enforce", "policy_mode=enforce"},
		{"policy_mode_enterprise_strict", "policy_mode=enterprise-strict"},
		{"policy_mode_permissive", "policy_mode=permissive"},
		{"allowed_upstream_singular", "allowed_upstream=evil-host"},
		{"allowed_upstreams_plural", "allowed_upstreams=169.254.169.254"},
		{"allowed_upstreams_csv", "allowed_upstreams=a,b,c"},
		{"allow_plain_http", "allow_plain_http=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &fakeMCPUpstreamRegistry{}
			handler := newMCPUpstreamHTTPHandler(t, registry)
			body := []byte(`{
				"name":"tenant-tools",
				"transport":"http",
				"endpoint":"https://mcp.example.com/tools",
				"auth_secret_ref":"secret://vault/mcp/tenant-tools",
				"risk":"medium",
				"enabled":true
			}`)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams?"+tc.query, bytes.NewReader(body))
			addMCPUpstreamAuth(req)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
			if registry.createCalls != 0 {
				t.Fatalf("create called %d times despite reject, want 0", registry.createCalls)
			}
			if !strings.Contains(rec.Body.String(), "invalid_request") {
				t.Fatalf("response did not include invalid_request code: %s", rec.Body.String())
			}
		})
	}
}

func TestHandlerUpdateRejectsPolicyQueryParams(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{entries: []edgecore.UpstreamServer{{
		Name:          "tenant-tools",
		TenantID:      "tenant-a",
		Transport:     "http",
		Endpoint:      "https://mcp.example.com/tools",
		AuthSecretRef: "secret://vault/mcp/tenant-tools",
		Risk:          "medium",
		Enabled:       true,
	}}}
	handler := newMCPUpstreamHTTPHandler(t, registry)
	body := []byte(`{
		"name":"tenant-tools",
		"transport":"http",
		"endpoint":"https://mcp.example.com/tools",
		"auth_secret_ref":"secret://vault/mcp/tenant-tools",
		"risk":"medium",
		"enabled":true
	}`)

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/edge/mcp/upstreams/tenant-tools?policy_mode=observe",
		bytes.NewReader(body),
	)
	addMCPUpstreamAuth(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if registry.updateCalls != 0 {
		t.Fatalf("update called %d times despite reject, want 0", registry.updateCalls)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Fatalf("response did not include invalid_request code: %s", rec.Body.String())
	}
}

// Internal-host SSRF vectors must be rejected at the API edge regardless of
// any caller-supplied query param. Covers the full attack surface from the
// findings (AWS IMDS, RFC1918, IPv6 link-local + ULA, cloud-metadata DNS).
func TestHandlerCreateRejectsInternalHostEndpoints(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
	}{
		{"aws_imds_v4", "http://169.254.169.254/latest/meta-data/"},
		{"aws_imds_v6", "http://[fd00:ec2::254]/"},
		{"gcp_metadata_dns", "http://metadata.google.internal/computeMetadata/v1/"},
		{"rfc1918", "http://10.0.0.1/"},
		{"loopback_v4", "http://127.0.0.1/"},
		{"ipv6_link_local", "http://[fe80::1]/"},
		{"ipv6_ula", "http://[fd00::1]/"},
		{"ipv4_unspecified", "http://0.0.0.0/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &fakeMCPUpstreamRegistry{}
			handler := newMCPUpstreamHTTPHandler(t, registry)
			body := fmt.Appendf(nil, `{
				"name":"tenant-tools",
				"transport":"http",
				"endpoint":%q,
				"auth_secret_ref":"secret://vault/mcp/tenant-tools",
				"risk":"medium",
				"enabled":true
			}`, tc.endpoint)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams", bytes.NewReader(body))
			addMCPUpstreamAuth(req)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
			if registry.createCalls != 0 {
				t.Fatalf("create called %d times despite reject, want 0", registry.createCalls)
			}
		})
	}
}

func TestHandlerDisableIdempotent(t *testing.T) {
	registry := &fakeMCPUpstreamRegistry{}
	handler := newMCPUpstreamHTTPHandler(t, registry)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/mcp/upstreams/tenant-tools/disable", nil)
		addMCPUpstreamAuth(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable #%d status = %d body=%s, want 200", i+1, rec.Code, rec.Body.String())
		}
	}
	if got := registry.disableCalls["tenant-a/tenant-tools"]; got != 2 {
		t.Fatalf("disable calls = %d, want 2", got)
	}
}

func newMCPUpstreamHTTPHandler(t *testing.T, registry *fakeMCPUpstreamRegistry) http.Handler {
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

func addMCPUpstreamAuth(req *http.Request) {
	req.Header.Set("X-API-Key", mcpUpstreamTestAPIKey)
	req.Header.Set("X-Tenant-ID", "tenant-a")
}

type fakeMCPUpstreamRegistry struct {
	entries      []edgecore.UpstreamServer
	createCalls  int
	updateCalls  int
	disableCalls map[string]int
}

func (f *fakeMCPUpstreamRegistry) Create(_ context.Context, upstream *edgecore.UpstreamServer) error {
	f.createCalls++
	f.entries = append(f.entries, *upstream)
	return nil
}

func (f *fakeMCPUpstreamRegistry) Get(_ context.Context, tenantID, name string) (*edgecore.UpstreamServer, bool, error) {
	for i := range f.entries {
		entry := f.entries[i]
		if entry.TenantID == tenantID && entry.Name == name {
			return &entry, true, nil
		}
	}
	return nil, false, nil
}

func (f *fakeMCPUpstreamRegistry) List(_ context.Context, tenantID string) ([]edgecore.UpstreamServer, error) {
	out := make([]edgecore.UpstreamServer, 0, len(f.entries))
	for _, entry := range f.entries {
		if entry.TenantID == tenantID || entry.TenantID == "*" {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *fakeMCPUpstreamRegistry) Update(_ context.Context, upstream *edgecore.UpstreamServer) error {
	f.updateCalls++
	for i := range f.entries {
		if f.entries[i].TenantID == upstream.TenantID && f.entries[i].Name == upstream.Name {
			f.entries[i] = *upstream
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (f *fakeMCPUpstreamRegistry) Disable(_ context.Context, tenantID, name string) error {
	if f.disableCalls == nil {
		f.disableCalls = make(map[string]int)
	}
	f.disableCalls[tenantID+"/"+name]++
	return nil
}

func (f *fakeMCPUpstreamRegistry) Enable(_ context.Context, tenantID, name string) error {
	return nil
}
