//go:build capproduction

package capproduction

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/replay"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	gnats "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

const (
	pinnedNATSServerVersion = "2.12.6"
	productionStream        = "CAP_PRODUCTION"
	productionTopic         = "job.production.echo"
)

type productionHarness struct {
	t              *testing.T
	redis          redis.UniversalClient
	server         *gnats.Server
	connection     *nats.Conn
	bus            *bus.NatsBus
	engine         *scheduler.Engine
	registry       *scheduler.MemoryRegistry
	subscriber     *scheduler.HandshakeSubscriber
	issuer         *scheduler.SessionTokenIssuer
	replay         *switchableReplay
	resolveSession scheduler.ProductionSessionResolver
	store          *store.RedisJobStore
	safety         *recordingSafety
	tls            productionTLS
	runID          string
	workerID       string
	agentID        string
	tenantID       string
	workerKey      *ecdsa.PrivateKey
	rotatedKey     *ecdsa.PrivateKey
	schedulerKey   *ecdsa.PrivateKey
	gatewayKey     *ecdsa.PrivateKey
}

func newProductionHarness(t *testing.T) *productionHarness {
	t.Helper()
	redisURL := requiredEnvironment(t, "CAP_PRODUCTION_REDIS_URL")
	h := &productionHarness{t: t, runID: randomHex(t, 6), tls: newProductionTLS(t)}
	h.workerID, h.agentID = "worker-production-"+h.runID, "agent-production-"+h.runID
	h.tenantID = "tenant-production-" + h.runID
	h.workerKey, h.rotatedKey = generateP256(t), generateP256(t)
	h.schedulerKey, h.gatewayKey = generateP256(t), generateP256(t)
	h.redis = connectProductionRedis(t, redisURL)
	h.store = store.NewRedisJobStoreFromClient(h.redis)
	h.startNATS()
	h.configureCordumTLS()
	h.connection = h.connectNATS()
	h.createProductionStream()
	h.issuer = newProductionIssuer(t, h.redis)
	h.startSchedulerBoundary()
	t.Cleanup(h.close)
	return h
}

func (h *productionHarness) startNATS() {
	h.t.Helper()
	if gnats.VERSION != pinnedNATSServerVersion {
		h.t.Fatalf("embedded NATS version=%s want=%s", gnats.VERSION, pinnedNATSServerVersion)
	}
	options := &gnats.Options{
		Host: "127.0.0.1", Port: -1, NoSigs: true, NoLog: true,
		JetStream: true, StoreDir: h.t.TempDir(), TLSConfig: h.tls.server, TLSVerify: true,
	}
	server, err := gnats.NewServer(options)
	if err != nil {
		h.t.Fatalf("new embedded NATS: %v", err)
	}
	server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		server.Shutdown()
		h.t.Fatal("embedded TLS NATS did not become ready")
	}
	h.server = server
}

func (h *productionHarness) configureCordumTLS() {
	h.t.Setenv("CORDUM_ENV", "production")
	h.t.Setenv("CORDUM_PRODUCTION", "true")
	h.t.Setenv("CORDUM_NATS_ALLOW_NOAUTH", "")
	h.t.Setenv("NATS_USE_JETSTREAM", "false")
	h.t.Setenv("NATS_TLS_CA", h.tls.caPath)
	h.t.Setenv("NATS_TLS_CERT", h.tls.clientCert)
	h.t.Setenv("NATS_TLS_KEY", h.tls.clientKey)
	h.t.Setenv("NATS_TLS_SERVER_NAME", "localhost")
}

func (h *productionHarness) natsURL() string {
	return fmt.Sprintf("tls://127.0.0.1:%d", h.server.Addr().(*net.TCPAddr).Port)
}

func (h *productionHarness) connectNATS() *nats.Conn {
	h.t.Helper()
	connection, err := nats.Connect(h.natsURL(), nats.Secure(h.tls.client.Clone()), nats.Timeout(3*time.Second))
	if err != nil {
		h.t.Fatalf("connect authenticated NATS: %v", err)
	}
	return connection
}

func (h *productionHarness) createProductionStream() {
	h.t.Helper()
	js, err := h.connection.JetStream()
	if err != nil {
		h.t.Fatalf("JetStream context: %v", err)
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name: productionStream, Subjects: []string{h.directSubject()}, Storage: nats.MemoryStorage,
	})
	if err != nil {
		h.t.Fatalf("create production stream: %v", err)
	}
}

func (h *productionHarness) startSchedulerBoundary() {
	h.t.Helper()
	credentials := h.enrollWorker()
	service := h.newHandshakeService()
	middleware := h.installTransportBoundary()
	var err error
	h.subscriber, err = scheduler.NewHandshakeSubscriber(h.bus, service)
	if err != nil {
		h.t.Fatalf("start handshake subscriber: %v", err)
	}
	if err := h.subscriber.Start(); err != nil {
		h.t.Fatalf("start handshake subscriber: %v", err)
	}
	h.startEngine(middleware, credentials)
}

func (h *productionHarness) newHandshakeService() *scheduler.HandshakeService {
	h.t.Helper()
	trust := &staticTrustResolver{identity: &scheduler.HandshakeTrustIdentity{
		WorkerID: h.workerID, AgentID: h.agentID, TenantID: h.tenantID,
		ProofKeyID: "worker-key", PublicKey: &h.workerKey.PublicKey,
		AllowedTopics: []string{productionTopic, h.directSubject()},
	}}
	service, err := scheduler.NewHandshakeService(
		h.issuer, trust, scheduler.NewRedisHandshakeChallengeStore(h.redis), productionAudit{},
		scheduler.HandshakeServiceOptions{
			Audience: scheduler.WorkerHandshakeAudience, SchedulerID: "cordum-scheduler",
			SchedulerKeyID: "scheduler-key", SchedulerPrivateKey: h.schedulerKey,
			Skew: 5 * time.Second, ChallengeTTL: 30 * time.Second,
		},
	)
	if err != nil {
		h.t.Fatalf("new handshake service: %v", err)
	}
	return service
}

func (h *productionHarness) installTransportBoundary() *scheduler.SessionTokenMiddleware {
	h.t.Helper()
	target, err := bus.NewNatsBus(h.natsURL())
	if err != nil {
		h.t.Fatalf("new scheduler NATS bus: %v", err)
	}
	if !target.ProductionTransportReady() {
		h.t.Fatal("mutual-TLS NATS did not satisfy authenticated transport readiness")
	}
	h.bus = target
	middleware := scheduler.NewSessionTokenMiddleware(
		h.issuer, scheduler.HandshakeModeEnforce, scheduler.NewHandshakeMissingTracker(),
	)
	resolveSession, err := scheduler.NewProductionSessionResolver(middleware)
	if err != nil {
		h.t.Fatalf("new production session resolver: %v", err)
	}
	h.resolveSession = resolveSession
	sharedReplay := replay.NewRedisReplayStore(h.redis, replay.WithKeyPrefix("cap:e2e:"+h.runID+":"))
	h.replay = &switchableReplay{delegate: sharedReplay}
	boundary := &scheduler.ProductionRawBoundary{ResolveKey: h.resolveProductionKey, Replay: h.replay}
	if err := scheduler.InstallProductionRawAdmission(target, boundary, resolveSession); err != nil {
		h.t.Fatalf("install production admission: %v", err)
	}
	encoder, err := bus.NewProductionPacketEncoder(bus.ProductionPacketEncoderOptions{
		Key: h.schedulerKey, KeyID: "scheduler-key",
	})
	if err != nil {
		h.t.Fatalf("new production encoder: %v", err)
	}
	if err := target.SetPacketEncoder(encoder); err != nil {
		h.t.Fatalf("set production encoder: %v", err)
	}
	target.FreezePacketSecurity()
	return middleware
}

func (h *productionHarness) enrollWorker() *scheduler.WorkerCredentialCache {
	h.t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&h.workerKey.PublicKey)
	if err != nil {
		h.t.Fatalf("marshal worker proof key: %v", err)
	}
	service := workercredentials.NewService(configsvc.NewFromClient(h.redis))
	_, err = service.Create(context.Background(), workercredentials.IssueInput{
		TenantID: h.tenantID, WorkerID: h.workerID, AgentID: h.agentID,
		ProofKeyID: "worker-key", ProofAlgorithm: workercredentials.ProofAlgorithmECDSAP256SHA256,
		ProofPublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
		AllowedPools:      []string{"production"}, AllowedTopics: []string{productionTopic}, CreatedBy: "capproduction",
	})
	if err != nil {
		h.t.Fatalf("enroll production worker: %v", err)
	}
	return scheduler.NewWorkerCredentialCache(service)
}

func (h *productionHarness) startEngine(
	middleware *scheduler.SessionTokenMiddleware, credentials *scheduler.WorkerCredentialCache,
) {
	h.registry = scheduler.NewMemoryRegistry()
	h.registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: h.workerID, Pool: "production", MaxParallelJobs: 1})
	h.registry.UpdateHandshakeTrust(&pb.Handshake{
		ComponentId: h.workerID, ReadyTopics: []string{productionTopic},
	}, true)
	h.safety = &recordingSafety{tenant: h.tenantID}
	h.engine = scheduler.NewEngine(
		h.bus, h.safety, h.registry, directStrategy{workerID: h.workerID}, h.store, nil,
	).WithSessionMiddleware(middleware).
		WithProductionIdentityEnforcement(true).
		WithWorkerCredentialCache(credentials)
	if err := h.engine.Start(); err != nil {
		h.t.Fatalf("start scheduler engine: %v", err)
	}
}

func (h *productionHarness) resolveProductionKey(tenant, sender, keyID string) (*ecdsa.PublicKey, error) {
	switch {
	case tenant == "_system" && sender == "api-gateway" && keyID == "gateway-key":
		return &h.gatewayKey.PublicKey, nil
	case tenant == h.tenantID && sender == h.workerID && keyID == "worker-key":
		return &h.workerKey.PublicKey, nil
	case tenant == h.tenantID && sender == h.workerID && keyID == "worker-key-next":
		return &h.rotatedKey.PublicKey, nil
	default:
		return nil, errors.New("production key unavailable")
	}
}

func (h *productionHarness) directSubject() string { return bus.DirectSubject(h.workerID) }

func (h *productionHarness) close() {
	if h.engine != nil {
		h.engine.Stop()
	}
	if h.subscriber != nil {
		_ = h.subscriber.Close()
	}
	if h.registry != nil {
		h.registry.Close()
	}
	if h.bus != nil {
		h.bus.Close()
	}
	if h.connection != nil {
		h.connection.Close()
	}
	if h.server != nil {
		h.server.Shutdown()
		h.server.WaitForShutdown()
	}
	if h.redis != nil {
		_ = h.redis.Close()
	}
}
