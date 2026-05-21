package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/licensing"
)

type operatorErrorBody struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Status int    `json:"status"`
}

func assertOperatorErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want %d body=%s", rec.Code, status, rec.Body.String())
	}
	rawBody := rec.Body.String()
	var body operatorErrorBody
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rawBody)
	}
	if body.Code != code {
		t.Fatalf("code=%q want %q body=%s", body.Code, code, rawBody)
	}
	if body.Status != status {
		t.Fatalf("body.status=%d want %d body=%s", body.Status, status, rawBody)
	}
}

func TestHandleMCPUsageInvalidRangeReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/mcp/usage?since=2000&until=1000", nil))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleMCPUsage(rec, req)

	assertOperatorErrorCode(t, rec, http.StatusBadRequest, "MCP_RANGE_INVALID")
}

func TestHandleMCPOutboundInvalidSignatureStatusReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/mcp/outbound?sig_status=bogus", nil))
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handleMCPOutbound(rec, req)

	assertOperatorErrorCode(t, rec, http.StatusBadRequest, "MCP_SIGNATURE_STATUS_INVALID")
}

func TestHandleAgentToolVisibilityMissingIDReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/agents//tools", nil))
	rec := httptest.NewRecorder()
	s.handleAgentToolVisibility(rec, req)

	assertOperatorErrorCode(t, rec, http.StatusBadRequest, "MCP_AGENT_ID_REQUIRED")
}

func TestHandleMCPVerifyMethodRequiredReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/mcp/verify-signature", strings.NewReader(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleMCPVerifySignature(rec, req)

	assertOperatorErrorCode(t, rec, http.StatusBadRequest, "MCP_VERIFY_REQUEST_INVALID")
}

func TestHandleDelegateAgentMissingTargetReturnsStableCode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-a/delegate", strings.NewReader(`{}`)))
	req.SetPathValue("id", "agent-a")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleDelegateAgent(rec, req)

	assertOperatorErrorCode(t, rec, http.StatusBadRequest, "DELEGATION_REQUEST_INVALID")
}
