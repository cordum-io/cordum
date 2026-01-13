package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
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

func startTestSafetyServer(t *testing.T, decision pb.DecisionType, reason string) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterSafetyKernelServer(srv, &safetyTestServer{decision: decision, reason: reason})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial safety kernel: %v", err)
	}
	cleanup := func() {
		srv.Stop()
		_ = lis.Close()
		_ = conn.Close()
		if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatalf("safety kernel serve error: %v", err)
		}
	}
	return conn, cleanup
}

func TestSafetyClientAllow(t *testing.T) {
	conn, cleanup := startTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
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
	conn, cleanup := startTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_DENY, "blocked")
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
		record, err := client.Check(req)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}
		if record.Decision != SafetyDeny {
			t.Fatalf("expected deny on failure %d", i)
		}
	}

	record, err := client.Check(req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if record.Decision != SafetyDeny || record.Reason != "safety kernel circuit open" {
		t.Fatalf("expected circuit open deny, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientHalfOpenClosesAfterSuccesses(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}}
	req := &pb.JobRequest{JobId: "1", Topic: "job.default"}

	// Trip the circuit open.
	for i := 0; i < safetyCircuitFailBudget; i++ {
		if _, err := client.Check(req); err != nil {
			t.Fatalf("check failed: %v", err)
		}
	}

	// Force transition into half-open state.
	client.mu.Lock()
	client.openUntil = time.Now().Add(-time.Second)
	client.state = circuitOpen
	client.mu.Unlock()

	// Swap client to a successful responder to allow closing.
	client.client = allowSafetyKernelClient{}

	record, err := client.Check(req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", record.Decision)
	}
	// Second success should close the circuit.
	record, err = client.Check(req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
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

func TestDecisionFromProto(t *testing.T) {
	cases := map[pb.DecisionType]SafetyDecision{
		pb.DecisionType_DECISION_TYPE_ALLOW:                  SafetyAllow,
		pb.DecisionType_DECISION_TYPE_DENY:                   SafetyDeny,
		pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:          SafetyRequireApproval,
		pb.DecisionType_DECISION_TYPE_THROTTLE:               SafetyThrottle,
		pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS: SafetyAllowWithConstraints,
		pb.DecisionType_DECISION_TYPE_UNSPECIFIED:            SafetyDeny,
	}
	for dec, expect := range cases {
		if got := decisionFromProto(dec); got != expect {
			t.Fatalf("decision %v expected %v got %v", dec, expect, got)
		}
	}
}

func TestSafetyTransportCredentialsInsecure(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	t.Setenv("SAFETY_KERNEL_INSECURE", "true")
	creds := safetyTransportCredentials()
	if creds == nil {
		t.Fatalf("expected credentials")
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("expected insecure protocol, got %s", creds.Info().SecurityProtocol)
	}
}

func TestSafetyTransportCredentialsFallback(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "/nonexistent")
	t.Setenv("SAFETY_KERNEL_INSECURE", "")
	creds := safetyTransportCredentials()
	if creds == nil {
		t.Fatalf("expected credentials")
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("expected insecure fallback, got %s", creds.Info().SecurityProtocol)
	}
}
