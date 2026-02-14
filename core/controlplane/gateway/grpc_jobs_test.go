package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubmitJobGRPCAndStatus(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	ctx := context.Background()

	req := &pb.SubmitJobRequest{
		Prompt:         "hello",
		Topic:          "job.default",
		OrgId:          "org-1",
		PrincipalId:    "principal-1",
		IdempotencyKey: "dup-key",
	}
	resp, err := s.SubmitJob(ctx, req)
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}
	if resp.JobId == "" || resp.TraceId == "" {
		t.Fatalf("expected job + trace ids")
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected 1 bus publish, got %d", len(bus.published))
	}

	status, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: resp.JobId})
	if err != nil {
		t.Fatalf("get job status: %v", err)
	}
	if status.Status != string(model.JobStatePending) {
		t.Fatalf("expected pending status, got %s", status.Status)
	}

	repeat, err := s.SubmitJob(ctx, req)
	if err != nil {
		t.Fatalf("submit job idempotent: %v", err)
	}
	if repeat.JobId != resp.JobId {
		t.Fatalf("expected same job id for idempotency")
	}
	if len(bus.published) != 1 {
		t.Fatalf("expected no new publish on idempotent submit")
	}
}

func TestSubmitJobGRPCRejectsDisallowedMemoryID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"context": map[string]any{
				"allowed_memory_ids": []string{"repo:*"},
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	req := &pb.SubmitJobRequest{
		Prompt:   "hello",
		Topic:    "job.default",
		OrgId:    "org-1",
		MemoryId: "kb:secret",
	}
	_, err := s.SubmitJob(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestSubmitJobGRPCRespectsConcurrentJobsLimit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	if err := s.configSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"rate_limits": map[string]any{
				"concurrent_jobs": 1,
				"queue_size":      0,
			},
		},
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	seedJobID := "job-seed"
	if err := s.jobStore.SetTenant(ctx, seedJobID, "org-1"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if err := s.jobStore.SetState(ctx, seedJobID, model.JobStatePending); err != nil {
		t.Fatalf("set state: %v", err)
	}

	req := &pb.SubmitJobRequest{
		Prompt:      "hello",
		Topic:       "job.default",
		OrgId:       "org-1",
		PrincipalId: "principal-1",
	}
	_, err := s.SubmitJob(ctx, req)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
}

func TestSubmitJobGRPCTenantMismatchDenied(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.WithValue(context.Background(), authContextKey{}, &AuthContext{
		Tenant:           "tenant-a",
		AllowCrossTenant: false,
	})

	_, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{
		Prompt: "hello",
		Topic:  "job.default",
		OrgId:  "tenant-b",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestSubmitJobGRPCTenantCrossTenantAllowed(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.WithValue(context.Background(), authContextKey{}, &AuthContext{
		Tenant:           "tenant-a",
		AllowCrossTenant: true,
	})

	resp, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{
		Prompt: "hello",
		Topic:  "job.default",
		OrgId:  "tenant-b",
	})
	if err != nil {
		t.Fatalf("submit job: %v", err)
	}
	tenant, err := s.jobStore.GetTenant(context.Background(), resp.JobId)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant != "tenant-b" {
		t.Fatalf("expected tenant-b, got %q", tenant)
	}
}

func TestSubmitJobGRPCDefaultsTenantFromAuthOrServer(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.tenant = "server-tenant"

	ctxAuth := context.WithValue(context.Background(), authContextKey{}, &AuthContext{
		Tenant: "tenant-a",
	})
	resp, err := s.SubmitJob(ctxAuth, &pb.SubmitJobRequest{
		Prompt: "hello",
		Topic:  "job.default",
	})
	if err != nil {
		t.Fatalf("submit job with auth tenant: %v", err)
	}
	tenant, err := s.jobStore.GetTenant(context.Background(), resp.JobId)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant != "tenant-a" {
		t.Fatalf("expected tenant-a, got %q", tenant)
	}

	resp, err = s.SubmitJob(context.Background(), &pb.SubmitJobRequest{
		Prompt: "hello",
		Topic:  "job.default",
	})
	if err != nil {
		t.Fatalf("submit job with server tenant: %v", err)
	}
	tenant, err = s.jobStore.GetTenant(context.Background(), resp.JobId)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tenant != "server-tenant" {
		t.Fatalf("expected server-tenant, got %q", tenant)
	}
}

func TestDialSafetyKernelTLSRequired(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_REQUIRED", "true")
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	if _, _, err := dialSafetyKernel("localhost:50051"); err == nil {
		t.Fatalf("expected tls required error")
	}
}
