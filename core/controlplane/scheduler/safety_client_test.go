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
	record, err := client.Check(&pb.JobRequest{JobId: "1", Topic: "job.default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Decision != SafetyAllow || record.Reason != "" {
		t.Fatalf("expected allow, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientDeny(t *testing.T) {
	conn, cleanup := startTestSafetyServer(pb.DecisionType_DECISION_TYPE_DENY, "blocked")
	defer cleanup()

	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn}
	record, err := client.Check(&pb.JobRequest{JobId: "1", Topic: "sys.destroy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Decision != SafetyDeny || record.Reason != "blocked" {
		t.Fatalf("expected deny, got %v reason=%s", record.Decision, record.Reason)
	}
}

type failingSafetyKernelClient struct{}

func (f failingSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Evaluate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

type allowSafetyKernelClient struct{}

func (a allowSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Evaluate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return &pb.ListSnapshotsResponse{}, nil
}

func TestSafetyClientCircuitOpens(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}}
	req := &pb.JobRequest{JobId: "1", Topic: "job.default"}

	for i := 0; i < safetyCircuitFailBudget; i++ {
		record, _ := client.Check(req)
		if record.Decision != SafetyDeny {
			t.Fatalf("expected deny on failure %d", i)
		}
	}

	record, _ := client.Check(req)
	if record.Decision != SafetyDeny || record.Reason != "safety kernel circuit open" {
		t.Fatalf("expected circuit open deny, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientHalfOpenClosesAfterSuccesses(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}}
	req := &pb.JobRequest{JobId: "1", Topic: "job.default"}

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

	record, _ := client.Check(req)
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", record.Decision)
	}
	// Second success should close the circuit.
	record, _ = client.Check(req)
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", record.Decision)
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
