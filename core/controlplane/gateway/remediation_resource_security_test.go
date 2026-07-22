package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/resourceio"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestRemediateJobRejectsUnsafeContextMigration(t *testing.T) {
	tests := []struct {
		name       string
		pointer    string
		reference  *agentv1.ResourceRef
		strict     bool
		wantStatus int
	}{
		{name: "legacy strict", pointer: "redis://ctx:job-remediate", strict: true, wantStatus: http.StatusBadRequest},
		{name: "cross job legacy", pointer: "redis://ctx:other-job", wantStatus: http.StatusBadRequest},
		{name: "structured requires writer", reference: &agentv1.ResourceRef{ResolverId: "operator"}, wantStatus: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, bus, _ := newTestGateway(t)
			if test.strict {
				s.memoryResourceReader.Compatibility = resourceio.LegacyCompatibility{}
			}
			request := &pb.JobRequest{
				JobId: "job-remediate", TenantId: "tenant-a", Topic: "job.test",
				ContextPtr: test.pointer, ContextRef: test.reference,
			}
			seedRemediationResourceJob(t, s, request)
			req := withAuth(
				httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-remediate/remediate",
					bytes.NewBufferString("{\"remediation_id\":\"safe\"}")),
				&auth.AuthContext{Tenant: "tenant-a", Role: "admin"},
			)
			req.SetPathValue("id", request.GetJobId())
			rec := httptest.NewRecorder()
			s.handleRemediateJob(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, test.wantStatus, rec.Body.String())
			}
			bus.mu.Lock()
			published := len(bus.published)
			bus.mu.Unlock()
			if published != 0 {
				t.Fatalf("unsafe remediation published %d messages", published)
			}
		})
	}
}

func seedRemediationResourceJob(t *testing.T, s *server, request *pb.JobRequest) {
	t.Helper()
	ctx := context.Background()
	if err := s.jobStore.SetJobMeta(ctx, request); err != nil {
		t.Fatalf("SetJobMeta() error = %v", err)
	}
	if err := s.jobStore.SetJobRequest(ctx, request); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	if err := s.jobStore.SetTenant(ctx, request.GetJobId(), request.GetTenantId()); err != nil {
		t.Fatalf("SetTenant() error = %v", err)
	}
	if testPointer := request.GetContextPtr(); testPointer == "redis://ctx:job-remediate" {
		if err := s.memStore.PutContext(ctx, "ctx:job-remediate", []byte("{\"safe\":true}")); err != nil {
			t.Fatalf("PutContext() error = %v", err)
		}
	}
	if err := s.jobStore.SetSafetyDecision(ctx, request.GetJobId(), model.SafetyDecisionRecord{
		Decision: model.SafetyDeny,
		Remediations: []*pb.PolicyRemediation{{
			Id: "safe", ReplacementTopic: "job.safe",
		}},
	}); err != nil {
		t.Fatalf("SetSafetyDecision() error = %v", err)
	}
}
