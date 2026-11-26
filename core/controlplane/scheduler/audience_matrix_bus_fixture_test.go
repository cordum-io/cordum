package scheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	matrixTenant     = "tenant-matrix"
	matrixWorkerID   = "worker-matrix"
	matrixProofKeyID = "worker-key"
	matrixGatewayKey = "gateway-key"
	matrixPool       = "matrix"
	matrixTopic      = "job.matrix"
	matrixWait       = 5 * time.Second
)

// busMatrixFixture is one scheduler under the production profile: a real
// NatsBus with CAP-PRODUCTION raw admission and the production encoder, an
// enforce-mode session middleware, a durable job store, and an enrolled worker.
type busMatrixFixture struct {
	kit       *matrixKit
	session   string // the worker's bound session from its completed handshake
	jobs      *store.RedisJobStore
	registry  *MemoryRegistry
	bus       *matrixObservedBus
	conn      *nats.Conn
	keys      map[string]*ecdsa.PrivateKey
	admitted  chan bus.RawAdmissionDisposition
	refreshes atomic.Int64
}

func newBusMatrixFixture(t *testing.T, workerReady bool) *busMatrixFixture {
	t.Helper()
	mode := matrixProductionMode(t)
	// Non-production transport: the embedded NATS has no mTLS. Identity and
	// admission run the production path (WithProductionIdentityEnforcement).
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "false")
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	t.Cleanup(cleanup)
	workerKey, gatewayKey := generateProtocolKey(t), generateProtocolKey(t)
	f := &busMatrixFixture{
		kit:      newMatrixKit(t, issuer, rdb, matrixBinding(), &workerKey.PublicKey, []string{matrixPool}, []string{matrixTopic}),
		jobs:     store.NewRedisJobStoreFromClient(rdb),
		registry: NewMemoryRegistryWithTTL(time.Minute),
		keys:     map[string]*ecdsa.PrivateKey{servicetoken.IdentityGateway: gatewayKey, matrixWorkerID: workerKey},
		admitted: make(chan bus.RawAdmissionDisposition, 64),
	}
	t.Cleanup(f.registry.Close)
	f.session = f.kit.token(t, identity.WorkerSession)
	f.bus = f.startBus(t, NewSessionTokenMiddleware(issuer, mode, NewHandshakeMissingTracker()))
	if workerReady {
		f.registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: matrixWorkerID, Pool: matrixPool, MaxParallelJobs: 1})
		f.registry.UpdateHandshakeTrust(&pb.Handshake{ComponentId: matrixWorkerID, ReadyTopics: []string{matrixTopic}}, true)
	}
	cache := NewWorkerCredentialCache(workercredentials.NewService(configsvc.NewFromClient(rdb)))
	list := cache.list
	cache.list = func(ctx context.Context) ([]workercredentials.Credential, error) {
		f.refreshes.Add(1)
		return list(ctx)
	}
	engine := NewEngine(f.bus, NewSafetyBasic(), f.registry, productionDirectStrategy{workerID: matrixWorkerID}, f.jobs, nil).
		WithSessionMiddleware(f.bus.middleware).WithProductionIdentityEnforcement(true).WithWorkerCredentialCache(cache)
	if err := engine.Start(); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(engine.Stop)
	return f
}

func (f *busMatrixFixture) startBus(t *testing.T, middleware *SessionTokenMiddleware) *matrixObservedBus {
	t.Helper()
	ns := startProductionAdmissionNATS(t)
	target, err := bus.NewNatsBus(ns.ClientURL())
	if err != nil {
		t.Fatalf("new NATS bus: %v", err)
	}
	t.Cleanup(target.Close)
	resolver, err := NewProductionSessionResolver(middleware)
	if err != nil {
		t.Fatalf("production session resolver: %v", err)
	}
	hook := NewProductionRawAdmissionHook(&ProductionRawBoundary{ResolveKey: f.resolveKey, Replay: capsdk.NewInMemoryReplayStore()}, resolver)
	if err := target.SetRawPacketAdmission(func(ctx context.Context, subject string, raw []byte) bus.RawAdmissionResult {
		result := hook(ctx, subject, raw)
		select {
		case f.admitted <- result.Disposition:
		default:
		}
		return result
	}); err != nil {
		t.Fatalf("install raw admission: %v", err)
	}
	encoder, err := bus.NewProductionPacketEncoder(bus.ProductionPacketEncoderOptions{Key: generateProtocolKey(t), KeyID: "scheduler-key"})
	if err != nil {
		t.Fatalf("production encoder: %v", err)
	}
	if err := target.SetPacketEncoder(encoder); err != nil {
		t.Fatalf("set production encoder: %v", err)
	}
	target.FreezePacketSecurity()
	if f.conn, err = nats.Connect(ns.ClientURL()); err != nil {
		t.Fatalf("connect raw publisher: %v", err)
	}
	t.Cleanup(f.conn.Close)
	return &matrixObservedBus{NatsBus: target, middleware: middleware, handled: make(chan string, 64)}
}

// resolveKey is the static trust the capproduction harness uses: the gateway's
// key under the reserved tenant and the enrolled worker's proof key.
func (f *busMatrixFixture) resolveKey(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
	switch {
	case tenant == servicetoken.ReservedTenant && sender == servicetoken.IdentityGateway && keyID == matrixGatewayKey:
		return &f.keys[sender].PublicKey, nil
	case tenant == matrixTenant && sender == matrixWorkerID && keyID == matrixProofKeyID:
		return &f.keys[sender].PublicKey, nil
	}
	return nil, errors.New("production key unavailable")
}

func matrixBinding() SessionBinding {
	return SessionBinding{
		WorkerID: matrixWorkerID, AgentID: "agent-matrix", Tenant: matrixTenant,
		Audience: WorkerHandshakeAudience, ProofKeyID: matrixProofKeyID, SDKVersion: "node/1",
	}
}

func matrixJobIdentity() *pb.IdentityBinding {
	return &pb.IdentityBinding{TenantId: matrixTenant, PrincipalId: "principal-matrix", ActorId: "actor-matrix"}
}

// packet is the entry's legitimate packet from sender carrying credential c.
func (f *busMatrixFixture) packet(t *testing.T, c identity.Credential, sender string) *pb.BusPacket {
	t.Helper()
	token := f.session
	if c != identity.WorkerSession {
		token = f.kit.token(t, c)
	}
	return &pb.BusPacket{
		TraceId: "trace-matrix", SenderId: f.kit.sender(c, sender), ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt: timestamppb.Now(), AuthToken: token, Identity: matrixJobIdentity(),
	}
}

// present signs packet with its legitimate sender's key, publishes the exact
// wire bytes and returns the raw admission disposition, waiting for the
// scheduler handler to finish when the packet was delivered.
func (f *busMatrixFixture) present(t *testing.T, subject, signer string, packet *pb.BusPacket) bus.RawAdmissionDisposition {
	t.Helper()
	keyID := matrixProofKeyID
	if signer == servicetoken.IdentityGateway {
		keyID = matrixGatewayKey
	}
	messageID := make([]byte, 16)
	if _, err := rand.Read(messageID); err != nil {
		t.Fatalf("message id: %v", err)
	}
	packet.SignatureMetadata = &pb.SignatureMetadata{
		ProfileVersion: capsdk.ProductionProfileVersion, Algorithm: capsdk.ProductionAlgorithm, MessageId: messageID,
		Audience: subject, ExpiresAt: timestamppb.New(time.Now().Add(time.Minute)), KeyId: keyID,
	}
	wire, err := capsdk.SignProductionPacket(packet, f.keys[signer])
	if err != nil {
		t.Fatalf("sign %s packet: %v", subject, err)
	}
	if err := f.conn.Publish(subject, wire); err != nil {
		t.Fatalf("publish %s: %v", subject, err)
	}
	disposition := awaitMatrix(t, f.admitted, subject+" admission")
	if disposition == bus.RawAdmissionAccepted {
		for awaitMatrix(t, f.bus.handled, subject+" handler") != subject {
		}
	}
	return disposition
}

func awaitMatrix[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(matrixWait):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// dispatchedJob stores a job running on the enrolled worker, the way the
// durable dispatch leaves it, and returns its dispatch fence.
func (f *busMatrixFixture) dispatchedJob(t *testing.T, jobID string) *pb.DispatchIdentity {
	t.Helper()
	ctx := context.Background()
	request := &pb.JobRequest{
		JobId: jobID, Topic: matrixTopic, TenantId: matrixTenant, PrincipalId: "principal-matrix",
		Identity: matrixJobIdentity(), Meta: &pb.JobMetadata{TenantId: matrixTenant, ActorId: "actor-matrix"},
	}
	if err := f.jobs.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if err := f.jobs.SetTopic(ctx, jobID, matrixTopic); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	dispatchID, attempt, err := f.jobs.BeginDispatch(ctx, jobID, matrixWorkerID, matrixTenant)
	if err != nil {
		t.Fatalf("begin dispatch: %v", err)
	}
	if err := f.jobs.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	if err := f.jobs.SetJobRequest(ctx, request); err != nil {
		t.Fatalf("set job request: %v", err)
	}
	if attempt != 1 {
		t.Fatalf("begin dispatch attempt = %d, want 1", attempt)
	}
	return &pb.DispatchIdentity{DispatchId: dispatchID, Attempt: 1, AssignedWorkerId: matrixWorkerID}
}

// matrixObservedBus is the production NatsBus with each handler's completion
// and each successful outbound publish recorded.
type matrixObservedBus struct {
	*bus.NatsBus
	middleware *SessionTokenMiddleware
	handled    chan string
	mu         sync.Mutex
	published  map[string]int
}

func (b *matrixObservedBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	return b.NatsBus.Subscribe(subject, queue, func(packet *pb.BusPacket) error {
		defer b.done(subject)
		return handler(packet)
	})
}

func (b *matrixObservedBus) SubscribeWithContext(subject, queue string, handler func(context.Context, *pb.BusPacket) error) error {
	return b.NatsBus.SubscribeWithContext(subject, queue, func(ctx context.Context, packet *pb.BusPacket) error {
		defer b.done(subject)
		return handler(ctx, packet)
	})
}

func (b *matrixObservedBus) Publish(subject string, packet *pb.BusPacket) error {
	return b.PublishWithContext(context.Background(), subject, packet)
}

func (b *matrixObservedBus) PublishWithContext(ctx context.Context, subject string, packet *pb.BusPacket) error {
	if err := b.NatsBus.PublishWithContext(ctx, subject, packet); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.published == nil {
		b.published = map[string]int{}
	}
	b.published[subject]++
	return nil
}

func (b *matrixObservedBus) publishes(subject string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return fmt.Sprintf("%d publishes on %s", b.published[subject], subject)
}

func (b *matrixObservedBus) done(subject string) {
	select {
	case b.handled <- subject:
	default:
	}
}
