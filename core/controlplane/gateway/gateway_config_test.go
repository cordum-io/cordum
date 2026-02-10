package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteSchemaForbiddenWithoutAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Set auth provider so requireRole actually checks.
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer"}]`,
	})
	s.auth = provider

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/schemas/test-schema", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Api-Key", "viewer-key")
	req.SetPathValue("id", "test-schema")

	// Inject auth context with viewer role (not admin).
	authCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleDeleteSchema(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSchemaAllowedForAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// Register a schema first so the delete has something to work with.
	if err := s.schemaRegistry.Register(context.Background(), "test-schema", []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("register schema: %v", err)
	}

	// Set auth provider with admin key.
	provider := newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"admin-key","role":"admin"}]`,
	})
	s.auth = provider

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/schemas/test-schema", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Api-Key", "admin-key")
	req.SetPathValue("id", "test-schema")

	// Inject auth context with admin role.
	authCtx := &AuthContext{Role: "admin", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleDeleteSchema(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for admin, got %d: %s", rec.Code, rec.Body.String())
	}
}
