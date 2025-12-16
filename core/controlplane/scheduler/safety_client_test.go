package scheduler

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type safetyTestServer struct {
	pb.UnimplementedSafetyKernelServer
	decision pb.DecisionType
	reason   string
}

func (s *safetyTestServer) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{
		Decision: s.decision,
		Reason:   s.reason,
	}, nil
}

func startTestSafetyServer(decision pb.DecisionType, reason string) (*grpc.ClientConn, func()) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterSafetyKernelServer(srv, &safetyTestServer{decision: decision, reason: reason})

	go srv.Serve(lis)

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, _ := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	cleanup := func() {
		srv.Stop()
		lis.Close()
		conn.Close()
	}
	return conn, cleanup
}

func TestSafetyClientAllow(t *testing.T) {
	conn, cleanup := startTestSafetyServer(pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()

	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn}
	decision, reason := client.Check(&pb.JobRequest{JobId: "1", Topic: "job.echo"})
	if decision != SafetyAllow || reason != "" {
		t.Fatalf("expected allow, got %v reason=%s", decision, reason)
	}
}

func TestSafetyClientDeny(t *testing.T) {
	conn, cleanup := startTestSafetyServer(pb.DecisionType_DECISION_TYPE_DENY, "blocked")
	defer cleanup()

	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn}
	decision, reason := client.Check(&pb.JobRequest{JobId: "1", Topic: "sys.destroy"})
	if decision != SafetyDeny || reason != "blocked" {
		t.Fatalf("expected deny, got %v reason=%s", decision, reason)
	}
}

type failingSafetyKernelClient struct{}

func (f failingSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

type allowSafetyKernelClient struct{}

func (a allowSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func TestSafetyClientCircuitOpens(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}}
	req := &pb.JobRequest{JobId: "1", Topic: "job.echo"}

	for i := 0; i < safetyCircuitFailBudget; i++ {
		decision, _ := client.Check(req)
		if decision != SafetyDeny {
			t.Fatalf("expected deny on failure %d", i)
		}
	}

	decision, reason := client.Check(req)
	if decision != SafetyDeny || reason != "safety kernel circuit open" {
		t.Fatalf("expected circuit open deny, got %v reason=%s", decision, reason)
	}
}

func TestSafetyClientHalfOpenClosesAfterSuccesses(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}}
	req := &pb.JobRequest{JobId: "1", Topic: "job.echo"}

	// Trip the circuit open.
	for i := 0; i < safetyCircuitFailBudget; i++ {
		client.Check(req)
	}

	// Force transition into half-open state.
	client.mu.Lock()
	client.openUntil = time.Now().Add(-time.Second)
	client.state = circuitOpen
	client.mu.Unlock()

	// Swap client to a successful responder to allow closing.
	client.client = allowSafetyKernelClient{}

	decision, _ := client.Check(req)
	if decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", decision)
	}
	// Second success should close the circuit.
	decision, _ = client.Check(req)
	if decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", decision)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.state != circuitClosed {
		t.Fatalf("expected circuit to close after two successes, state=%v", client.state)
	}
	if client.failures != 0 || client.successes != 0 {
		t.Fatalf("expected counters reset, failures=%d successes=%d", client.failures, client.successes)
	}
}
