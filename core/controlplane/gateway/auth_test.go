package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
		"CORDUM_SUPER_SECRET_API_TOKEN",
		"API_KEY",
	} {
		t.Setenv(key, "")
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
}

func TestAuthenticateAllowsMissingKeyWhenNotRequired(t *testing.T) {
	provider := newBasicAuthForTest(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	ctx, err := provider.AuthenticateHTTP(req)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if ctx.Tenant != "default" {
		t.Fatalf("expected default tenant, got %q", ctx.Tenant)
	}
}

func TestAuthenticateRequiresKeyWhenConfigured(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": "key1",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
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
	req.Header.Set("X-API-Key", "bad")
	if _, err := provider.AuthenticateHTTP(req); err == nil {
		t.Fatalf("expected invalid api key error")
	}
}

func TestResolveTenantAndAccess(t *testing.T) {
	provider := newBasicAuthForTest(t, nil)
	s := &server{tenant: "default", auth: provider}
	if got, err := s.resolveTenant(httptest.NewRequest(http.MethodGet, "/", nil), ""); err != nil || got != "default" {
		t.Fatalf("expected default tenant, got %q err=%v", got, err)
	}
	if _, err := s.resolveTenant(httptest.NewRequest(http.MethodGet, "/", nil), "team-b"); err == nil {
		t.Fatalf("expected tenant access denied")
	}
	if err := s.requireTenantAccess(httptest.NewRequest(http.MethodGet, "/", nil), "team-b"); err == nil {
		t.Fatalf("expected tenant access denied")
	}
}

func TestResolvePrincipal(t *testing.T) {
	provider := newBasicAuthForTest(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Principal-Id", "alice")
	if got, err := provider.ResolvePrincipal(req, ""); err != nil || got != "alice" {
		t.Fatalf("expected principal alice, got %q err=%v", got, err)
	}
}

func TestRequireRoleNoop(t *testing.T) {
	provider := newBasicAuthForTest(t, nil)
	s := &server{auth: provider}
	req := requestWithAuthContext(&AuthContext{})
	if err := s.requireRole(req, "admin"); err != nil {
		t.Fatalf("expected role check to be noop: %v", err)
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
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if auth.called {
		t.Fatalf("expected auth not to be called for public path")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}
