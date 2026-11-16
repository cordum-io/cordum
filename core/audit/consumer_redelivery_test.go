package audit

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type auditDelivery struct {
	published, arrived time.Time
	hasPublished       bool
	err                error
}

type redeliveryAuditBus struct {
	*bus.NatsBus
	retryUntil time.Time
	deliveries chan auditDelivery
}

func (b *redeliveryAuditBus) SubscribeWithContext(subject, queue string, handler func(context.Context, *pb.BusPacket) error) error {
	first := true
	return b.NatsBus.SubscribeWithContext(subject, queue, func(ctx context.Context, packet *pb.BusPacket) error {
		at, ok := bus.PublishedAt(ctx)
		d := auditDelivery{published: at, hasPublished: ok, arrived: time.Now()}
		if first && !b.retryUntil.IsZero() {
			d.err = bus.RetryAfter(errors.New("consumer unavailable before processing"), time.Until(b.retryUntil))
		} else {
			d.err = handler(ctx, packet)
		}
		first = false
		b.deliveries <- d
		return d.err
	})
}

type auditRedeliveryHarness struct {
	observed *redeliveryAuditBus
	publish  *bus.NatsBus
	js       nats.JetStreamContext
	trust    *policysign.TrustStore
	packet   *pb.BusPacket
	expires  time.Time
	exporter *mockExporter
	chainer  *Chainer
	client   *redis.Client
}

func auditJetStream(t *testing.T) (*bus.NatsBus, *bus.NatsBus, nats.JetStreamContext) {
	t.Helper()
	t.Setenv("NATS_USE_JETSTREAM", "true")
	t.Setenv("NATS_JS_ACK_WAIT", "250ms")
	s, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	connect := func() *bus.NatsBus {
		b, err := bus.NewNatsBus(s.ClientURL())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return b
	}
	consumer, publisher := connect(), connect()
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream(nats.MaxWait(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return consumer, publisher, js
}

func newAuditRedeliveryHarness(t *testing.T, retry bool) *auditRedeliveryHarness {
	t.Helper()
	consumer, publisher, js := auditJetStream(t)
	chainer, client := newConsumerChainer(t)
	pub, priv := publisherAuthKey(t)
	trust := policysign.NewTrustStore()
	if err := trust.Add("cp", pub); err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Hour)
	if retry {
		issued = time.Now().Add(3*time.Second - servicetoken.DefaultServiceTTL - servicetoken.DefaultSkew)
	}
	packet := packetFor(t, testEvent())
	packet.SenderId = "private-audit-sender"
	// The bus naks a packet that fails CAP envelope validation before any handler runs.
	packet.TraceId = "audit-redelivery-trace"
	packet.CreatedAt = timestamppb.Now()
	packet.ProtocolVersion = capsdk.DefaultProtocolVersion
	packet.AuthToken = publisherAuthToken(t, priv, "cp", servicetoken.IdentityGateway, issued)
	observed := &redeliveryAuditBus{NatsBus: consumer, deliveries: make(chan auditDelivery, 8)}
	expires := issued.Add(servicetoken.DefaultServiceTTL + servicetoken.DefaultSkew)
	if retry {
		observed.retryUntil = expires.Add(500 * time.Millisecond)
	}
	exporter := &mockExporter{}
	c, err := NewNATSAuditConsumer(observed, exporter, WithChainer(chainer), WithPublisherAuth(trust, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return &auditRedeliveryHarness{observed, publisher, js, trust, packet, expires, exporter, chainer, client}
}

func (h *auditRedeliveryHarness) next(t *testing.T) auditDelivery {
	t.Helper()
	select {
	case d := <-h.observed.deliveries:
		return d
	case <-time.After(8 * time.Second):
		t.Fatal("audit delivery timed out")
		return auditDelivery{}
	}
}

func (h *auditRedeliveryHarness) send(t *testing.T) *nats.RawStreamMsg {
	t.Helper()
	if err := h.publish.Publish(capsdk.SubjectAuditExport, h.packet); err != nil {
		t.Fatal(err)
	}
	stored, err := h.js.GetLastMsg("CORDUM_SYS", capsdk.SubjectAuditExport)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func (h *auditRedeliveryHarness) settled(t *testing.T, sequence uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		info, err := h.js.ConsumerInfo("CORDUM_SYS", "dur_"+QueueAuditExporters+"__sys_audit_export")
		if err != nil {
			t.Fatal(err)
		}
		if info.AckFloor.Stream == sequence && info.NumAckPending == 0 && info.NumPending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("audit message not acknowledged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-h.observed.deliveries:
		t.Fatal("unexpected extra delivery")
	case <-time.After(750 * time.Millisecond): // Three AckWait periods.
	}
}

func (h *auditRedeliveryHarness) effects(t *testing.T, want int, drops float64) {
	t.Helper()
	chained, err := h.client.XLen(context.Background(), h.chainer.StreamKey(testEvent().TenantID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if chained != int64(want) || h.exporter.totalEvents() != want {
		t.Fatalf("chained=%d exported=%d, want %d each", chained, h.exporter.totalEvents(), want)
	}
	if got := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token")); got != drops {
		t.Fatalf("service-token drops=%v, want %v", got, drops)
	}
}

func TestNATSAuditConsumer_JetStreamRedeliveryAfterTokenExpiry(t *testing.T) {
	h := newAuditRedeliveryHarness(t, true)
	drops := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token"))
	stored := h.send(t)
	first := h.next(t)
	if !first.hasPublished || first.published.IsZero() || !first.published.Equal(stored.Time) {
		t.Fatal("first delivery lacks the stored-message timestamp")
	}
	if first.published.After(h.expires) {
		t.Fatal("box was slow: token expired before first publish")
	}
	if _, _, err := servicetoken.Verify(h.packet.AuthToken, h.trust.Lookup, first.published); err != nil {
		t.Fatalf("token invalid at publish: %v", err)
	}
	if first.err == nil {
		t.Fatal("first delivery did not request retry")
	}
	h.effects(t, 0, drops)
	second := h.next(t)
	if !second.hasPublished || !second.published.Equal(first.published) || second.err != nil {
		t.Fatalf("redelivery timestamp or handler result incorrect: %v", second.err)
	}
	if _, _, err := servicetoken.Verify(h.packet.AuthToken, h.trust.Lookup, second.arrived); !errors.Is(err, servicetoken.ErrExpired) {
		t.Fatalf("token not expired at redelivery: %v", err)
	}
	h.settled(t, stored.Sequence)
	h.effects(t, 1, drops)
	h.refused(t, 1) // The accepted packet's token on a new publish after expiry.
}

func TestNATSAuditConsumer_JetStreamRefusesStaleTokenOnFreshPublish(t *testing.T) {
	h := newAuditRedeliveryHarness(t, false)
	h.refused(t, 0)
}

func (h *auditRedeliveryHarness) refused(t *testing.T, previous int) {
	t.Helper()
	logs, err := os.CreateTemp(t.TempDir(), "refusal-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logs.Close(); err != nil {
			t.Error(err)
		}
	})
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)
	drops := testutil.ToFloat64(auditEventDrops.WithLabelValues("service_token"))
	stored := h.send(t)
	d := h.next(t)
	if !d.hasPublished || !d.published.Equal(stored.Time) || d.err != nil {
		t.Fatalf("refused delivery timestamp or handler result incorrect: %v", d.err)
	}
	if _, _, err := servicetoken.Verify(h.packet.AuthToken, h.trust.Lookup, d.published); !errors.Is(err, servicetoken.ErrExpired) {
		t.Fatalf("fresh publish must carry an expired token: %v", err)
	}
	h.settled(t, stored.Sequence)
	h.effects(t, previous, drops+1)
	out, err := os.ReadFile(logs.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "\n") != 1 || !strings.Contains(string(out), `msg="audit export event refused" reason=service_token`) {
		t.Fatal("expected exactly one fixed-field refusal log line")
	}
	for _, secret := range []string{h.packet.AuthToken, h.packet.SenderId, testEvent().TenantID, testEvent().JobID} {
		if strings.Contains(string(out), secret) {
			t.Fatal("refusal log contains caller-controlled data")
		}
	}
}
