package bus

import (
	"github.com/prometheus/client_golang/prometheus"
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

func init() {
	prometheus.MustRegister(busUnmarshalFailureTotal)
}
