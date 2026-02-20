package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

type stubBus struct {
	mu          sync.Mutex
	published   []publishedMessage
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
	b.mu.Unlock()
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
	mu        sync.Mutex
	snapshots []string
	resp      *pb.PolicyCheckResponse
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
	return c.response(), nil
}

func (c *stubSafetyClient) Explain(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return c.response(), nil
}

func (c *stubSafetyClient) Simulate(ctx context.Context, req *pb.PolicyCheckRequest, _ ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
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
	skipPrivateIPCheck.Store(true)

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
	s := &server{
		memStore:       memStore,
		jobStore:       jobStore,
		bus:            bus,
		workers:        make(map[string]*pb.Heartbeat),
		workerSeen:     make(map[string]time.Time),
		clients:        make(map[*websocket.Conn]*wsClient),
		eventsCh:       make(chan wsEvent, 8),
		workflowStore:  workflowStore,
		configSvc:      configSvc,
		dlqStore:       dlqStore,
		artifactStore:  artifactStore,
		lockStore:      lockStore,
		schemaRegistry: schemaRegistry,
		safetyClient:   safetyClient,
		started:        time.Now().UTC(),
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
