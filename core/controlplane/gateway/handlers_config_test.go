package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
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

// TestGetConfig_MissingDefault_Returns200Empty verifies that GET /api/v1/config
// returns 200 with an empty JSON object on clean installs (no config in Redis).
func TestGetConfig_MissingDefault_Returns200Empty(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty object, got %v", body)
	}
}

// TestGetConfig_MissingExplicitScope_Returns404 verifies that GET /api/v1/config
// with a non-default scope still returns 404 when the config doesn't exist.
func TestGetConfig_MissingExplicitScope_Returns404(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=nonexistent", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestGetConfig_ExistingDefault_Returns200WithData verifies that GET /api/v1/config
// returns the stored data when a system/default config exists.
func TestGetConfig_ExistingDefault_Returns200WithData(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	// Seed config via the config service directly.
	doc := &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"safetyStance": "strict", "rateLimitPerKey": 100},
	}
	if err := s.configSvc.Set(ctx, doc); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["safetyStance"] != "strict" {
		t.Fatalf("expected safetyStance=strict, got %v", body["safetyStance"])
	}
	if body["rateLimitPerKey"] != float64(100) {
		t.Fatalf("expected rateLimitPerKey=100, got %v", body["rateLimitPerKey"])
	}
}

// TestConfigBootstrap_FreshDeploy verifies that EnsureDefault creates a usable
// config document on a fresh deploy, and GET /api/v1/config returns it.
func TestConfigBootstrap_FreshDeploy(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	// Before bootstrap: GET /config returns empty object (graceful fallback).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleGetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-bootstrap: expected 200, got %d", rec.Code)
	}

	// Run EnsureDefault (what gateway startup does).
	if err := s.configSvc.EnsureDefault(ctx); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	// After bootstrap: GET /config returns the seeded defaults.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec = httptest.NewRecorder()
	s.handleGetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-bootstrap: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Must contain the seeded safety section.
	safety, ok := body["safety"].(map[string]any)
	if !ok {
		t.Fatalf("expected safety section in bootstrapped config, got %v", body)
	}
	if safety["enabled"] != true {
		t.Fatalf("expected safety.enabled=true, got %v", safety["enabled"])
	}
}

// TestSetConfig_ThenGet_Roundtrip verifies that POST then GET roundtrips correctly.
func TestSetConfig_ThenGet_Roundtrip(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// POST flat config (dashboard format — auto-wraps as system/default).
	payload := map[string]any{"maintenanceMode": true}
	body, _ := json.Marshal(payload)
	setReq := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	setReq.Header.Set("X-Tenant-ID", "default")
	setRR := httptest.NewRecorder()
	s.handleSetConfig(setRR, setReq)
	if setRR.Code != http.StatusNoContent {
		t.Fatalf("set config: %d %s", setRR.Code, setRR.Body.String())
	}

	// GET should return the data.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getRR := httptest.NewRecorder()
	s.handleGetConfig(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get config: %d %s", getRR.Code, getRR.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(getRR.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["maintenanceMode"] != true {
		t.Fatalf("expected maintenanceMode=true, got %v", result["maintenanceMode"])
	}
}

// TestConfigWritePublishesNotification verifies that handleSetConfig publishes
// a sys.config.changed NATS notification after a successful config write.
func TestConfigWritePublishesNotification(t *testing.T) {
	s, stubBus, _ := newTestGateway(t)

	payload := map[string]any{"testKey": "testValue"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify NATS notification was published.
	stubBus.mu.Lock()
	var found bool
	for _, msg := range stubBus.published {
		if msg.subject == capsdk.SubjectConfigChanged {
			found = true
			alert := msg.packet.GetAlert()
			if alert == nil {
				t.Fatalf("expected Alert payload in config change notification")
			}
			if alert.Message != "config changed" {
				t.Fatalf("expected message='config changed', got %q", alert.Message)
			}
			if alert.Details["scope"] != "system" {
				t.Fatalf("expected scope=system, got %q", alert.Details["scope"])
			}
			if alert.Details["scope_id"] != "default" {
				t.Fatalf("expected scope_id=default, got %q", alert.Details["scope_id"])
			}
			break
		}
	}
	stubBus.mu.Unlock()

	if !found {
		t.Fatalf("expected sys.config.changed notification to be published")
	}
}

// TestConfigWritePublishesNotification_NilBus verifies that handleSetConfig
// works gracefully when bus is nil (no NATS available).
func TestConfigWritePublishesNotification_NilBus(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.bus = nil // simulate no NATS

	payload := map[string]any{"noBus": true}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 even without bus, got %d: %s", rec.Code, rec.Body.String())
	}
}
