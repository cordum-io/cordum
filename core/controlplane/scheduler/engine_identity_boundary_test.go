package scheduler

import (
	"testing"

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
