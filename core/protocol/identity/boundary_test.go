package identity

import (
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeProductionJobPacketRejectsEnvelopeMismatchWithoutMutation(t *testing.T) {
	auth := productionAuthority()
	packet := &pb.BusPacket{
		Identity: &pb.IdentityBinding{TenantId: "tenant-other"},
		Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
			JobId: "job-1", Topic: "job.test", TenantId: auth.TenantId,
		}},
	}
	before := proto.Clone(packet)

	got, err := NormalizeProductionJobPacket(packet, auth)
	if !errors.Is(err, ErrProductionIdentityMismatch) {
		t.Fatalf("NormalizeProductionJobPacket() error = %v, want mismatch", err)
	}
	if got != nil {
		t.Fatalf("NormalizeProductionJobPacket() = %#v, want nil", got)
	}
	if !proto.Equal(packet, before) {
		t.Fatal("NormalizeProductionJobPacket mutated rejected input")
	}
}

func TestNormalizeProductionJobPacketFillsEnvelopeAndPayloadOnClone(t *testing.T) {
	auth := productionAuthority()
	packet := &pb.BusPacket{Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
		JobId: "job-1", Topic: "job.test",
	}}}

	got, err := NormalizeProductionJobPacket(packet, auth)
	if err != nil {
		t.Fatalf("NormalizeProductionJobPacket() error = %v", err)
	}
	if got == packet || got.GetJobRequest() == packet.GetJobRequest() {
		t.Fatal("NormalizeProductionJobPacket returned aliased input")
	}
	if !proto.Equal(got.GetIdentity(), auth) || !proto.Equal(got.GetJobRequest().GetIdentity(), auth) {
		t.Fatalf("canonical identities = envelope:%v payload:%v, want %v", got.GetIdentity(), got.GetJobRequest().GetIdentity(), auth)
	}
	if packet.GetIdentity() != nil || packet.GetJobRequest().GetIdentity() != nil {
		t.Fatal("NormalizeProductionJobPacket mutated accepted input")
	}
}

func TestNormalizeProductionPolicyCheckRejectsMismatchWithoutMutation(t *testing.T) {
	auth := productionAuthority()
	req := &pb.PolicyCheckRequest{
		JobId: "job-1", Topic: "job.test", Tenant: auth.TenantId,
		PrincipalId: "principal-other", Meta: &pb.JobMetadata{ActorId: auth.ActorId},
	}
	before := proto.Clone(req)

	got, err := NormalizeProductionPolicyCheckRequest(req, auth)
	if !errors.Is(err, ErrProductionIdentityMismatch) {
		t.Fatalf("NormalizeProductionPolicyCheckRequest() error = %v, want mismatch", err)
	}
	if got != nil {
		t.Fatalf("NormalizeProductionPolicyCheckRequest() = %#v, want nil", got)
	}
	if !proto.Equal(req, before) {
		t.Fatal("NormalizeProductionPolicyCheckRequest mutated rejected input")
	}
}

func TestNormalizeProductionPolicyCheckFillsCanonicalMirrors(t *testing.T) {
	auth := productionAuthority()
	req := &pb.PolicyCheckRequest{JobId: "job-1", Topic: "job.test"}

	got, err := NormalizeProductionPolicyCheckRequest(req, auth)
	if err != nil {
		t.Fatalf("NormalizeProductionPolicyCheckRequest() error = %v", err)
	}
	if got.GetTenant() != auth.GetTenantId() || got.GetPrincipalId() != auth.GetPrincipalId() {
		t.Fatalf("canonical fields = %q/%q", got.GetTenant(), got.GetPrincipalId())
	}
	if got.GetMeta().GetTenantId() != auth.GetTenantId() || got.GetMeta().GetActorId() != auth.GetActorId() {
		t.Fatalf("canonical meta = %#v", got.GetMeta())
	}
	if !proto.Equal(got.GetIdentity(), auth) {
		t.Fatalf("canonical identity = %#v, want %#v", got.GetIdentity(), auth)
	}
	if req.GetIdentity() != nil || req.GetMeta() != nil {
		t.Fatal("NormalizeProductionPolicyCheckRequest mutated input")
	}
}
