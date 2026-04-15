package scheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
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
	"google.golang.org/grpc/keepalive"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client        pb.SafetyKernelClient
	conn          *grpc.ClientConn
	cb            *RedisCircuitBreaker
	contextClient redis.UniversalClient // for dereferencing context_ptr (input content scanning)
}

const (
	safetyTimeout                     = 2 * time.Second
	inputContentMaxBytes              = 2 * 1024 * 1024 // 2 MiB, same as output
	inputPointerPrefix                = "redis://"
	safetyCircuitOpenFor              = 30 * time.Second
	safetyCircuitFailBudget           = 3
	safetyCircuitHalfOpenMax          = 3
	safetyCircuitCloseAfter           = 2
	envGRPCClientKeepaliveTime        = "CORDUM_GRPC_CLIENT_KEEPALIVE_TIME"
	envGRPCClientKeepaliveTimeout     = "CORDUM_GRPC_CLIENT_KEEPALIVE_TIMEOUT"
	grpcClientKeepaliveTimeDefault    = 30 * time.Second
	grpcClientKeepaliveTimeoutDefault = 10 * time.Second
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
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(grpcClientKeepaliveParams()),
	)
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

func grpcClientKeepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{
		Time:                env.DurationOr(envGRPCClientKeepaliveTime, grpcClientKeepaliveTimeDefault),
		Timeout:             env.DurationOr(envGRPCClientKeepaliveTimeout, grpcClientKeepaliveTimeoutDefault),
		PermitWithoutStream: true,
	}
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

// WithContextClient enables input content loading for pre-execution content scanning.
// The Redis client is used to dereference context_ptr payloads.
func (c *SafetyClient) WithContextClient(rdb redis.UniversalClient) *SafetyClient {
	c.contextClient = rdb
	return c
}

// loadInputContent dereferences context_ptr from Redis.
// Returns nil content on failure — metadata-only check proceeds.
func (c *SafetyClient) loadInputContent(ctx context.Context, contextPtr string) ([]byte, int64, error) {
	contextPtr = strings.TrimSpace(contextPtr)
	if contextPtr == "" || c.contextClient == nil {
		return nil, 0, nil
	}
	key := strings.TrimPrefix(contextPtr, inputPointerPrefix)
	if key == "" {
		return nil, 0, nil
	}
	raw, err := c.contextClient.Get(ctx, key).Bytes()
	if err != nil {
		return nil, 0, err
	}
	originalSize := int64(len(raw))
	if len(raw) > inputContentMaxBytes {
		raw = raw[:inputContentMaxBytes]
	}
	return raw, originalSize, nil
}

// Close releases the underlying connection.
func (c *SafetyClient) Close() error {
	if c.contextClient != nil {
		_ = c.contextClient.Close()
	}
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

	// Dereference context_ptr and attach input content for content-level policy scanning.
	// Failure is non-fatal for metadata-only rules. For scope rules that require
	// structured content, the kernel will enforce on_missing_input behavior.
	if ptr := req.GetContextPtr(); ptr != "" {
		content, originalSize, loadErr := c.loadInputContent(ctx, ptr)
		if loadErr != nil {
			slog.Warn("scheduler: input content load failed — scope rules may deny if content required",
				"component", "scheduler", "job_id", req.GetJobId(), "topic", req.GetTopic(),
				"context_ptr", ptr, "error", loadErr)
		} else if len(content) > 0 {
			checkReq.InputContent = content
			checkReq.InputSizeBytes = originalSize
			if ct := req.GetLabels()["content_type"]; ct != "" {
				checkReq.InputContentType = ct
			}
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
