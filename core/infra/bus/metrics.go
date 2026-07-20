package bus

import (
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	compatReasonUnsigned                 = "unsigned"
	compatReasonMissingSignatureMetadata = "missing_signature_metadata"
	compatReasonMissingIdentity          = "missing_identity"
	compatReasonLegacyPointer            = "legacy_pointer"
	compatReasonConfiguredFailOpen       = "configured_fail_open"
)

// busUnmarshalFailureTotal counts BusPackets dropped because proto.Unmarshal
// failed (reason="unmarshal") or because the post-unmarshal capsdk validator
// rejected the packet (reason="invalid"). Labelled by subject so a noisy
// publisher can be located without high-cardinality blowup.
//
// BUG-008: previously the non-durable subscriber silently dropped malformed
// packets — no metric, no audit event, no visibility. BUG-010: ValidateBusPacket
// catches packets that proto.Unmarshal accepted but downstream handlers would
// have to re-check. Both surface here.
var busUnmarshalFailureTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "cordum",
		Subsystem: "bus",
		Name:      "unmarshal_failure_total",
		Help:      "BusPackets dropped due to unmarshal or post-unmarshal validation failure, labelled by subject and reason.",
	},
	[]string{"subject", "reason"},
)

var busCompatibilityTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "cordum", Subsystem: "bus", Name: "compatibility_total",
		Help: "Compatibility-only packet and configuration observations by bounded reason.",
	},
	[]string{"reason"},
)

func init() {
	prometheus.MustRegister(busUnmarshalFailureTotal)
	prometheus.MustRegister(busCompatibilityTotal)
}

func observeCompatibilityPacket(packet *pb.BusPacket) {
	if len(packet.GetSignature()) == 0 {
		busCompatibilityTotal.WithLabelValues(compatReasonUnsigned).Inc()
	}
	if packet.GetSignatureMetadata() == nil {
		busCompatibilityTotal.WithLabelValues(compatReasonMissingSignatureMetadata).Inc()
	}
	identity := packet.GetIdentity()
	if identity == nil || identity.GetTenantId() == "" || identity.GetPrincipalId() == "" || identity.GetActorId() == "" {
		busCompatibilityTotal.WithLabelValues(compatReasonMissingIdentity).Inc()
	}
	if compatibilityPacketHasLegacyPointer(packet) {
		busCompatibilityTotal.WithLabelValues(compatReasonLegacyPointer).Inc()
	}
}

func compatibilityPacketHasLegacyPointer(packet *pb.BusPacket) bool {
	switch payload := packet.GetPayload().(type) {
	case *pb.BusPacket_JobRequest:
		request := payload.JobRequest
		return strings.TrimSpace(request.GetContextPtr()) != "" ||
			strings.TrimSpace(request.GetCompensation().GetContextPtr()) != ""
	case *pb.BusPacket_JobResult:
		return strings.TrimSpace(payload.JobResult.GetResultPtr()) != "" || len(payload.JobResult.GetArtifactPtrs()) > 0
	case *pb.BusPacket_JobProgress:
		return strings.TrimSpace(payload.JobProgress.GetResultPtr()) != "" || len(payload.JobProgress.GetArtifactPtrs()) > 0
	default:
		return false
	}
}
