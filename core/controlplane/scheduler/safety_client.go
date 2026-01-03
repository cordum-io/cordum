package scheduler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client pb.SafetyKernelClient
	conn   *grpc.ClientConn

	mu              sync.Mutex
	state           circuitState
	failures        int
	successes       int
	openUntil       time.Time
	halfOpenAllowed int
}

const (
	safetyTimeout            = 2 * time.Second
	safetyCircuitOpenFor     = 30 * time.Second
	safetyCircuitFailBudget  = 3
	safetyCircuitHalfOpenMax = 3
	safetyCircuitCloseAfter  = 2
)

type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
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
func (c *SafetyClient) Check(req *pb.JobRequest) (SafetyDecisionRecord, error) {
	if c.isCircuitOpen() {
		return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "safety kernel circuit open"}, nil
	}

	if !c.allowHalfOpenRequest() {
		return SafetyDecisionRecord{Decision: SafetyDeny, Reason: "safety kernel circuit half-open (throttled)"}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), safetyTimeout)
	defer cancel()

	checkReq := &pb.PolicyCheckRequest{
		JobId:       req.GetJobId(),
		Topic:       req.GetTopic(),
		Tenant:      ExtractTenant(req),
		PrincipalId: req.GetPrincipalId(),
		Priority:    req.GetPriority(),
		Budget:      req.GetBudget(),
		Labels:      req.GetLabels(),
		MemoryId:    req.GetMemoryId(),
		Meta:        req.GetMeta(),
	}
	if env := req.GetEnv(); env != nil {
		if eff := env[config.EffectiveConfigEnvVar]; eff != "" {
			checkReq.EffectiveConfig = []byte(eff)
		}
	}

	resp, err := c.client.Check(ctx, checkReq)
	if err != nil {
		c.recordFailure()
		return SafetyDecisionRecord{Decision: SafetyDeny, Reason: fmt.Sprintf("safety kernel error: %v", err)}, nil
	}
	c.recordSuccess()

	record := SafetyDecisionRecord{
		Decision:         decisionFromProto(resp.GetDecision()),
		Reason:           resp.GetReason(),
		RuleID:           resp.GetRuleId(),
		PolicySnapshot:   resp.GetPolicySnapshot(),
		Constraints:      resp.GetConstraints(),
		ApprovalRequired: resp.GetApprovalRequired(),
		ApprovalRef:      resp.GetApprovalRef(),
	}
	return record, nil
}

func decisionFromProto(dec pb.DecisionType) SafetyDecision {
	switch dec {
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		return SafetyAllow
	case pb.DecisionType_DECISION_TYPE_DENY:
		return SafetyDeny
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return SafetyRequireApproval
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return SafetyThrottle
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return SafetyAllowWithConstraints
	default:
		return SafetyDeny
	}
}

func (c *SafetyClient) isCircuitOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.state == circuitOpen && c.openUntil.Before(now) {
		c.state = circuitHalfOpen
		c.successes = 0
		c.halfOpenAllowed = safetyCircuitHalfOpenMax
	}
	return c.state == circuitOpen
}

func (c *SafetyClient) allowHalfOpenRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != circuitHalfOpen {
		return true
	}
	if c.halfOpenAllowed > 0 {
		c.halfOpenAllowed--
		return true
	}
	return false
}

func (c *SafetyClient) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.state {
	case circuitClosed:
		c.failures++
		if c.failures >= safetyCircuitFailBudget {
			c.state = circuitOpen
			c.openUntil = time.Now().Add(safetyCircuitOpenFor)
			c.failures = 0
		}
	case circuitHalfOpen:
		c.state = circuitOpen
		c.openUntil = time.Now().Add(safetyCircuitOpenFor)
		c.failures = 0
	}
}

func (c *SafetyClient) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case circuitClosed:
		c.failures = 0
	case circuitHalfOpen:
		c.successes++
		if c.successes >= safetyCircuitCloseAfter {
			c.state = circuitClosed
			c.failures = 0
			c.successes = 0
			c.halfOpenAllowed = 0
		}
	default:
		c.failures = 0
	}
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
