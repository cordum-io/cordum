package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/resource"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
)

type gatewayResourceResolver struct {
	content []byte
	media   string
	err     error
	trusted resource.TrustedContext
	ref     *agentv1.ResourceRef
	calls   int
}

func (r *gatewayResourceResolver) Resolve(
	_ context.Context,
	ref *agentv1.ResourceRef,
	trusted resource.TrustedContext,
) (resource.ResolvedResource, error) {
	r.calls++
	r.ref = ref
	r.trusted = trusted
	return resource.ResolvedResource{Content: r.content, MediaType: r.media}, r.err
}

func TestHandleResolveMemoryUsesAuthenticatedJobScope(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := s.jobStore.SetTenant(context.Background(), "job-1", "tenant-a"); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	resolver := &gatewayResourceResolver{content: []byte(`{"ok":true}`), media: "application/json"}
	s.memoryResourceReader = resourceio.Reader{Resolver: resolver}
	body := `{"job_id":"job-1","reference":{"resolverId":"operator"}}`
	req := authenticatedMemoryRequest(body, "tenant-a")
	rec := httptest.NewRecorder()

	s.handleResolveMemory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := (resource.TrustedContext{TenantID: "tenant-a", JobID: "job-1"})
	if resolver.trusted != want || resolver.ref.GetResolverId() != "operator" {
		t.Fatalf("resolver got trusted=%#v ref=%#v", resolver.trusted, resolver.ref)
	}
	if strings.Contains(rec.Body.String(), "operator") || strings.Contains(rec.Body.String(), "uri") {
		t.Fatalf("response exposed reference details: %s", rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["media_type"] != "application/json" || response["size_bytes"] != float64(11) {
		t.Fatalf("unexpected response metadata: %#v", response)
	}
}

func TestHandleResolveMemoryRejectsUntrustedOrAmbiguousInput(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		tenant string
		status int
	}{
		{name: "outer tenant authority", body: `{"job_id":"job-1","tenant":"tenant-a","reference":{}}`, tenant: "tenant-a", status: 400},
		{name: "outer unknown", body: `{"job_id":"job-1","extra":true,"reference":{}}`, tenant: "tenant-a", status: 400},
		{name: "duplicate job id", body: `{"job_id":"job-1","job_id":"job-2","reference":{}}`, tenant: "tenant-a", status: 400},
		{name: "inner unknown", body: `{"job_id":"job-1","reference":{"resolverId":"operator","credential":"secret"}}`, tenant: "tenant-a", status: 400},
		{name: "duplicate inner field", body: `{"job_id":"job-1","reference":{"resolverId":"operator","resolverId":"other"}}`, tenant: "tenant-a", status: 400},
		{name: "trailing document", body: `{"job_id":"job-1","reference":{}} {}`, tenant: "tenant-a", status: 400},
		{name: "missing auth", body: `{"job_id":"job-1","reference":{}}`, status: 401},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			resolver := &gatewayResourceResolver{content: []byte("ok"), media: "text/plain"}
			s.memoryResourceReader = resourceio.Reader{Resolver: resolver}
			if err := s.jobStore.SetTenant(context.Background(), "job-1", "tenant-a"); err != nil {
				t.Fatalf("SetTenant() error = %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/memory/resolve", strings.NewReader(test.body))
			if test.tenant != "" {
				req = withAuth(req, &auth.AuthContext{Tenant: test.tenant, Role: "admin"})
			}
			rec := httptest.NewRecorder()
			s.handleResolveMemory(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, test.status, rec.Body.String())
			}
			if resolver.calls != 0 {
				t.Fatalf("resolver called %d times for rejected input", resolver.calls)
			}
		})
	}
}

func TestHandleResolveMemoryFailsClosedOnTenantOrResolver(t *testing.T) {
	tests := []struct {
		name       string
		jobTenant  string
		withReader bool
		status     int
	}{
		{name: "tenant mismatch", jobTenant: "tenant-b", withReader: true, status: 403},
		{name: "unknown job", withReader: true, status: 403},
		{name: "resolver unavailable", jobTenant: "tenant-a", status: 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			resolver := &gatewayResourceResolver{content: []byte("ok"), media: "text/plain"}
			if test.withReader {
				s.memoryResourceReader = resourceio.Reader{Resolver: resolver}
			}
			if test.jobTenant != "" {
				if err := s.jobStore.SetTenant(context.Background(), "job-1", test.jobTenant); err != nil {
					t.Fatalf("SetTenant() error = %v", err)
				}
			}
			req := authenticatedMemoryRequest(`{"job_id":"job-1","reference":{"resolverId":"operator"}}`, "tenant-a")
			rec := httptest.NewRecorder()
			s.handleResolveMemory(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, test.status, rec.Body.String())
			}
			if resolver.calls != 0 {
				t.Fatalf("resolver called %d times", resolver.calls)
			}
		})
	}
}

func TestHandleResolveMemoryBoundsRequestBody(t *testing.T) {
	s, _, _ := newTestGateway(t)
	large := `{"job_id":"job-1","reference":{"uri":"` + strings.Repeat("a", maxMemoryResolveRequestBytes) + `"}}`
	req := authenticatedMemoryRequest(large, "tenant-a")
	rec := httptest.NewRecorder()

	s.handleResolveMemory(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleResolveMemoryRejectsOversizedDeclarationBeforeResolution(t *testing.T) {
	s, _, _ := newTestGateway(t)
	if err := s.jobStore.SetTenant(context.Background(), "job-1", "tenant-a"); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	resolver := &gatewayResourceResolver{content: []byte("ok"), media: "text/plain"}
	s.memoryResourceReader = resourceio.Reader{Resolver: resolver}
	body := `{"job_id":"job-1","reference":{"resolverId":"operator","sizeBytes":1048577}}`
	req := authenticatedMemoryRequest(body, "tenant-a")
	rec := httptest.NewRecorder()

	s.handleResolveMemory(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver called %d times for oversized declaration", resolver.calls)
	}
}

func TestHandleGetMemoryDefaultsStrictAndCompatibilityIsObservable(t *testing.T) {
	strict := (&server{memStore: &stubMemStore{}})
	strictReq := withAuth(
		httptest.NewRequest(http.MethodGet, "/api/v1/memory?key=res:job-1", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin"},
	)
	strictRec := httptest.NewRecorder()
	strict.handleGetMemory(strictRec, strictReq)
	if strictRec.Code != http.StatusBadRequest {
		t.Fatalf("strict status = %d, want 400", strictRec.Code)
	}

	s, _, _ := newTestGateway(t)
	if err := s.jobStore.SetTenant(context.Background(), "job-1", "tenant-a"); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	if err := s.memStore.PutResult(context.Background(), store.MakeResultKey("job-1"), []byte("ok")); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	var observed resourceio.LegacyUse
	s.WithLegacyMemoryCompatibility(func(use resourceio.LegacyUse) { observed = use })
	req := withAuth(
		httptest.NewRequest(http.MethodGet, "/api/v1/memory?key=res:job-1", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "user-1"},
	)
	rec := httptest.NewRecorder()
	s.handleGetMemory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("compat status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := (resourceio.LegacyUse{Component: "gateway.memory", TenantID: "tenant-a", JobID: "job-1"})
	if observed != want {
		t.Fatalf("legacy observation = %#v, want %#v", observed, want)
	}
}

func authenticatedMemoryRequest(body, tenant string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory/resolve", bytes.NewBufferString(body))
	return withAuth(req, &auth.AuthContext{Tenant: tenant, Role: "admin", PrincipalID: "user-1"})
}
