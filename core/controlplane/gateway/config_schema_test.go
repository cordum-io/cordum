package gateway

import (
	"bytes"
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
	setRR := httptest.NewRecorder()
	s.handleSetConfig(setRR, setReq)
	if setRR.Code != http.StatusNoContent {
		t.Fatalf("set config: %d %s", setRR.Code, setRR.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config?scope=system&scope_id=default", nil)
	getRR := httptest.NewRecorder()
	s.handleGetConfig(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get config: %d %s", getRR.Code, getRR.Body.String())
	}

	effReq := httptest.NewRequest(http.MethodGet, "/api/v1/config/effective?org_id=default", nil)
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
	regRR := httptest.NewRecorder()
	s.handleRegisterSchema(regRR, regReq)
	if regRR.Code != http.StatusNoContent {
		t.Fatalf("register schema: %d %s", regRR.Code, regRR.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/schemas", nil)
	listRR := httptest.NewRecorder()
	s.handleListSchemas(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list schemas: %d %s", listRR.Code, listRR.Body.String())
	}

	getSchemaReq := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/test/schema", nil)
	getSchemaReq.SetPathValue("id", "test/schema")
	getSchemaRR := httptest.NewRecorder()
	s.handleGetSchema(getSchemaRR, getSchemaReq)
	if getSchemaRR.Code != http.StatusOK {
		t.Fatalf("get schema: %d %s", getSchemaRR.Code, getSchemaRR.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/schemas/test/schema", nil)
	delReq.SetPathValue("id", "test/schema")
	delRR := httptest.NewRecorder()
	s.handleDeleteSchema(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete schema: %d %s", delRR.Code, delRR.Body.String())
	}
}
