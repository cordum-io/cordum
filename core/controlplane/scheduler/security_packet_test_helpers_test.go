package scheduler

import (
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func completeSecurityTestEnvelope(packet *pb.BusPacket) *pb.BusPacket {
	if packet == nil {
		return nil
	}
	if packet.GetTraceId() == "" {
		packet.TraceId = "trace-security-test"
	}
	if packet.GetSenderId() == "" {
		packet.SenderId = testPacketSender(packet)
	}
	if packet.GetProtocolVersion() == 0 {
		packet.ProtocolVersion = 1
	}
	if packet.GetCreatedAt() == nil {
		packet.CreatedAt = timestamppb.Now()
	}
	return packet
}

func testPacketSender(packet *pb.BusPacket) string {
	switch payload := packet.GetPayload().(type) {
	case *pb.BusPacket_Heartbeat:
		return payload.Heartbeat.GetWorkerId()
	case *pb.BusPacket_JobResult:
		return payload.JobResult.GetWorkerId()
	case *pb.BusPacket_Handshake:
		return payload.Handshake.GetComponentId()
	case *pb.BusPacket_JobCancel:
		return payload.JobCancel.GetRequestedBy()
	default:
		return "test-publisher"
	}
}

func boundTestBinding(workerID, tenant, sdkVersion string) SessionBinding {
	return SessionBinding{
		WorkerID: workerID, AgentID: workerID + "-agent", Tenant: tenant,
		Audience: WorkerHandshakeAudience, ProofKeyID: workerID + "-proof", SDKVersion: sdkVersion,
	}
}
