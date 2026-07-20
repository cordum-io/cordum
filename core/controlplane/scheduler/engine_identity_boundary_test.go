package scheduler

import (
	"context"
	"errors"
	"testing"

	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestHandlePacketProductionRejectsIdentityMismatchBeforeSideEffects(t *testing.T) {
	store := newFakeJobStore()
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithProductionIdentityEnforcement(true)
	authority := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a",
	}
	req := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-attacker",
	}
	packet := completeSecurityTestEnvelope(&pb.BusPacket{
		Identity: authority,
		Payload:  &pb.BusPacket_JobRequest{JobRequest: req},
	})
	before := proto.Clone(packet)

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket() error = %v", err)
	}
	if len(store.states) != 0 || len(store.reqs) != 0 {
		t.Fatalf("identity mismatch reached store: states=%v requests=%v", store.states, store.reqs)
	}
	if len(bus.snapshotPublished()) != 0 {
		t.Fatalf("identity mismatch reached bus: %v", bus.snapshotPublished())
	}
	if !proto.Equal(packet, before) {
		t.Fatal("HandlePacket mutated rejected packet")
	}
}

func TestValidateProductionJobResultIdentityBindsSessionTenantToStoredJob(t *testing.T) {
	store := newFakeJobStore()
	jobIdentity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, jobIdentity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	engine := &Engine{jobStore: store}
	workerIdentity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "worker-a", ActorId: "worker-a",
	}
	result := &pb.JobResult{JobId: "job-1", WorkerId: "worker-a", Identity: workerIdentity}
	packet := &pb.BusPacket{SenderId: "worker-a", Identity: workerIdentity}

	if err := engine.validateProductionJobResultIdentity(
		context.Background(), packet, result,
		&SessionTokenClaims{Subject: "worker-a", Tenant: "tenant-a"},
	); err != nil {
		t.Fatalf("validateProductionJobResultIdentity() error = %v", err)
	}
}

func TestValidateProductionJobResultIdentityRejectsCrossTenantBeforeMutation(t *testing.T) {
	store := newFakeJobStore()
	jobIdentity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, jobIdentity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	engine := &Engine{jobStore: store}
	workerIdentity := &pb.IdentityBinding{
		TenantId: "tenant-b", PrincipalId: "worker-b", ActorId: "worker-b",
	}
	result := &pb.JobResult{JobId: "job-1", WorkerId: "worker-b", Identity: workerIdentity}
	packet := &pb.BusPacket{SenderId: "worker-b", Identity: workerIdentity}
	before := proto.Clone(result)

	err = engine.validateProductionJobResultIdentity(
		context.Background(), packet, result,
		&SessionTokenClaims{Subject: "worker-b", Tenant: "tenant-b"},
	)
	if !errors.Is(err, ErrProductionResultIdentityMismatch) {
		t.Fatalf("validateProductionJobResultIdentity() error = %v, want mismatch", err)
	}
	if !proto.Equal(result, before) {
		t.Fatal("result identity validation mutated rejected result")
	}
}

func TestHandlePacketProductionRejectsCrossTenantJobResult(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.IssueBound(
		context.Background(), boundTestBinding("worker-b", "tenant-b", "go/test"),
	)
	if err != nil {
		t.Fatalf("IssueBound() error = %v", err)
	}
	store := newFakeJobStore()
	jobIdentity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, jobIdentity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	store.states["job-1"] = JobStateRunning
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithSessionMiddleware(NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())).
		WithProductionIdentityEnforcement(true)
	workerIdentity := &pb.IdentityBinding{
		TenantId: "tenant-b", PrincipalId: "worker-b", ActorId: "worker-b",
	}
	packet := completeSecurityTestEnvelope(&pb.BusPacket{
		SenderId: "worker-b", Identity: workerIdentity,
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-1", WorkerId: "worker-b",
			Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, Identity: workerIdentity,
		}},
	})
	packet = reparseWithTypedAuthToken(t, packet, token)

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket() error = %v", err)
	}
	store.mu.RLock()
	got := store.states["job-1"]
	store.mu.RUnlock()
	if got != JobStateRunning {
		t.Fatalf("cross-tenant result changed state to %q", got)
	}
}

func TestHandlePacketProductionRejectsCrossTenantJobCancel(t *testing.T) {
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.IssueBound(
		context.Background(), boundTestBinding("worker-b", "tenant-b", "go/test"),
	)
	if err != nil {
		t.Fatalf("IssueBound() error = %v", err)
	}
	store := newFakeJobStore()
	jobIdentity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, jobIdentity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	store.states["job-1"] = JobStateRunning
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithSessionMiddleware(NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())).
		WithProductionIdentityEnforcement(true)
	workerIdentity := &pb.IdentityBinding{
		TenantId: "tenant-b", PrincipalId: "worker-b", ActorId: "worker-b",
	}
	packet := completeSecurityTestEnvelope(&pb.BusPacket{
		SenderId: "worker-b", Identity: workerIdentity,
		Payload: &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{
			JobId: "job-1", RequestedBy: "worker-b", Identity: workerIdentity,
		}},
	})
	packet = reparseWithTypedAuthToken(t, packet, token)

	if err := engine.HandlePacket(packet); err != nil {
		t.Fatalf("HandlePacket() error = %v", err)
	}
	store.mu.RLock()
	got := store.states["job-1"]
	store.mu.RUnlock()
	if got != JobStateRunning {
		t.Fatalf("cross-tenant cancel changed state to %q", got)
	}
}
