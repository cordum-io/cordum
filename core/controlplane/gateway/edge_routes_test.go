package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	edgeRouteTestAPIKey  = "edge-route-test-key"
	edgeRouteTenant      = "tenant-edge-a"
	edgeRouteOtherAPIKey = "edge-route-other-key"
	edgeRouteOtherTenant = "tenant-edge-b"
)

type edgeRouteExpectation struct {
	method string
	path   string
}

func TestGatewayEdgeRoutesRegisteredAndTenantScoped(t *testing.T) {
	s, _ := newEdgeRouteTestServer(t)
	routes := make(map[string]routeInfo, len(s.Routes()))
	for _, route := range s.Routes() {
		routes[route.methodPathKey()] = route
	}

	for _, want := range edgeRouteExpectations() {
		got, ok := routes[want.method+" "+want.path]
		if !ok {
			t.Fatalf("missing Edge route registration for %s %s", want.method, want.path)
		}
		if got.Auth == "public" {
			t.Fatalf("Edge route %s %s was registered as public", want.method, want.path)
		}
		if got.Auth != "tenant" {
			t.Fatalf("Edge route %s %s auth = %q, want tenant", want.method, want.path, got.Auth)
		}
	}
}

func TestGatewayEdgeRoutesRequireAuthTenantAndReachHandlers(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	missingAuth := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401", rr.Code)
	}

	missingTenant := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	addEdgeRouteAuth(missingTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingTenant)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing tenant status = %d, want 403", rr.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions", nil)
	addEdgeRouteAuth(authorized)
	authorized.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authorized)
	if rr.Code == http.StatusNotFound {
		t.Fatalf("authorized Edge sessions list returned 404; route is not wired")
	}
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
		t.Fatalf("authorized Edge sessions list was rejected by auth/tenant middleware: %d", rr.Code)
	}
}

func TestGatewayEdgeSessionCreateRejectsBadJSONAndTenantMismatch(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", strings.NewReader(`{"agent_product":`))
	addEdgeRouteAuth(badJSON)
	badJSON.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, badJSON)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400", rr.Code)
	}

	mismatchBody := []byte(`{"tenant_id":"tenant-edge-b","agent_product":"claude-code"}`)
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader(mismatchBody))
	addEdgeRouteAuth(mismatch)
	mismatch.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, mismatch)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("body tenant mismatch status = %d, want 403", rr.Code)
	}
}

func TestGatewayEdgeSessionCreateUsesExistingBodyLimit(t *testing.T) {
	t.Setenv(envGatewayMaxJSONBodyBytes, "32")
	_, handler := newEdgeRouteTestServer(t)

	body := bytes.Repeat([]byte("x"), 64)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/sessions", bytes.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("oversized Edge session body status = %d, want existing tier-limit 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "max_body_bytes") {
		t.Fatalf("oversized Edge session response did not use existing max_body_bytes error")
	}
}

func TestGatewayEdgeSessionLifecycleResponseContract(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"repo":"github.com/cordum/cordum",
		"git_branch":"main",
		"cwd":"D:/Cordum/cordum",
		"policy_snapshot":"snap-edge-005",
		"policy_mode":"observe",
		"enforcement_layers":{"pre_tool_use":true},
		"labels":{"purpose":"edge005"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertNoEdgeTokenLeak(t, create.Body.String())

	var createResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, create, &createResp)
	if createResp.SessionID == "" {
		t.Fatalf("create session response missing session_id: %#v", createResp)
	}
	if createResp.ExecutionID == "" {
		t.Fatalf("create session response missing execution_id: %#v", createResp)
	}
	if createResp.TraceID == "" {
		t.Fatalf("create session response missing trace_id: %#v", createResp)
	}
	if createResp.PolicySnapshot != "snap-edge-005" {
		t.Fatalf("create session policy_snapshot = %q, want snap-edge-005", createResp.PolicySnapshot)
	}
	if createResp.DashboardURL != "/edge/sessions/"+createResp.SessionID {
		t.Fatalf("dashboard_url = %q, want relative session URL", createResp.DashboardURL)
	}
	if createResp.Session.SessionID != createResp.SessionID {
		t.Fatalf("nested session_id = %q, want %q", createResp.Session.SessionID, createResp.SessionID)
	}
	if createResp.Session.TenantID != edgeRouteTenant {
		t.Fatalf("session tenant_id = %q, want %q", createResp.Session.TenantID, edgeRouteTenant)
	}
	if createResp.Session.PrincipalID != "principal-edge-a" {
		t.Fatalf("principal_id = %q, want auth principal fallback", createResp.Session.PrincipalID)
	}
	if createResp.Session.Status != edgecore.SessionStatusRunning {
		t.Fatalf("session status = %q, want running", createResp.Session.Status)
	}
	if createResp.Session.PolicyMode != edgecore.PolicyModeObserve {
		t.Fatalf("session policy_mode = %q, want observe", createResp.Session.PolicyMode)
	}
	if !createResp.Session.EnforcementLayers["pre_tool_use"] {
		t.Fatalf("session enforcement_layers missing pre_tool_use=true: %#v", createResp.Session.EnforcementLayers)
	}
	if createResp.Execution.ExecutionID != createResp.ExecutionID {
		t.Fatalf("nested execution_id = %q, want %q", createResp.Execution.ExecutionID, createResp.ExecutionID)
	}
	if createResp.Execution.SessionID != createResp.SessionID {
		t.Fatalf("initial execution session_id = %q, want %q", createResp.Execution.SessionID, createResp.SessionID)
	}
	if createResp.Execution.TraceID != createResp.TraceID {
		t.Fatalf("initial execution trace_id = %q, want %q", createResp.Execution.TraceID, createResp.TraceID)
	}
	if createResp.Execution.PolicySnapshot != createResp.PolicySnapshot {
		t.Fatalf("initial execution policy_snapshot = %q, want %q", createResp.Execution.PolicySnapshot, createResp.PolicySnapshot)
	}

	get := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get session status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	var gotSession edgecore.EdgeSession
	decodeEdgeRouteJSON(t, get, &gotSession)
	if gotSession.SessionID != createResp.SessionID || gotSession.TraceID != createResp.TraceID {
		t.Fatalf("get session mismatch: %#v", gotSession)
	}

	list := edgeRouteGET(t, handler, "/api/v1/edge/sessions")
	if list.Code != http.StatusOK {
		t.Fatalf("list sessions status = %d, want 200 body=%s", list.Code, list.Body.String())
	}
	var page edgeSessionPageJSON
	decodeEdgeRouteJSON(t, list, &page)
	if len(page.Items) != 1 || page.Items[0].SessionID != createResp.SessionID {
		t.Fatalf("list sessions items = %#v, want one created session", page.Items)
	}

	heartbeat := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID+"/heartbeat", `{}`)
	if heartbeat.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, want 200 body=%s", heartbeat.Code, heartbeat.Body.String())
	}
	var heartbeatResp edgeHeartbeatResponseJSON
	decodeEdgeRouteJSON(t, heartbeat, &heartbeatResp)
	if heartbeatResp.SessionID != createResp.SessionID || !heartbeatResp.HeartbeatAlive {
		t.Fatalf("heartbeat response = %#v, want same session and alive=true", heartbeatResp)
	}

	end := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+createResp.SessionID+"/end", `{"status":"ended"}`)
	if end.Code != http.StatusOK {
		t.Fatalf("end session status = %d, want 200 body=%s", end.Code, end.Body.String())
	}
	var ended edgecore.EdgeSession
	decodeEdgeRouteJSON(t, end, &ended)
	if ended.Status != edgecore.SessionStatusEnded || ended.EndedAt == nil {
		t.Fatalf("ended session = %#v, want status ended with ended_at", ended)
	}
}

func TestGatewayEdgeExecutionLifecycleResponseContract(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	create := edgeRoutePOST(t, handler, "/api/v1/edge/executions", `{
		"session_id":"`+session.SessionID+`",
		"adapter":"claude-code-hook",
		"mode":"local-dev",
		"workflow_run_id":"workflow-link-only",
		"step_id":"step-link-only",
		"job_id":"edge-job-link-only",
		"attempt":2,
		"worker_id":"worker-link-only",
		"policy_snapshot":"snap-execution-005",
		"labels":{"purpose":"edge005-exec"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create execution status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertNoEdgeTokenLeak(t, create.Body.String())

	var created edgecore.AgentExecution
	decodeEdgeRouteJSON(t, create, &created)
	if created.ExecutionID == "" {
		t.Fatalf("create execution missing execution_id: %#v", created)
	}
	if created.SessionID != session.SessionID || created.TenantID != edgeRouteTenant {
		t.Fatalf("execution tenant/session mismatch: %#v", created)
	}
	if created.Adapter != edgecore.AdapterClaudeCodeHook || created.Mode != edgecore.ExecutionModeLocalDev {
		t.Fatalf("execution adapter/mode = %q/%q", created.Adapter, created.Mode)
	}
	if created.WorkflowRunID != "workflow-link-only" || created.JobID != "edge-job-link-only" {
		t.Fatalf("execution optional links were not preserved: %#v", created)
	}
	if created.Status != edgecore.ExecutionStatusRunning {
		t.Fatalf("execution status = %q, want running", created.Status)
	}

	get := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+created.ExecutionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get execution status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	var got edgecore.AgentExecution
	decodeEdgeRouteJSON(t, get, &got)
	if got.ExecutionID != created.ExecutionID || got.JobID != "edge-job-link-only" {
		t.Fatalf("get execution mismatch: %#v", got)
	}

	linkedJob := edgeRouteGET(t, handler, "/api/v1/jobs/edge-job-link-only")
	if linkedJob.Code != http.StatusNotFound {
		t.Fatalf("linked job status = %d, want 404 proving execution create did not create Job state; body=%s", linkedJob.Code, linkedJob.Body.String())
	}

	end := edgeRoutePOST(t, handler, "/api/v1/edge/executions/"+created.ExecutionID+"/end", `{"status":"succeeded"}`)
	if end.Code != http.StatusOK {
		t.Fatalf("end execution status = %d, want 200 body=%s", end.Code, end.Body.String())
	}
	var ended edgecore.AgentExecution
	decodeEdgeRouteJSON(t, end, &ended)
	if ended.Status != edgecore.ExecutionStatusSucceeded || ended.EndedAt == nil {
		t.Fatalf("ended execution = %#v, want status succeeded with ended_at", ended)
	}
}

func TestGatewayEdgeSessionCreateRedactsBeforePersistenceAndResponse(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	rawPolicy := "sk-edge005sessionsecret"
	rawRepo := "secret://edge005-repo"
	rawCWD := "Authorization: Bearer edge005sessionbearer"
	rawLabel := "github_pat_edge005sessionsecret"
	body := `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"repo":"` + rawRepo + `",
		"cwd":"` + rawCWD + `",
		"policy_snapshot":"` + rawPolicy + `",
		"policy_mode":"observe",
		"enforcement_layers":{"secret://edge005-layer":true},
		"labels":{"api_key":"` + rawLabel + `","purpose":"edge005"}
	}`
	create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", body)
	if create.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertBodyOmits(t, create.Body.String(), rawPolicy, rawRepo, rawCWD, rawLabel, "secret://edge005-layer")
	if !bodyHasRedactionMarker(create.Body.String()) {
		t.Fatalf("create session response did not include redaction marker: %s", create.Body.String())
	}

	var created edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, create, &created)
	get := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+created.SessionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get session status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	assertBodyOmits(t, get.Body.String(), rawPolicy, rawRepo, rawCWD, rawLabel, "secret://edge005-layer")
	if !bodyHasRedactionMarker(get.Body.String()) {
		t.Fatalf("stored session readback did not include redaction marker: %s", get.Body.String())
	}
}

func TestGatewayEdgeExecutionCreateRedactsBeforePersistenceAndResponse(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	rawRun := "secret://edge005-workflow"
	rawJob := "sk-edge005executionsecret"
	rawWorker := "Authorization: Bearer edge005executionbearer"
	rawPolicy := "github_pat_edge005executionsecret"
	rawLabel := "secret://edge005-exec-label"
	create := edgeRoutePOST(t, handler, "/api/v1/edge/executions", `{
		"session_id":"`+session.SessionID+`",
		"workflow_run_id":"`+rawRun+`",
		"job_id":"`+rawJob+`",
		"worker_id":"`+rawWorker+`",
		"policy_snapshot":"`+rawPolicy+`",
		"labels":{"token":"`+rawLabel+`","purpose":"edge005-exec"}
	}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create execution status = %d, want 201 body=%s", create.Code, create.Body.String())
	}
	assertBodyOmits(t, create.Body.String(), rawRun, rawJob, rawWorker, rawPolicy, rawLabel)
	if !bodyHasRedactionMarker(create.Body.String()) {
		t.Fatalf("create execution response did not include redaction marker: %s", create.Body.String())
	}

	var created edgecore.AgentExecution
	decodeEdgeRouteJSON(t, create, &created)
	get := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+created.ExecutionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get execution status = %d, want 200 body=%s", get.Code, get.Body.String())
	}
	assertBodyOmits(t, get.Body.String(), rawRun, rawJob, rawWorker, rawPolicy, rawLabel)
	if !bodyHasRedactionMarker(get.Body.String()) {
		t.Fatalf("stored execution readback did not include redaction marker: %s", get.Body.String())
	}
}

func TestGatewayEdgeSessionCreateCleansUpPartialStateOnLaterFailure(t *testing.T) {
	for _, tc := range []struct {
		name                string
		failCreateExecution bool
		failHeartbeat       bool
	}{
		{name: "execution create failure", failCreateExecution: true},
		{name: "heartbeat failure", failHeartbeat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, handler := newEdgeRouteTestServer(t)
			base := edgecore.NewRedisStoreFromClient(s.jobStore.Client())
			failing := &edgeCreateSessionFailureStore{
				Store:               base,
				failCreateExecution: tc.failCreateExecution,
				failHeartbeat:       tc.failHeartbeat,
			}
			s.edgeStore = failing

			create := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
				"agent_product":"claude-code",
				"mode":"local-dev",
				"policy_snapshot":"snap-edge-005-cleanup"
			}`)
			if create.Code != http.StatusInternalServerError {
				t.Fatalf("create session status = %d, want 500 body=%s", create.Code, create.Body.String())
			}
			if failing.sessionID == "" {
				t.Fatalf("test store did not observe CreateSession")
			}
			if got, found, err := base.GetSession(context.Background(), edgeRouteTenant, failing.sessionID); err != nil || found || got != nil {
				t.Fatalf("partial session remained after failed create: found=%v got=%#v err=%v", found, got, err)
			}
			if failing.executionID != "" {
				if got, found, err := base.GetExecution(context.Background(), edgeRouteTenant, failing.executionID); err != nil || found || got != nil {
					t.Fatalf("partial execution remained after failed create: found=%v got=%#v err=%v", found, got, err)
				}
			}
		})
	}
}

func TestGatewayEdgeValidationErrorsDoNotEchoRequestPayload(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)

	secretLabelKey := strings.Repeat("x", edgecore.MaxLabelKeyBytes+1) + "-super-secret-token"
	body := `{
		"agent_product":"claude-code",
		"mode":"local-dev",
		"policy_snapshot":"snap-edge-005",
		"labels":{"` + secretLabelKey + `":"redacted-value"}
	}`
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid label status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "super-secret-token") || strings.Contains(rr.Body.String(), "redacted-value") {
		t.Fatalf("validation error echoed request payload/secret: %s", rr.Body.String())
	}
}

func TestGatewayEdgeErrorMappingAndTenantIsolation(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	otherTenantGet := httptest.NewRequest(http.MethodGet, "/api/v1/edge/sessions/"+session.SessionID, nil)
	addEdgeRouteAuthFor(otherTenantGet, edgeRouteOtherAPIKey)
	otherTenantGet.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, otherTenantGet)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status = %d, want 404 body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), session.SessionID) || strings.Contains(rr.Body.String(), edgeRouteTenant) {
		t.Fatalf("cross-tenant miss leaked protected identifiers: %s", rr.Body.String())
	}

	invalidEnd := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"running"}`)
	if invalidEnd.Code != http.StatusBadRequest {
		t.Fatalf("invalid terminal status = %d, want 400 body=%s", invalidEnd.Code, invalidEnd.Body.String())
	}

	staleEnd := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/end", `{"status":"ended","ended_at":"1970-01-01T00:00:00Z"}`)
	if staleEnd.Code != http.StatusBadRequest {
		t.Fatalf("invalid ended_at status = %d, want 400 body=%s", staleEnd.Code, staleEnd.Body.String())
	}

	s.edgeStore = nil
	unavailable := edgeRouteGET(t, handler, "/api/v1/edge/sessions")
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store status = %d, want 503 body=%s", unavailable.Code, unavailable.Body.String())
	}
	if unavailable.Body.String() != "{\"error\":\"service unavailable\",\"status\":503}\n" {
		t.Fatalf("nil store response = %q, want generic service unavailable", unavailable.Body.String())
	}
}

func newEdgeRouteTestServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[` +
			`{"key":"` + edgeRouteTestAPIKey + `","tenant":"` + edgeRouteTenant + `","role":"admin","principal_id":"principal-edge-a"},` +
			`{"key":"` + edgeRouteOtherAPIKey + `","tenant":"` + edgeRouteOtherTenant + `","role":"admin","principal_id":"principal-edge-b"}` +
			`]`,
	})
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return s, apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
}

func addEdgeRouteAuth(req *http.Request) {
	addEdgeRouteAuthFor(req, edgeRouteTestAPIKey)
}

func addEdgeRouteAuthFor(req *http.Request, apiKey string) {
	req.Header.Set("X-API-Key", edgeRouteTestAPIKey)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
}

type edgeSessionCreateResponseJSON struct {
	SessionID      string                  `json:"session_id"`
	ExecutionID    string                  `json:"execution_id"`
	TraceID        string                  `json:"trace_id"`
	PolicySnapshot string                  `json:"policy_snapshot"`
	DashboardURL   string                  `json:"dashboard_url"`
	Session        edgecore.EdgeSession    `json:"session"`
	Execution      edgecore.AgentExecution `json:"execution"`
}

type edgeSessionPageJSON struct {
	Items      []edgecore.EdgeSession `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

type edgeHeartbeatResponseJSON struct {
	SessionID      string `json:"session_id"`
	HeartbeatAlive bool   `json:"heartbeat_alive"`
}

func createEdgeRouteSession(t *testing.T, handler http.Handler) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"snap-session-for-execution",
		"policy_mode":"observe"
	}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create session status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &session)
	return session
}

func edgeRouteGET(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func edgeRoutePOST(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeEdgeRouteJSON(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON response %q: %v", rr.Body.String(), err)
	}
}

func assertNoEdgeTokenLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"hook_policy_token", "enterprise_hook_token", "api_key", "secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Edge response leaked forbidden token/secret field %q in %s", forbidden, body)
		}
	}
}

func assertBodyOmits(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if strings.Contains(body, value) {
			t.Fatalf("response leaked raw value %q in %s", value, body)
		}
	}
}

func bodyHasRedactionMarker(body string) bool {
	return strings.Contains(body, "<redacted>") || strings.Contains(body, `\u003credacted\u003e`)
}

type edgeCreateSessionFailureStore struct {
	edgecore.Store
	failCreateExecution bool
	failHeartbeat       bool
	sessionID           string
	executionID         string
}

func (s *edgeCreateSessionFailureStore) CreateSession(ctx context.Context, session edgecore.EdgeSession) error {
	s.sessionID = session.SessionID
	return s.Store.CreateSession(ctx, session)
}

func (s *edgeCreateSessionFailureStore) CreateExecution(ctx context.Context, execution edgecore.AgentExecution) error {
	s.executionID = execution.ExecutionID
	if s.failCreateExecution {
		return errors.New("injected create execution failure")
	}
	return s.Store.CreateExecution(ctx, execution)
}

func (s *edgeCreateSessionFailureStore) TouchHeartbeat(ctx context.Context, tenantID, sessionID string) error {
	if s.failHeartbeat {
		return errors.New("injected heartbeat failure")
	}
	return s.Store.TouchHeartbeat(ctx, tenantID, sessionID)
}

func edgeRouteExpectations() []edgeRouteExpectation {
	return []edgeRouteExpectation{
		{method: http.MethodPost, path: "/api/v1/edge/sessions"},
		{method: http.MethodGet, path: "/api/v1/edge/sessions"},
		{method: http.MethodGet, path: "/api/v1/edge/sessions/{session_id}"},
		{method: http.MethodPost, path: "/api/v1/edge/sessions/{session_id}/heartbeat"},
		{method: http.MethodPost, path: "/api/v1/edge/sessions/{session_id}/end"},
		{method: http.MethodPost, path: "/api/v1/edge/executions"},
		{method: http.MethodGet, path: "/api/v1/edge/executions/{execution_id}"},
		{method: http.MethodPost, path: "/api/v1/edge/executions/{execution_id}/end"},
		{method: http.MethodPost, path: "/api/v1/edge/evaluate"},
	}
}

func (r routeInfo) methodPathKey() string {
	return r.Method + " " + r.Path
}
