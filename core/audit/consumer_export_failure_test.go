package audit

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/bus"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A failed Export is acked, not retried: the event is already chained, and
// Chainer.Append would chain it again on a redelivery.
func TestNATSAuditConsumer_JetStreamExportFailureAckedNotRetried(t *testing.T) {
	consumer, publisher, js := auditJetStream(t)
	chainer, client := newConsumerChainer(t)
	packet := packetFor(t, testEvent())
	// The bus naks a packet that fails CAP envelope validation before any handler runs.
	packet.TraceId = "audit-export-failure-trace"
	packet.CreatedAt = timestamppb.Now()
	packet.ProtocolVersion = capsdk.DefaultProtocolVersion
	observed := &redeliveryAuditBus{NatsBus: consumer, deliveries: make(chan auditDelivery, 8)}
	exporter := &mockExporter{failNext: 1} // A redelivery would export.
	c, err := NewNATSAuditConsumer(observed, exporter, WithChainer(chainer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	h := &auditRedeliveryHarness{observed: observed, publish: publisher, js: js, packet: packet}

	logs, err := os.CreateTemp(t.TempDir(), "export-failure-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logs.Close(); err != nil {
			t.Error(err)
		}
	})
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelError})))
	defer slog.SetDefault(prev)
	failures := testutil.ToFloat64(auditEventDrops.WithLabelValues("export_failed"))

	stored := h.send(t)
	d := h.next(t)
	if d.err == nil {
		t.Fatal("export failure did not reach the bus as a handler error")
	}
	if delay, retry := bus.RetryDelay(d.err); retry {
		t.Fatalf("export failure asks the bus to redeliver after %v", delay)
	}
	h.settled(t, stored.Sequence)
	chained, err := client.XLen(context.Background(), chainer.StreamKey(testEvent().TenantID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if chained != 1 || exporter.totalEvents() != 0 {
		t.Fatalf("chained=%d exported=%d, want 1 and 0", chained, exporter.totalEvents())
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("export_failed")) - failures; got != 1 {
		t.Fatalf("export_failed drops rose by %v, want 1", got)
	}
	out, err := os.ReadFile(logs.Name())
	if err != nil {
		t.Fatal(err)
	}
	// The suffix is the whole line after its time stamp, so no payload field fits.
	want := `level=ERROR msg="audit consumer: export failed, event not retried" ` +
		`event_type=safety.decision tenant_id=tenant-1 job_id=job-99 error="mock export failure"` + "\n"
	if strings.Count(string(out), "\n") != 1 || !strings.HasSuffix(string(out), " "+want) {
		t.Fatalf("export failure log = %q, want one line ending %q", out, want)
	}
}
