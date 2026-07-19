package scheduler

import (
	"log/slog"

	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (e *Engine) validateInboundPacket(packet *pb.BusPacket, source string) bool {
	if err := capsdk.ValidateBusPacket(packet); err != nil {
		slog.Warn("invalid CAP envelope rejected",
			"source", source,
			"trace_id", safeTraceID(packet),
			"sender_id", safeSenderID(packet),
			"validation_error", err,
		)
		if e != nil && e.metrics != nil {
			e.metrics.IncValidationRejections()
		}
		return false
	}
	return true
}
