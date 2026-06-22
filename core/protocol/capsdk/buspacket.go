package capsdk

// BusPacket required-field validator. Mirrors ValidateHandshakeRequest's
// contract: a minimal post-unmarshal guard that catches packets which a
// permissive proto.Unmarshal accepted but every downstream handler would
// have to re-check independently. Keep MINIMAL — over-rejection silently
// drops legitimate traffic.

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// ValidateBusPacket enforces the required-field contract for an unmarshalled
// BusPacket. It is intentionally narrow: trace_id, sender_id, and a non-nil
// payload oneof. Add more checks only if every legitimate publisher already
// satisfies them — see BUG-010 risk note in the bug-hunt report.
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
	return nil
}
