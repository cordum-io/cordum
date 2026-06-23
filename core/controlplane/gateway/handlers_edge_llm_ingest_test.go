package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/llmingest"
	"github.com/cordum/cordum/core/licensing"
)

// awsExampleKey matches the edge redactor's AWS key pattern and is the
// canonical AWS docs example key — safe to embed in a test.
const llmTestAWSKey = "AKIAIOSFODNN7EXAMPLE"

const testLLMProxyRole = "llm_proxy"

func enableLLMIngest(t *testing.T) {
	t.Helper()
	t.Setenv(envLLMIngestEnabled, "true")
}

func setupLLMIngestRBAC(t *testing.T, s *server) {
	t.Helper()
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
	})
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        "user",
		Description: "test same-tenant job writer without llm ingest permission",
		Permissions: []string{auth.PermJobsWrite},
	}); err != nil {
		t.Fatalf("put user role: %v", err)
	}
	if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{
		Name:        testLLMProxyRole,
		Description: "test trusted llm proxy",
		Permissions: []string{auth.PermJobsWrite, auth.PermLLMIngest},
	}); err != nil {
		t.Fatalf("put llm proxy role: %v", err)
	}
}

func llmIngestAuthContext(role, principalID string) *auth.AuthContext {
	return &auth.AuthContext{
		Tenant:      edgeRouteTenant,
		PrincipalID: principalID,
		Role:        role,
		AuthSource:  auth.AuthSourceAPIKey,
	}
}

// createLLMIngestParents creates a session (owned by a developer principal, NOT
// the proxy) plus an execution under the given adapter — the proxy is shared
// tenant infrastructure, so ingest binds to the llm-proxy adapter, not a
// per-session collector.
func createLLMIngestParents(t *testing.T, s *server, suffix string, adapter edgecore.AgentAdapter) (edgecore.EdgeSession, edgecore.AgentExecution) {
	t.Helper()
	now := time.Now().UTC()
	session := edgecore.EdgeSession{
		SessionID:      "sess-llm-" + suffix,
		TenantID:       edgeRouteTenant,
		PrincipalID:    "dev-" + suffix,
		PrincipalType:  edgecore.PrincipalTypeHuman,
		AgentProduct:   "github-copilot",
		AgentVersion:   "test",
		Mode:           edgecore.SessionModeEnterpriseManaged,
		TraceID:        "trace-llm-" + suffix,
		PolicySnapshot: "snap-llm",
		PolicyMode:     edgecore.PolicyModeEnforce,
		Status:         edgecore.SessionStatusRunning,
		RiskSummary:    edgecore.RiskSummary{MaxRisk: edgecore.RiskLevelLow},
		StartedAt:      now,
	}
	if err := s.edgeStore.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create llm session: %v", err)
	}
	execution := edgecore.AgentExecution{
		ExecutionID:    "exec-llm-" + suffix,
		SessionID:      session.SessionID,
		TenantID:       edgeRouteTenant,
		Adapter:        adapter,
		Mode:           edgecore.ExecutionModeEnterpriseManaged,
		TraceID:        session.TraceID,
		WorkerID:       "dev-" + suffix,
		PolicySnapshot: session.PolicySnapshot,
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      now,
	}
	if err := s.edgeStore.CreateExecution(context.Background(), execution); err != nil {
		t.Fatalf("create llm execution: %v", err)
	}
	return session, execution
}

func llmIngestBody(proxyID, sessionID, executionID, sourceEventID, content string) string {
	return llmIngestBodyWithNonce(proxyID, sessionID, executionID, sourceEventID, content, "")
}

func llmIngestBodyWithNonce(proxyID, sessionID, executionID, sourceEventID, content, nonce string) string {
	event := map[string]any{
		"tenant_id":       edgeRouteTenant,
		"session_id":      sessionID,
		"execution_id":    executionID,
		"source_event_id": sourceEventID,
		"observed_at":     "2026-06-24T12:00:00Z",
		"kind":            "llm.request.pre",
		"provider":        "anthropic",
		"model":           "claude-opus-4-8",
		"direction":       "prompt",
		"content":         content,
	}
	batch := map[string]any{
		"source": map[string]any{"source_id": proxyID},
		"events": []any{event},
	}
	if nonce != "" {
		batch["nonce"] = nonce
	}
	out, err := json.Marshal(batch)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func postLLMIngestWithAuth(t *testing.T, s *server, authCtx *auth.AuthContext, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/llm/events", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, authCtx)
	rr := httptest.NewRecorder()
	s.handleEdgeLLMIngest(rr, req)
	return rr
}

func decodeLLMIngestResponse(t *testing.T, rr *httptest.ResponseRecorder) llmIngestResponse {
	t.Helper()
	var resp llmIngestResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	return resp
}

func TestLLMIngestDisabledReturns503(t *testing.T) {
	// Feature flag unset (default).
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", "sess-x", "exec-x", "evt-x", "hello"))
	assertEdgeErrorShape(t, rr, http.StatusServiceUnavailable, edgeErrCodeServiceUnavailable)
}

func TestLLMIngest_RejectsRegularUser(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	_, execution := createLLMIngestParents(t, s, "regular", edgecore.AdapterLLMProxy)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext("user", "principal-user"),
		llmIngestBody("principal-user", "sess-llm-regular", execution.ExecutionID, "evt-1", "hello"))
	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
}

func TestLLMIngest_AcceptsCleanPrompt(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "clean", edgecore.AdapterLLMProxy)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-clean", "summarize the repo"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeLLMIngestResponse(t, rr)
	if resp.AcceptedCount != 1 {
		t.Fatalf("accepted_count = %d, want 1", resp.AcceptedCount)
	}
	if len(resp.Decisions) != 1 || resp.Decisions[0].Decision != llmingest.DecisionRecord {
		t.Fatalf("want one record decision, got %+v", resp.Decisions)
	}
}

func TestLLMIngest_RedactsSecretInPrompt(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "secret", edgecore.AdapterLLMProxy)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-secret",
			"deploy with "+llmTestAWSKey+" now"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeLLMIngestResponse(t, rr)
	if len(resp.Decisions) != 1 {
		t.Fatalf("want one decision, got %+v", resp.Decisions)
	}
	d := resp.Decisions[0]
	if d.Decision != llmingest.DecisionRedact {
		t.Fatalf("decision = %q, want redact", d.Decision)
	}
	if strings.Contains(rr.Body.String(), llmTestAWSKey) {
		t.Fatalf("response leaked the raw secret: %s", rr.Body.String())
	}
	found := false
	for _, f := range d.Findings {
		if f == "aws_credential" {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings missing aws_credential: %v", d.Findings)
	}
}

func TestLLMIngest_RejectsTenantMismatch(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "tmismatch", edgecore.AdapterLLMProxy)
	// Body event claims a different tenant than the X-Tenant-ID header.
	body := strings.Replace(
		llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-tm", "hi"),
		`"tenant_id":"`+edgeRouteTenant+`"`, `"tenant_id":"other-tenant"`, 1)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"), body)
	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeTenantMismatch)
}

func TestLLMIngest_RejectsMismatchedSourceID(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "srcmismatch", edgecore.AdapterLLMProxy)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-2", session.SessionID, execution.ExecutionID, "evt-sm", "hi"))
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
		t.Fatalf("source mismatch status = %d, want 400 or 403 body=%s", rr.Code, rr.Body.String())
	}
}

func TestLLMIngest_RejectsNonLLMProxyExecution(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	// Execution created under the hook adapter — the proxy must not be able to
	// annotate it with LLM events.
	session, execution := createLLMIngestParents(t, s, "hookexec", edgecore.AdapterClaudeCodeHook)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-hook", "hi"))
	assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
}

func TestLLMIngest_RejectsMissingParents(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	rr := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"),
		llmIngestBody("llm-proxy-1", "sess-missing", "exec-missing", "evt-missing", "hi"))
	if rr.Code == http.StatusCreated {
		t.Fatalf("missing parents should not succeed; body=%s", rr.Body.String())
	}
	if rr.Code < 400 || rr.Code >= 500 {
		t.Fatalf("missing parents status = %d, want a 4xx", rr.Code)
	}
}

func TestLLMIngest_ReplayDedup(t *testing.T) {
	enableLLMIngest(t)
	s, _ := newEdgeRouteTestServer(t)
	setupLLMIngestRBAC(t, s)
	session, execution := createLLMIngestParents(t, s, "replay", edgecore.AdapterLLMProxy)
	nonce := "nonce-llm-replay-0001"
	body := llmIngestBodyWithNonce("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-replay", "hi", nonce)

	first := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"), body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first submit status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	second := postLLMIngestWithAuth(t, s, llmIngestAuthContext(testLLMProxyRole, "llm-proxy-1"), body)
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 body=%s", second.Code, second.Body.String())
	}
	resp := decodeLLMIngestResponse(t, second)
	if !resp.Replayed {
		t.Fatalf("replayed flag should be true on duplicate nonce: %+v", resp)
	}
}
