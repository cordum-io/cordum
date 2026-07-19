//go:build handshakeinterop

package handshakeinterop

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/policysign"
	gnats "github.com/nats-io/nats-server/v2/server"
	"github.com/redis/go-redis/v9"
)

const pinnedNATSServerVersion = "2.12.6"

type interopIdentity struct {
	workerID, agentID, tenantID, keyID, sdkVersion string
	privateKey                                     *ecdsa.PrivateKey
}

type interopServer struct {
	t                                        *testing.T
	redis                                    redis.UniversalClient
	issuer                                   *scheduler.SessionTokenIssuer
	nats                                     *gnats.Server
	natsPort                                 int
	buses                                    []*bus.NatsBus
	subscribers                              []*scheduler.HandshakeSubscriber
	replicas                                 []*countedService
	schedulerKey                             *ecdsa.PrivateKey
	schedulerKeyID                           string
	schedulerID                              string
	identities                               map[string]*interopIdentity
	resolver                                 scheduler.HandshakeTrustResolver
	config                                   *configsvc.Service
	agents                                   *store.AgentIdentityStore
	runID                                    string
	crossReplicaOK                           bool
	closeOnce                                sync.Once
	workerConfig                             []byte
	ownedWorkerConfig                        []byte
	workerConfigTTL                          time.Duration
	workerConfigExists, workerConfigCaptured bool
	ownedWorkerConfigCaptured                bool
}

type cleanupRegistrar interface {
	Cleanup(func())
}

func registerInteropServerCleanup(registrar cleanupRegistrar, server *interopServer) {
	registrar.Cleanup(server.Close)
}

func newInteropServer(t *testing.T, redisURL string) *interopServer {
	t.Helper()
	if gnats.VERSION != pinnedNATSServerVersion {
		t.Fatalf("embedded NATS version=%s want=%s", gnats.VERSION, pinnedNATSServerVersion)
	}
	rdb := connectRealRedis(t, redisURL)
	server := &interopServer{
		t: t, redis: rdb,
		schedulerKeyID: "scheduler-interop-key", schedulerID: "scheduler-interop",
		identities: make(map[string]*interopIdentity), runID: randomServerID(t),
	}
	registerInteropServerCleanup(t, server)
	server.captureWorkerConfigState()
	server.schedulerKey = generateP256(t)
	server.issuer = newInteropIssuer(t, rdb)
	server.startNATS(-1)
	return server
}

func connectRealRedis(t *testing.T, redisURL string) redis.UniversalClient {
	t.Helper()
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse CAP_HANDSHAKE_REDIS_URL: %v", err)
	}
	client := redis.NewClient(options)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("real Redis is unreachable: %v", err)
	}
	return client
}

func newInteropIssuer(t *testing.T, rdb redis.UniversalClient) *scheduler.SessionTokenIssuer {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate token key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("interop-token-key", publicKey); err != nil {
		t.Fatalf("add token key: %v", err)
	}
	issuer, err := scheduler.NewSessionTokenIssuer(privateKey, "interop-token-key", trust, rdb,
		scheduler.SessionTokenIssuerOptions{Lifetime: 5 * time.Minute, Skew: 5 * time.Second})
	if err != nil {
		t.Fatalf("new token issuer: %v", err)
	}
	return issuer
}

func generateP256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return key
}

func (s *interopServer) startNATS(port int) {
	s.t.Helper()
	options := &gnats.Options{Host: "127.0.0.1", Port: port, NoSigs: true, NoLog: true}
	server, err := gnats.NewServer(options)
	if err != nil {
		s.t.Fatalf("new embedded NATS: %v", err)
	}
	server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		server.Shutdown()
		s.t.Fatal("embedded NATS did not become ready")
	}
	s.nats, s.natsPort = server, server.Addr().(*net.TCPAddr).Port
}

func (s *interopServer) natsURL() string { return fmt.Sprintf("nats://127.0.0.1:%d", s.natsPort) }

func (s *interopServer) addIdentity(label string) *interopIdentity {
	s.t.Helper()
	identity := &interopIdentity{
		workerID: "interop-" + label + "-" + s.runID, agentID: "agent-" + label + "-" + s.runID,
		tenantID: "tenant-interop-" + s.runID, keyID: "proof-" + label + "-" + s.runID,
		sdkVersion: "cap-" + label + "/v2", privateKey: generateP256(s.t),
	}
	s.identities[label] = identity
	return identity
}

func randomServerID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random run ID: %v", err)
	}
	return fmt.Sprintf("%x", value)
}

func (s *interopServer) enrollIdentities() {
	s.t.Helper()
	s.config = configsvc.NewFromClient(s.redis)
	credentials := workercredentials.NewService(s.config)
	s.agents = store.NewAgentIdentityStoreFromClient(s.redis)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, identity := range s.identities {
		s.enrollIdentity(ctx, credentials, identity)
		s.captureOwnedWorkerConfigState()
	}
	resolver, err := scheduler.NewCredentialHandshakeTrustResolver(credentials, s.agents)
	if err != nil {
		s.t.Fatalf("new production trust resolver: %v", err)
	}
	s.resolver = resolver
}

func (s *interopServer) enrollIdentity(ctx context.Context, credentials *workercredentials.Service, identity *interopIdentity) {
	s.t.Helper()
	created, err := s.agents.Create(ctx, store.AgentIdentity{
		ID: identity.agentID, TenantID: identity.tenantID, Name: identity.workerID,
		Owner: "handshake-interop", RiskTier: "low", Status: "active",
		AllowedTopics: []string{"job.interop"},
	})
	if err != nil {
		s.t.Fatalf("create agent identity: %v", err)
	}
	if err := s.agents.LinkWorker(ctx, created.ID, identity.workerID); err != nil {
		s.t.Fatalf("link worker identity: %v", err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&identity.privateKey.PublicKey)
	if err != nil {
		s.t.Fatalf("marshal proof key: %v", err)
	}
	issued, err := credentials.Create(ctx, workercredentials.IssueInput{
		TenantID: identity.tenantID, WorkerID: identity.workerID, AgentID: identity.agentID,
		ProofKeyID: identity.keyID, ProofAlgorithm: workercredentials.ProofAlgorithmECDSAP256SHA256,
		ProofPublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKey})),
		AllowedTopics:     []string{"job.interop"}, CreatedBy: "handshake-interop",
	})
	if err != nil {
		s.t.Fatalf("create worker credential: %v", err)
	}
	if issued.Credential.WorkerID != identity.workerID {
		s.t.Fatal("worker credential authority mismatch")
	}
}

func (s *interopServer) startReplica() *countedService {
	s.t.Helper()
	natsBus, err := bus.NewNatsBus(s.natsURL())
	if err != nil {
		s.t.Fatalf("new NatsBus replica: %v", err)
	}
	service, err := scheduler.NewHandshakeService(s.issuer, s.resolver,
		scheduler.NewRedisHandshakeChallengeStore(s.redis), &interopAudit{}, scheduler.HandshakeServiceOptions{
			Audience: scheduler.WorkerHandshakeAudience, SchedulerID: s.schedulerID,
			SchedulerKeyID: s.schedulerKeyID, SchedulerPrivateKey: s.schedulerKey,
			Skew: time.Minute, ChallengeTTL: 30 * time.Second,
		})
	if err != nil {
		natsBus.Close()
		s.t.Fatalf("new handshake service: %v", err)
	}
	counted := &countedService{service: service, called: make(chan struct{}, 1)}
	subscriber, err := scheduler.NewHandshakeSubscriber(natsBus, counted)
	if err != nil {
		natsBus.Close()
		s.t.Fatalf("new subscriber: %v", err)
	}
	if err := subscriber.Start(); err != nil {
		natsBus.Close()
		s.t.Fatalf("start subscriber: %v", err)
	}
	s.buses = append(s.buses, natsBus)
	s.subscribers = append(s.subscribers, subscriber)
	s.replicas = append(s.replicas, counted)
	s.awaitReplicaReady(counted)
	return counted
}

func (s *interopServer) restartNATS() {
	s.t.Helper()
	port := s.natsPort
	s.nats.Shutdown()
	s.nats.WaitForShutdown()
	s.startNATS(port)
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		connected := len(s.buses) == 2
		for _, natsBus := range s.buses {
			connected = connected && natsBus.IsConnected()
		}
		if connected {
			s.awaitActiveReplicasReady()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.t.Fatal("NatsBus replicas did not reconnect")
}

func (s *interopServer) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		for _, subscriber := range s.subscribers {
			_ = subscriber.Close()
		}
		for _, natsBus := range s.buses {
			natsBus.Close()
		}
		if s.nats != nil {
			s.nats.Shutdown()
			s.nats.WaitForShutdown()
		}
		s.cleanupOwnedState()
		if s.redis != nil {
			_ = s.redis.Close()
		}
	})
}
