package scheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client pb.SafetyKernelClient
	conn   *grpc.ClientConn
	cb     *RedisCircuitBreaker
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
	creds, err := safetyTransportCredentials()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("dial safety kernel: %w", err)
	}
	return &SafetyClient{
		client: pb.NewSafetyKernelClient(conn),
		conn:   conn,
		cb: NewRedisCircuitBreaker(nil, "cordum:cb:safety", CircuitBreakerOpts{
			FailThreshold: safetyCircuitFailBudget,
			OpenDuration:  safetyCircuitOpenFor,
			HalfOpenMax:   safetyCircuitHalfOpenMax,
			CloseAfter:    safetyCircuitCloseAfter,
		}),
	}, nil
}

// WithRedis enables the distributed circuit breaker backed by Redis.
// Without this, the circuit breaker operates locally per-replica.
func (c *SafetyClient) WithRedis(rdb redis.UniversalClient) *SafetyClient {
	if rdb != nil {
		c.cb = NewRedisCircuitBreaker(rdb, "cordum:cb:safety", CircuitBreakerOpts{
			FailThreshold: safetyCircuitFailBudget,
			OpenDuration:  safetyCircuitOpenFor,
			HalfOpenMax:   safetyCircuitHalfOpenMax,
			CloseAfter:    safetyCircuitCloseAfter,
		})
	}
	return c
}

// Close releases the underlying connection.
func (c *SafetyClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Check forwards the request to the safety kernel; denies on error/timeout.
func (c *SafetyClient) Check(ctx context.Context, req *pb.JobRequest) (SafetyDecisionRecord, error) {
	if c.cb.IsOpen(ctx) {
		return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "safety kernel circuit open"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, safetyTimeout)
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
		c.cb.RecordFailure(ctx)
		return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: fmt.Sprintf("safety kernel error: %v", err)}, nil
	}
	c.cb.RecordSuccess(ctx)

	record := SafetyDecisionRecord{
		Decision:         decisionFromProto(resp.GetDecision()),
		Reason:           resp.GetReason(),
		RuleID:           resp.GetRuleId(),
		PolicySnapshot:   resp.GetPolicySnapshot(),
		Constraints:      resp.GetConstraints(),
		ApprovalRequired: resp.GetApprovalRequired(),
		ApprovalRef:      resp.GetApprovalRef(),
		Remediations:     resp.GetRemediations(),
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

func safetyTransportCredentials() (credentials.TransportCredentials, error) {
	caPath := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_CA"))
	requireTLS := env.IsProduction() || env.Bool("SAFETY_KERNEL_TLS_REQUIRED")
	insecureAllowed := env.Bool("SAFETY_KERNEL_INSECURE")

	if caPath == "" {
		if requireTLS {
			return nil, fmt.Errorf("safety_kernel_tls_ca required")
		}
		if insecureAllowed || !env.IsProduction() {
			return insecure.NewCredentials(), nil
		}
		return nil, fmt.Errorf("safety kernel tls required")
	}

	pool := x509.NewCertPool()
	pem, err := os.ReadFile(caPath) // #nosec -- CA path is configured by the operator.
	if err != nil {
		return nil, fmt.Errorf("safety kernel tls ca read: %w", err)
	}
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		return nil, fmt.Errorf("safety kernel tls ca parse: %s", caPath)
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	return credentials.NewTLS(cfg), nil
}
