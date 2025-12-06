package scheduler

import (
	"context"
	"fmt"
	"time"

	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client pb.SafetyKernelClient
	conn   *grpc.ClientConn
}

// NewSafetyClient dials the safety kernel at addr.
func NewSafetyClient(addr string) (*SafetyClient, error) {
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial safety kernel: %w", err)
	}
	return &SafetyClient{
		client: pb.NewSafetyKernelClient(conn),
		conn:   conn,
	}, nil
}

// Close releases the underlying connection.
func (c *SafetyClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Check forwards the request to the safety kernel; denies on error/timeout.
func (c *SafetyClient) Check(req *pb.JobRequest) (SafetyDecision, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.client.Check(ctx, &pb.PolicyCheckRequest{
		JobId: req.GetJobId(),
		Topic: req.GetTopic(),
	})
	if err != nil {
		return SafetyDeny, fmt.Sprintf("safety kernel error: %v", err)
	}

	switch resp.GetDecision() {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return SafetyAllow, ""
	case pb.DecisionType_DECISION_TYPE_DENY:
		return SafetyDeny, resp.GetReason()
	default:
		return SafetyDeny, "unsupported decision"
	}
}
