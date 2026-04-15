package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

type stubBus struct {
	mu          sync.Mutex
	published   []publishedMessage
	publishErr  error
	failSubject string
	subs        map[string][]func(*pb.BusPacket) error
	queueGroups map[string][]string // subject -> queue groups used
}

type publishedMessage struct {
	subject string
	packet  *pb.BusPacket
}

func (b *stubBus) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	b.published = append(b.published, publishedMessage{subject: subject, packet: packet})
	err := b.publishErr
	failSubject := b.failSubject
	b.mu.Unlock()
	if err != nil && (failSubject == "" || failSubject == subject) {
		return err
	}
	return nil
}

func (b *stubBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	if handler == nil {
		return nil
	}
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[string][]func(*pb.BusPacket) error{}
	}
	if b.queueGroups == nil {
		b.queueGroups = map[string][]string{}
	}
	b.subs[subject] = append(b.subs[subject], handler)
	b.queueGroups[subject] = append(b.queueGroups[subject], queue)
	b.mu.Unlock()
	return nil
}

func (b *stubBus) IsConnected() bool {
	return true
}

func (b *stubBus) Status() string {
	return "CONNECTED"
}

func (b *stubBus) emit(subject string, packet *pb.BusPacket) {
	b.mu.Lock()
	var handlers []func(*pb.BusPacket) error
	for sub, subs := range b.subs {
		if subjectMatches(sub, subject) {
			handlers = append(handlers, subs...)
		}
	}
	b.mu.Unlock()
	for _, handler := range handlers {
		_ = handler(packet)
	}
}

func subjectMatches(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if strings.HasSuffix(pattern, ">") {
		prefix := strings.TrimSuffix(pattern, ">")
		return strings.HasPrefix(subject, prefix)
	}
	if strings.Contains(pattern, "*") {
		pParts := strings.Split(pattern, ".")
		sParts := strings.Split(subject, ".")
		if len(pParts) != len(sParts) {
			return false
		}
		for i, part := range pParts {
			if part == "*" {
				continue
			}
			if part != sParts[i] {
				return false
			}
		}
		return true
	}
	return false
}

type stubSafetyClient struct {
	mu          sync.Mutex
	snapshots   []string
	resp        *pb.PolicyCheckResponse
	simulateErr error
	evaluateErr error
}

func (c *stubSafetyClient) setSnapshots(snapshots []string) {
	c.mu.Lock()
	c.snapshots = snapshots
	c.mu.Unlock()
}

func (c *stubSafetyClient) setResponse(resp *pb.PolicyCheckResponse) {
	c.mu.Lock()
	c.resp = resp
	c.mu.Unlock()
}

func (c *stubSafetyClient) Check(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return c.response(), nil
}

func (c *stubSafetyClient) Evaluate(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	c.mu.Lock()
	evalErr := c.evaluateErr
	c.mu.Unlock()
	if evalErr != nil {
		return nil, evalErr
	}
	return c.response(), nil
}

func (c *stubSafetyClient) Explain(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return c.response(), nil
}

func (c *stubSafetyClient) Simulate(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	c.mu.Lock()
	simErr := c.simulateErr
	c.mu.Unlock()
	if simErr != nil {
		return nil, simErr
	}
	return c.response(), nil
}

func (c *stubSafetyClient) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest, _ ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	c.mu.Lock()
	out := append([]string{}, c.snapshots...)
	c.mu.Unlock()
	return &pb.ListSnapshotsResponse{Snapshots: out}, nil
}

func (c *stubSafetyClient) response() *pb.PolicyCheckResponse {
	c.mu.Lock()
	resp := c.resp
	c.mu.Unlock()
	if resp != nil {
		return resp
	}
	return &pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:         "ok",
		PolicySnapshot: "snap-test",
	}
}

func newTestGateway(t *testing.T) (*server, *stubBus, *stubSafetyClient) {
	t.Helper()

	// Allow loopback in tests (httptest.NewServer binds to 127.0.0.1).
	prevSkip := skipPrivateIPCheck.Load()
	skipPrivateIPCheck.Store(true)
	t.Cleanup(func() { skipPrivateIPCheck.Store(prevSkip) })

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	redisURL := "redis://" + srv.Addr()
	memStore, err := store.NewRedisStore(redisURL)
	if err != nil {
		t.Fatalf("mem store: %v", err)
	}
	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	workflowStore, err := wf.NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	configSvc, err := configsvc.New(redisURL)
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	schemaRegistry, err := schema.NewRegistry(redisURL)
	if err != nil {
		t.Fatalf("schema registry: %v", err)
	}
	dlqStore, err := store.NewDLQStore(redisURL, 0)
	if err != nil {
		t.Fatalf("dlq store: %v", err)
	}
	artifactStore, err := artifacts.NewRedisStore(redisURL)
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	lockStore, err := locks.NewRedisStore(redisURL)
	if err != nil {
		t.Fatalf("lock store: %v", err)
	}

	bus := &stubBus{}
	safetyClient := &stubSafetyClient{snapshots: []string{"snap-test"}}
	entitlements := licensing.NewEntitlementResolver()
	s := &server{
		memStore:              memStore,
		jobStore:              jobStore,
		bus:                   bus,
		workers:               make(map[string]*pb.Heartbeat),
		workerSeen:            make(map[string]time.Time),
		clients:               make(map[*websocket.Conn]*wsClient),
		eventsCh:              make(chan wsEvent, 8),
		entitlements:          entitlements,
		workflowStore:         workflowStore,
		configSvc:             configSvc,
		topicRegistry:         topicregistry.NewService(configSvc),
		workerCredentialStore: workercredentials.NewService(configSvc),
		dlqStore:              dlqStore,
		artifactStore:         artifactStore,
		lockStore:             lockStore,
		schemaRegistry:        schemaRegistry,
		safetyClient:          safetyClient,
		started:               time.Now().UTC(),
	}

	t.Cleanup(func() {
		_ = memStore.Close()
		_ = jobStore.Close()
		_ = workflowStore.Close()
		_ = configSvc.Close()
		_ = schemaRegistry.Close()
		_ = dlqStore.Close()
		_ = artifactStore.Close()
		_ = lockStore.Close()
	})

	return s, bus, safetyClient
}

func setTestEntitlements(t *testing.T, s *server, plan licensing.Plan, mutate func(*licensing.Entitlements)) {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	setTestLicense(t, s, licensing.Claims{
		Plan:         string(plan),
		Entitlements: &entitlements,
	})
}

func setTestLicense(t *testing.T, s *server, claims licensing.Claims) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal license payload: %v", err)
	}

	licenseBytes, err := json.Marshal(map[string]any{
		"payload":   json.RawMessage(payloadBytes),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(licenseBytes))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	s.entitlements = resolver
}

// failingSafetyClient is a test stub whose Evaluate always returns an error,
// used to exercise the POLICY_CHECK_FAIL_MODE (open/closed) paths.
type failingSafetyClient struct {
	pb.SafetyKernelClient
}

func (c *failingSafetyClient) Evaluate(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("safety kernel unavailable")
}

func (c *failingSafetyClient) Check(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("safety kernel unavailable")
}

func (c *failingSafetyClient) Explain(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("safety kernel unavailable")
}

func (c *failingSafetyClient) Simulate(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, errors.New("safety kernel unavailable")
}

func (c *failingSafetyClient) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest, _ ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return nil, errors.New("safety kernel unavailable")
}
