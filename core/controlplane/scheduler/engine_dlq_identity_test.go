package scheduler

import (
	"context"
	"testing"

	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestEmitDLQProductionEchoesStoredCanonicalIdentity(t *testing.T) {
	bus := &fakeBus{}
	jobStore := newFakeJobStore()
	authority := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-dlq", Topic: "job.test"}, authority,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if err := jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	engine := &Engine{ctx: context.Background(), bus: bus, jobStore: jobStore}
	engine.productionIdentity.Store(true)

	if err := engine.emitDLQ(req.GetJobId(), req.GetTopic(), pb.JobStatus_JOB_STATUS_DENIED, "denied", "policy_denied"); err != nil {
		t.Fatalf("emitDLQ() error = %v", err)
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	packet := bus.published[len(bus.published)-1].packet
	if !proto.Equal(packet.GetIdentity(), authority) ||
		!proto.Equal(packet.GetJobResult().GetIdentity(), authority) {
		t.Fatalf("DLQ identity = envelope:%v result:%v", packet.GetIdentity(), packet.GetJobResult().GetIdentity())
	}
	if packet.GetJobResult().GetWorkerId() != defaultSenderID {
		t.Fatalf("worker id = %q, want %q", packet.GetJobResult().GetWorkerId(), defaultSenderID)
	}
}
