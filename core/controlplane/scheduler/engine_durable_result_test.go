package scheduler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestTrustedEventEvidenceUsesExactVerifiedRawMetadata(t *testing.T) {
	identity := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"}
	packet := &pb.BusPacket{
		SenderId: "worker-1", Identity: identity,
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("0123456789abcdef")},
	}
	claims := &SessionTokenClaims{Subject: "worker-1", Tenant: "tenant-a"}
	wantDigest := bytes.Repeat([]byte{0xA5}, 32)
	authority := &bus.RawAdmissionAuthority{
		ActualSubject: "sys.job.result", SessionSubject: "worker-1", TenantID: "tenant-a",
		Identity: identity, MessageID: []byte("0123456789abcdef"), UnsignedDigest: wantDigest,
	}

	messageID, digest, err := trustedEventEvidence(authority, packet, claims)
	if err != nil {
		t.Fatalf("trustedEventEvidence() error = %v", err)
	}
	if !bytes.Equal(messageID, authority.MessageID) || !bytes.Equal(digest, wantDigest) {
		t.Fatalf("evidence = %x/%x, want exact authority %x/%x", messageID, digest, authority.MessageID, wantDigest)
	}
	packet.SignatureMetadata.MessageId = []byte("fedcba9876543210")
	if _, _, err := trustedEventEvidence(authority, packet, claims); err == nil {
		t.Fatal("trustedEventEvidence() accepted packet/authority message-id mismatch")
	}
}

func TestHandleJobResultProductionRejectsMissingDispatchFence(t *testing.T) {
	jobStore := newFakeJobStore()
	jobStore.states["job-no-fence"] = JobStateRunning
	jobStore.topics["job-no-fence"] = "job.test"
	engine := &Engine{ctx: context.Background(), jobStore: jobStore}
	engine.productionIdentity.Store(true)

	err := engine.handleJobResult(&pb.JobResult{
		JobId: "job-no-fence", WorkerId: "worker-1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	})
	if err != nil {
		t.Fatalf("handleJobResult() error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), "job-no-fence")
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state != JobStateRunning {
		t.Fatalf("state = %q, want unchanged running", state)
	}
}

func TestCompatResultStillPublishesSchedulerAcceptedEvent(t *testing.T) {
	jobStore := newFakeJobStore()
	jobStore.states["job-compat-accepted"] = JobStateRunning
	jobStore.topics["job-compat-accepted"] = "job.test"
	bus := &fakeBus{}
	engine := &Engine{ctx: context.Background(), jobStore: jobStore, bus: bus}
	result := &pb.JobResult{
		JobId: "job-compat-accepted", WorkerId: "worker-1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}
	packet := &pb.BusPacket{SenderId: "worker-1", Payload: &pb.BusPacket_JobResult{JobResult: result}}
	if err := engine.handleCompatJobResult(packet, result); err != nil {
		t.Fatalf("handleCompatJobResult() error = %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 || bus.published[0].subject != "sys.internal.job.result.accepted" ||
		bus.published[0].packet.GetSenderId() != defaultSenderID {
		t.Fatalf("compat accepted publishes = %+v", bus.published)
	}
}

func TestPublishSchedulerAcceptedPacketProducesValidSchedulerAuthoredResult(t *testing.T) {
	bus := &fakeBus{}
	engine := &Engine{ctx: context.Background(), bus: bus}
	result := &pb.JobResult{
		JobId: "job-accepted", WorkerId: "worker-1",
		Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}
	packet := &pb.BusPacket{
		TraceId: "trace-accepted", SenderId: "worker-1", CreatedAt: timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload:         &pb.BusPacket_JobResult{JobResult: result},
	}

	if err := engine.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedResult, packet); err != nil {
		t.Fatalf("publishSchedulerAcceptedPacket() error = %v", err)
	}
	published := bus.snapshotPublished()
	if len(published) != 1 {
		t.Fatalf("published packets = %d, want 1", len(published))
	}
	accepted := published[0].packet
	if err := capsdk.ValidateBusPacket(accepted); err != nil {
		t.Fatalf("scheduler-authored accepted packet is invalid: %v", err)
	}
	if accepted.GetSenderId() != defaultSenderID || accepted.GetJobResult().GetWorkerId() != defaultSenderID {
		t.Fatalf("accepted authority = envelope:%q result:%q, want %q", accepted.GetSenderId(), accepted.GetJobResult().GetWorkerId(), defaultSenderID)
	}
	if result.GetWorkerId() != "worker-1" {
		t.Fatalf("original result worker = %q, want unchanged", result.GetWorkerId())
	}
}

func TestDurableResultOutboxReplaysAfterPublishFailure(t *testing.T) {
	engine, jobStore, bus, result, packet, claims := productionResultFixture(t)
	bus.publishErr = errors.New("broker unavailable")
	bus.failSubject = "sys.internal.job.result.accepted"
	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err == nil {
		t.Fatal("first handleProductionJobResult() error = nil, want retry")
	}
	effects, err := jobStore.PendingJobEffects(context.Background(), 10)
	if err != nil || len(effects) != 1 {
		t.Fatalf("pending after publish failure = (%d, %v), want one", len(effects), err)
	}
	bus.mu.Lock()
	bus.publishErr, bus.failSubject, bus.published = nil, "", nil
	bus.mu.Unlock()
	if err := engine.drainPendingJobEffects(context.Background(), jobStore, 10); err != nil {
		t.Fatalf("drainPendingJobEffects() error = %v", err)
	}
	if err := engine.drainPendingJobEffects(context.Background(), jobStore, 10); err != nil {
		t.Fatalf("second drainPendingJobEffects() error = %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 {
		t.Fatalf("successful accepted-result deliveries = %d, want exactly one", len(bus.published))
	}
}

func TestHandleProductionJobResultUsesAuthenticatedFenceAndAppliesOnce(t *testing.T) {
	engine, jobStore, bus, result, packet, claims := productionResultFixture(t)
	result.WorkerId = "attacker-echo"

	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("handleProductionJobResult(first) error = %v", err)
	}
	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("handleProductionJobResult(redelivery) error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateSucceeded {
		t.Fatalf("GetState() = (%q, %v), want succeeded", state, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 1 || bus.published[0].subject != "sys.internal.job.result.accepted" {
		t.Fatalf("accepted publishes = %+v, want exactly one trusted result", bus.published)
	}
	acceptedPacket := bus.published[0].packet
	if err := capsdk.ValidateBusPacket(acceptedPacket); err != nil {
		t.Fatalf("durable accepted packet is invalid: %v", err)
	}
	accepted := acceptedPacket.GetJobResult()
	if accepted.GetWorkerId() != defaultSenderID || accepted.GetDispatch().GetAssignedWorkerId() != claims.Subject {
		t.Fatalf("accepted authority = scheduler:%q worker:%q, want %q/%q",
			accepted.GetWorkerId(), accepted.GetDispatch().GetAssignedWorkerId(), defaultSenderID, claims.Subject)
	}
}

func TestHandleProductionJobResultRejectsStaleAttemptWithoutMutation(t *testing.T) {
	engine, jobStore, bus, result, packet, claims := productionResultFixture(t)
	if _, _, err := jobStore.BeginDispatch(
		context.Background(), result.GetJobId(), claims.Subject, claims.Tenant,
	); err != nil {
		t.Fatalf("BeginDispatch(new attempt) error = %v", err)
	}
	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("stale handleProductionJobResult() error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateRunning {
		t.Fatalf("state after stale result = (%q, %v), want running", state, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("stale result published %d effects, want zero", len(bus.published))
	}
}

func TestHandleProductionJobResultRejectsWrongTokenTenant(t *testing.T) {
	engine, jobStore, bus, result, packet, claims := productionResultFixture(t)
	claims.Tenant = "tenant-evil"
	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("wrong-tenant handleProductionJobResult() error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateRunning {
		t.Fatalf("state after wrong token tenant = (%q, %v), want running", state, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("wrong-tenant result published %d effects, want zero", len(bus.published))
	}
}

func TestHandleProductionJobResultRetriesTransientStoreFailure(t *testing.T) {
	engine, jobStore, _, result, packet, claims := productionResultFixture(t)
	if err := jobStore.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims)
	var retryable interface{ RetryDelay() time.Duration }
	if !errors.As(err, &retryable) {
		t.Fatalf("transient store error = %v, want retryable", err)
	}
}

func TestHandleProductionJobResultRetainsAsyncOutputSafety(t *testing.T) {
	engine, _, _, result, packet, claims := productionResultFixture(t)
	checker := &stubOutputChecker{
		metaRecord:    OutputSafetyRecord{Decision: OutputAllow},
		contentRecord: OutputSafetyRecord{Decision: OutputAllow, Phase: "async"},
	}
	engine.WithOutputSafety(checker).WithOutputSafetyEnabled(true)
	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("handleProductionJobResult() error = %v", err)
	}
	engine.wg.Wait()
	if checker.metaCalls.Load() != 1 || checker.contentCalls.Load() != 1 {
		t.Fatalf("output safety calls = meta:%d content:%d, want 1/1",
			checker.metaCalls.Load(), checker.contentCalls.Load())
	}
}

// TestHandleProductionJobResultOutputQuarantineCorrectsAcceptedResultStatus
// guards against an output-policy bypass in the workflow layer for the
// production/durable path: productionResultState only ever tracked a
// quarantine/deny disposition in a local model.JobState, never on the
// *pb.JobResult that gets cloned into the durable effect and republished
// verbatim onto SubjectAcceptedResult (see processDurableJobEffect). The
// workflow engine's applyResult() (core/workflow/engine_state.go) treats
// JOB_STATUS_SUCCEEDED as step success and records ResultPtr as the step
// output, so an uncorrected status would let a quarantined/unsafe result
// flow into the workflow layer as if it had succeeded.
func TestHandleProductionJobResultOutputQuarantineCorrectsAcceptedResultStatus(t *testing.T) {
	engine, jobStore, bus, result, packet, claims := productionResultFixture(t)
	checker := &stubOutputChecker{metaRecord: OutputSafetyRecord{Decision: OutputQuarantine, Reason: "secret leak"}}
	engine.WithOutputChecker(checker).WithOutputSafetyEnabled(true)

	if err := engine.handleProductionJobResult(productionEventAuthority(t, packet), packet, result, claims); err != nil {
		t.Fatalf("handleProductionJobResult() error = %v", err)
	}

	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateQuarantined {
		t.Fatalf("GetState() = (%q, %v), want quarantined", state, err)
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	var accepted *pb.JobResult
	for _, msg := range bus.published {
		if msg.subject == "sys.internal.job.result.accepted" {
			accepted = msg.packet.GetJobResult()
		}
	}
	if accepted == nil {
		t.Fatalf("no accepted-result publish found in %+v", bus.published)
	}
	if accepted.GetStatus() != pb.JobStatus_JOB_STATUS_DENIED {
		t.Fatalf("accepted result status = %s, want %s (quarantined output must not be republished as succeeded)",
			accepted.GetStatus(), pb.JobStatus_JOB_STATUS_DENIED)
	}
	if accepted.GetErrorCodeEnum() != pb.ErrorCode_ERROR_CODE_SAFETY_DENIED {
		t.Fatalf("accepted result error code = %s, want %s", accepted.GetErrorCodeEnum(), pb.ErrorCode_ERROR_CODE_SAFETY_DENIED)
	}
}

func productionResultFixture(
	t *testing.T,
) (*Engine, *store.RedisJobStore, *fakeBus, *pb.JobResult, *pb.BusPacket, *SessionTokenClaims) {
	t.Helper()
	srv := miniredis.RunT(t)
	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	t.Cleanup(func() { _ = jobStore.Close() })
	ctx := context.Background()
	jobID := "job-production-apply"
	if err := jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("SetState(pending) error = %v", err)
	}
	if err := jobStore.SetTopic(ctx, jobID, "job.test"); err != nil {
		t.Fatalf("SetTopic() error = %v", err)
	}
	dispatchID, attempt, err := jobStore.BeginDispatch(ctx, jobID, "worker-1", "tenant-a")
	if err != nil {
		t.Fatalf("BeginDispatch() error = %v", err)
	}
	if err := jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("SetState(running) error = %v", err)
	}
	identity := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"}
	request := &pb.JobRequest{
		JobId: jobID, Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Identity: identity, Meta: &pb.JobMetadata{TenantId: "tenant-a", ActorId: "actor-a"},
	}
	if err := jobStore.SetJobRequest(ctx, request); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	result := &pb.JobResult{
		JobId: jobID, WorkerId: "worker-1", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		Identity: identity, Dispatch: &pb.DispatchIdentity{
			DispatchId: dispatchID, Attempt: uint64(attempt), AssignedWorkerId: "attacker-echo",
		},
	}
	packet := &pb.BusPacket{
		SenderId: "worker-1", Identity: identity, Signature: []byte("verified-signature"),
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("0123456789abcdef")},
		Payload:           &pb.BusPacket_JobResult{JobResult: result},
	}
	bus := &fakeBus{}
	engine := &Engine{ctx: ctx, jobStore: jobStore, bus: bus}
	engine.productionIdentity.Store(true)
	claims := &SessionTokenClaims{Subject: "worker-1", Tenant: "tenant-a"}
	return engine, jobStore, bus, result, packet, claims
}

func productionEventAuthority(t *testing.T, packet *pb.BusPacket) *bus.RawAdmissionAuthority {
	t.Helper()
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal production event fixture: %v", err)
	}
	digest := sha256.Sum256(wire)
	return &bus.RawAdmissionAuthority{
		ActualSubject: "sys.job.event", SessionSubject: packet.GetSenderId(),
		TenantID: packet.GetIdentity().GetTenantId(), Identity: packet.GetIdentity(),
		MessageID:      append([]byte(nil), packet.GetSignatureMetadata().GetMessageId()...),
		UnsignedDigest: append([]byte(nil), digest[:]...),
	}
}
