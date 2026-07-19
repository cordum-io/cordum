package capsdk

// BusPacket boundary validation rejects malformed or unsupported envelopes
// before any handler can mutate state. Keep this contract aligned with CAP v1.

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ValidateBusPacket enforces the CAP v1 envelope and payload-identity contract
// for an unmarshalled BusPacket. Callers must reject a non-nil error before
// dispatching the packet to any stateful handler.
func ValidateBusPacket(pkt *pb.BusPacket) error {
	if pkt == nil {
		return errors.New("buspacket: nil packet")
	}
	var missing []string
	if strings.TrimSpace(pkt.GetTraceId()) == "" {
		missing = append(missing, "trace_id")
	}
	if strings.TrimSpace(pkt.GetSenderId()) == "" {
		missing = append(missing, "sender_id")
	}
	if pkt.GetPayload() == nil {
		missing = append(missing, "payload")
	}
	if len(missing) > 0 {
		return fmt.Errorf("buspacket: missing required field(s): %s", strings.Join(missing, ", "))
	}
	if err := validateSecurityEnvelope(pkt); err != nil {
		return err
	}
	return validatePayloadIdentity(pkt)
}

func validateSecurityEnvelope(pkt *pb.BusPacket) error {
	if pkt.GetProtocolVersion() != DefaultProtocolVersion {
		return fmt.Errorf("buspacket: protocol_version must equal %d", DefaultProtocolVersion)
	}
	createdAt := pkt.GetCreatedAt()
	if createdAt == nil {
		return errors.New("buspacket: created_at is required")
	}
	if err := createdAt.CheckValid(); err != nil {
		return fmt.Errorf("buspacket: created_at is invalid: %w", err)
	}
	return nil
}

func validatePayloadIdentity(pkt *pb.BusPacket) error {
	sender := pkt.GetSenderId()
	switch payload := pkt.GetPayload().(type) {
	case *pb.BusPacket_Heartbeat:
		if payload.Heartbeat == nil || payload.Heartbeat.GetWorkerId() != sender {
			return errors.New("buspacket: heartbeat.worker_id must equal sender_id")
		}
	case *pb.BusPacket_JobResult:
		if payload.JobResult == nil || payload.JobResult.GetWorkerId() != sender {
			return errors.New("buspacket: job_result.worker_id must equal sender_id")
		}
	case *pb.BusPacket_Handshake:
		if payload.Handshake == nil || payload.Handshake.GetComponentId() != sender {
			return errors.New("buspacket: handshake.component_id must equal sender_id")
		}
		versions := payload.Handshake.GetSupportedVersions()
		if len(versions) != 1 || versions[0] != DefaultProtocolVersion {
			return fmt.Errorf("buspacket: handshake.supported_versions must equal [%d]", DefaultProtocolVersion)
		}
	}
	return nil
}
