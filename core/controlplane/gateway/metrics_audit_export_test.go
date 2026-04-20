package gateway

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// TestAuditExportMetrics_ObserveIncrementsCounter pins the metric
// wiring. Uses promtest utils so the test doesn't race other tests
// mutating the same registry — every assertion is a delta.
func TestAuditExportMetrics_ObserveIncrementsCounter(t *testing.T) {
	before := testutil.ToFloat64(auditExportCounter.WithLabelValues("json", "ok"))
	observeAuditExport("json", "ok", 42)
	after := testutil.ToFloat64(auditExportCounter.WithLabelValues("json", "ok"))
	if after-before != 1 {
		t.Errorf("json/ok counter delta = %v, want 1", after-before)
	}
}

func TestAuditExportMetrics_NonOKDoesNotObserveHistogram(t *testing.T) {
	// Histogram count for the csv label.
	beforeCount := testutil.CollectAndCount(auditExportEventsHistogram)
	observeAuditExport("csv", "forbidden", 0)
	afterCount := testutil.CollectAndCount(auditExportEventsHistogram)
	// A 403 must NOT add a sample to the histogram — its event_count
	// is definitionally 0 and would skew the bucket statistics.
	if afterCount != beforeCount {
		t.Errorf("histogram sample count changed on forbidden outcome: before=%d after=%d",
			beforeCount, afterCount)
	}
	// But the counter MUST increment so operators see the 403 surge.
	after := testutil.ToFloat64(auditExportCounter.WithLabelValues("csv", "forbidden"))
	if after < 1 {
		t.Errorf("counter did not record forbidden outcome: %v", after)
	}
}

func TestAuditExportMetrics_EmptyLabelsFallback(t *testing.T) {
	// observeAuditExport should not panic if the handler passes an
	// empty format/status (shouldn't happen in production but we want
	// the metrics layer to stay forgiving).
	beforeUnknown := testutil.ToFloat64(auditExportCounter.WithLabelValues("unknown", "unknown"))
	observeAuditExport("", "", 0)
	afterUnknown := testutil.ToFloat64(auditExportCounter.WithLabelValues("unknown", "unknown"))
	if afterUnknown-beforeUnknown != 1 {
		t.Errorf("unknown/unknown delta = %v, want 1", afterUnknown-beforeUnknown)
	}
}

func TestAuditExportMetrics_OKRecordsHistogram(t *testing.T) {
	// Grab the concrete Histogram for the json label so we can read
	// its sample count directly. CollectAndCount returns time-series
	// counts (labels), not samples, so it's the wrong tool here.
	hist := auditExportEventsHistogram.WithLabelValues("json")
	countBefore := samplesIn(t, hist)
	observeAuditExport("json", "ok", 250)
	countAfter := samplesIn(t, hist)
	if countAfter-countBefore != 1 {
		t.Errorf("histogram samples delta = %d, want 1", countAfter-countBefore)
	}
}

// samplesIn reads the sample count on a Prometheus histogram observer
// by round-tripping through its Collect implementation.
func samplesIn(t *testing.T, observer prometheus.Observer) uint64 {
	t.Helper()
	h, ok := observer.(prometheus.Histogram)
	if !ok {
		t.Fatalf("observer is not a Histogram: %T", observer)
	}
	ch := make(chan prometheus.Metric, 1)
	h.Collect(ch)
	close(ch)
	select {
	case m := <-ch:
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		if pb.Histogram == nil {
			return 0
		}
		return pb.Histogram.GetSampleCount()
	default:
		return 0
	}
}
