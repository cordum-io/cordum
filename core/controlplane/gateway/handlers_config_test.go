package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
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
	authCtx := &auth.AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))

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
	authCtx := &auth.AuthContext{Role: "admin", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))

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
	requireStableErrorCode(t, rec, http.StatusNotFound, "CONFIG_NOT_FOUND")
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

func TestSetConfigRejectsBundlesInSystemDefault(t *testing.T) {
	s, _, _ := newTestGateway(t)

	payload := map[string]any{
		"bundles": map[string]any{
			"test-pack/default": map[string]any{"content": "allow"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusBadRequest, "CONFIG_KEY_FORBIDDEN")
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp["error"] != "bundles must be written to system/policy scope, not system/default" {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestHandleRegisterSchemaMissingIDReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewBufferString(`{"schema":{"type":"object"}}`)), &auth.AuthContext{
		Tenant: "default",
		Role:   "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleRegisterSchema(rec, req)

	requireStableErrorCode(t, rec, http.StatusBadRequest, "CONFIG_SCHEMA_VIOLATION")
}

func TestSetConfigAllowsBundlesInSystemPolicy(t *testing.T) {
	s, _, _ := newTestGateway(t)

	payload := map[string]any{
		"scope":    "system",
		"scope_id": "policy",
		"data": map[string]any{
			"bundles": map[string]any{
				"test-pack/default": map[string]any{"content": "allow"},
			},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleSetConfig(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "policy")
	if err != nil {
		t.Fatalf("get policy config: %v", err)
	}
	bundles, ok := doc.Data["bundles"].(map[string]any)
	if !ok || bundles["test-pack/default"] == nil {
		t.Fatalf("expected bundles written to system/policy, got %v", doc.Data["bundles"])
	}
}

func TestMigrateLegacyPolicyBundlesMovesDefaultBundlesToPolicy(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.default": "default"},
			},
			"bundles": map[string]any{
				"legacy-only": map[string]any{"content": "legacy"},
				"existing":    map[string]any{"content": "stale"},
			},
		},
	}); err != nil {
		t.Fatalf("seed default config: %v", err)
	}
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"existing": map[string]any{"content": "current"},
			},
		},
	}); err != nil {
		t.Fatalf("seed policy config: %v", err)
	}

	migrated, count, err := migrateLegacyPolicyBundles(ctx, s.configSvc)
	if err != nil {
		t.Fatalf("migrate legacy bundles: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to run")
	}
	if count != 2 {
		t.Fatalf("expected 2 legacy bundles counted, got %d", count)
	}

	defaultDoc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get default config: %v", err)
	}
	if _, exists := defaultDoc.Data["bundles"]; exists {
		t.Fatalf("expected bundles removed from system/default, got %v", defaultDoc.Data["bundles"])
	}
	if _, ok := defaultDoc.Data["pools"].(map[string]any); !ok {
		t.Fatalf("expected sibling pools data preserved, got %v", defaultDoc.Data["pools"])
	}

	policyDoc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "policy")
	if err != nil {
		t.Fatalf("get policy config: %v", err)
	}
	bundles, ok := policyDoc.Data["bundles"].(map[string]any)
	if !ok {
		t.Fatalf("expected bundles map in policy doc, got %T", policyDoc.Data["bundles"])
	}
	legacyOnly, ok := bundles["legacy-only"].(map[string]any)
	if !ok || legacyOnly["content"] != "legacy" {
		t.Fatalf("expected legacy-only bundle migrated, got %v", bundles["legacy-only"])
	}
	existing, ok := bundles["existing"].(map[string]any)
	if !ok || existing["content"] != "current" {
		t.Fatalf("expected existing policy bundle preserved, got %v", bundles["existing"])
	}

	migrated, count, err = migrateLegacyPolicyBundles(ctx, s.configSvc)
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if migrated || count != 0 {
		t.Fatalf("expected idempotent no-op on second run, got migrated=%v count=%d", migrated, count)
	}
}
