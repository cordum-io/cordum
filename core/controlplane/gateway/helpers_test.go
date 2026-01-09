package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/gorilla/websocket"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/memory"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"google.golang.org/grpc"
)

type stubBus struct {
	mu        sync.Mutex
	published []publishedMessage
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

func (b *stubBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

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

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	redisURL := "redis://" + srv.Addr()
	memStore, err := memory.NewRedisStore(redisURL)
	if err != nil {
		t.Fatalf("mem store: %v", err)
	}
	jobStore, err := memory.NewRedisJobStore(redisURL)
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
	dlqStore, err := memory.NewDLQStore(redisURL)
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
		clients:        make(map[*websocket.Conn]chan *pb.BusPacket),
		eventsCh:       make(chan *pb.BusPacket, 8),
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
