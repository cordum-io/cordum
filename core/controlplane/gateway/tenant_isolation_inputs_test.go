package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	isoTenantA = "tenant-iso-a"
	isoTenantB = "tenant-iso-b"
)

func isoMarker(label string) string { return "ISO-" + label + "-" + uuid.NewString() }

// isoRequest authenticates as tenant; the auth tenant is never empty.
func isoRequest(method, target string, body []byte, tenant string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	return withAuth(req, &auth.AuthContext{Tenant: tenant, Role: "admin", PrincipalID: "principal-" + tenant})
}

// isoAssertNoBytes fails when the body carries the marker or the whole stored
// object, in plain text or base64.
func isoAssertNoBytes(t *testing.T, rec *httptest.ResponseRecorder, marker string, object []byte) {
	t.Helper()
	body := rec.Body.String()
	if strings.Contains(body, marker) || strings.Contains(body, base64.StdEncoding.EncodeToString(object)) {
		t.Fatalf("response carries the stored object: %s", body)
	}
}

func isoGetArtifact(s *server, ptr, tenant, header string) *httptest.ResponseRecorder {
	req := isoRequest(http.MethodGet, "/api/v1/artifacts/"+ptr, nil, tenant)
	if header != "" {
		req.Header.Set("X-Tenant-ID", header)
	}
	req.SetPathValue("ptr", ptr)
	rec := httptest.NewRecorder()
	s.handleGetArtifact(rec, req)
	return rec
}

func TestTenantIsolationArtifactGet(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	stamped := isoMarker("artifact-stamped")
	body, err := json.Marshal(artifactPutRequest{Content: stamped, ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	put := httptest.NewRecorder()
	s.handlePutArtifact(put, isoRequest(http.MethodPost, "/api/v1/artifacts", body, isoTenantB))
	var putResp struct {
		Ptr string `json:"artifact_ptr"`
	}
	if put.Code != http.StatusOK || json.Unmarshal(put.Body.Bytes(), &putResp) != nil || putResp.Ptr == "" {
		t.Fatalf("tenant B put: status %d body %s", put.Code, put.Body.String())
	}
	seed := func(content string, labels map[string]string) string {
		ptr, err := s.artifactStore.Put(ctx, []byte(content), artifacts.Metadata{ContentType: "text/plain", Labels: labels})
		if err != nil {
			t.Fatalf("seed artifact: %v", err)
		}
		return ptr
	}
	unlabeled, labelOnly := isoMarker("artifact-unlabeled"), isoMarker("artifact-tenant-label")
	cases := []struct{ name, ptr, content string }{
		{"label stamped by the gateway", putResp.Ptr, stamped},
		{"no tenant label", seed(unlabeled, nil), unlabeled},
		{"label tenant instead of tenant_id", seed(labelOnly, map[string]string{"tenant": isoTenantB}), labelOnly},
	}

	// Control: tenant B reads the stamped artifact through the same request.
	ownerRec := isoGetArtifact(s, putResp.Ptr, isoTenantB, "")
	var ownerResp struct {
		Content string `json:"content_base64"`
	}
	if err := json.Unmarshal(ownerRec.Body.Bytes(), &ownerResp); err != nil || ownerRec.Code != http.StatusOK {
		t.Fatalf("tenant B own read: status %d body %s", ownerRec.Code, ownerRec.Body.String())
	}
	if got, _ := base64.StdEncoding.DecodeString(ownerResp.Content); string(got) != stamped {
		t.Fatalf("tenant B own read returned %q", got)
	}

	for _, tc := range cases {
		for _, header := range []string{"", isoTenantB} {
			t.Run(tc.name+" header="+header, func(t *testing.T) {
				wantContent, wantMeta, err := s.artifactStore.Get(ctx, tc.ptr)
				if err != nil {
					t.Fatalf("read seeded artifact: %v", err)
				}
				rec := isoGetArtifact(s, tc.ptr, isoTenantA, header)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("tenant A read: status %d, want 403", rec.Code)
				}
				isoAssertNoBytes(t, rec, tc.content, []byte(tc.content))
				gotContent, gotMeta, err := s.artifactStore.Get(ctx, tc.ptr)
				if err != nil || !bytes.Equal(gotContent, wantContent) || !reflect.DeepEqual(gotMeta, wantMeta) {
					t.Fatalf("tenant B artifact changed: err=%v meta %+v -> %+v", err, wantMeta, gotMeta)
				}
			})
		}
	}
}

func TestTenantIsolationLegacyMemory(t *testing.T) {
	s, _, _ := newTestGateway(t) // legacy compatibility is on
	s.auth = &tenantStrictAuth{role: "admin"}
	resolverMarker := isoMarker("resolver")
	resolver := &gatewayResourceResolver{content: []byte(resolverMarker), media: "application/octet-stream"}
	s.memoryResourceReader.Resolver = resolver
	ctx := context.Background()

	const jobA, jobB = "job-iso-a", "job-iso-b"
	ctxMarker, resMarker := isoMarker("ctx"), isoMarker("res")
	ctxObject := []byte(`{"input":"` + ctxMarker + `"}`)
	resObject := []byte(`{"output":"` + resMarker + `"}`)
	ctxKey, resKey := store.MakeContextKey(jobB), store.MakeResultKey(jobB)
	for job, tenant := range map[string]string{jobA: isoTenantA, jobB: isoTenantB} {
		if err := s.jobStore.SetTenant(ctx, job, tenant); err != nil {
			t.Fatalf("SetTenant(%s): %v", job, err)
		}
	}
	if err := s.memStore.PutContext(ctx, ctxKey, ctxObject); err != nil {
		t.Fatalf("seed ctx: %v", err)
	}
	if err := s.memStore.PutResult(ctx, resKey, resObject); err != nil {
		t.Fatalf("seed res: %v", err)
	}
	read := func(query, tenant string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleGetMemory(rec, isoRequest(http.MethodGet, "/api/v1/memory?"+query, nil, tenant))
		return rec
	}

	probes := []struct {
		name, query, marker string
		object              []byte
	}{
		{"ctx pointer", "ptr=" + url.QueryEscape(store.PointerForKey(ctxKey)), ctxMarker, ctxObject},
		{"res pointer", "ptr=" + url.QueryEscape(store.PointerForKey(resKey)), resMarker, resObject},
		{"ctx key", "key=" + url.QueryEscape(ctxKey), ctxMarker, ctxObject},
		{"res key", "key=" + url.QueryEscape(resKey), resMarker, resObject},
	}
	for _, p := range probes {
		t.Run("memory read "+p.name, func(t *testing.T) {
			owner := read(p.query, isoTenantB)
			if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), p.marker) {
				t.Fatalf("tenant B own read: status %d body %s", owner.Code, owner.Body.String())
			}
			rec := read(p.query, isoTenantA)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("tenant A read: status %d, want 403", rec.Code)
			}
			isoAssertNoBytes(t, rec, p.marker, p.object)
		})
	}

	t.Run("resolve with tenant B job id", func(t *testing.T) {
		reference := `{"resolver_id":"operator","uri":"redis://resources/cap:resource:` + isoTenantB + ":" + jobB + `:item"}`
		body := []byte(`{"job_id":"` + jobB + `","reference":` + reference + `}`)
		owner := httptest.NewRecorder()
		s.handleResolveMemory(owner, isoRequest(http.MethodPost, "/api/v1/memory/resolve", body, isoTenantB))
		if owner.Code != http.StatusOK || resolver.calls != 1 ||
			!strings.Contains(owner.Body.String(), base64.StdEncoding.EncodeToString(resolver.content)) {
			t.Fatalf("tenant B own resolve: status %d body %s calls %d", owner.Code, owner.Body.String(), resolver.calls)
		}
		rec := httptest.NewRecorder()
		s.handleResolveMemory(rec, isoRequest(http.MethodPost, "/api/v1/memory/resolve", body, isoTenantA))
		if rec.Code != http.StatusForbidden || resolver.calls != 1 {
			t.Fatalf("status %d, resolver calls %d; want 403 and only the owner call", rec.Code, resolver.calls)
		}
		isoAssertNoBytes(t, rec, resolverMarker, resolver.content)
	})

	for _, p := range []struct {
		name    string
		kind    resourceio.LegacyKind
		pointer string
		object  []byte
	}{
		{"ctx", resourceio.LegacyContext, store.PointerForKey(ctxKey), ctxObject},
		{"res", resourceio.LegacyResult, store.PointerForKey(resKey), resObject},
	} {
		t.Run("tenant A job with tenant B "+p.name+" pointer", func(t *testing.T) {
			owned, err := s.readGatewayJobResource(ctx, gatewayJobResourceRequest{
				JobID: jobB, TenantID: isoTenantB, LegacyPointer: p.pointer,
				LegacyKind: p.kind, Component: "gateway.isolation",
			})
			if err != nil || !bytes.Equal(owned.Content, p.object) {
				t.Fatalf("tenant B own read: %q, %v", owned.Content, err)
			}
			got, err := s.readGatewayJobResource(ctx, gatewayJobResourceRequest{
				JobID: jobA, TenantID: isoTenantA, LegacyPointer: p.pointer,
				LegacyKind: p.kind, Component: "gateway.isolation",
			})
			if !errors.Is(err, resourceio.ErrLegacyScopeMismatch) || len(got.Content) != 0 {
				t.Fatalf("err = %v, %d bytes returned; want ErrLegacyScopeMismatch and none", err, len(got.Content))
			}
		})
	}

	gotCtx, ctxErr := s.memStore.GetContext(ctx, ctxKey)
	gotRes, resErr := s.memStore.GetResult(ctx, resKey)
	if ctxErr != nil || resErr != nil || !bytes.Equal(gotCtx, ctxObject) || !bytes.Equal(gotRes, resObject) {
		t.Fatalf("tenant B objects changed: ctx err=%v res err=%v", ctxErr, resErr)
	}
}

func TestTenantIsolationJobRequest(t *testing.T) {
	s, _, _ := newTestGateway(t) // legacy compatibility is on
	s.auth = &tenantStrictAuth{role: "admin"}
	ctx := context.Background()

	const jobB = "job-iso-request-b"
	labelMarker, ctxMarker := isoMarker("job-label"), isoMarker("job-ctx")
	ctxKey := store.MakeContextKey(jobB)
	if err := s.memStore.PutContext(ctx, ctxKey, []byte(`{"input":"`+ctxMarker+`"}`)); err != nil {
		t.Fatalf("seed ctx: %v", err)
	}
	req := &pb.JobRequest{
		JobId: jobB, Topic: "job.test", TenantId: isoTenantB,
		ContextPtr: store.PointerForKey(ctxKey), Labels: map[string]string{"note": labelMarker},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("SetJobMeta: %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("SetJobRequest: %v", err)
	}
	if err := s.jobStore.SetState(ctx, jobB, model.JobStatePending); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	get := func(tenant string) *httptest.ResponseRecorder {
		httpReq := isoRequest(http.MethodGet, "/api/v1/jobs/"+jobB, nil, tenant)
		httpReq.SetPathValue("id", jobB)
		rec := httptest.NewRecorder()
		s.handleGetJob(rec, httpReq)
		return rec
	}

	// Control: tenant B reads its own job and sees both markers.
	owner := get(isoTenantB)
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), labelMarker) ||
		!strings.Contains(owner.Body.String(), ctxMarker) {
		t.Fatalf("tenant B own read: status %d body %s", owner.Code, owner.Body.String())
	}
	before, err := s.jobStore.GetJobRequest(ctx, jobB)
	if err != nil {
		t.Fatalf("read stored job request: %v", err)
	}

	rec := get(isoTenantA)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant A read: status %d, want 403", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, labelMarker) || strings.Contains(body, ctxMarker) {
		t.Fatalf("response carries the stored job input: %s", body)
	}
	after, err := s.jobStore.GetJobRequest(ctx, jobB)
	tenant, tenantErr := s.jobStore.GetTenant(ctx, jobB)
	if err != nil || !proto.Equal(before, after) || tenantErr != nil || tenant != isoTenantB {
		t.Fatalf("tenant B job changed: err=%v tenant=%q tenantErr=%v", err, tenant, tenantErr)
	}
}
