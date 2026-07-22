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
	"github.com/cordum/cordum/core/infra/resourceio"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// SafetyClient implements SafetyChecker by calling the SafetyKernel gRPC service.
type SafetyClient struct {
	client                        pb.SafetyKernelClient
	conn                          *grpc.ClientConn
	cb                            *RedisCircuitBreaker
	contextClient                 redis.UniversalClient // for dereferencing context_ptr (input content scanning)
	resourceReader                resourceio.Reader
	productionIdentityEnforcement bool
}

const (
	safetyTimeout                     = 2 * time.Second
	inputContentMaxBytes              = 2 * 1024 * 1024 // 2 MiB, same as output
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
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
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

// WithProductionIdentityEnforcement makes authenticated IdentityBinding
// mandatory at the Safety Kernel boundary. Compatibility mode leaves additive,
// potentially partial identity mirrors untouched.
func (c *SafetyClient) WithProductionIdentityEnforcement(enabled bool) *SafetyClient {
	c.productionIdentityEnforcement = enabled
	return c
}

// CurrentPolicySnapshot returns the latest policy snapshot hash from the
// safety kernel. Returns empty string on error or if the kernel is unreachable.
// Implements SnapshotProvider for the reconciler's stale-approval detection.
func (c *SafetyClient) CurrentPolicySnapshot(ctx context.Context) string {
	if c.cb.IsOpen(ctx) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, safetyTimeout)
	defer cancel()
	resp, err := c.client.ListSnapshots(ctx, &pb.ListSnapshotsRequest{})
	if err != nil || resp == nil || len(resp.Snapshots) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Snapshots[0])
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
	normalized, err := normalizeSafetyJobRequest(req, c.productionIdentityEnforcement)
	if err != nil {
		return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "job identity rejected"}, err
	}
	req = normalized
	if c.cb.IsOpen(ctx) {
		return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "safety kernel circuit open"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, safetyTimeout)
	defer cancel()

	checkReq, err := normalizeSafetyPolicyRequest(req, &pb.PolicyCheckRequest{
		JobId:       req.GetJobId(),
		Topic:       req.GetTopic(),
		Tenant:      ExtractTenant(req),
		PrincipalId: req.GetPrincipalId(),
		Priority:    req.GetPriority(),
		Budget:      req.GetBudget(),
		Labels:      req.GetLabels(),
		MemoryId:    req.GetMemoryId(),
		Meta:        req.GetMeta(),
	}, c.productionIdentityEnforcement)
	if err != nil {
		return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "policy identity rejected"}, err
	}
	if env := req.GetEnv(); env != nil {
		if eff := env[config.EffectiveConfigEnvVar]; eff != "" {
			checkReq.EffectiveConfig = []byte(eff)
		}
	}

	if req.GetContextRef() != nil || strings.TrimSpace(req.GetContextPtr()) != "" {
		if err := c.attachInputContent(ctx, req, checkReq); err != nil {
			slog.Warn("scheduler: input resource rejected",
				"component", "scheduler", "job_id", req.GetJobId(), "topic", req.GetTopic(),
				"error", err)
			return SafetyDecisionRecord{Decision: SafetyUnavailable, Reason: "input resource rejected"}, nil
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

func normalizeSafetyJobRequest(req *pb.JobRequest, enforce bool) (*pb.JobRequest, error) {
	if !enforce {
		return req, nil
	}
	normalized, err := jobidentity.NormalizeProductionJobRequest(req, req.GetIdentity())
	if err != nil {
		return nil, fmt.Errorf("normalize safety job identity: %w", err)
	}
	return normalized, nil
}

func normalizeSafetyPolicyRequest(
	job *pb.JobRequest,
	check *pb.PolicyCheckRequest,
	enforce bool,
) (*pb.PolicyCheckRequest, error) {
	if !enforce {
		return check, nil
	}
	normalized, err := jobidentity.NormalizeProductionPolicyCheckRequest(check, job.GetIdentity())
	if err != nil {
		return nil, fmt.Errorf("normalize safety policy identity: %w", err)
	}
	return normalized, nil
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
