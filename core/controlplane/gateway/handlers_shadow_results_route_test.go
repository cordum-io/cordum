package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestShadowResultsRouteRegistered_Summary asserts
// GET /api/v1/policy/shadows/{id}/results/summary is wired into the mux.
// The request supplies a valid from/to range so the handler runs end-to-end;
// any response other than the ServeMux's default 404 proves the route resolves.
func TestShadowResultsRouteRegistered_Summary(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	u := "/api/v1/policy/shadows/test~bundle/results/summary?from=0&to=1000"
	req := adminCtx(httptest.NewRequest(http.MethodGet, u, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("GET summary route not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestShadowResultsRouteRegistered_Comparisons asserts
// GET /api/v1/policy/shadows/{id}/results/comparisons is wired into the mux.
func TestShadowResultsRouteRegistered_Comparisons(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	u := "/api/v1/policy/shadows/test~bundle/results/comparisons?from=0&to=1000"
	req := adminCtx(httptest.NewRequest(http.MethodGet, u, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("GET comparisons route not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestShadowResultsRouteRegistered_Timeseries asserts
// GET /api/v1/policy/shadows/{id}/results/timeseries is wired into the mux.
// Uses bucket=1m (the backend's whitelisted string enum, not the plan's
// erroneous bucketMs numeric form).
func TestShadowResultsRouteRegistered_Timeseries(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	u := "/api/v1/policy/shadows/test~bundle/results/timeseries?from=0&to=60000&bucket=1m"
	req := adminCtx(httptest.NewRequest(http.MethodGet, u, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("GET timeseries route not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
