package gateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestJobRequestToPolicyCheckRequestCarriesCanonicalIdentity(t *testing.T) {
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	req := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-a",
		PrincipalId: "principal-a", Identity: identity,
		Meta: &pb.JobMetadata{TenantId: "tenant-a", ActorId: "actor-a"},
	}

	got := jobRequestToPolicyCheckRequest(req)

	if !proto.Equal(got.GetIdentity(), identity) {
		t.Fatalf("policy identity = %v, want canonical %v", got.GetIdentity(), identity)
	}
}

func TestMetaToPolicyCheckRequestCarriesPersistedCanonicalIdentity(t *testing.T) {
	got := metaToPolicyCheckRequest("job-1", map[string]string{
		"tenant": "tenant-a", "principal": "principal-a", "actor_id": "actor-a",
	})
	want := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	if !proto.Equal(got.GetIdentity(), want) {
		t.Fatalf("policy identity = %v, want persisted canonical %v", got.GetIdentity(), want)
	}
}

func TestHandlePolicyReplayProductionRejectsConflictingStoredIdentity(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	s.capProfile = capprofile.Production
	ctx := context.Background()
	identity := &pb.IdentityBinding{
		TenantId: "default", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	canonical := &pb.JobRequest{
		JobId: "replay-conflict", Topic: "job.test", TenantId: "default",
		PrincipalId: "principal-a", Identity: identity,
		Meta: &pb.JobMetadata{TenantId: "default", ActorId: "actor-a"},
	}
	if err := s.jobStore.SetJobMeta(ctx, canonical); err != nil {
		t.Fatalf("SetJobMeta() error = %v", err)
	}
	conflicting := proto.Clone(canonical).(*pb.JobRequest)
	conflicting.TenantId = "tenant-attacker"
	if err := s.jobStore.SetJobRequest(ctx, conflicting); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	if err := s.jobStore.SetState(ctx, canonical.GetJobId(), model.JobStatePending); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}

	now := time.Now().UTC()
	rec := replayRequest(t, s, map[string]any{
		"from":              now.Add(-time.Hour).Format(time.RFC3339),
		"to":                now.Add(time.Hour).Format(time.RFC3339),
		"candidate_content": "rules:\n  - id: allow-all\n    match:\n      topics: [job.*]\n    decision: allow\n",
	}, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeReplayResponse(t, rec)
	if resp.Summary.TotalJobs != 1 || resp.Summary.Evaluated != 0 || resp.Summary.Errored != 1 {
		t.Fatalf("summary = %+v, want one rejected identity", resp.Summary)
	}
}

func TestHandlePolicyReplayProductionRejectsIncompleteMetadataIdentity(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policyReplayAuth{}
	s.capProfile = capprofile.Production
	ctx := context.Background()
	req := &pb.JobRequest{
		JobId: "replay-incomplete", Topic: "job.test", TenantId: "default",
		Meta: &pb.JobMetadata{TenantId: "default"},
	}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatalf("SetJobMeta() error = %v", err)
	}
	if err := s.jobStore.SetState(ctx, req.GetJobId(), model.JobStatePending); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}

	now := time.Now().UTC()
	rec := replayRequest(t, s, map[string]any{
		"from":              now.Add(-time.Hour).Format(time.RFC3339),
		"to":                now.Add(time.Hour).Format(time.RFC3339),
		"candidate_content": "rules:\n  - id: allow-all\n    match:\n      topics: [job.*]\n    decision: allow\n",
	}, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := decodeReplayResponse(t, rec)
	if resp.Summary.TotalJobs != 1 || resp.Summary.Evaluated != 0 || resp.Summary.Errored != 1 {
		t.Fatalf("summary = %+v, want one rejected incomplete identity", resp.Summary)
	}
}
