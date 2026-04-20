package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigAndSchemaHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	configPayload := map[string]any{
		"scope":    "system",
		"scope_id": "default",
		"data": map[string]any{
			"feature": "on",
		},
	}
	body, _ := json.Marshal(configPayload)
	setReq := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewReader(body))
	setReq.Header.Set("X-Tenant-ID", "default")
	setRR := httptest.NewRecorder()
	s.handleSetConfig(setRR, setReq)
	if setRR.Code != http.StatusNoContent {
		t.Fatalf("set config: %d %s", setRR.Code, setRR.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getRR := httptest.NewRecorder()
	s.handleGetConfig(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get config: %d %s", getRR.Code, getRR.Body.String())
	}

	effReq := httptest.NewRequest(http.MethodGet, "/api/v1/config/effective?org_id=default", nil)
	effReq.Header.Set("X-Tenant-ID", "default")
	effRR := httptest.NewRecorder()
	s.handleGetEffectiveConfig(effRR, effReq)
	if effRR.Code != http.StatusOK {
		t.Fatalf("effective config: %d %s", effRR.Code, effRR.Body.String())
	}

	schemaPayload := map[string]any{
		"id": "test/schema",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}
	schemaBody, _ := json.Marshal(schemaPayload)
	regReq := httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewReader(schemaBody))
	regReq.Header.Set("X-Tenant-ID", "default")
	regRR := httptest.NewRecorder()
	s.handleRegisterSchema(regRR, regReq)
	if regRR.Code != http.StatusNoContent {
		t.Fatalf("register schema: %d %s", regRR.Code, regRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
	listRR := httptest.NewRecorder()
	s.handleListSchemas(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list schemas: %d %s", listRR.Code, listRR.Body.String())
	}

	getSchemaReq := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/test/schema", nil)
	getSchemaReq.Header.Set("X-Tenant-ID", "default")
	getSchemaReq.SetPathValue("id", "test/schema")
	getSchemaRR := httptest.NewRecorder()
	s.handleGetSchema(getSchemaRR, getSchemaReq)
	if getSchemaRR.Code != http.StatusOK {
		t.Fatalf("get schema: %d %s", getSchemaRR.Code, getSchemaRR.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/schemas/test/schema", nil)
	delReq.Header.Set("X-Tenant-ID", "default")
	delReq.SetPathValue("id", "test/schema")
	delRR := httptest.NewRecorder()
	s.handleDeleteSchema(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete schema: %d %s", delRR.Code, delRR.Body.String())
	}
}

func TestSchemaRegister_ViewerForbidden(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer"}]`,
	})

	schemaPayload := map[string]any{
		"id":     "test/viewer",
		"schema": map[string]any{"type": "object"},
	}
	body, _ := json.Marshal(schemaPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schemas", bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	authCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleRegisterSchema(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer on schema register, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSchemaList_ViewerAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"viewer-key","role":"viewer"}]`,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	req.Header.Set("X-Tenant-ID", "default")
	authCtx := &AuthContext{Role: "viewer", Tenant: "default"}
	req = req.WithContext(context.WithValue(req.Context(), authContextKey{}, authCtx))

	rec := httptest.NewRecorder()
	s.handleListSchemas(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for viewer on schema list, got %d: %s", rec.Code, rec.Body.String())
	}
}
