package gateway

import (
	"context"
	"encoding/json"
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

func TestApprovalContextUsesStructuredResourceOnlyByDefault(t *testing.T) {
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
				JobId: "job-approval-detail", TenantId: "tenant-a", Topic: "job.test",
				ContextPtr: "redis://ctx:job-approval-detail",
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
			setupApprovalContextResource(t, s, request)
			req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/approvals/job-approval-detail/context", nil),
				&auth.AuthContext{Tenant: "tenant-a", Role: "admin"})
			req.SetPathValue("job_id", request.GetJobId())
			rec := httptest.NewRecorder()
			s.handleApprovalContext(rec, req)
			response := decodeApprovalContextResponse(t, rec)
			approval, ok := response["approval"].(map[string]any)
			if !ok {
				t.Fatalf("approval response = %#v", response)
			}
			_, hasInput := approval["job_input"]
			if hasInput != test.wantJobInput {
				t.Fatalf("job_input present = %v, want %v; approval = %#v", hasInput, test.wantJobInput, approval)
			}
		})
	}
}

func TestApprovalContextFailsClosedOnMissingPersistedTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setupApprovalContextResource(t, s, &pb.JobRequest{
		JobId: "job-approval-detail", Topic: "job.test",
		ContextPtr: "redis://ctx:job-approval-detail",
	})
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/approvals/job-approval-detail/context", nil),
		&auth.AuthContext{Tenant: "tenant-a", Role: "admin"})
	req.SetPathValue("job_id", "job-approval-detail")
	rec := httptest.NewRecorder()
	s.handleApprovalContext(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
}

func setupApprovalContextResource(t *testing.T, s *server, request *pb.JobRequest) {
	t.Helper()
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
}

func decodeApprovalContextResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
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
