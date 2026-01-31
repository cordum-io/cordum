package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleLogin_ValidAPIKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key","role":"admin","principal_id":"alice","tenant":"default"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"alice","password":"test-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Token != "test-key" {
		t.Fatalf("expected token test-key, got %q", resp.Token)
	}
	if resp.User.Tenant != "default" {
		t.Fatalf("expected tenant default, got %q", resp.User.Tenant)
	}
	if resp.User.ID != "alice" {
		t.Fatalf("expected user ID alice, got %q", resp.User.ID)
	}
	if len(resp.User.Roles) == 0 || resp.User.Roles[0] != "admin" {
		t.Fatalf("expected role admin, got %v", resp.User.Roles)
	}
}

func TestHandleLogin_InvalidAPIKey(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"valid-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"user","password":"invalid-key"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_EmptyPassword(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `{"username":"user","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	body := `not valid json`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSession_ValidSession(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"session-key","role":"viewer","principal_id":"bob"}]`,
	})
	s := &server{auth: provider, tenant: "default"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	authCtx := &AuthContext{
		APIKey:      "session-key",
		Tenant:      "default",
		PrincipalID: "bob",
		Role:        "viewer",
	}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))
	rec := httptest.NewRecorder()

	s.handleSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp AuthLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.User.ID != "bob" {
		t.Fatalf("expected user ID bob, got %q", resp.User.ID)
	}
	if len(resp.User.Roles) == 0 || resp.User.Roles[0] != "viewer" {
		t.Fatalf("expected role viewer, got %v", resp.User.Roles)
	}
}

func TestHandleSession_NoAuthContext(t *testing.T) {
	s := &server{tenant: "default"}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	rec := httptest.NewRecorder()

	s.handleSession(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleLogout_Success(t *testing.T) {
	s := &server{tenant: "default"}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()

	s.handleLogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestLoginIsPublicPath(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})

	if !provider.IsPublicPath("/api/v1/auth/login") {
		t.Fatal("expected /api/v1/auth/login to be public")
	}
	if !provider.IsPublicPath("/api/v1/auth/config") {
		t.Fatal("expected /api/v1/auth/config to be public")
	}
	if provider.IsPublicPath("/api/v1/auth/session") {
		t.Fatal("expected /api/v1/auth/session to NOT be public")
	}
	if provider.IsPublicPath("/api/v1/jobs") {
		t.Fatal("expected /api/v1/jobs to NOT be public")
	}
}

func TestBasicAuthProvidesAuthConfig(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-key"}]`,
	})

	cfg := provider.AuthConfig()
	if !cfg.PasswordEnabled {
		t.Fatal("expected password_enabled to be true")
	}
	if cfg.SessionTTL != "24h" {
		t.Fatalf("expected session_ttl 24h, got %q", cfg.SessionTTL)
	}
	if cfg.DefaultTenant != "default" {
		t.Fatalf("expected default tenant, got %q", cfg.DefaultTenant)
	}
}

func TestBasicAuthProvidesAuthConfig_NoKeys(t *testing.T) {
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_ALLOW_INSECURE_NO_AUTH": "1",
	})

	cfg := provider.AuthConfig()
	if cfg.PasswordEnabled {
		t.Fatal("expected password_enabled to be false when no keys")
	}
}
