package store

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestSetJobMetaRejectsCanonicalIdentityMismatchBeforeWrite(t *testing.T) {
	server := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + server.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	req := &pb.JobRequest{
		JobId: "job-identity", Topic: "job.test", TenantId: "tenant-attacker",
		Identity: &pb.IdentityBinding{
			TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a",
		},
	}

	err = store.SetJobMeta(context.Background(), req)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("SetJobMeta() error = %v, want identity mismatch", err)
	}
	if server.Exists(jobMetaKey(req.GetJobId())) {
		t.Fatal("SetJobMeta wrote metadata before rejecting identity")
	}
}

func TestSetJobMetaPersistsCanonicalIdentityFields(t *testing.T) {
	server := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + server.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	auth := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-identity", Topic: "job.test"}, auth,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}

	if err := store.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("SetJobMeta() error = %v", err)
	}
	if got, _ := store.GetTenant(context.Background(), req.GetJobId()); got != auth.GetTenantId() {
		t.Fatalf("tenant = %q, want %q", got, auth.GetTenantId())
	}
	if got, _ := store.GetPrincipal(context.Background(), req.GetJobId()); got != auth.GetPrincipalId() {
		t.Fatalf("principal = %q, want %q", got, auth.GetPrincipalId())
	}
	if got, _ := store.GetActorID(context.Background(), req.GetJobId()); got != auth.GetActorId() {
		t.Fatalf("actor = %q, want %q", got, auth.GetActorId())
	}
}

func TestSetJobMetaPreservesCompatPartialIdentity(t *testing.T) {
	server := miniredis.RunT(t)
	store, err := NewRedisJobStore("redis://" + server.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	req := &pb.JobRequest{
		JobId: "job-compat", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a"},
	}

	if err := store.SetJobMeta(context.Background(), req); err != nil {
		t.Fatalf("SetJobMeta() error = %v", err)
	}
	if got, _ := store.GetTenant(context.Background(), req.GetJobId()); got != "tenant-a" {
		t.Fatalf("tenant = %q, want compat tenant", got)
	}
}
