package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlePutAndGetArtifact(t *testing.T) {
	s, _, _ := newTestGateway(t)

	payload := map[string]any{
		"content":      "hello",
		"content_type": "text/plain",
	}
	body, _ := json.Marshal(payload)
	putReq := httptest.NewRequest(http.MethodPost, "/api/v1/artifacts", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	s.handlePutArtifact(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected put status: %d", putRec.Code)
	}
	var putResp map[string]any
	if err := json.NewDecoder(putRec.Body).Decode(&putResp); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	ptr, _ := putResp["artifact_ptr"].(string)
	if ptr == "" {
		t.Fatalf("missing artifact pointer")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/artifacts/"+ptr, nil)
	getReq.SetPathValue("ptr", ptr)
	getRec := httptest.NewRecorder()
	s.handleGetArtifact(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected get status: %d", getRec.Code)
	}
	var getResp map[string]any
	if err := json.NewDecoder(getRec.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	encoded, _ := getResp["content_base64"].(string)
	decoded, _ := base64.StdEncoding.DecodeString(encoded)
	if string(decoded) != "hello" {
		t.Fatalf("unexpected artifact content")
	}
}
