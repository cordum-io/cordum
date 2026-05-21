package gateway

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

type codedErrorBody struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Status int    `json:"status"`
}

func requireStableErrorCode(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status=%d want %d body=%s", rr.Code, status, rr.Body.String())
	}
	var body codedErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rr.Body.String())
	}
	if body.Code != code {
		t.Fatalf("code=%q want %q body=%s", body.Code, code, rr.Body.String())
	}
	if body.Status != status {
		t.Fatalf("body.status=%d want %d body=%s", body.Status, status, rr.Body.String())
	}
}

func TestHandlePolicyGlobalValidationReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rr := putPolicyGlobal(t, s, map[string]any{
		"sections": map[string]any{
			"not_a_real_section": map[string]any{"content": globalInputContent},
		},
	})

	requireStableErrorCode(t, rr, http.StatusBadRequest, "POLICY_VALIDATION_FAILED")
}

func TestHandleEvalDatasetCreateCollisionReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := bindEvalDatasetRoutes(t, s)

	if rr := evalPostCreate(t, mux, evalCreateBody("coded-collision", 1, 1), "admin"); rr.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := evalPostCreate(t, mux, evalCreateBody("coded-collision", 1, 1), "admin")

	requireStableErrorCode(t, rr, http.StatusConflict, "EVAL_DATASET_VERSION_CONFLICT")
}

func TestHandlePackInstallMissingBundleReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("owner", "tester"); err != nil {
		t.Fatalf("write owner field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/packs/install", &body)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	s.handleInstallPack(rr, req)

	requireStableErrorCode(t, rr, http.StatusBadRequest, "PACK_INSTALL_INVALID")
}
