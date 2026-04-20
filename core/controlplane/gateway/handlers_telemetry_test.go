package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/telemetry"
)

func seedTelemetryCollector(t *testing.T, s *server) {
	t.Helper()

	store := telemetry.NewStoreWithClient(s.jobStore.Client())
	s.telemetry = telemetry.NewCollector(telemetry.CollectorOptions{
		Mode:              telemetry.ModeLocalOnly,
		Store:             store,
		TierProvider:      func() string { return "community" },
		JobStore:          s.jobStore,
		WorkflowStore:     s.workflowStore,
		ConfigSvc:         s.configSvc,
		SchemaRegistry:    s.schemaRegistry,
		TopicRegistry:     s.topicRegistry,
		WorkerCredentials: s.workerCredentialStore,
		TenantID:          "default",
	})
	if _, err := s.telemetry.CollectNow(context.Background()); err != nil {
		t.Fatalf("CollectNow(): %v", err)
	}
}

func TestTelemetryHandlersReturnDataForAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "admin"}
	seedTelemetryCollector(t, s)

	statusReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/status", nil), &AuthContext{Tenant: "default", Role: "admin"})
	statusRec := httptest.NewRecorder()
	s.handleGetTelemetryStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body=%s", statusRec.Code, statusRec.Body.String())
	}

	inspectReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/inspect", nil), &AuthContext{Tenant: "default", Role: "admin"})
	inspectRec := httptest.NewRecorder()
	s.handleGetTelemetryInspect(inspectRec, inspectReq)
	if inspectRec.Code != http.StatusOK {
		t.Fatalf("inspect code = %d, body=%s", inspectRec.Code, inspectRec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(inspectRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal inspect payload: %v", err)
	}
	if payload["schema_version"] == nil {
		t.Fatalf("expected schema_version in inspect payload, got %+v", payload)
	}

	exportReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/export", nil), &AuthContext{Tenant: "default", Role: "admin"})
	exportRec := httptest.NewRecorder()
	s.handleGetTelemetryExport(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export code = %d, body=%s", exportRec.Code, exportRec.Body.String())
	}
	if cd := exportRec.Header().Get("Content-Disposition"); cd == "" {
		t.Fatal("expected export content disposition header")
	}

	usageReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/usage", nil), &AuthContext{Tenant: "default", Role: "admin"})
	usageRec := httptest.NewRecorder()
	s.handleGetTelemetryUsage(usageRec, usageReq)
	if usageRec.Code != http.StatusOK {
		t.Fatalf("usage code = %d, body=%s", usageRec.Code, usageRec.Body.String())
	}
	var usage map[string]any
	if err := json.Unmarshal(usageRec.Body.Bytes(), &usage); err != nil {
		t.Fatalf("unmarshal usage payload: %v", err)
	}
	if usage["usage"] == nil || usage["workers"] == nil {
		t.Fatalf("expected usage and workers fields, got %+v", usage)
	}
}

func TestTelemetryHandlersRequireAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	seedTelemetryCollector(t, s)

	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/telemetry/status", nil), &AuthContext{Tenant: "default", Role: "viewer"})
	rec := httptest.NewRecorder()
	s.handleGetTelemetryStatus(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}
