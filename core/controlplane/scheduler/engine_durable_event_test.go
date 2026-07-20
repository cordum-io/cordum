package scheduler

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestProductionProgressFenceRejectsRedeliveryStaleAndWrongWorker(t *testing.T) {
	engine, jobStore, _, result, _, claims := productionResultFixture(t)
	progress := &pb.JobProgress{
		JobId: result.GetJobId(), Identity: result.GetIdentity(), Dispatch: result.GetDispatch(),
	}
	packet := &pb.BusPacket{
		SenderId: claims.Subject, Identity: result.GetIdentity(), Signature: []byte("verified-signature"),
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("progress-msg-001")},
		Payload:           &pb.BusPacket_JobProgress{JobProgress: progress},
	}
	accepted, err := engine.acceptProductionDispatchEvent(packet, progress.GetDispatch(), claims, "progress")
	if err != nil || !accepted {
		t.Fatalf("current progress = (%v, %v), want accepted", accepted, err)
	}
	accepted, err = engine.acceptProductionDispatchEvent(packet, progress.GetDispatch(), claims, "progress")
	if err != nil || accepted {
		t.Fatalf("redelivered progress = (%v, %v), want duplicate reject", accepted, err)
	}
	if _, _, err := jobStore.BeginDispatch(context.Background(), result.GetJobId(), claims.Subject, claims.Tenant); err != nil {
		t.Fatalf("BeginDispatch(newer) error = %v", err)
	}
	packet.SignatureMetadata.MessageId = []byte("progress-msg-002")
	accepted, err = engine.acceptProductionDispatchEvent(packet, progress.GetDispatch(), claims, "progress")
	if err != nil || accepted {
		t.Fatalf("stale progress = (%v, %v), want reject", accepted, err)
	}
	wrong := *claims
	wrong.Subject = "worker-evil"
	accepted, err = engine.acceptProductionDispatchEvent(packet, progress.GetDispatch(), &wrong, "progress")
	if err != nil || accepted {
		t.Fatalf("wrong-worker progress = (%v, %v), want reject", accepted, err)
	}
}

func TestProductionCancelSeparatesPrivilegedAllAttemptOperation(t *testing.T) {
	engine, _, _, result, _, claims := productionResultFixture(t)
	cancel := &pb.JobCancel{JobId: result.GetJobId(), Identity: result.GetIdentity()}
	workerPacket := &pb.BusPacket{Payload: &pb.BusPacket_JobCancel{JobCancel: cancel}}
	accepted, err := engine.productionCancelAuthorized(workerPacket, cancel, claims)
	if err != nil || accepted {
		t.Fatalf("unfenced worker cancel = (%v, %v), want reject", accepted, err)
	}
	service := &SessionTokenClaims{Subject: defaultSenderID, Tenant: "_system"}
	accepted, err = engine.productionCancelAuthorized(workerPacket, cancel, service)
	if err != nil || !accepted {
		t.Fatalf("privileged service cancel = (%v, %v), want accepted", accepted, err)
	}
}

func TestProductionServiceCancelInvalidatesEveryWorkerAttempt(t *testing.T) {
	engine, jobStore, _, result, _, _ := productionResultFixture(t)
	cancel := &pb.JobCancel{
		JobId: result.GetJobId(), RequestedBy: defaultSenderID, Identity: result.GetIdentity(),
	}
	if err := engine.handleProductionServiceCancel(cancel); err != nil {
		t.Fatalf("handleProductionServiceCancel() error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateCancelled {
		t.Fatalf("state after service cancel = (%q, %v), want cancelled", state, err)
	}
	apply := model.JobResultApply{
		JobID: result.GetJobId(), DispatchID: result.GetDispatch().GetDispatchId(),
		Attempt: int(result.GetDispatch().GetAttempt()), WorkerID: "worker-1", Tenant: "tenant-a",
		MessageID: []byte("cancelled-result1"), Digest: []byte("digest"),
		State: model.JobStateSucceeded, Effect: []byte("effect"),
	}
	if got, err := jobStore.ApplyJobResult(context.Background(), apply); err != nil || got != model.JobEventRejected {
		t.Fatalf("old worker after service cancel = (%v, %v), want rejected", got, err)
	}
}

func TestProductionWorkerCancelAtomicallyAppliesOnce(t *testing.T) {
	engine, jobStore, bus, result, _, claims := productionResultFixture(t)
	cancel := &pb.JobCancel{
		JobId: result.GetJobId(), RequestedBy: claims.Subject, Reason: "worker stopped",
		Identity: result.GetIdentity(), Dispatch: result.GetDispatch(),
	}
	packet := &pb.BusPacket{
		SenderId: claims.Subject, Identity: result.GetIdentity(), Signature: []byte("verified-signature"),
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("cancel-msg-00001")},
		Payload:           &pb.BusPacket_JobCancel{JobCancel: cancel},
	}
	if err := engine.handleProductionWorkerCancel(packet, cancel, claims); err != nil {
		t.Fatalf("handleProductionWorkerCancel(first) error = %v", err)
	}
	if err := engine.handleProductionWorkerCancel(packet, cancel, claims); err != nil {
		t.Fatalf("handleProductionWorkerCancel(redelivery) error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), cancel.GetJobId())
	if err != nil || state != model.JobStateCancelled {
		t.Fatalf("GetState() = (%q, %v), want cancelled", state, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	acceptedResults := 0
	for _, published := range bus.published {
		if published.subject == "sys.internal.job.result.accepted" &&
			published.packet.GetJobResult().GetStatus() == pb.JobStatus_JOB_STATUS_CANCELLED {
			acceptedResults++
		}
	}
	if acceptedResults != 1 {
		t.Fatalf("accepted cancel results = %d, want exactly one", acceptedResults)
	}
}

func TestProductionWorkerCancelRejectsMessageIDDigestConflict(t *testing.T) {
	engine, _, bus, result, _, claims := productionResultFixture(t)
	cancel := &pb.JobCancel{
		JobId: result.GetJobId(), RequestedBy: claims.Subject, Reason: "first",
		Identity: result.GetIdentity(), Dispatch: result.GetDispatch(),
	}
	packet := &pb.BusPacket{
		SenderId: claims.Subject, Identity: result.GetIdentity(), Signature: []byte("verified-signature"),
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("cancel-conflict1")},
		Payload:           &pb.BusPacket_JobCancel{JobCancel: cancel},
	}
	if err := engine.handleProductionWorkerCancel(packet, cancel, claims); err != nil {
		t.Fatalf("handleProductionWorkerCancel(first) error = %v", err)
	}
	bus.mu.Lock()
	published := len(bus.published)
	bus.mu.Unlock()
	cancel.Reason = "tampered"
	if err := engine.handleProductionWorkerCancel(packet, cancel, claims); err != nil {
		t.Fatalf("handleProductionWorkerCancel(conflict) error = %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != published {
		t.Fatalf("digest conflict published %d extra effects", len(bus.published)-published)
	}
}

func TestProductionWorkerCancelRejectsStaleAttemptWithoutMutation(t *testing.T) {
	engine, jobStore, bus, result, _, claims := productionResultFixture(t)
	cancel := &pb.JobCancel{
		JobId: result.GetJobId(), RequestedBy: claims.Subject,
		Identity: result.GetIdentity(), Dispatch: result.GetDispatch(),
	}
	packet := &pb.BusPacket{
		SenderId: claims.Subject, Identity: result.GetIdentity(), Signature: []byte("verified-signature"),
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte("cancel-msg-stale")},
		Payload:           &pb.BusPacket_JobCancel{JobCancel: cancel},
	}
	if _, _, err := jobStore.BeginDispatch(
		context.Background(), result.GetJobId(), claims.Subject, claims.Tenant,
	); err != nil {
		t.Fatalf("BeginDispatch(new attempt) error = %v", err)
	}
	if err := engine.handleProductionWorkerCancel(packet, cancel, claims); err != nil {
		t.Fatalf("handleProductionWorkerCancel(stale) error = %v", err)
	}
	state, err := jobStore.GetState(context.Background(), result.GetJobId())
	if err != nil || state != model.JobStateRunning {
		t.Fatalf("state after stale cancel = (%q, %v), want running", state, err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.published) != 0 {
		t.Fatalf("stale cancel published %d effects, want zero", len(bus.published))
	}
}
