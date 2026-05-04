package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

// crossTenantFixture stages a two-tenant Edge environment for cross-tenant
// negative tests. Tenant A and Tenant B each own a session + execution
// pre-created via authenticated POSTs through the real handler chain so
// every isolation assertion runs against the same auth + tenant
// middleware stack as positive-path tests.
type crossTenantFixture struct {
	server  *server
	handler http.Handler
	tenantA crossTenantPrincipal
	tenantB crossTenantPrincipal
}

// crossTenantPrincipal is one tenant slot with a pre-created session and
// execution. Cross-tenant tests authenticate as one principal and probe
// the other principal's resources via header injection or ID guessing.
type crossTenantPrincipal struct {
	tenantID    string
	apiKey      string
	principalID string
	session     edgeSessionCreateResponseJSON
	execution   edgecore.AgentExecution
}

func newCrossTenantFixture(t *testing.T) *crossTenantFixture {
	t.Helper()
	s, handler := newEdgeRouteTestServer(t)

	tenantA := crossTenantPrincipal{
		tenantID:    edgeRouteTenant,
		apiKey:      edgeRouteTestAPIKey,
		principalID: "principal-edge-a",
	}
	tenantA.session = createEdgeRouteSessionForTenant(t, handler, tenantA.apiKey, tenantA.tenantID)
	tenantA.execution = createCrossTenantExecution(t, handler, tenantA.apiKey, tenantA.tenantID, tenantA.session.SessionID)

	tenantB := crossTenantPrincipal{
		tenantID:    edgeRouteOtherTenant,
		apiKey:      edgeRouteOtherAPIKey,
		principalID: "principal-edge-b",
	}
	tenantB.session = createEdgeRouteSessionForTenant(t, handler, tenantB.apiKey, tenantB.tenantID)
	tenantB.execution = createCrossTenantExecution(t, handler, tenantB.apiKey, tenantB.tenantID, tenantB.session.SessionID)

	return &crossTenantFixture{
		server:  s,
		handler: handler,
		tenantA: tenantA,
		tenantB: tenantB,
	}
}

func createCrossTenantExecution(t *testing.T, handler http.Handler, apiKey, tenantID, sessionID string) edgecore.AgentExecution {
	t.Helper()
	rr := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, apiKey, tenantID, "/api/v1/edge/executions",
		`{"session_id":"`+sessionID+`","adapter":"claude-code-hook","mode":"local-dev"}`, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create execution as tenant %q status = %d, want 201 body=%s", tenantID, rr.Code, rr.Body.String())
	}
	var execution edgecore.AgentExecution
	if err := json.Unmarshal(rr.Body.Bytes(), &execution); err != nil {
		t.Fatalf("decode execution as tenant %q: %v body=%s", tenantID, err, rr.Body.String())
	}
	return execution
}

// crossTenantRequest builds a request authenticated as apiKey but targeting
// headerTenant. Pass headerTenant != apiKey's tenant to exercise X-Tenant-ID
// header injection — the gateway must derive tenant from auth, never body
// or header (task rail: Tenant MUST come from auth context, NOT body).
func crossTenantRequest(t *testing.T, method, path, body, apiKey, headerTenant string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	addEdgeRouteAuthFor(req, apiKey)
	if headerTenant != "" {
		req.Header.Set("X-Tenant-ID", headerTenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// do runs req through the fixture handler chain.
func (fix *crossTenantFixture) do(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	fix.handler.ServeHTTP(rr, req)
	return rr
}

// asAttacker authenticates as tenantA's admin and targets the path with
// tenantA's X-Tenant-ID header (the most realistic attack: a legitimate
// tenant probing IDs they may have observed). The path is expected to
// include tenantB's resource ID; isolation requires 404 (preferred) or 403.
func (fix *crossTenantFixture) asAttacker(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return fix.do(crossTenantRequest(t, method, path, body, fix.tenantA.apiKey, fix.tenantA.tenantID))
}

// asAttackerWithHeader authenticates as tenantA but advertises a different
// X-Tenant-ID header. Tests that the gateway ignores the spoofed header and
// derives tenant from auth.
func (fix *crossTenantFixture) asAttackerWithHeader(t *testing.T, method, path, body, headerTenant string) *httptest.ResponseRecorder {
	t.Helper()
	return fix.do(crossTenantRequest(t, method, path, body, fix.tenantA.apiKey, headerTenant))
}

// assertCrossTenantBlocked enforces the task's preferred outcome: 404 (no
// info leak) or 403. Anything else — including the resource leaking
// through with a 200 — is a hard failure.
func assertCrossTenantBlocked(t *testing.T, rr *httptest.ResponseRecorder, label string) {
	t.Helper()
	switch rr.Code {
	case http.StatusNotFound, http.StatusForbidden:
		return
	default:
		t.Fatalf("%s: cross-tenant access status = %d, want 404 or 403 body=%s", label, rr.Code, rr.Body.String())
	}
}

// assertNoTenantBLeak asserts that the response body does NOT echo
// tenantB's resource identifiers. Even on a 404 or 403, leaking the ID
// confirms its existence — defeating the no-info-leak property.
func (fix *crossTenantFixture) assertNoTenantBLeak(t *testing.T, rr *httptest.ResponseRecorder, label string) {
	t.Helper()
	body := rr.Body.String()
	leaks := []string{
		fix.tenantB.session.SessionID,
		fix.tenantB.execution.ExecutionID,
		fix.tenantB.tenantID,
		fix.tenantB.principalID,
	}
	for _, leak := range leaks {
		if leak == "" {
			continue
		}
		if strings.Contains(body, leak) {
			t.Fatalf("%s: response body leaked tenant B value %q body=%s", label, leak, body)
		}
	}
}

func TestCrossTenantFixtureProvisionsIsolatedTenants(t *testing.T) {
	fix := newCrossTenantFixture(t)

	if fix.tenantA.tenantID == fix.tenantB.tenantID {
		t.Fatalf("fixture tenants must differ: %q == %q", fix.tenantA.tenantID, fix.tenantB.tenantID)
	}
	if fix.tenantA.apiKey == fix.tenantB.apiKey {
		t.Fatalf("fixture API keys must differ: %q == %q", fix.tenantA.apiKey, fix.tenantB.apiKey)
	}
	if fix.tenantA.session.SessionID == "" || fix.tenantB.session.SessionID == "" {
		t.Fatalf("fixture sessions missing IDs: A=%q B=%q", fix.tenantA.session.SessionID, fix.tenantB.session.SessionID)
	}
	if fix.tenantA.session.SessionID == fix.tenantB.session.SessionID {
		t.Fatalf("session collision: A=%q == B=%q", fix.tenantA.session.SessionID, fix.tenantB.session.SessionID)
	}
	if fix.tenantA.execution.ExecutionID == fix.tenantB.execution.ExecutionID {
		t.Fatalf("execution collision: A=%q == B=%q", fix.tenantA.execution.ExecutionID, fix.tenantB.execution.ExecutionID)
	}
	if fix.tenantA.execution.TenantID != edgeRouteTenant {
		t.Fatalf("tenantA execution tenant_id = %q, want %q", fix.tenantA.execution.TenantID, edgeRouteTenant)
	}
	if fix.tenantB.execution.TenantID != edgeRouteOtherTenant {
		t.Fatalf("tenantB execution tenant_id = %q, want %q", fix.tenantB.execution.TenantID, edgeRouteOtherTenant)
	}
}
