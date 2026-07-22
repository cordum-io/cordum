package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestHandleGetMemoryCompatibilityFailsClosedOnOwnership(t *testing.T) {
	tests := []struct {
		name       string
		jobStore   bool
		jobTenant  string
		authTenant string
		status     int
	}{
		{name: "job store unavailable", authTenant: "tenant-a", status: http.StatusServiceUnavailable},
		{name: "owner missing", jobStore: true, authTenant: "tenant-a", status: http.StatusForbidden},
		{name: "owner mismatch", jobStore: true, jobTenant: "tenant-b", authTenant: "tenant-a", status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var s *server
			if test.jobStore {
				s, _, _ = newTestGateway(t)
			} else {
				s = &server{memStore: &stubMemStore{result: map[string][]byte{"res:job-1": []byte("ok")}}}
			}
			if test.jobTenant != "" {
				if err := s.jobStore.SetTenant(context.Background(), "job-1", test.jobTenant); err != nil {
					t.Fatalf("SetTenant() error = %v", err)
				}
			}
			observed := 0
			s.WithLegacyMemoryCompatibility(func(resourceio.LegacyUse) { observed++ })
			identity := &auth.AuthContext{Tenant: test.authTenant, Role: "admin", AllowCrossTenant: true}
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/memory?key=res:job-1", nil), identity)
			rec := httptest.NewRecorder()
			s.handleGetMemory(rec, req)
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, test.status, rec.Body.String())
			}
			if observed != 0 {
				t.Fatalf("denied read emitted %d compatibility observations", observed)
			}
		})
	}
}

func TestHandleGetMemoryRejectsAmbiguousCompatibilityInput(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.WithLegacyMemoryCompatibility(nil)
	req := withAuth(
		httptest.NewRequest(http.MethodGet, "/api/v1/memory?key=res:job-1&ptr=redis://res:job-1", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin"},
	)
	rec := httptest.NewRecorder()
	s.handleGetMemory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestReadGatewayJobResourceEnforcesLegacyScopeAndDualContent(t *testing.T) {
	mem := &stubMemStore{context: map[string][]byte{
		"ctx:job-1": []byte("same"),
		"ctx:job-2": []byte("other"),
	}}
	resolver := &gatewayResourceResolver{content: []byte("same"), media: "application/octet-stream"}
	s := &server{memStore: mem, memoryResourceReader: resourceio.Reader{Resolver: resolver}}
	request := gatewayJobResourceRequest{
		JobID:         "job-1",
		TenantID:      "tenant-a",
		Reference:     &agentv1.ResourceRef{ResolverId: "operator"},
		LegacyPointer: "redis://ctx:job-1",
		LegacyKind:    resourceio.LegacyContext,
		Component:     "gateway.job",
	}
	if _, err := s.readGatewayJobResource(context.Background(), request); !errors.Is(err, resourceio.ErrStructuredRequired) {
		t.Fatalf("strict dual read error = %v, want ErrStructuredRequired", err)
	}
	s.WithLegacyMemoryCompatibility(nil)
	resolved, err := s.readGatewayJobResource(context.Background(), request)
	if err != nil || string(resolved.Content) != "same" {
		t.Fatalf("dual read = %q, %v", resolved.Content, err)
	}
	request.LegacyPointer = "redis://ctx:job-2"
	if _, err := s.readGatewayJobResource(context.Background(), request); !errors.Is(err, resourceio.ErrLegacyScopeMismatch) {
		t.Fatalf("cross-job read error = %v, want ErrLegacyScopeMismatch", err)
	}
	request.LegacyPointer = "redis://ctx:job-1"
	resolver.content = []byte("different")
	if _, err := s.readGatewayJobResource(context.Background(), request); !errors.Is(err, resourceio.ErrDualContentMismatch) {
		t.Fatalf("mismatched dual read error = %v, want ErrDualContentMismatch", err)
	}
}

func TestHandleGetJobDoesNotDereferenceLegacyInStrictMode(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.memoryResourceReader.Compatibility = resourceio.LegacyCompatibility{}
	seedGatewayJobResources(t, s, &pb.JobRequest{
		JobId: "job-1", TenantId: "tenant-a", Topic: "job.test",
		ContextPtr: "redis://ctx:job-1",
	})
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin"})
	req.SetPathValue("id", "job-1")
	rec := httptest.NewRecorder()

	s.handleGetJob(rec, req)

	response := decodeGatewayJobResponse(t, rec)
	if response["context"] != nil || response["result"] != nil {
		t.Fatalf("strict response dereferenced legacy data: %#v", response)
	}
	if response["context_ptr"] != "" || response["result_ptr"] != "" {
		t.Fatalf("strict response exposed legacy pointers: %#v", response)
	}
}

func TestHandleGetJobResolvesStructuredContext(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.memoryResourceReader = resourceio.Reader{Resolver: &gatewayResourceResolver{
		content: []byte("{\"prompt\":\"safe\"}"),
		media:   "application/json",
	}}
	seedGatewayJobResources(t, s, &pb.JobRequest{
		JobId: "job-1", TenantId: "tenant-a", Topic: "job.test",
		ContextRef: &agentv1.ResourceRef{ResolverId: "operator"},
	})
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-1", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin"})
	req.SetPathValue("id", "job-1")
	rec := httptest.NewRecorder()

	s.handleGetJob(rec, req)

	response := decodeGatewayJobResponse(t, rec)
	contextValue, ok := response["context"].(map[string]any)
	if !ok || contextValue["prompt"] != "safe" {
		t.Fatalf("structured context = %#v", response["context"])
	}
	if response["context_ptr"] != "" {
		t.Fatalf("structured response synthesized pointer: %#v", response)
	}
}

func seedGatewayJobResources(t *testing.T, s *server, request *pb.JobRequest) {
	t.Helper()
	ctx := context.Background()
	if err := s.jobStore.SetState(ctx, request.GetJobId(), model.JobStatePending); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}
	if err := s.jobStore.SetTopic(ctx, request.GetJobId(), request.GetTopic()); err != nil {
		t.Fatalf("SetTopic() error = %v", err)
	}
	if err := s.jobStore.SetTenant(ctx, request.GetJobId(), request.GetTenantId()); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, request); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	if err := s.memStore.PutContext(ctx, store.MakeContextKey(request.GetJobId()), []byte("{\"prompt\":\"legacy\"}")); err != nil {
		t.Fatalf("PutContext() error = %v", err)
	}
	if err := s.memStore.PutResult(ctx, store.MakeResultKey(request.GetJobId()), []byte("{\"result\":\"legacy\"}")); err != nil {
		t.Fatalf("PutResult() error = %v", err)
	}
	if err := s.jobStore.SetResultPtr(ctx, request.GetJobId(), "redis://res:"+request.GetJobId()); err != nil {
		t.Fatalf("SetResultPtr() error = %v", err)
	}
}

func decodeGatewayJobResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestListApprovalsUsesStructuredContextAndRejectsLegacyByDefault(t *testing.T) {
	tests := []struct {
		name         string
		strictLegacy bool
		structured   bool
		wantJobInput bool
	}{
		{name: "legacy strict", strictLegacy: true},
		{name: "structured", structured: true, wantJobInput: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			request := &pb.JobRequest{
				JobId: "job-approval-resource", TenantId: "tenant-a", Topic: "job.test",
				ContextPtr: "redis://ctx:job-approval-resource",
			}
			if test.strictLegacy {
				s.memoryResourceReader.Compatibility = resourceio.LegacyCompatibility{}
			}
			if test.structured {
				request.ContextPtr = ""
				request.ContextRef = &agentv1.ResourceRef{ResolverId: "operator"}
				s.memoryResourceReader = resourceio.Reader{Resolver: &gatewayResourceResolver{
					content: []byte("{\"amount\":42}"),
					media:   "application/json",
				}}
			}
			setupApprovalJob(t, s, request.GetJobId(), request.GetTenantId(), model.SafetyDecisionRecord{
				Decision: model.SafetyRequireApproval, ApprovalRequired: true,
			})
			if err := s.jobStore.SetJobRequest(context.Background(), request); err != nil {
				t.Fatalf("SetJobRequest() error = %v", err)
			}
			if err := s.memStore.PutContext(
				context.Background(),
				store.MakeContextKey(request.GetJobId()),
				[]byte("{\"amount\":7}"),
			); err != nil {
				t.Fatalf("PutContext() error = %v", err)
			}
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil),
				&auth.AuthContext{Tenant: "tenant-a", Role: "admin"})
			rec := httptest.NewRecorder()
			s.handleListApprovals(rec, req)
			item := firstApprovalItem(t, rec)
			_, hasInput := item["job_input"]
			if hasInput != test.wantJobInput {
				t.Fatalf("job_input present = %v, want %v; item = %#v", hasInput, test.wantJobInput, item)
			}
			if _, exposed := item["context_ptr"]; exposed {
				t.Fatalf("approval item exposed context pointer: %#v", item)
			}
		})
	}
}

func firstApprovalItem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	items, ok := response["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("approval items = %#v, want one", response["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("approval item type = %T", items[0])
	}
	return item
}
