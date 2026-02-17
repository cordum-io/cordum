package bus

import (
	"fmt"

	"github.com/cordum/cordum/core/infra/buildinfo"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PublishHandshake sends a Handshake message on sys.handshake so the scheduler
// (and other services) can track connected components. The call is intended to
// be non-fatal — callers should log a warning on error and continue startup.
//
// Services expected to publish handshakes:
//   - api-gateway       (COMPONENT_ROLE_GATEWAY)
//   - scheduler         (COMPONENT_ROLE_SCHEDULER)
//   - workflow-engine   (COMPONENT_ROLE_ORCHESTRATOR)
//
// Services that do NOT connect to NATS and therefore skip handshake:
//   - safety-kernel     (gRPC only)
//   - context-engine    (gRPC only)
func PublishHandshake(b *NatsBus, componentID string, role pb.ComponentRole, caps map[string]bool) error {
	if b == nil {
		return fmt.Errorf("publish handshake: bus is nil")
	}
	if componentID == "" {
		return fmt.Errorf("publish handshake: empty component ID")
	}
	packet := &pb.BusPacket{
		TraceId:         uuid.New().String(),
		SenderId:        componentID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &pb.BusPacket_Handshake{
			Handshake: &pb.Handshake{
				ComponentId:       componentID,
				Role:              role,
				SupportedVersions: []int32{int32(capsdk.DefaultProtocolVersion)},
				Capabilities:      caps,
				SdkVersion:        buildinfo.Version,
			},
		},
	}
	return b.Publish(capsdk.SubjectHandshake, packet)
}
