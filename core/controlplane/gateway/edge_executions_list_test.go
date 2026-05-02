package gateway

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

type edgeExecutionPageJSON struct {
	Items      []edgecore.AgentExecution `json:"items"`
	NextCursor string                    `json:"next_cursor"`
}

func TestGatewayEdgeExecutionsListRouteRegisteredAndTenantScoped(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	routes := make(map[string]routeInfo, len(s.Routes()))
	for _, route := range s.Routes() {
		routes[route.methodPathKey()] = route
	}

	got, ok := routes[http.MethodGet+" /api/v1/edge/executions"]
	if !ok {
		t.Fatalf("missing Edge executions list route registration")
	}
	if got.Auth != "tenant" {
		t.Fatalf("Edge executions list auth = %q, want tenant", got.Auth)
	}

	session := createEdgeRouteSession(t, handler)
	missingAuth := httptest.NewRequest(http.MethodGet, "/api/v1/edge/executions?session_id="+session.SessionID, nil)
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401 body=%s", rr.Code, rr.Body.String())
	}
}

func TestGatewayEdgeExecutionsListByJobAndWorkflowRun(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	first := createLinkedEdgeRouteExecution(t, handler, session.SessionID, "job-edge-026", "run-edge-026", "step-build")
	second := createLinkedEdgeRouteExecution(t, handler, session.SessionID, "job-other", "run-edge-026", "step-review")
	createLinkedEdgeRouteExecution(t, handler, session.SessionID, "job-other", "run-other", "step-ignore")

	jobRR := edgeRouteGET(t, handler, "/api/v1/edge/executions?job_id=job-edge-026")
	if jobRR.Code != http.StatusOK {
		t.Fatalf("job execution list status = %d, want 200 body=%s", jobRR.Code, jobRR.Body.String())
	}
	var jobPage edgeExecutionPageJSON
	decodeEdgeRouteJSON(t, jobRR, &jobPage)
	assertExecutionIDsEqualSet(t, jobPage.Items, []string{first.ExecutionID})
	if jobPage.Items[0].JobID != "job-edge-026" {
		t.Fatalf("job-filtered execution job_id = %q, want job-edge-026", jobPage.Items[0].JobID)
	}

	runRR := edgeRouteGET(t, handler, "/api/v1/edge/executions?workflow_run_id=run-edge-026")
	if runRR.Code != http.StatusOK {
		t.Fatalf("run execution list status = %d, want 200 body=%s", runRR.Code, runRR.Body.String())
	}
	var runPage edgeExecutionPageJSON
	decodeEdgeRouteJSON(t, runRR, &runPage)
	assertExecutionIDsEqualSet(t, runPage.Items, []string{first.ExecutionID, second.ExecutionID})
}

func TestGatewayEdgeExecutionsListRejectsMissingIndexAndOmitsCrossTenant(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	createLinkedEdgeRouteExecution(t, handler, session.SessionID, "job-edge-026", "run-edge-026", "step-build")

	missingIndex := edgeRouteGET(t, handler, "/api/v1/edge/executions")
	assertEdgeErrorShape(t, missingIndex, http.StatusBadRequest, edgeErrCodeInvalidRequest)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/executions?job_id=job-edge-026", nil)
	addEdgeRouteAuthFor(req, edgeRouteOtherAPIKey)
	req.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cross-tenant list status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	var page edgeExecutionPageJSON
	decodeEdgeRouteJSON(t, rr, &page)
	if len(page.Items) != 0 {
		t.Fatalf("cross-tenant list returned %#v, want empty", page.Items)
	}
	assertBodyOmits(t, rr.Body.String(), edgeRouteTenant, session.SessionID)
}

func createLinkedEdgeRouteExecution(
	t *testing.T,
	handler http.Handler,
	sessionID string,
	jobID string,
	runID string,
	stepID string,
) edgecore.AgentExecution {
	t.Helper()
	body := `{"session_id":"` + sessionID + `","adapter":"claude-code-hook","mode":"local-dev",` +
		`"job_id":"` + jobID + `","workflow_run_id":"` + runID + `","step_id":"` + stepID + `"}`
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/executions", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create linked execution status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var execution edgecore.AgentExecution
	decodeEdgeRouteJSON(t, rr, &execution)
	return execution
}

func assertExecutionIDsEqualSet(t *testing.T, got []edgecore.AgentExecution, want []string) {
	t.Helper()
	gotIDs := make([]string, 0, len(got))
	for _, item := range got {
		gotIDs = append(gotIDs, item.ExecutionID)
	}
	sort.Strings(gotIDs)
	sort.Strings(want)
	if strings.Join(gotIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("execution ids = %#v, want %#v", gotIDs, want)
	}
}
