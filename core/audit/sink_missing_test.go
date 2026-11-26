package audit

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNATSAuditPublisher_NilFallbackDropsOnPublishFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	bus := &mockAuditBus{publishErr: errors.New("publish unavailable")}
	pub := NewNATSAuditPublisher(bus, nil)
	before := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_fallback"))
	pub.Send(testEvent())
	pub.Send(testEvent())
	if len(bus.published) != 0 {
		t.Fatalf("published %d events, want 0", len(bus.published))
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_fallback")) - before; got != 2 {
		t.Fatalf("no_fallback drops = %v, want 2", got)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(logs.Bytes(), []byte(`"level":"WARN"`)); got != 2 {
		t.Fatalf("warnings = %d, want one per dropped event", got)
	}
}

func TestNATSAuditPublisher_NilFallbackDropsOnMarshalFailure(t *testing.T) {
	bus := &mockAuditBus{}
	pub := NewNATSAuditPublisher(bus, nil)
	event := testEvent()
	event.Timestamp = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	before := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_fallback"))
	pub.Send(event)
	if len(bus.published) != 0 {
		t.Fatalf("published %d events, want 0", len(bus.published))
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_fallback")) - before; got != 1 {
		t.Fatalf("no_fallback drops = %v, want 1", got)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBufferedExporter_CountsRejectedEvents(t *testing.T) {
	for _, reason := range []string{"buffer_full", "closed"} {
		t.Run(reason, func(t *testing.T) {
			b := &BufferedExporter{ch: make(chan SIEMEvent, 1), done: make(chan struct{})}
			if reason == "closed" {
				b.closed = true
				close(b.done)
			} else {
				b.Send(testEvent())
			}
			before := testutil.ToFloat64(auditEventDrops.WithLabelValues(reason))
			b.Send(testEvent())
			if got := testutil.ToFloat64(auditEventDrops.WithLabelValues(reason)) - before; got != 1 {
				t.Fatalf("%s drops = %v, want 1", reason, got)
			}
		})
	}
}

func TestBufferedExporter_NilExporterBatchAndDrain(t *testing.T) {
	for _, batchSize := range []int{1, 100} {
		t.Run(fmt.Sprintf("batch_%d", batchSize), func(t *testing.T) {
			before := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_exporter"))
			b := NewBufferedExporter(nil, WithBatchSize(batchSize), WithFlushInterval(time.Hour))
			t.Cleanup(func() {
				if err := b.Close(); err != nil {
					t.Error(err)
				}
			})
			b.Send(testEvent())
			if batchSize == 1 {
				waitForDropCount(t, "no_exporter", before+1)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_exporter")) - before; got != 1 {
				t.Fatalf("no_exporter drops = %v, want 1", got)
			}
		})
	}
}

func TestBufferedExporter_NilExporterFlushDropsBatch(t *testing.T) {
	before := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_exporter"))
	b := NewBufferedExporter(nil, WithFlushInterval(10*time.Millisecond))
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 3; i++ {
		b.Send(testEvent())
	}
	waitForDropCount(t, "no_exporter", before+3)
	b.Send(testEvent())
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_exporter")) - before; got != 4 {
		t.Fatalf("no_exporter drops = %v, want 4", got)
	}
}

func TestNATSAuditPublisher_NilExporterFallbackPublishFailure(t *testing.T) {
	bus := &mockAuditBus{publishErr: errors.New("publish unavailable")}
	b := NewBufferedExporter(nil, WithFlushInterval(10*time.Millisecond))
	pub := NewNATSAuditPublisher(bus, b)
	t.Cleanup(func() {
		if err := pub.Close(); err != nil {
			t.Error(err)
		}
	})
	before := testutil.ToFloat64(auditEventDrops.WithLabelValues("no_exporter"))
	pub.Send(testEvent())
	waitForDropCount(t, "no_exporter", before+1)
}

func waitForDropCount(t *testing.T, reason string, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if testutil.ToFloat64(auditEventDrops.WithLabelValues(reason)) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s count = %v, want %v", reason, testutil.ToFloat64(auditEventDrops.WithLabelValues(reason)), want)
}
