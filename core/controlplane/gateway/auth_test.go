package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type publicPathAuth struct {
	called bool
}

var errUnauthorized = errors.New("unauthorized")

func (p *publicPathAuth) AuthenticateHTTP(*http.Request) (*AuthContext, error) {
	p.called = true
	return nil, errUnauthorized
}

func (p *publicPathAuth) AuthenticateGRPC(context.Context) (*AuthContext, error) {
	return nil, errUnauthorized
}

func (p *publicPathAuth) RequireRole(*http.Request, ...string) error { return nil }

func (p *publicPathAuth) ResolveTenant(*http.Request, string, string) (string, error) { return "", nil }

func (p *publicPathAuth) RequireTenantAccess(*http.Request, string) error { return nil }

func (p *publicPathAuth) ResolvePrincipal(*http.Request, string) (string, error) { return "", nil }

func (p *publicPathAuth) IsPublicPath(path string) bool { return path == "/api/v1/auth/config" }

func newBasicAuthForTest(t *testing.T, env map[string]string) *BasicAuthProvider {
	t.Helper()
	for _, key := range []string{
		"CORDUM_API_KEYS",
		"CORDUM_API_KEY",
		"API_KEY",
		"CORDUM_API_KEYS_PATH",
		"CORDUM_ALLOW_HEADER_PRINCIPAL",
		"CORDUM_ALLOW_INSECURE_NO_AUTH",
		"CORDUM_ENV",
		"CORDUM_PRODUCTION",
		"CORDUM_JWT_HMAC_SECRET",
		"CORDUM_JWT_PUBLIC_KEY",
		"CORDUM_JWT_PUBLIC_KEY_PATH",
		"CORDUM_JWT_REQUIRED",
		"CORDUM_JWT_ISSUER",
		"CORDUM_JWT_AUDIENCE",
		"CORDUM_JWT_DEFAULT_ROLE",
		"CORDUM_JWT_CLOCK_SKEW",
	} {
		t.Setenv(key, "")
	}
	if env == nil {
		env = map[string]string{}
	}
	authKeys := []string{
		"CORDUM_API_KEYS",
		"CORDUM_API_KEY",
		"API_KEY",
		"CORDUM_API_KEYS_PATH",
		"CORDUM_JWT_HMAC_SECRET",
		"CORDUM_JWT_PUBLIC_KEY",
		"CORDUM_JWT_PUBLIC_KEY_PATH",
		"CORDUM_ALLOW_INSECURE_NO_AUTH",
	}
	authConfigured := false
	for _, key := range authKeys {
		if strings.TrimSpace(env[key]) != "" {
			authConfigured = true
			break
		}
	}
	if !authConfigured {
		env["CORDUM_API_KEYS"] = "test-api-key"
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	provider, err := newBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("new basic auth provider: %v", err)
	}
	return provider
}

func requestWithAuthContext(auth *AuthContext) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	return req.WithContext(context.WithValue(req.Context(), authContextKey{}, auth))
}

func TestNormalizeRole(t *testing.T) {
	cases := map[string]string{
		"secops":   "admin",
		"operator": "admin",
		"ADMIN":    "admin",
		"viewer":   "viewer",
		"":         "",
	}
	for raw, expect := range cases {
		if got := normalizeRole(raw); got != expect {
			t.Fatalf("role %q expected %q got %q", raw, expect, got)
		}
	}
}

func TestParseAPIKeysFormats(t *testing.T) {
	entries, err := parseAPIKeys(`[{"key":"k1"}]`)
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "k1" {
		t.Fatalf("unexpected list entries: %#v", entries)
	}

	entries, err = parseAPIKeys(`{"k2":{}}`)
	if err != nil {
		t.Fatalf("parse map: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "k2" {
		t.Fatalf("unexpected map entries: %#v", entries)
	}

	entries, err = parseAPIKeys(`{"keys":[{"key":"k3"}]}`)
	if err != nil {
		t.Fatalf("parse wrapped: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "k3" {
		t.Fatalf("unexpected wrapped entries: %#v", entries)
	}

	entries, err = parseAPIKeys("t4:key4:admin:alice")
	if err != nil {
		t.Fatalf("parse colon: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "key4" {
		t.Fatalf("unexpected colon entries: %#v", entries)
	}
	if entries[0].Tenant != "t4" || entries[0].Role != "admin" || entries[0].PrincipalID != "alice" {
		t.Fatalf("unexpected colon metadata: %#v", entries[0])
	}
}

func TestAuthenticateRequiresKeyByDefault(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	if _, err := provider.AuthenticateHTTP(req); err == nil {
		t.Fatalf("expected api key required error")
	}
}

func TestAuthenticateAllowsAnonymousWhenExplicitlyEnabled(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ALLOW_INSECURE_NO_AUTH": "1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	ctx, err := provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if ctx.Role != "anonymous" {
		t.Fatalf("expected anonymous role, got %q", ctx.Role)
	}
}

func TestAuthenticateRequiresKeyWhenConfigured(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	if _, err := provider.AuthenticateHTTP(req); err == nil {
		t.Fatalf("expected api key required error")
	}

	req.Header.Set("X-API-Key", "key1")
	ctx, err := provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if ctx.APIKey != "key1" {
		t.Fatalf("expected api key to match")
	}
}

func TestAuthenticateRejectsInvalidKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "bad")
	if _, err := provider.AuthenticateHTTP(req); err == nil {
		t.Fatalf("expected invalid api key error")
	}
}

func TestResolveTenantAndAccess(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	s := &server{tenant: "default", auth: provider}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "default")
	if got, err := s.resolveTenant(req, ""); err != nil || got != "default" {
		t.Fatalf("expected default tenant, got %q err=%v", got, err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Tenant-ID", "default")
	if err := s.requireTenantAccess(req2, ""); err == nil {
		t.Fatalf("expected empty tenant to be rejected")
	}
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("X-Tenant-ID", "default")
	if _, err := s.resolveTenant(req3, "team-b"); err == nil {
		t.Fatalf("expected tenant access denied")
	}
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4.Header.Set("X-Tenant-ID", "default")
	if err := s.requireTenantAccess(req4, "team-b"); err == nil {
		t.Fatalf("expected tenant access denied")
	}
}

func TestResolvePrincipal(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Id", "alice")
	if got, err := provider.ResolvePrincipal(req, ""); err != nil || got != "alice" {
		t.Fatalf("expected principal alice, got %q err=%v", got, err)
	}
}

func TestRequireRoleDeniesEmptyRole(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	s := &server{auth: provider}
	req := requestWithAuthContext(&AuthContext{})
	if err := s.requireRole(req, "admin"); err == nil {
		t.Fatalf("expected role required error")
	}
}

func TestRequireRoleEnforces(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key1","role":"viewer"}]`,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "key1")
	authCtx, err := provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	if err := provider.RequireRole(req, "admin"); err == nil {
		t.Fatalf("expected role enforcement failure")
	}
	if err := provider.RequireRole(req, "viewer"); err != nil {
		t.Fatalf("expected viewer role to pass: %v", err)
	}
}

func TestAPIKeyExpiry(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key1","expires_at":"2000-01-01T00:00:00Z"}]`,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "key1")
	if _, err := provider.AuthenticateHTTP(req); err == nil {
		t.Fatalf("expected expired api key error")
	}
}

func TestAPIKeyFileReload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/keys.json"
	if err := os.WriteFile(path, []byte(`[{"key":"key1"}]`), 0o600); err != nil {
		t.Fatalf("write keys: %v", err)
	}
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS_PATH": path,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "key1")
	if _, err := provider.AuthenticateHTTP(req); err != nil {
		t.Fatalf("authenticate key1: %v", err)
	}

	if err := os.WriteFile(path, []byte(`[{"key":"key2"}]`), 0o600); err != nil {
		t.Fatalf("write keys: %v", err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("touch keys: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req2.Header.Set("X-Tenant-ID", "default")
	req2.Header.Set("X-API-Key", "key2")
	if _, err := provider.AuthenticateHTTP(req2); err != nil {
		t.Fatalf("authenticate key2: %v", err)
	}
}

func TestAuthContextHelpers(t *testing.T) {
	ctx := context.WithValue(context.Background(), authContextKey{}, &AuthContext{Tenant: "default"})
	if got := authFromContext(ctx); got == nil || got.Tenant != "default" {
		t.Fatalf("expected auth context from ctx")
	}
	if got := authFromRequest(requestWithAuthContext(&AuthContext{Tenant: "team"})); got == nil || got.Tenant != "team" {
		t.Fatalf("expected auth context from request")
	}
}

func TestAPIKeyMiddlewareSkipsPublicPaths(t *testing.T) {
	auth := &publicPathAuth{}
	handler := apiKeyMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if auth.called {
		t.Fatalf("expected auth not to be called for public path")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestBasicAuthPublicPathAllowsAuthConfig(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"API_KEY": "test-key",
	})
	handler := apiKeyMiddleware(provider, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestResolveTenantCrossTenantDenied(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key1","tenant":"team-a"}]`,
	})
	// Tenant A authenticated, requests tenant B without AllowCrossTenant → denied
	req := requestWithAuthContext(&AuthContext{
		APIKey: "key1",
		Tenant: "team-a",
	})
	_, err := provider.ResolveTenant(req, "team-b", "")
	if err == nil {
		t.Fatalf("expected tenant access denied when requesting team-b as team-a")
	}
	if err.Error() != "tenant access denied" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveTenantOwnTenantAllowed(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key1","tenant":"team-a"}]`,
	})
	// Tenant A authenticated, requests own tenant → allowed
	req := requestWithAuthContext(&AuthContext{
		APIKey: "key1",
		Tenant: "team-a",
	})
	got, err := provider.ResolveTenant(req, "team-a", "")
	if err != nil {
		t.Fatalf("expected own tenant to be allowed: %v", err)
	}
	if got != "team-a" {
		t.Fatalf("expected team-a, got %q", got)
	}
}

func TestResolveTenantCrossTenantAllowedWithFlag(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"key1","tenant":"team-a","allow_cross_tenant":true}]`,
	})
	// AllowCrossTenant=true, tenant A requests tenant B → allowed
	req := requestWithAuthContext(&AuthContext{
		APIKey:           "key1",
		Tenant:           "team-a",
		AllowCrossTenant: true,
	})
	got, err := provider.ResolveTenant(req, "team-b", "")
	if err != nil {
		t.Fatalf("expected cross-tenant to be allowed with flag: %v", err)
	}
	if got != "team-b" {
		t.Fatalf("expected team-b, got %q", got)
	}
}

func TestResolveTenantAnonymousDeniedNonDefault(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ALLOW_INSECURE_NO_AUTH": "1",
	})
	// Anonymous user (no auth context) requests non-default tenant → denied
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "default")
	_, err := provider.ResolveTenant(req, "other-tenant", "")
	if err == nil {
		t.Fatalf("expected tenant access denied for anonymous user requesting non-default tenant")
	}
}

func TestBasicAuthRequiresKeyInProduction(t *testing.T) {
	t.Setenv("CORDUM_ENV", "production")
	t.Setenv("CORDUM_API_KEYS", "")
	t.Setenv("CORDUM_API_KEY", "")
	t.Setenv("API_KEY", "")
	if _, err := newBasicAuthProvider("default"); err == nil {
		t.Fatalf("expected api key requirement in production")
	}
}

// ---------------------------------------------------------------------------
// Public path ceiling tests
// ---------------------------------------------------------------------------

// maliciousPathAuth is a provider that claims every /api/ path is public.
type maliciousPathAuth struct{ publicPathAuth }

func (m *maliciousPathAuth) IsPublicPath(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

func TestPublicPathCeilingBlocksArbitraryPaths(t *testing.T) {
	auth := &maliciousPathAuth{}
	handler := apiKeyMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// /api/v1/jobs should be blocked even though provider says it's public.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/jobs: expected 401 got %d", rr.Code)
	}

	// /api/v1/config should be blocked too — not in ceiling.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/config: expected 401 got %d", rr.Code)
	}
}

func TestPublicPathCeilingAllowsWhitelistedPaths(t *testing.T) {
	auth := &maliciousPathAuth{}
	handler := apiKeyMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/api/v1/auth/config", "/api/v1/auth/login"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s: expected 200 got %d", path, rr.Code)
		}
	}
}

func TestTenantMiddlewareCeilingBlocksArbitraryPaths(t *testing.T) {
	auth := &maliciousPathAuth{}
	handler := tenantMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// /api/v1/jobs without tenant header should get 403 (not bypassed).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("GET /api/v1/jobs: expected 403 got %d", rr.Code)
	}
}

func TestTenantMiddlewareCeilingAllowsWhitelistedPaths(t *testing.T) {
	auth := &maliciousPathAuth{}
	handler := tenantMiddleware(auth, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("GET /api/v1/auth/config: expected 200 got %d", rr.Code)
	}
}
