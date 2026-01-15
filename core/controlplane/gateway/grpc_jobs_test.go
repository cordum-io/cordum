package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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
	if status.Status != string(scheduler.JobStatePending) {
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

func TestDialSafetyKernelTLSRequired(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_REQUIRED", "true")
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	if _, _, err := dialSafetyKernel("localhost:50051"); err == nil {
		t.Fatalf("expected tls required error")
	}
}
