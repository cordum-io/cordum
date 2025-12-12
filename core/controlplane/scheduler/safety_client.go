package scheduler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client pb.SafetyKernelClient
	conn   *grpc.ClientConn

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const (
	safetyTimeout           = 2 * time.Second
	safetyCircuitOpenFor    = 30 * time.Second
	safetyCircuitFailBudget = 3
)

// NewSafetyClient dials the safety kernel at addr.
func NewSafetyClient(addr string) (*SafetyClient, error) {
	creds := safetyTransportCredentials()
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(creds))
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
	now := time.Now()
	if c.isCircuitOpen(now) {
		return SafetyDeny, "safety kernel circuit open"
	}

	ctx, cancel := context.WithTimeout(context.Background(), safetyTimeout)
	defer cancel()

	tenant := req.GetTenantId()
	if tenant == "" {
		tenant = req.GetEnv()["tenant_id"]
	}
	if tenant == "" {
		tenant = "default"
	}
	resp, err := c.client.Check(ctx, &pb.PolicyCheckRequest{
		JobId:       req.GetJobId(),
		Topic:       req.GetTopic(),
		Tenant:      tenant,
		PrincipalId: req.GetPrincipalId(),
		Priority:    req.GetPriority(),
		Budget:      req.GetBudget(),
		Labels:      req.GetLabels(),
		MemoryId:    req.GetMemoryId(),
	})
	if err != nil {
		c.recordFailure()
		return SafetyDeny, fmt.Sprintf("safety kernel error: %v", err)
	}
	c.recordSuccess()

	switch resp.GetDecision() {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return SafetyAllow, ""
	case pb.DecisionType_DECISION_TYPE_DENY:
		return SafetyDeny, resp.GetReason()
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return SafetyRequireHuman, resp.GetReason()
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return SafetyThrottle, resp.GetReason()
	default:
		return SafetyDeny, "unsupported decision"
	}
}

func (c *SafetyClient) isCircuitOpen(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openUntil.After(now)
}

func (c *SafetyClient) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= safetyCircuitFailBudget {
		c.openUntil = time.Now().Add(safetyCircuitOpenFor)
		c.failures = 0
	}
}

func (c *SafetyClient) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	c.openUntil = time.Time{}
}

func safetyTransportCredentials() credentials.TransportCredentials {
	if caPath := os.Getenv("SAFETY_KERNEL_TLS_CA"); caPath != "" {
		if creds, err := credentials.NewClientTLSFromFile(caPath, ""); err == nil {
			return creds
		}
	}
	if os.Getenv("SAFETY_KERNEL_INSECURE") == "true" {
		return insecure.NewCredentials()
	}
	// Default to insecure to preserve compatibility; operators can set SAFETY_KERNEL_TLS_CA to enable TLS.
	return insecure.NewCredentials()
}
