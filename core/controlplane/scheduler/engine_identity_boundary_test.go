package scheduler

import (
	"context"
	"errors"
	"testing"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
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
	result := &pb.JobResult{JobId: "job-1", WorkerId: "worker-a", Identity: jobIdentity}
	packet := &pb.BusPacket{SenderId: "worker-a", Identity: jobIdentity}

	if err := engine.validateProductionJobResultIdentity(
		context.Background(), packet, result,
		&SessionTokenClaims{Subject: "worker-a", Tenant: "tenant-a"},
	); err != nil {
		t.Fatalf("validateProductionJobResultIdentity() error = %v", err)
	}
}

func TestValidateProductionJobResultIdentityRejectsSameTenantRequesterMismatch(t *testing.T) {
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
	spoofed := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "admin", ActorId: "admin",
	}
	engine := &Engine{jobStore: store}
	result := &pb.JobResult{JobId: "job-1", WorkerId: "worker-a", Identity: spoofed}
	packet := &pb.BusPacket{SenderId: "worker-a", Identity: spoofed}

	err = engine.validateProductionJobResultIdentity(
		context.Background(), packet, result,
		&SessionTokenClaims{Subject: "worker-a", Tenant: "tenant-a"},
	)
	if !errors.Is(err, ErrProductionResultIdentityMismatch) {
		t.Fatalf("validateProductionJobResultIdentity() error = %v, want mismatch", err)
	}
}

func TestValidateProductionJobEventIdentityRejectsMissingVerifiedClaims(t *testing.T) {
	store := newFakeJobStore()
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, identity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	engine := &Engine{jobStore: store}

	err = engine.validateProductionJobEventIdentity(
		context.Background(), &pb.BusPacket{Identity: identity}, "job-1", identity, nil,
	)
	if !errors.Is(err, ErrProductionResultIdentityMismatch) {
		t.Fatalf("validateProductionJobEventIdentity() error = %v, want mismatch", err)
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

func TestPublishCancelProductionEchoesStoredJobIdentity(t *testing.T) {
	store := newFakeJobStore()
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, identity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := store.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithProductionIdentityEnforcement(true)

	engine.publishCancel("job-1", "test")

	published := bus.snapshotPublished()
	if len(published) != 1 {
		t.Fatalf("published packets = %d, want 1", len(published))
	}
	packet := published[0].packet
	if !proto.Equal(packet.GetIdentity(), identity) || !proto.Equal(packet.GetJobCancel().GetIdentity(), identity) {
		t.Fatalf("cancel identities = envelope:%v payload:%v", packet.GetIdentity(), packet.GetJobCancel().GetIdentity())
	}
}

func TestPublishCancelProductionFailsClosedWithoutStoredIdentity(t *testing.T) {
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithProductionIdentityEnforcement(true)

	engine.publishCancel("missing-job", "test")

	if got := len(bus.snapshotPublished()); got != 0 {
		t.Fatalf("published packets = %d, want 0", got)
	}
}

func TestReplayApprovalPublishProductionEchoesRequestIdentity(t *testing.T) {
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "sys.workflow.approval.gate"}, identity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), newFakeJobStore(), nil).
		WithProductionIdentityEnforcement(true)

	err = engine.replayApprovalPublish("trace-1", req, ApprovalRecord{
		PublishTarget: ApprovalPublishTargetDLQAndResult,
	})
	if err != nil {
		t.Fatalf("replayApprovalPublish() error = %v", err)
	}
	published := bus.snapshotPublished()
	if len(published) != 2 {
		t.Fatalf("published packets = %d, want 2", len(published))
	}
	if published[1].subject != capsdk.SubjectAcceptedResult {
		t.Fatalf("result subject = %q, want %q", published[1].subject, capsdk.SubjectAcceptedResult)
	}
	result := published[1].packet
	if !proto.Equal(result.GetIdentity(), identity) || !proto.Equal(result.GetJobResult().GetIdentity(), identity) {
		t.Fatalf("result identities = envelope:%v payload:%v", result.GetIdentity(), result.GetJobResult().GetIdentity())
	}
	if result.GetJobResult().GetWorkerId() != defaultSenderID {
		t.Fatalf("worker id = %q, want %q", result.GetJobResult().GetWorkerId(), defaultSenderID)
	}
}

func TestProcessApprovalGateProductionEchoesRequestIdentity(t *testing.T) {
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "requester-a", ActorId: "requester-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(&pb.JobRequest{
		JobId: "job-gate", Topic: capsdk.SubjectApprovalGate, Labels: map[string]string{
			"approval_granted": "true", "approval_snapshot": workflowGateSnapshot,
		},
	}, identity)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	jobHash, err := HashJobRequest(req)
	if err != nil {
		t.Fatalf("HashJobRequest() error = %v", err)
	}
	store := newFakeJobStore()
	store.safety[req.GetJobId()] = SafetyDecisionRecord{
		Decision: SafetyRequireApproval, ApprovalRequired: true,
		PolicySnapshot: workflowGateSnapshot, JobHash: jobHash,
	}
	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithProductionIdentityEnforcement(true)

	if err := engine.processJob(testCtx(t), req, "trace-gate"); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}
	published := bus.snapshotPublished()
	if len(published) != 1 || published[0].subject != capsdk.SubjectAcceptedResult {
		t.Fatalf("published = %#v, want one scheduler-accepted result", published)
	}
	packet := published[0].packet
	if !proto.Equal(packet.GetIdentity(), identity) || !proto.Equal(packet.GetJobResult().GetIdentity(), identity) {
		t.Fatalf("result identities = envelope:%v payload:%v", packet.GetIdentity(), packet.GetJobResult().GetIdentity())
	}
}

func TestPublishSchedulerResultProductionRejectsMissingBus(t *testing.T) {
	engine := &Engine{}
	engine.productionIdentity.Store(true)
	err := engine.publishSchedulerResult(&pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "job-no-bus"}},
	})
	if err == nil {
		t.Fatal("production scheduler result with nil bus succeeded")
	}
}
