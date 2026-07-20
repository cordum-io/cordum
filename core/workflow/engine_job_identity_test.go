package workflow

import (
	"errors"
	"testing"

	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestMakeJobPacketProductionRejectsCompensationEscalationWithoutMutation(t *testing.T) {
	run := productionIdentityRun()
	req := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Compensation: &pb.Compensation{Topic: "job.undo", PrincipalId: "principal-admin"},
	}
	before := proto.Clone(req)
	engine := NewEngine(nil, nil).WithProductionIdentityEnforcement(true)

	packet, err := engine.makeJobPacket("trace-1", run, req)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("makeJobPacket() error = %v, want mismatch", err)
	}
	if packet != nil {
		t.Fatalf("makeJobPacket() = %#v, want nil", packet)
	}
	if !proto.Equal(req, before) {
		t.Fatal("makeJobPacket mutated rejected request")
	}
}

func TestMakeJobPacketProductionBindsEnvelopeAndRequest(t *testing.T) {
	run := productionIdentityRun()
	req := &pb.JobRequest{JobId: "job-1", Topic: "job.test"}
	engine := NewEngine(nil, nil).WithProductionIdentityEnforcement(true)

	packet, err := engine.makeJobPacket("trace-1", run, req)
	if err != nil {
		t.Fatalf("makeJobPacket() error = %v", err)
	}
	if !proto.Equal(packet.GetIdentity(), run.Identity) || !proto.Equal(packet.GetJobRequest().GetIdentity(), run.Identity) {
		t.Fatalf("packet identities = envelope:%v request:%v", packet.GetIdentity(), packet.GetJobRequest().GetIdentity())
	}
	if req.GetIdentity() != nil {
		t.Fatal("makeJobPacket mutated input request")
	}
}

func TestCloneIdentityBindingPreservesUnknownsWithoutAliasing(t *testing.T) {
	original := productionIdentityRun().Identity
	original.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})

	cloned := cloneIdentityBinding(original)
	if !proto.Equal(cloned, original) {
		t.Fatalf("cloneIdentityBinding() = %v, want %v", cloned, original)
	}
	cloned.ActorId = "changed"
	if original.GetActorId() != "principal-a" {
		t.Fatal("cloneIdentityBinding returned an alias")
	}
}

func productionIdentityRun() *WorkflowRun {
	return &WorkflowRun{
		ID: "run-1", OrgID: "tenant-a",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a"},
	}
}
