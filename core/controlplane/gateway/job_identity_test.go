package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/capprofile"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeHTTPJobRequestProductionRejectsActorSpoofWithoutMutation(t *testing.T) {
	s := &server{capProfile: capprofile.Production}
	httpReq := httptest.NewRequest("POST", "/api/v1/jobs", nil)
	httpReq = withAuth(httpReq, &auth.AuthContext{
		Tenant: "tenant-a", PrincipalID: "principal-a", Role: "user",
	})
	job := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Meta: &pb.JobMetadata{TenantId: "tenant-a", ActorId: "actor-attacker"},
	}
	before := proto.Clone(job)

	got, err := s.normalizeHTTPJobRequest(httpReq, job)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("normalizeHTTPJobRequest() error = %v, want mismatch", err)
	}
	if got != nil {
		t.Fatalf("normalizeHTTPJobRequest() = %#v, want nil", got)
	}
	if !proto.Equal(job, before) {
		t.Fatal("normalizeHTTPJobRequest mutated rejected input")
	}
}

func TestNormalizeGRPCJobRequestProductionUsesAuthenticatedAuthority(t *testing.T) {
	s := &server{capProfile: capprofile.Production}
	ctx := context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{
		Tenant: "tenant-a", PrincipalID: "principal-a", Role: "user",
	})
	job := &pb.JobRequest{JobId: "job-1", Topic: "job.test"}

	got, err := s.normalizeGRPCJobRequest(ctx, job)
	if err != nil {
		t.Fatalf("normalizeGRPCJobRequest() error = %v", err)
	}
	if got.GetTenantId() != "tenant-a" || got.GetPrincipalId() != "principal-a" {
		t.Fatalf("canonical identity = %q/%q", got.GetTenantId(), got.GetPrincipalId())
	}
	if got.GetIdentity().GetActorId() != "principal-a" {
		t.Fatalf("actor = %q, want authenticated principal", got.GetIdentity().GetActorId())
	}
	if job.GetIdentity() != nil {
		t.Fatal("normalizeGRPCJobRequest mutated input")
	}
}

func TestNormalizeJobRequestCompatPreservesLegacyShape(t *testing.T) {
	s := &server{capProfile: capprofile.Compat}
	job := &pb.JobRequest{JobId: "job-1", Topic: "job.test", TenantId: "legacy"}

	got, err := s.normalizeGRPCJobRequest(context.Background(), job)
	if err != nil {
		t.Fatalf("normalizeGRPCJobRequest() error = %v", err)
	}
	if got != job {
		t.Fatal("compat request should pass through without cloning")
	}
}

func TestProductionHTTPIdentityBindsInitiatingCaller(t *testing.T) {
	s := &server{capProfile: capprofile.Production}
	httpReq := httptest.NewRequest("POST", "/api/v1/workflows/wf/runs", nil)
	httpReq = withAuth(httpReq, &auth.AuthContext{Tenant: "tenant-a", PrincipalID: "principal-a"})

	got, err := s.productionHTTPIdentity(httpReq, "tenant-a")
	if err != nil {
		t.Fatalf("productionHTTPIdentity() error = %v", err)
	}
	if got.GetTenantId() != "tenant-a" || got.GetPrincipalId() != "principal-a" || got.GetActorId() != "principal-a" {
		t.Fatalf("productionHTTPIdentity() = %#v", got)
	}
}
