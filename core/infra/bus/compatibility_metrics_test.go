package bus

import (
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCompatibilityTelemetryCountsOnlyObservedWeaknesses(t *testing.T) {
	packet := &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			ResultPtr: "redis://legacy-result", ArtifactPtrs: []string{"redis://legacy-artifact"},
		}},
	}
	reasons := []string{
		compatReasonUnsigned, compatReasonMissingSignatureMetadata,
		compatReasonMissingIdentity, compatReasonLegacyPointer,
	}
	before := make(map[string]float64, len(reasons))
	for _, reason := range reasons {
		before[reason] = testutil.ToFloat64(busCompatibilityTotal.WithLabelValues(reason))
	}
	observeCompatibilityPacket(packet)
	for _, reason := range reasons {
		got := testutil.ToFloat64(busCompatibilityTotal.WithLabelValues(reason))
		if got != before[reason]+1 {
			t.Fatalf("compatibility counter %q = %v, want %v", reason, got, before[reason]+1)
		}
	}
}

func TestConfiguredCompatibilityAdmissionIsTelemetryVisible(t *testing.T) {
	before := testutil.ToFloat64(busCompatibilityTotal.WithLabelValues(compatReasonConfiguredFailOpen))
	b := &NatsBus{}
	if err := b.SetRawPacketAdmission(nil); err != nil {
		t.Fatalf("SetRawPacketAdmission(nil): %v", err)
	}
	after := testutil.ToFloat64(busCompatibilityTotal.WithLabelValues(compatReasonConfiguredFailOpen))
	if after != before+1 {
		t.Fatalf("configured fail-open counter = %v, want %v", after, before+1)
	}
}
