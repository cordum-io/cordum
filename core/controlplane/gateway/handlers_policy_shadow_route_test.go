package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// isMuxDefault404 distinguishes Go's http.ServeMux built-in "not found"
// response ("404 page not found\n") from handler-written 404 bodies.
// Handler-written 404s carry the gateway's JSON error envelope, so treating
// those as "route registered, handler ran" is correct.
func isMuxDefault404(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusNotFound && strings.TrimSpace(rec.Body.String()) == "404 page not found"
}

// TestShadowRouteRegistered_PUT asserts PUT /api/v1/policy/shadows/{id}
// is wired into the mux. Before registration the mux returns its default 404
// "404 page not found\n"; after registration the handler runs and returns a
// JSON error for bad body or a success response.
func TestShadowRouteRegistered_PUT(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	body := strings.NewReader(`{"content":"version: \"1\"\nrules: []\n"}`)
	req := adminCtx(httptest.NewRequest(http.MethodPut, "/api/v1/policy/shadows/test~bundle", body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("PUT /api/v1/policy/shadows/{id} not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestShadowRouteRegistered_GET asserts GET /api/v1/policy/shadows/{id}
// is wired into the mux.
func TestShadowRouteRegistered_GET(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/shadows/test~bundle", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("GET /api/v1/policy/shadows/{id} not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestShadowRouteRegistered_DELETE asserts DELETE /api/v1/policy/shadows/{id}
// is wired into the mux.
func TestShadowRouteRegistered_DELETE(t *testing.T) {
	_, mux := newRouteCoverageMux(t)
	req := adminCtx(httptest.NewRequest(http.MethodDelete, "/api/v1/policy/shadows/test~bundle", nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if isMuxDefault404(rec) {
		t.Fatalf("DELETE /api/v1/policy/shadows/{id} not registered: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
