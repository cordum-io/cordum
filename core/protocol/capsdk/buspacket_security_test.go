package capsdk

import (
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateBusPacketRejectsInvalidSecurityEnvelope(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		packet *pb.BusPacket
		field  string
	}{
		"unsupported version": {securityHeartbeatPacket("worker-1", "worker-1"), "protocol_version"},
		"missing created at":  {securityHeartbeatPacket("worker-1", "worker-1"), "created_at"},
		"invalid created at":  {securityHeartbeatPacket("worker-1", "worker-1"), "created_at"},
		"invalid nanos":       {securityHeartbeatPacket("worker-1", "worker-1"), "created_at"},
		"heartbeat mismatch":  {securityHeartbeatPacket("sender-1", "worker-1"), "heartbeat.worker_id"},
		"result mismatch":     {securityResultPacket("sender-1", "worker-1"), "job_result.worker_id"},
		"handshake mismatch":  {securityHandshakePacket("sender-1", "worker-1"), "handshake.component_id"},
	}
	tests["unsupported version"].packet.ProtocolVersion = 2
	tests["missing created at"].packet.CreatedAt = nil
	tests["invalid created at"].packet.CreatedAt = &timestamppb.Timestamp{Seconds: 253402300800}
	tests["invalid nanos"].packet.CreatedAt = &timestamppb.Timestamp{Nanos: 1_000_000_000}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := ValidateBusPacket(test.packet)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("ValidateBusPacket() error = %v, want field %q", err, test.field)
			}
		})
	}
}

func TestValidateBusPacketAllowsReservedIdentityToRelayJobResult(t *testing.T) {
	t.Parallel()
	packet := securityResultPacket(servicetoken.IdentityScheduler, "worker-1")
	if err := ValidateBusPacket(packet); err != nil {
		t.Fatalf("ValidateBusPacket() error = %v, want nil for scheduler-relayed result", err)
	}
}

func TestValidateBusPacketRejectsNonReservedSenderWorkerMismatch(t *testing.T) {
	t.Parallel()
	packet := securityResultPacket("worker-2", "worker-1")
	err := ValidateBusPacket(packet)
	if err == nil || !strings.Contains(err.Error(), "job_result.worker_id") {
		t.Fatalf("ValidateBusPacket() error = %v, want job_result.worker_id mismatch", err)
	}
}

func TestValidateBusPacketRequiresExactHandshakeVersion(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		versions []int32
		wantErr  bool
	}{
		"missing":     {nil, true},
		"unsupported": {[]int32{999}, true},
		"ambiguous":   {[]int32{1, 999}, true},
		"exact v1":    {[]int32{1}, false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			packet := securityHandshakePacket("worker-1", "worker-1")
			packet.GetHandshake().SupportedVersions = test.versions
			err := ValidateBusPacket(packet)
			if test.wantErr && (err == nil || !strings.Contains(err.Error(), "handshake.supported_versions")) {
				t.Fatalf("ValidateBusPacket() error = %v, want supported_versions error", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateBusPacket() error = %v, want nil", err)
			}
		})
	}
}

func securityEnvelope(sender string) *pb.BusPacket {
	return &pb.BusPacket{
		TraceId: "trace-security", SenderId: sender,
		ProtocolVersion: DefaultProtocolVersion, CreatedAt: timestamppb.Now(),
	}
}

func securityHeartbeatPacket(sender, worker string) *pb.BusPacket {
	packet := securityEnvelope(sender)
	packet.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: worker}}
	return packet
}

func securityResultPacket(sender, worker string) *pb.BusPacket {
	packet := securityEnvelope(sender)
	packet.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{WorkerId: worker}}
	return packet
}

func securityHandshakePacket(sender, worker string) *pb.BusPacket {
	packet := securityEnvelope(sender)
	packet.Payload = &pb.BusPacket_Handshake{Handshake: &pb.Handshake{
		ComponentId: worker, SupportedVersions: []int32{DefaultProtocolVersion},
	}}
	return packet
}
