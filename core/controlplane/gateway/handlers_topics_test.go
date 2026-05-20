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
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/infra/registry"
)

func TestHandleListTopics(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.topicRegistry.SetMany(context.Background(), []topicregistry.Registration{
		{Name: "job.alpha", Pool: "pool-a", Status: topicregistry.StatusActive},
		{Name: "job.beta", Pool: "pool-b", Status: topicregistry.StatusDeprecated},
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}
	snapshot := registry.Snapshot{
		Workers: []registry.WorkerSummary{
			{WorkerID: "w-1", Pool: "pool-a"},
			{WorkerID: "w-2", Pool: "pool-a"},
			{WorkerID: "w-3", Pool: "pool-c"},
		},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := s.memStore.PutResult(context.Background(), registry.SnapshotKey, data); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/topics", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	rec := httptest.NewRecorder()
	s.handleListTopics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []topicResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(resp.Items))
	}
	if resp.Items[0].Name != "job.alpha" || resp.Items[0].ActiveWorkerCount != 2 {
		t.Fatalf("unexpected first topic: %+v", resp.Items[0])
	}
	if resp.Items[1].Name != "job.beta" || resp.Items[1].ActiveWorkerCount != 0 {
		t.Fatalf("unexpected second topic: %+v", resp.Items[1])
	}
}

func TestHandleCreateDeleteTopic(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{},
				"pools": map[string]any{
					"pool-a": map[string]any{"status": "active"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pools config: %v", err)
	}

	body := []byte(`{"name":"job.external","pool":"pool-a","requires":["cap.a"],"risk_tags":["safe"]}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	rec := httptest.NewRecorder()
	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reg, registryEmpty, err := s.topicRegistry.GetForTenant(context.Background(), "default", "job.external")
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if registryEmpty || reg == nil {
		t.Fatalf("expected created topic to exist, got registryEmpty=%v reg=%v", registryEmpty, reg)
	}
	if reg.Pool != "pool-a" || reg.Status != topicregistry.StatusActive {
		t.Fatalf("unexpected topic record: %+v", reg)
	}

	delReq := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/topics/job.external", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	delReq.SetPathValue("name", "job.external")
	delRec := httptest.NewRecorder()
	s.handleDeleteTopic(delRec, delReq)

	if delRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", delRec.Code, delRec.Body.String())
	}
	reg, registryEmpty, err = s.topicRegistry.GetForTenant(context.Background(), "default", "job.external")
	if err != nil {
		t.Fatalf("get deleted topic: %v", err)
	}
	if reg != nil {
		t.Fatalf("expected deleted topic to be absent, got reg=%v registryEmpty=%v", reg, registryEmpty)
	}
}

func TestDeleteTopicScrubsPoolMapping(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.to-delete": "pool-a"},
				"pools": map[string]any{
					"pool-a": map[string]any{"status": "active"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pools config: %v", err)
	}
	if err := s.topicRegistry.Set(ctx, topicregistry.Registration{
		Name:     "job.to-delete",
		TenantID: "default",
		Pool:     "pool-a",
		Status:   topicregistry.StatusActive,
	}); err != nil {
		t.Fatalf("seed topic registry: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/topics/job.to-delete", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.SetPathValue("name", "job.to-delete")
	rec := httptest.NewRecorder()
	s.handleDeleteTopic(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get pools config: %v", err)
	}
	topics, _, err := extractPoolsFromConfig(doc)
	if err != nil {
		t.Fatalf("extract pools: %v", err)
	}
	if _, ok := topics["job.to-delete"]; ok {
		t.Fatalf("expected delete topic to scrub pool mapping, got topics=%v", topics)
	}
}

func TestHandleCreateTopicAllowsDisabledPackTopicWithoutPool(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body := []byte(`{"name":"job.demo-pack.echo","pack_id":"demo-pack","status":"disabled","input_schema_id":"demo-pack/Input","output_schema_id":"demo-pack/Output"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	rec := httptest.NewRecorder()
	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	reg, registryEmpty, err := s.topicRegistry.GetForTenant(context.Background(), "default", "job.demo-pack.echo")
	if err != nil {
		t.Fatalf("get topic: %v", err)
	}
	if registryEmpty || reg == nil {
		t.Fatalf("expected created disabled topic to exist, got registryEmpty=%v reg=%v", registryEmpty, reg)
	}
	if reg.Pool != "" || reg.PackID != "demo-pack" || reg.Status != topicregistry.StatusDisabled {
		t.Fatalf("unexpected disabled topic record: %+v", reg)
	}
}

func TestCreateTopicInvalidName(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.invalid!topic"}`)), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusBadRequest, "TOPIC_SCHEMA_VIOLATION")
}

func TestCreateTopicPoolNotFound(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.external","pool":"pool-a"}`)), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "POOL_NOT_FOUND")
}

func TestCreateTopicArrayTooLong(t *testing.T) {
	s, _, _ := newTestGateway(t)

	requires := make([]string, maxTopicArrayItems+1)
	for i := range requires {
		requires[i] = "cap.test"
	}
	body, err := json.Marshal(map[string]any{
		"name":     "job.external",
		"requires": requires,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewReader(body)), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusBadRequest, "TOPIC_SCHEMA_VIOLATION")
}

func TestDeleteTopicMissingReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/topics/job.missing", nil), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.SetPathValue("name", "job.missing")
	rr := httptest.NewRecorder()
	s.handleDeleteTopic(rr, req)

	requireStableErrorCode(t, rr, http.StatusNotFound, "TOPIC_NOT_FOUND")
}

func TestCreateTopicServiceFailure(t *testing.T) {
	s, _, _ := newTestGateway(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", bytes.NewBufferString(`{"name":"job.external"}`)).WithContext(ctx), &auth.AuthContext{
		Tenant: "default", Role: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleListTopics_ScopedToTenant verifies that handleListTopics returns
// only the caller tenant's records — never another tenant's topic schemas.
// Regression for PR #276 audit finding (CRITICAL, handlers_topics.go:46-84).
func TestHandleListTopics_ScopedToTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.topicRegistry.SetMany(context.Background(), []topicregistry.Registration{
		{Name: "job.tenantA-only", TenantID: "tenant-a", Pool: "pool-a", Status: topicregistry.StatusActive},
		{Name: "job.tenantB-secret", TenantID: "tenant-b", Pool: "pool-b", Status: topicregistry.StatusActive, RiskTags: []string{"confidential"}},
		{Name: "job.global", Pool: "pool-shared", Status: topicregistry.StatusActive},
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/topics", nil), &auth.AuthContext{
		Tenant: "tenant-a", Role: "viewer",
	})
	rec := httptest.NewRecorder()
	s.handleListTopics(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []topicResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := map[string]bool{}
	for _, item := range resp.Items {
		names[item.Name] = true
	}
	if names["job.tenantB-secret"] {
		t.Fatalf("CROSS-TENANT LEAK: tenant-a viewer saw tenant-b's topic in list: %+v", resp.Items)
	}
	if !names["job.tenantA-only"] {
		t.Fatalf("tenant-a should see its own topic, got %+v", resp.Items)
	}
	if !names["job.global"] {
		t.Fatalf("tenant-a should see global (no-tenant) topic, got %+v", resp.Items)
	}
}

// TestHandleCreateTopic_PersistsTenantID verifies that handleCreateTopic
// stamps the caller tenant onto the persisted Registration record instead of
// writing a global empty-TenantID entry that overwrites cross-tenant data.
// Regression for PR #276 audit finding (CRITICAL, handlers_topics.go:86-170).
func TestHandleCreateTopic_PersistsTenantID(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{},
				"pools": map[string]any{
					"pool-a": map[string]any{"status": "active"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed pools config: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"job.tenant-a-feature","pool":"pool-a"}`)
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/topics", body), &auth.AuthContext{
		Tenant: "tenant-a", Role: "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreateTopic(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	reg, _, err := s.topicRegistry.GetForTenant(context.Background(), "tenant-a", "job.tenant-a-feature")
	if err != nil {
		t.Fatalf("GetForTenant: %v", err)
	}
	if reg == nil {
		t.Fatalf("topic not persisted under tenant-a")
	}
	if reg.TenantID != "tenant-a" {
		t.Fatalf("expected TenantID=tenant-a, got %q — record is GLOBAL and visible to every tenant", reg.TenantID)
	}

	// tenant-b must not see it via tenant-scoped lookup.
	other, _, err := s.topicRegistry.GetForTenant(context.Background(), "tenant-b", "job.tenant-a-feature")
	if err != nil {
		t.Fatalf("GetForTenant b: %v", err)
	}
	if other != nil {
		t.Fatalf("CROSS-TENANT LEAK: tenant-b can read tenant-a's topic via GetForTenant: %+v", other)
	}
}

// TestHandleDeleteTopic_RejectsCrossTenant verifies that handleDeleteTopic
// cannot delete another tenant's topic by name guess. Returns 404 (not 403)
// to avoid an existence oracle.
// Regression for PR #276 audit finding (CRITICAL, handlers_topics.go:172-208).
func TestHandleDeleteTopic_RejectsCrossTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)

	if err := s.topicRegistry.SetMany(context.Background(), []topicregistry.Registration{
		{Name: "job.tenantB-target", TenantID: "tenant-b", Pool: "pool-b", Status: topicregistry.StatusActive},
	}); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	// Tenant A admin attempts to delete tenant B's topic by name guess.
	req := withAuth(httptest.NewRequest(http.MethodDelete, "/api/v1/topics/job.tenantB-target", nil), &auth.AuthContext{
		Tenant: "tenant-a", Role: "admin",
	})
	req.SetPathValue("name", "job.tenantB-target")
	rec := httptest.NewRecorder()
	s.handleDeleteTopic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("CROSS-TENANT DELETE ALLOWED: tenant-a admin deleted tenant-b's topic; expected 404 not %d (body=%s)", rec.Code, rec.Body.String())
	}
	requireStableErrorCode(t, rec, http.StatusNotFound, "TOPIC_NOT_FOUND")

	// Tenant B's topic must still exist.
	reg, _, err := s.topicRegistry.GetForTenant(context.Background(), "tenant-b", "job.tenantB-target")
	if err != nil {
		t.Fatalf("get tenant-b topic after attempted cross-tenant delete: %v", err)
	}
	if reg == nil {
		t.Fatalf("REGRESSION: tenant-b's topic was deleted by tenant-a admin's cross-tenant request")
	}
}
