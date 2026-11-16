package audit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/env"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const matrixTenant = "tenant-matrix"

// TestAudienceMatrixBusAuditExport presents every credential on
// sys.audit.export in the packet the gateway's NATSAuditPublisher sends. The
// protected effect is a chained, exported audit event. The baseline is the
// publisher's own send, which carries no credential.
func TestAudienceMatrixBusAuditExport(t *testing.T) {
	t.Setenv(env.EnvMode, "production")
	t.Setenv("CORDUM_NATS_ALLOW_PLAINTEXT", "true")
	t.Setenv("CORDUM_NATS_ALLOW_NOAUTH", "true")
	t.Setenv("NATS_USE_JETSTREAM", "false")
	present := func(t *testing.T, c identity.Credential) identity.Result {
		token := audienceCredential(t, c)
		f := newAuditMatrixFixture(t)
		capture := &capturingAuditBus{}
		audit.NewNATSAuditPublisher(capture, nil).Send(matrixAuditEvent())
		require.Equal(t, capsdk.SubjectAuditExport, capture.subject)
		packet := capture.packet
		if c == identity.AgentIDHeader {
			packet.SenderId = token
		} else {
			packet.AuthToken = token
		}
		return f.observe(t, func() { require.NoError(t, f.publisher.Publish(capsdk.SubjectAuditExport, packet)) })
	}
	probe := func(t *testing.T) identity.Result {
		f := newAuditMatrixFixture(t)
		return f.observe(t, func() { audit.NewNATSAuditPublisher(f.publisher, nil).Send(matrixAuditEvent()) })
	}
	identity.Run(t, identity.BusAuditExport, present, identity.Baseline(probe),
		identity.Skip(identity.Anonymous, "PR1-22"),
		identity.Skip(identity.UserSession, "PR1-22"),
		identity.Skip(identity.APIKeyStatic, "PR1-22"),
		identity.Skip(identity.APIKeyManaged, "PR1-22"),
		identity.Skip(identity.GatewayJWT, "PR1-22"),
		identity.Skip(identity.GatewayJWTWrongAudience, "PR1-22"),
		identity.Skip(identity.OIDCBearer, "PR1-22"),
		identity.Skip(identity.OIDCBearerWrongAudience, "PR1-22"),
		identity.Skip(identity.DelegationToken, "PR1-22"),
		identity.Skip(identity.ServiceToken, "PR1-22"),
		identity.Skip(identity.WorkerCredential, "PR1-22"),
		identity.Skip(identity.WorkerSession, "PR1-22"),
		identity.Skip(identity.WorkerSessionWrongAudience, "PR1-22"),
		identity.Skip(identity.SCIMBearer, "PR1-22"),
		identity.Skip(identity.AgentdNonce, "PR1-22"),
		identity.Skip(identity.AgentIDHeader, "PR1-22"))
}

func matrixAuditEvent() audit.SIEMEvent {
	return audit.SIEMEvent{Timestamp: time.Now().UTC(), EventType: "matrix.audit", Severity: "info",
		TenantID: matrixTenant, Action: "matrix-action", Decision: "allow"}
}

// capturingAuditBus records the packet NATSAuditPublisher.Send builds.
type capturingAuditBus struct {
	subject string
	packet  *pb.BusPacket
}

func (b *capturingAuditBus) Publish(subject string, packet *pb.BusPacket) error {
	b.subject, b.packet = subject, packet
	return nil
}

func (b *capturingAuditBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

// observedAuditBus is a real NatsBus that reports each packet its handler returned on.
type observedAuditBus struct {
	*bus.NatsBus
	handled chan error
}

func (b observedAuditBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	return b.NatsBus.Subscribe(subject, queue, func(p *pb.BusPacket) error {
		err := handler(p)
		b.handled <- err
		return err
	})
}

// recordingExporter is the SIEM sink: it keeps every exported event.
type recordingExporter struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (e *recordingExporter) Export(_ context.Context, events []audit.SIEMEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, events...)
	return nil
}

func (e *recordingExporter) Close() error { return nil }

func (e *recordingExporter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

type auditMatrixFixture struct {
	rdb       *redis.Client
	chainer   *audit.Chainer
	exporter  *recordingExporter
	publisher *bus.NatsBus
	handled   chan error
}

// newAuditMatrixFixture wires a fresh consumer the way the gateway does for
// AUDIT_TRANSPORT=nats: a NatsBus, the SIEM exporter and the audit chain.
func newAuditMatrixFixture(t *testing.T) *auditMatrixFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	f := &auditMatrixFixture{rdb: rdb, chainer: audit.NewChainer(rdb, audit.ChainKeyPrefix),
		exporter: &recordingExporter{}, handled: make(chan error, 1)}
	ns := startMatrixNATS(t)
	subs := ns.NumSubscriptions()
	consumer, err := audit.NewNATSAuditConsumer(observedAuditBus{matrixNatsBus(t, ns), f.handled}, f.exporter,
		audit.WithChainer(f.chainer))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, consumer.Close()) })
	require.Eventually(t, func() bool { return ns.NumSubscriptions() > subs }, 5*time.Second, 5*time.Millisecond,
		"audit export subscription not registered")
	f.publisher = matrixNatsBus(t, ns)
	return f
}

// observe snapshots the exports and the tenant's chain around send. Accepted
// needs both to change and an Effect either one (PR1-01D's matrixResult rule).
func (f *auditMatrixFixture) observe(t *testing.T, send func()) identity.Result {
	t.Helper()
	exportsBefore, chainBefore := f.exporter.count(), f.chainLength(t)
	send()
	select {
	case err := <-f.handled:
		if err != nil {
			t.Fatalf("audit consumer handler error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("packet not delivered to the audit consumer")
	}
	exportsAfter, chainAfter := f.exporter.count(), f.chainLength(t)
	exported, chained := exportsAfter != exportsBefore, chainAfter != chainBefore
	return identity.Result{
		Accepted: exported && chained, Effect: exported || chained,
		Evidence: fmt.Sprintf("state snapshots (Accepted from the same snapshots); SIEM exports %d -> %d; audit chain length %d -> %d",
			exportsBefore, exportsAfter, chainBefore, chainAfter),
	}
}

// chainLength is XLEN of the tenant's chain stream; a read error fails the test.
func (f *auditMatrixFixture) chainLength(t *testing.T) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := f.rdb.XLen(ctx, f.chainer.StreamKey(matrixTenant)).Result()
	if err != nil {
		t.Fatalf("read audit chain length: %v", err)
	}
	return n
}

// startMatrixNATS is core/infra/bus/handshake_test.go's startTestNATS.
func startMatrixNATS(t *testing.T) *server.Server {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("start test nats: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns
}

func matrixNatsBus(t *testing.T, ns *server.Server) *bus.NatsBus {
	t.Helper()
	b, err := bus.NewNatsBus(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(b.Close)
	return b
}
