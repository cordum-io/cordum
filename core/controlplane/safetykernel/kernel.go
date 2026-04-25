package safetykernel

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	infraHealth "github.com/cordum/cordum/core/infra/health"
	cordumotel "github.com/cordum/cordum/core/infra/otel"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tlsreload"
	"github.com/cordum/cordum/core/licensing"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
)

type server struct {
	pb.UnimplementedSafetyKernelServer
	pb.UnimplementedOutputPolicyServiceServer
	mu                sync.RWMutex
	policy            *config.SafetyPolicy
	outputRules       []compiledOutputRule
	inputRules        []compiledInputRule
	scanners          map[string]OutputScanner
	snapshot          string
	snapshots         []string
	resultClient      redis.UniversalClient
	velocityChecker   *velocityChecker
	policyVersion     atomic.Uint64
	cacheMu           sync.Mutex
	cacheTTL          time.Duration
	cache             map[string]cacheEntry
	cacheMaxSize      int
	entitlements      *licensing.EntitlementResolver
	customBundleCount int
	shadowEvaluator   *ShadowEvaluator

	// Agent identity store for enriching policy evaluation with agent context.
	agentStore    *store.AgentIdentityStore
	agentCacheMu  sync.Mutex
	agentCache    map[string]agentCacheEntry
	agentCacheTTL time.Duration

	// Server-side risk tag derivation registry. When a deriver is registered
	// for a topic, it replaces client-supplied risk_tags with authoritative
	// tags derived from the job content. Prevents risk tag spoofing.
	tagDeriverRegistry *TagDeriverRegistry
}

const (
	defaultPolicyConfigID           = "policy"
	defaultPolicyConfigKey          = "bundles"
	envDecisionCacheTTL             = "SAFETY_DECISION_CACHE_TTL"
	envDecisionCacheMaxSize         = "SAFETY_DECISION_CACHE_MAX_SIZE"
	envPolicyMaxBytes               = "SAFETY_POLICY_MAX_BYTES"
	defaultPolicyMaxBytes           = 2 * 1024 * 1024
	defaultDecisionCacheMaxSize     = 10000
	snapshotHistoryKey              = "cordum:safety:snapshots"
	snapshotHistoryMax              = 10
	customPolicyBundlePrefix        = "secops/"
	envGRPCServerKeepaliveTime      = "CORDUM_GRPC_SERVER_KEEPALIVE_TIME"
	envGRPCServerKeepaliveTimeout   = "CORDUM_GRPC_SERVER_KEEPALIVE_TIMEOUT"
	envGRPCServerMaxConnectionAge   = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE"
	envGRPCServerMaxConnectionGrace = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE_GRACE"
	envGRPCServerEnforcementMinTime = "CORDUM_GRPC_SERVER_ENFORCEMENT_MIN_TIME"
)

type cacheEntry struct {
	resp          *pb.PolicyCheckResponse
	expires       time.Time
	policyVersion uint64
}

type agentCacheEntry struct {
	identity *store.AgentIdentity
	expires  time.Time
}

func (s *server) SetShadowEvaluator(eval *ShadowEvaluator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shadowEvaluator = eval
}

const defaultAgentCacheTTL = 30 * time.Second

// policyLookupIP allows tests to override DNS resolution for policy URL validation.
var policyLookupIP = net.LookupIP

// policyEvalTestHook is called inside the evaluate recover closure before policy.Evaluate.
// It is nil in production; tests may set it to inject panics for fail-closed verification.
var policyEvalTestHook func()

var defaultDecisionTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_safety_default_decision_total",
	Help: "Total policy evaluations that fell through to the default decision",
}, []string{"decision"})

func init() {
	prometheus.MustRegister(defaultDecisionTotal)
}

type configChangeBus interface {
	ReplaceSubscription(prev *nats.Subscription, subject, queue string, handler func(*pb.BusPacket) error) (*nats.Subscription, error)
	AddReconnectHandler(handler func(*nats.Conn))
	AddDisconnectHandler(handler func(*nats.Conn, error))
}

func registerConfigChangeNotifications(natsBus configChangeBus, notifyCh chan struct{}) {
	if natsBus == nil || notifyCh == nil {
		return
	}

	callback := func(_ *pb.BusPacket) (err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("safety-kernel: config subscription panic",
					"panic", r,
					"stack", string(debug.Stack()),
				)
				err = nil
			}
		}()
		select {
		case notifyCh <- struct{}{}:
		default:
		}
		return nil
	}

	var (
		subMu     sync.Mutex
		configSub *nats.Subscription
	)
	subscribe := func() error {
		subMu.Lock()
		defer subMu.Unlock()
		sub, err := natsBus.ReplaceSubscription(configSub, capsdk.SubjectConfigChanged, "", callback)
		if err != nil {
			return err
		}
		configSub = sub
		return nil
	}

	natsBus.AddDisconnectHandler(func(_ *nats.Conn, err error) {
		slog.Error("safety-kernel: NATS disconnected, falling back to poll", "err", err)
	})
	natsBus.AddReconnectHandler(func(_ *nats.Conn) {
		slog.Error("safety-kernel: NATS reconnected, re-subscribing", "subject", capsdk.SubjectConfigChanged)
		if err := subscribe(); err != nil {
			slog.Error("safety-kernel: failed to re-subscribe to config change notifications", "subject", capsdk.SubjectConfigChanged, "err", err)
			return
		}
		slog.Info("safety-kernel: re-subscribed to config change notifications", "subject", capsdk.SubjectConfigChanged)
	})

	if err := subscribe(); err != nil {
		slog.Warn("safety-kernel: failed to subscribe to config change notifications, relying on poll", "err", err)
		return
	}
	slog.Info("safety-kernel: subscribed to config change notifications", "subject", capsdk.SubjectConfigChanged)
}

// Run starts the Safety Kernel gRPC server and blocks until it exits.
func Run(cfg *config.Config) error {
	return RunWithEntitlements(cfg, nil)
}

// RunWithEntitlements starts the Safety Kernel with an optional shared
// entitlement resolver. Nil falls back to community defaults.
func RunWithEntitlements(cfg *config.Config, resolver *licensing.EntitlementResolver) error {
	if cfg == nil {
		cfg = config.Load()
	}

	if _, err := cordumotel.InitTracer("cordum-safety-kernel"); err != nil {
		slog.Error("otel tracer init failed", "error", err)
	}
	defer func() {
		if err := cordumotel.Shutdown(context.Background()); err != nil {
			slog.Error("otel tracer shutdown failed", "error", err)
		}
	}()

	policySource := policySourceFromEnv(cfg.SafetyPolicyPath)
	loader := newPolicyLoader(cfg, policySource, resolver)
	defer loader.Close()
	policy, snapshot, customBundleCount, err := loader.Load(context.Background())
	if err != nil {
		return fmt.Errorf("load safety policy: %w", err)
	}

	var natsBus *bus.NatsBus
	natsBus, err = bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		slog.Warn("safety-kernel: NATS connection failed, relying on poll", "err", err)
	} else {
		defer natsBus.Close()
	}

	notifyCh := make(chan struct{}, 1)
	if natsBus != nil {
		registerConfigChangeNotifications(natsBus, notifyCh)
	}

	lis, err := net.Listen("tcp", cfg.SafetyKernelAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.SafetyKernelAddr, err)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	cert := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_CERT"))
	key := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_TLS_KEY"))
	if cert != "" || key != "" {
		if cert == "" || key == "" {
			return fmt.Errorf("safety kernel tls requires both SAFETY_KERNEL_TLS_CERT and SAFETY_KERNEL_TLS_KEY")
		}
		reloader, err := tlsreload.NewCertReloader(cert, key, "safety-kernel")
		if err != nil {
			return fmt.Errorf("safety kernel tls keypair: %w", err)
		}
		go reloader.WatchLoop(context.Background(), 30*time.Second)
		tlsCfg := &tls.Config{
			GetCertificate: reloader.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			tlsCfg.MinVersion = tls.VersionTLS13
		}
		serverCreds = grpc.Creds(credentials.NewTLS(tlsCfg))
	}
	if env.IsProduction() && cert == "" {
		return fmt.Errorf("safety kernel tls required in production")
	}

	cacheMax := resolveDecisionCacheMax()
	resultClient, err := redisutil.NewClient(cfg.RedisURL)
	if err != nil {
		slog.Warn("safety-kernel: output result redis client disabled", "err", err)
	}
	var agentStore *store.AgentIdentityStore
	if resultClient != nil {
		agentStore = store.NewAgentIdentityStoreFromClient(resultClient)
	}
	tagRegistry := NewTagDeriverRegistry()
	registerBuiltinTagDerivers(tagRegistry)
	// Load deriver registrations from topic registry (pack-installed derivers).
	// These take precedence over built-in registrations.
	if loader.configSvc != nil {
		if entries, err := loadTopicDeriverEntries(context.Background(), loader.configSvc); err != nil {
			slog.Warn("safety-kernel: failed to load tag derivers from topic registry", "err", err)
		} else if n := loadTagDeriversFromTopics(tagRegistry, entries); n > 0 {
			slog.Info("safety-kernel: loaded tag derivers from topic registry", "count", n)
		}
	}

	srv := &server{
		cacheTTL:           parseDurationEnv(envDecisionCacheTTL),
		cache:              map[string]cacheEntry{},
		cacheMaxSize:       cacheMax,
		scanners:           loadOutputScanners(),
		resultClient:       resultClient,
		velocityChecker:    newVelocityChecker(resultClient),
		entitlements:       resolver,
		agentStore:         agentStore,
		agentCacheTTL:      defaultAgentCacheTTL,
		tagDeriverRegistry: tagRegistry,
	}

	// Phase-2 shadow dual-evaluation: constructs the shadow loader +
	// evaluator and attaches them to srv via SetShadowEvaluator so
	// evaluate() can Submit active decisions without a nil-panic. When
	// configsvc or NATS is unavailable setupShadowEvaluation returns
	// (nil, nil) and the kernel still boots with shadow eval as a no-op
	// — see shadow_eval.go for the fallback chain.
	shadowLoader, shadowEvaluator := setupShadowEvaluation(srv, loader, natsBus)
	if shadowEvaluator != nil {
		defer shadowEvaluator.Close()
	}
	if shadowLoader != nil {
		defer shadowLoader.Close()
	}

	// Lifecycle context for background goroutines — cancelled when Run returns.
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	defer lifecycleCancel()

	if err := srv.setPolicyWithBundleCount(lifecycleCtx, policy, snapshot, customBundleCount); err != nil {
		return fmt.Errorf("initial policy load: %w", err)
	}

	var wg sync.WaitGroup
	if loader.ShouldWatch() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.watchPolicy(lifecycleCtx, loader, notifyCh)
		}()
	}

	grpcServer := grpc.NewServer(
		serverCreds,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge:      env.DurationOr(envGRPCServerMaxConnectionAge, 2*time.Hour),
			MaxConnectionAgeGrace: env.DurationOr(envGRPCServerMaxConnectionGrace, 30*time.Second),
			Time:                  env.DurationOr(envGRPCServerKeepaliveTime, 30*time.Second),
			Timeout:               env.DurationOr(envGRPCServerKeepaliveTimeout, 10*time.Second),
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             env.DurationOr(envGRPCServerEnforcementMinTime, 15*time.Second),
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterSafetyKernelServer(grpcServer, srv)
	pb.RegisterOutputPolicyServiceServer(grpcServer, srv)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if env.Bool(env.EnvGRPCReflection) {
		reflection.Register(grpcServer)
	}

	// Admin HTTP server for health probes (Docker/K8s).
	adminAddr := strings.TrimSpace(os.Getenv("SAFETY_KERNEL_ADMIN_ADDR"))
	if adminAddr == "" {
		adminAddr = ":9095"
	}
	skProbes := infraHealth.New()
	skProbes.RegisterReadiness("redis", func(ctx context.Context) error {
		if srv.resultClient == nil {
			return fmt.Errorf("not initialized")
		}
		return srv.resultClient.Ping(ctx).Err()
	})
	adminMux := http.NewServeMux()
	skProbes.Register(adminMux)
	adminMux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		slog.Info("safety-kernel: admin server started", "addr", adminAddr)
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("safety-kernel: admin server error", "error", err)
		}
	}()
	skProbes.SetStartupComplete()

	slog.Info("safety-kernel: listening", "addr", cfg.SafetyKernelAddr)

	// Graceful shutdown: on SIGINT/SIGTERM, drain in-flight RPCs then stop.
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	go func() {
		<-sigCtx.Done()
		slog.Info("safety-kernel: shutting down gracefully")

		const shutdownTimeout = 15 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		grpcDone := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(grpcDone)
		}()
		select {
		case <-grpcDone:
			slog.Info("safety-kernel: gRPC server drained")
		case <-shutdownCtx.Done():
			slog.Warn("safety-kernel: gRPC graceful stop timed out")
		}

		// Flush buffered OTLP spans BEFORE any forced gRPC stop. In the
		// graceful-drain case all spans are already ended; we just empty
		// the BatchSpanProcessor queue. In the timeout case we flush
		// completed spans that are still queued so they aren't lost when
		// we forcibly tear down request handlers below. No-op when
		// CORDUM_OTEL_ENDPOINT is unset. Bounded by the existing shutdown
		// deadline so it can't block past the 15s drain budget.
		if err := shutdownTracing(shutdownCtx); err != nil {
			slog.Warn("safety-kernel: tracer shutdown returned error", "error", err)
		}

		// If graceful drain didn't complete in time, force the server
		// down now. Spans started by handlers killed here won't be
		// recorded -- that is the intentional cost of the timeout path,
		// not an oversight of the flush ordering above.
		select {
		case <-grpcDone:
			// Already drained -- nothing to force.
		default:
			slog.Warn("safety-kernel: forcing gRPC stop after tracer flush")
			grpcServer.Stop()
		}
	}()

	serveErr := grpcServer.Serve(lis)
	lifecycleCancel()
	wg.Wait()
	if serveErr != nil {
		return fmt.Errorf("grpc serve: %w", serveErr)
	}
	return nil
}

func (s *server) currentEntitlements() licensing.Entitlements {
	if s != nil && s.entitlements != nil {
		return s.entitlements.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func (s *server) resolvedPlan() licensing.Plan {
	if s != nil && s.entitlements != nil {
		return s.entitlements.ResolvedPlan()
	}
	return licensing.PlanCommunity
}

func (s *server) velocityRuleLimit() int64 {
	if entitlements := s.currentEntitlements(); entitlements.Limits != nil {
		for _, key := range []string{"velocity_rule_count", "velocity_rules"} {
			if limit, ok := entitlements.Limits[key]; ok {
				return limit
			}
		}
	}
	switch s.resolvedPlan() {
	case licensing.PlanEnterprise:
		return licensing.Unlimited
	case licensing.PlanTeam:
		return 20
	default:
		return 3
	}
}

func effectiveVelocityRuleCount(policy *config.SafetyPolicy, limit int64) int {
	if policy == nil || limit == 0 {
		return 0
	}
	count := 0
	for _, rule := range policy.EffectiveRules() {
		if rule.Velocity == nil {
			continue
		}
		count++
		if limit != licensing.Unlimited && int64(count) >= limit {
			break
		}
	}
	return count
}

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "check")
}

func (s *server) Evaluate(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "evaluate")
}

func (s *server) Explain(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "explain")
}

func (s *server) Simulate(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	return s.evaluate(ctx, req, "simulate")
}

func (s *server) ListSnapshots(ctx context.Context, _ *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	// Prefer Redis for cross-replica consistency.
	if s.resultClient != nil {
		rCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		vals, err := s.resultClient.LRange(rCtx, snapshotHistoryKey, 0, -1).Result()
		cancel()
		if err == nil && len(vals) > 0 {
			return &pb.ListSnapshotsResponse{Snapshots: vals}, nil
		}
	}
	// Fallback to local slice if Redis unavailable or empty.
	s.mu.RLock()
	snapshots := append([]string{}, s.snapshots...)
	s.mu.RUnlock()
	return &pb.ListSnapshotsResponse{Snapshots: snapshots}, nil
}

func (s *server) evaluate(ctx context.Context, req *pb.PolicyCheckRequest, method string) (*pb.PolicyCheckResponse, error) {
	decision := pb.DecisionType_DECISION_TYPE_DENY
	reason := ""

	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(req.GetTenant())
	meta := req.GetMeta()
	if tenant == "" && meta != nil {
		tenant = strings.TrimSpace(meta.GetTenantId())
	}

	// Snapshot all policy-related pointers under a single RLock to prevent
	// TOCTOU races with concurrent setPolicy() calls. The RLock is read-only
	// so concurrent evaluations still run in parallel.
	s.mu.RLock()
	policy := s.policy
	snapshot := s.snapshot
	inputRules := s.inputRules
	scanners := s.scanners
	shadowEvaluator := s.shadowEvaluator
	defaultTenant := ""
	if policy != nil {
		defaultTenant = strings.TrimSpace(policy.DefaultTenant)
	}
	s.mu.RUnlock()

	// Bypass decision cache when the active policy has effective velocity rules.
	// Velocity decisions depend on sliding-window state that changes with every
	// request, so caching any result (even a fallthrough ALLOW) would prevent
	// the window from advancing correctly.
	policyHasVelocity := effectiveVelocityRuleCount(policy, s.velocityRuleLimit()) > 0
	cacheKey := ""
	if s.cacheTTL > 0 && !policyHasVelocity {
		cacheKey = cacheKeyForRequest(req, snapshot)
		if cacheKey != "" {
			if cached := s.getCachedDecision(cacheKey); cached != nil {
				out := clonePolicyResponse(cached)
				if out.GetApprovalRequired() {
					out.ApprovalRef = req.GetJobId()
				}
				out.PolicySnapshot = snapshot
				return out, nil
			}
		}
	}

	// Fail-closed: when no policy is loaded, deny all requests.
	// This prevents a misconfigured deployment from silently allowing everything.
	if policy == nil {
		return &pb.PolicyCheckResponse{
			Decision:       pb.DecisionType_DECISION_TYPE_DENY,
			Reason:         "no policy loaded — fail-closed",
			PolicySnapshot: snapshot,
		}, nil
	}

	if tenant == "" {
		tenant = defaultTenant
	}
	if tenant == "" {
		tenant = "default"
	}

	if topic == "" {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "missing topic"}, nil
	}
	if !strings.HasPrefix(topic, "job.") {
		return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "unsupported topic"}, nil
	}

	input := config.PolicyInput{
		Tenant:     tenant,
		Topic:      topic,
		Labels:     req.GetLabels(),
		Meta:       policyMetaFromRequest(req),
		MCP:        extractMCPRequest(req.GetLabels()),
		Delegation: delegationContextFromRequest(req),
	}

	// Server-side risk tag derivation: when a deriver is registered for this
	// topic, replace client-supplied risk tags with authoritative tags derived
	// from the job content. This prevents risk tag spoofing attacks where a
	// client submits high-risk content with low-risk tags to bypass policy.
	if s.tagDeriverRegistry != nil {
		if derivedTags, ok := s.tagDeriverRegistry.Derive(topic, req.GetLabels(), req.GetInputContent()); ok {
			clientTags := input.Meta.RiskTags
			input.Meta.RiskTags = derivedTags
			// Also override the protobuf meta so input rule evaluation
			// (which reads from meta.GetRiskTags()) uses derived tags.
			if meta != nil {
				meta.RiskTags = derivedTags
			}
			if !tagsEqual(clientTags, derivedTags) {
				slog.Warn("risk tags overridden by server-side deriver",
					"component", "safety",
					"topic", topic,
					"job_id", req.GetJobId(),
					"client_tags", clientTags,
					"derived_tags", derivedTags,
				)
			}
		}
	}

	input.SecretsPresent = secretsPresent(input.Meta, req.GetLabels())
	s.enrichAgentContext(ctx, req.GetLabels(), &input)

	evalTracer := cordumotel.Tracer("cordum-safety-kernel")
	_, evalSpan := evalTracer.Start(ctx, "safety.evaluate",
		oteltrace.WithSpanKind(oteltrace.SpanKindServer),
	)
	defer evalSpan.End()
	evalSpan.SetAttributes(
		attribute.String("cordum.topic", topic),
		attribute.String("cordum.tenant", tenant),
		attribute.String("cordum.job_id", req.GetJobId()),
	)
	if input.Meta.AgentID != "" {
		evalSpan.SetAttributes(attribute.String("cordum.agent_id", input.Meta.AgentID))
	}

	slog.Debug("policy evaluation starting", "component", "safety", "tenant", tenant, "topic", topic, "jobId", req.GetJobId())
	var policyDecision config.PolicyDecision
	evalStart := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("safety-kernel: CRITICAL policy evaluation panic", "panic", r, "stack", string(debug.Stack()))
				policyDecision = config.PolicyDecision{
					Decision: "deny",
					Reason:   fmt.Sprintf("policy evaluation panic: %v", r),
				}
			}
		}()
		if policyEvalTestHook != nil {
			policyEvalTestHook()
		}
		if policyHasVelocity {
			policyDecision = s.evaluateRulesWithVelocity(ctx, policy, input, req.GetJobId(), method)
		} else {
			policyDecision = policy.Evaluate(input)
		}
		if tp, ok := policy.Tenants[tenant]; ok {
			if ok, mcpReason := config.MCPAllowed(tp.MCP, input.MCP); !ok {
				policyDecision.Decision = "deny"
				policyDecision.Reason = mcpReason
			}
		}
	}()
	slog.Debug("policy evaluation complete", "component", "safety", "tenant", tenant, "topic", topic, "decision", policyDecision.Decision, "ruleId", policyDecision.RuleID, "duration", time.Since(evalStart).String())
	evalSpan.SetAttributes(
		attribute.String("cordum.safety_decision", policyDecision.Decision),
		attribute.String("cordum.safety_rule_id", policyDecision.RuleID),
		attribute.String("cordum.safety_rule_name", policyDecision.RuleID),
		attribute.String("cordum.safety_reason", policyDecision.Reason),
	)
	if strings.HasPrefix(policyDecision.Reason, "no matching rule") {
		defaultDecisionTotal.WithLabelValues(policyDecision.Decision).Inc()
	}

	constraints := toProtoConstraints(policyDecision.Constraints)
	switch policyDecision.Decision {
	case "deny":
		decision = pb.DecisionType_DECISION_TYPE_DENY
		reason = policyDecision.Reason
	case "require_approval":
		decision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
		reason = policyDecision.Reason
	case "throttle":
		decision = pb.DecisionType_DECISION_TYPE_THROTTLE
		reason = policyDecision.Reason
	case "allow_with_constraints":
		decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
	case "allow":
		decision = pb.DecisionType_DECISION_TYPE_ALLOW
		if constraints != nil {
			decision = pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS
		}
	}

	// Effective config can further restrict allowed topics or MCP access.
	if eff, ok := config.ParseEffectiveSafety(req.GetEffectiveConfig()); ok {
		if matchAny(eff.DeniedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic '%s' denied by effective config", topic)
		}
		if len(eff.AllowedTopics) > 0 && !matchAny(eff.AllowedTopics, topic) {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = fmt.Sprintf("topic '%s' not allowed by effective config", topic)
		}
		if ok, mcpReason := config.MCPAllowed(eff.MCP, input.MCP); !ok {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = mcpReason
		}
	}

	// Input rule evaluation — runs scanners/patterns/structured scope checks
	// against job input payload.
	//
	// Important: evaluate even when InputContent is empty. Some rules (especially
	// structured scope rules with on_missing_input=deny) must fail closed when the
	// scheduler cannot provide the content, and pure metadata rules do not require
	// content at all.
	//
	// Input rules can only escalate (allow→deny or allow→require_approval), never downgrade.
	ruleID := policyDecision.RuleID
	if len(inputRules) > 0 {
		if decision == pb.DecisionType_DECISION_TYPE_ALLOW || decision == pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS {
			// Mirror the output-policy tracing: wrap input rule evaluation
			// in an opt-in span so production deployments with
			// CORDUM_OTEL_ENDPOINT set get full input-side telemetry. The
			// helper is a no-op when the endpoint is unset.
			_, finishInput := evaluationSpan(ctx, "input", req.GetPrincipalId(), topic, tenant)
			inputDecision := "allow"
			matchedCount := 0

			inputContent := req.GetInputContent()
			// Fall back to _content.prompt label when InputContent is not set.
			// The gateway injects this label for submit-time policy checks.
			if len(inputContent) == 0 {
				if prompt, ok := req.GetLabels()["_content.prompt"]; ok && prompt != "" {
					inputContent = []byte(prompt)
				}
			}
			evalReq := inputEvaluateRequest{
				tenant:      tenant,
				topic:       topic,
				contentType: req.GetInputContentType(),
				content:     inputContent,
				inputSize:   req.GetInputSizeBytes(),
			}
			if meta != nil {
				evalReq.capabilities = append(evalReq.capabilities, meta.GetCapability())
				evalReq.riskTags = append(evalReq.riskTags, meta.GetRiskTags()...)
			}
			for _, rule := range inputRules {
				matched, findings := evaluateInputRule(rule, evalReq, scanners)
				if !matched {
					continue
				}
				matchedCount++
				switch rule.decision {
				case "deny":
					decision = pb.DecisionType_DECISION_TYPE_DENY
					reason = inputRuleReason(rule, findings)
					ruleID = rule.id
					inputDecision = "deny"
				case "require_approval", "require-approval", "require_human":
					decision = pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
					reason = inputRuleReason(rule, findings)
					ruleID = rule.id
					inputDecision = "require_human"
				}
				slog.Info("input rule matched", "component", "safety", "rule", rule.id, "decision", rule.decision, "findings", len(findings))
				break // first matching input rule wins
			}
			finishInput(inputDecision, matchedCount)
		}
	}

	approvalRequired := policyDecision.ApprovalRequired || decision == pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN
	approvalRef := ""
	if approvalRequired {
		approvalRef = req.GetJobId()
	}

	resp := &pb.PolicyCheckResponse{
		Decision:         decision,
		Reason:           reason,
		PolicySnapshot:   snapshot,
		RuleId:           ruleID,
		Constraints:      constraints,
		ApprovalRequired: approvalRequired,
		ApprovalRef:      approvalRef,
		Remediations:     toProtoRemediations(policyDecision.Remediations),
	}
	if shadowEvaluator != nil {
		shadowEvaluator.Submit(
			config.PolicyDecision{
				Decision:         shadowDecisionName(decision, approvalRequired),
				Reason:           reason,
				RuleID:           ruleID,
				Constraints:      policyDecision.Constraints,
				Remediations:     policyDecision.Remediations,
				ApprovalRequired: approvalRequired,
			},
			input,
			tenant,
			req.GetJobId(),
		)
	}

	slog.Info("policy evaluation result", "component", "safety", "tenant", tenant, "topic", topic, "jobId", req.GetJobId(), "decision", resp.Decision.String(), "ruleId", resp.RuleId)

	if cacheKey != "" && s.cacheTTL > 0 {
		cacheResp := clonePolicyResponse(resp)
		cacheResp.ApprovalRef = ""
		s.setCachedDecision(cacheKey, cacheResp)
	}

	return resp, nil
}

func shadowDecisionName(decision pb.DecisionType, approvalRequired bool) string {
	switch decision {
	case pb.DecisionType_DECISION_TYPE_DENY:
		return "deny"
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return "require_approval"
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return "throttle"
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return "allow_with_constraints"
	case pb.DecisionType_DECISION_TYPE_ALLOW:
		if approvalRequired {
			return "require_approval"
		}
		return "allow"
	default:
		if approvalRequired {
			return "require_approval"
		}
		return "deny"
	}
}

func cacheKeyForRequest(req *pb.PolicyCheckRequest, snapshot string) string {
	if req == nil {
		return ""
	}
	clone, ok := proto.Clone(req).(*pb.PolicyCheckRequest)
	if !ok || clone == nil {
		return ""
	}
	clone.JobId = ""
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(clone)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return snapshot + ":" + hex.EncodeToString(sum[:])
}

func (s *server) getCachedDecision(key string) *pb.PolicyCheckResponse {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache == nil {
		return nil
	}
	entry, ok := s.cache[key]
	if !ok {
		return nil
	}
	// Read version inside cacheMu to prevent TOCTOU: setPolicyWithBundleCount
	// bumps the atomic version before clearing cache under this same lock, so
	// any read here always reflects the latest version.
	if entry.policyVersion != s.policyVersion.Load() {
		delete(s.cache, key)
		return nil
	}
	if time.Now().After(entry.expires) {
		delete(s.cache, key)
		return nil
	}
	return clonePolicyResponse(entry.resp)
}

func (s *server) setCachedDecision(key string, resp *pb.PolicyCheckResponse) {
	if key == "" || resp == nil || s.cacheTTL <= 0 {
		return
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache == nil {
		s.cache = map[string]cacheEntry{}
	}
	if s.cacheMaxSize > 0 && len(s.cache) >= s.cacheMaxSize {
		now := time.Now()
		// Sweep expired entries first.
		for k, entry := range s.cache {
			if now.After(entry.expires) {
				delete(s.cache, k)
			}
		}
		// If still at capacity, evict the entry closest to expiry (oldest).
		for len(s.cache) >= s.cacheMaxSize {
			var oldestKey string
			var oldestExp time.Time
			for k, entry := range s.cache {
				if oldestKey == "" || entry.expires.Before(oldestExp) {
					oldestKey = k
					oldestExp = entry.expires
				}
			}
			if oldestKey == "" {
				break
			}
			delete(s.cache, oldestKey)
		}
	}
	s.cache[key] = cacheEntry{
		resp:          resp,
		expires:       time.Now().Add(s.cacheTTL),
		policyVersion: s.policyVersion.Load(),
	}
}

func clonePolicyResponse(resp *pb.PolicyCheckResponse) *pb.PolicyCheckResponse {
	if resp == nil {
		return nil
	}
	clone, ok := proto.Clone(resp).(*pb.PolicyCheckResponse)
	if !ok || clone == nil {
		return resp
	}
	return clone
}

func parseDurationEnv(key string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}

func parseIntEnv(key string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}
	return val
}

// resolveDecisionCacheMax reads envDecisionCacheMaxSize and enforces the
// non-positive guard. A non-positive override (zero, negative, or a parse
// fallback that landed below 1) is treated as operator error and falls back
// to defaultDecisionCacheMaxSize with a WARN — silently honoring cacheMax==0
// would disable the cache entirely and push every request to the policy
// evaluator, and cacheMax<0 is a programmer typo that must never reach
// runtime.
func resolveDecisionCacheMax() int {
	cacheMax := parseIntEnv(envDecisionCacheMaxSize, defaultDecisionCacheMaxSize)
	if cacheMax <= 0 {
		slog.Warn("safety-kernel: ignoring non-positive decision cache size override",
			"env", envDecisionCacheMaxSize,
			"override", cacheMax,
			"default", defaultDecisionCacheMaxSize,
		)
		return defaultDecisionCacheMaxSize
	}
	return cacheMax
}

func toProtoRemediations(remediations []config.PolicyRemediation) []*pb.PolicyRemediation {
	if len(remediations) == 0 {
		return nil
	}
	out := make([]*pb.PolicyRemediation, 0, len(remediations))
	for _, rem := range remediations {
		r := rem
		out = append(out, &pb.PolicyRemediation{
			Id:                    r.ID,
			Title:                 r.Title,
			Summary:               r.Summary,
			ReplacementTopic:      r.ReplacementTopic,
			ReplacementCapability: r.ReplacementCapability,
			AddLabels:             r.AddLabels,
			RemoveLabels:          append([]string{}, r.RemoveLabels...),
		})
	}
	return out
}

// enrichAgentContext looks up agent identity from labels and populates
// policy meta with agent context for policy evaluation. Uses a TTL cache
// to avoid per-evaluation Redis lookups.
func (s *server) enrichAgentContext(ctx context.Context, labels map[string]string, input *config.PolicyInput) {
	if s.agentStore == nil || len(labels) == 0 {
		return
	}
	agentID := strings.TrimSpace(labels["agent_id"])
	if agentID == "" {
		return
	}
	input.Meta.AgentID = agentID

	identity := s.getAgentFromCache(agentID)
	if identity == nil {
		var err error
		identity, err = s.agentStore.Get(ctx, agentID)
		if err != nil {
			slog.Warn("safety-kernel: agent identity lookup failed", "agent_id", agentID, "error", err)
			return
		}
		if identity == nil {
			return
		}
		s.putAgentInCache(agentID, identity)
	}

	input.Meta.AgentRiskTier = identity.RiskTier
	input.Meta.AgentDataClassifications = identity.DataClassifications
	input.Meta.AgentName = identity.Name
	input.Meta.AgentTeam = identity.Team
}

func (s *server) getAgentFromCache(agentID string) *store.AgentIdentity {
	s.agentCacheMu.Lock()
	defer s.agentCacheMu.Unlock()
	if s.agentCache == nil {
		return nil
	}
	entry, ok := s.agentCache[agentID]
	if !ok || time.Now().After(entry.expires) {
		delete(s.agentCache, agentID)
		return nil
	}
	return entry.identity
}

func (s *server) putAgentInCache(agentID string, identity *store.AgentIdentity) {
	s.agentCacheMu.Lock()
	defer s.agentCacheMu.Unlock()
	if s.agentCache == nil {
		s.agentCache = make(map[string]agentCacheEntry)
	}
	ttl := s.agentCacheTTL
	if ttl == 0 {
		ttl = defaultAgentCacheTTL
	}
	s.agentCache[agentID] = agentCacheEntry{
		identity: identity,
		expires:  time.Now().Add(ttl),
	}
}

func policyMetaFromRequest(req *pb.PolicyCheckRequest) config.PolicyMeta {
	meta := req.GetMeta()
	out := config.PolicyMeta{}
	if meta == nil {
		if req.GetPrincipalId() != "" {
			out.ActorID = req.GetPrincipalId()
		}
		return out
	}
	out.ActorID = meta.GetActorId()
	out.ActorType = actorTypeString(meta.GetActorType())
	out.IdempotencyKey = meta.GetIdempotencyKey()
	out.Capability = meta.GetCapability()
	out.RiskTags = append(out.RiskTags, meta.GetRiskTags()...)
	out.Requires = append(out.Requires, meta.GetRequires()...)
	out.PackID = meta.GetPackId()
	if out.ActorID == "" {
		out.ActorID = req.GetPrincipalId()
	}
	return out
}

func tagsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func actorTypeString(val pb.ActorType) string {
	switch val {
	case pb.ActorType_ACTOR_TYPE_HUMAN:
		return "human"
	case pb.ActorType_ACTOR_TYPE_SERVICE:
		return "service"
	default:
		return ""
	}
}

func secretsPresent(meta config.PolicyMeta, labels map[string]string) bool {
	if labels != nil {
		if v := strings.TrimSpace(labels["secrets_present"]); v != "" {
			return v == "true" || v == "1" || strings.EqualFold(v, "yes")
		}
	}
	for _, tag := range meta.RiskTags {
		if strings.EqualFold(tag, "secrets") {
			return true
		}
	}
	return false
}

func extractMCPRequest(labels map[string]string) config.MCPRequest {
	if len(labels) == 0 {
		return config.MCPRequest{}
	}
	return config.MCPRequest{
		Server:   pickLabel(labels, "mcp.server", "mcp_server", "mcpServer"),
		Tool:     pickLabel(labels, "mcp.tool", "mcp_tool", "mcpTool"),
		Resource: pickLabel(labels, "mcp.resource", "mcp_resource", "mcpResource"),
		Action:   strings.ToLower(pickLabel(labels, "mcp.action", "mcp_action", "mcpAction")),
	}
}

func pickLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if val, ok := labels[key]; ok && strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func toProtoConstraints(c config.PolicyConstraints) *pb.PolicyConstraints {
	if isConstraintsEmpty(c) {
		return nil
	}
	out := &pb.PolicyConstraints{
		Budgets: &pb.BudgetConstraints{
			MaxRuntimeMs:      c.Budgets.MaxRuntimeMs,
			MaxRetries:        c.Budgets.MaxRetries,
			MaxArtifactBytes:  c.Budgets.MaxArtifactBytes,
			MaxConcurrentJobs: c.Budgets.MaxConcurrentJobs,
		},
		Sandbox: &pb.SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: c.Sandbox.NetworkAllowlist,
			FsReadOnly:       c.Sandbox.FsReadOnly,
			FsReadWrite:      c.Sandbox.FsReadWrite,
		},
		Toolchain: &pb.ToolchainConstraints{
			AllowedTools:    c.Toolchain.AllowedTools,
			AllowedCommands: c.Toolchain.AllowedCommands,
		},
		Diff: &pb.DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: c.Diff.DenyPathGlobs,
		},
		RedactionLevel: c.RedactionLevel,
	}
	return out
}

func isConstraintsEmpty(c config.PolicyConstraints) bool {
	return c.Budgets.MaxRuntimeMs == 0 && c.Budgets.MaxRetries == 0 && c.Budgets.MaxArtifactBytes == 0 && c.Budgets.MaxConcurrentJobs == 0 &&
		!c.Sandbox.Isolated && len(c.Sandbox.NetworkAllowlist) == 0 && len(c.Sandbox.FsReadOnly) == 0 && len(c.Sandbox.FsReadWrite) == 0 &&
		len(c.Toolchain.AllowedTools) == 0 && len(c.Toolchain.AllowedCommands) == 0 &&
		c.Diff.MaxFiles == 0 && c.Diff.MaxLines == 0 && len(c.Diff.DenyPathGlobs) == 0 &&
		strings.TrimSpace(c.RedactionLevel) == ""
}

func matchAny(patterns []string, value string) bool {
	if value == "" {
		return false
	}
	for _, pat := range patterns {
		if configMatch(pat, value) {
			return true
		}
	}
	return false
}

func configMatch(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := pathMatch(pattern, value)
	return ok
}

func pathMatch(pattern, value string) (bool, error) {
	return pathMatchImpl(pattern, value)
}

// path.Match is small; wrap to keep helpers testable.
func pathMatchImpl(pattern, value string) (bool, error) {
	return path.Match(pattern, value)
}

func (s *server) watchPolicy(ctx context.Context, loader *policyLoader, notifyCh <-chan struct{}) {
	interval := 30 * time.Second
	if raw := os.Getenv("SAFETY_POLICY_RELOAD_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			interval = d
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reload := func(trigger string) {
		if trigger != "poll" {
			slog.Info("safety-kernel: policy reload triggered", "trigger", trigger)
		}

		policy, snapshot, customBundleCount, err := loader.Load(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("safety-kernel: policy reload failed", "err", err, "trigger", trigger)
			return
		}
		s.mu.RLock()
		current := s.snapshot
		s.mu.RUnlock()
		if snapshot != "" && snapshot != current {
			if err := s.setPolicyWithBundleCount(ctx, policy, snapshot, customBundleCount); err != nil {
				slog.Error("safety-kernel: setPolicyWithBundleCount failed", "err", err, "trigger", trigger)
				return
			}
			slog.Info("safety-kernel: policy snapshot updated", "snapshot", snapshot, "trigger", trigger)
		}

		// Reload tag derivers from topic registry. Pack installs update the
		// topic registry and publish a config change notification, so this
		// picks up newly installed pack derivers without a kernel restart.
		if loader.configSvc != nil && s.tagDeriverRegistry != nil {
			if entries, err := loadTopicDeriverEntries(ctx, loader.configSvc); err != nil {
				slog.Warn("safety-kernel: tag deriver reload failed", "err", err, "trigger", trigger)
			} else if n := loadTagDeriversFromTopics(s.tagDeriverRegistry, entries); n > 0 {
				slog.Info("safety-kernel: tag derivers reloaded from topic registry", "count", n, "trigger", trigger)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reload("poll")
		case <-notifyCh:
			reload("notification")
		}
	}
}

func (s *server) setPolicy(ctx context.Context, policy *config.SafetyPolicy, snapshot string) error {
	return s.setPolicyWithBundleCount(ctx, policy, snapshot, 0)
}

// setPolicyWithBundleCount atomically swaps the active policy, trims the
// snapshot history and persists the new snapshot to Redis for cross-replica
// consistency. Callers MUST pass a non-nil ctx — the Redis persistence call
// derives its deadline from the caller so lock-contention paths (policy
// reload, graceful shutdown) cannot orphan a hung Redis write behind a
// detached context.Background(). Tests in this package construct a ctx via
// context.Background() or t.Context() at the call site.
func (s *server) setPolicyWithBundleCount(ctx context.Context, policy *config.SafetyPolicy, snapshot string, customBundleCount int) error {
	if ctx == nil {
		return fmt.Errorf("safety-kernel: setPolicyWithBundleCount: nil context")
	}
	newVersion := s.policyVersion.Add(1)

	s.mu.Lock()
	s.policy = policy
	s.outputRules = compileOutputRules(policy)
	s.inputRules = compileInputRules(policy)
	s.snapshot = snapshot
	s.customBundleCount = customBundleCount
	if snapshot != "" {
		s.snapshots = append([]string{snapshot}, s.snapshots...)
		if len(s.snapshots) > snapshotHistoryMax {
			s.snapshots = s.snapshots[:snapshotHistoryMax]
		}
	}
	s.mu.Unlock()

	// Persist snapshot to Redis for cross-replica consistency. The deadline
	// is derived from the caller's ctx — if the caller (watchPolicy reload
	// or Run() startup) is cancelled, this write unblocks promptly rather
	// than orphaning a Redis round-trip behind context.Background().
	if snapshot != "" && s.resultClient != nil {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := s.resultClient.LPush(rctx, snapshotHistoryKey, snapshot).Err(); err != nil {
			slog.Warn("safety-kernel: snapshot redis LPUSH failed", "err", err)
		} else if err := s.resultClient.LTrim(rctx, snapshotHistoryKey, 0, snapshotHistoryMax-1).Err(); err != nil {
			slog.Warn("safety-kernel: snapshot redis LTRIM failed", "err", err)
		}
	}

	// Clear decision cache — all entries were created under a previous policy version.
	s.cacheMu.Lock()
	s.cache = map[string]cacheEntry{}
	s.cacheMu.Unlock()

	slog.Info("safety-kernel: policy updated, cache invalidated", "version", newVersion)
	return nil
}

type policyLoader struct {
	source       string
	configSvc    *configsvc.Service
	configScope  configsvc.Scope
	configID     string
	configKey    string
	entitlements *licensing.EntitlementResolver
}

func newPolicyLoader(cfg *config.Config, source string, resolver *licensing.EntitlementResolver) *policyLoader {
	loader := &policyLoader{source: source, entitlements: resolver}
	if strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_DISABLE")) != "" {
		return loader
	}
	scope := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_SCOPE"))
	if scope == "" {
		scope = string(configsvc.ScopeSystem)
	}
	id := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_ID"))
	if id == "" {
		id = defaultPolicyConfigID
	}
	key := strings.TrimSpace(os.Getenv("SAFETY_POLICY_CONFIG_KEY"))
	if key == "" {
		key = defaultPolicyConfigKey
	}
	loader.configScope = configsvc.Scope(scope)
	loader.configID = id
	loader.configKey = key
	if cfg == nil {
		return loader
	}
	svc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		slog.Warn("safety-kernel: config service disabled", "err", err)
		return loader
	}
	loader.configSvc = svc
	return loader
}

func (l *policyLoader) Close() {
	if l == nil || l.configSvc == nil {
		return
	}
	_ = l.configSvc.Close()
}

func (l *policyLoader) ShouldWatch() bool {
	if l == nil {
		return false
	}
	return l.source != "" || l.configSvc != nil
}

func (l *policyLoader) currentEntitlements() licensing.Entitlements {
	if l != nil && l.entitlements != nil {
		return l.entitlements.Entitlements()
	}
	return licensing.DefaultEntitlements(licensing.PlanCommunity)
}

func (l *policyLoader) resolvedPlan() licensing.Plan {
	if l != nil && l.entitlements != nil {
		return l.entitlements.ResolvedPlan()
	}
	return licensing.PlanCommunity
}

func (l *policyLoader) policyBundleLimit() int64 {
	entitlements := l.currentEntitlements()
	if limit := entitlements.MaxPolicyBundles; limit != 0 {
		return limit
	}
	if entitlements.Limits != nil {
		if limit, ok := entitlements.Limits["max_policy_bundles"]; ok {
			return limit
		}
	}
	if l.resolvedPlan() == licensing.PlanCommunity {
		return 0
	}
	return licensing.Unlimited
}

func (l *policyLoader) Load(ctx context.Context) (*config.SafetyPolicy, string, int, error) {
	basePolicy, baseSnapshot, err := loadPolicyBundle(l.source)
	if err != nil {
		return nil, "", 0, err
	}
	fragmentPolicy, fragmentSnapshot, customBundleCount, err := l.loadFragments(ctx)
	if err != nil {
		return nil, "", 0, err
	}
	merged := mergePolicies(basePolicy, fragmentPolicy)
	return merged, combineSnapshots(baseSnapshot, fragmentSnapshot), customBundleCount, nil
}

func (l *policyLoader) loadFragments(ctx context.Context) (*config.SafetyPolicy, string, int, error) {
	if l == nil || l.configSvc == nil {
		return nil, "", 0, nil
	}
	doc, err := l.configSvc.Get(ctx, l.configScope, l.configID)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, "", 0, nil
		}
		return nil, "", 0, err
	}
	if doc.Data == nil {
		return nil, "", 0, nil
	}
	rawBundles, ok := doc.Data[l.configKey].(map[string]any)
	if !ok || len(rawBundles) == 0 {
		return nil, "", 0, nil
	}
	keys := make([]string, 0, len(rawBundles))
	for key := range rawBundles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	var merged *config.SafetyPolicy
	var skippedCount int
	customBundleCount := 0
	bundleLimit := l.policyBundleLimit()
	verifier := newBundleVerifier()
	for _, key := range keys {
		content, ok := extractPolicyFragment(rawBundles[key])
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		isCustomBundle := strings.HasPrefix(key, customPolicyBundlePrefix)
		if isCustomBundle && bundleLimit != licensing.Unlimited {
			projected := int64(customBundleCount + 1)
			if bundleLimit == 0 || projected > bundleLimit {
				slog.Warn("safety-kernel: custom policy bundle skipped by tier limit",
					"bundle_id", key,
					"allowed", bundleLimit,
					"plan", l.resolvedPlan(),
					"upgrade_url", licensing.DefaultUpgradeURL,
				)
				skippedCount++
				continue
			}
		}
		if err := verifyBundleSignature(key, []byte(content), fragmentSignature(rawBundles[key]), verifier.mode, verifier.store); err != nil {
			return nil, "", customBundleCount, err
		}
		policy, err := config.ParseSafetyPolicy([]byte(content))
		if err != nil {
			slog.Error("skipping malformed policy fragment",
				"key", key,
				"error", err,
			)
			skippedCount++
			continue
		}
		hasher.Write([]byte(key))
		hasher.Write([]byte{0})
		hasher.Write([]byte(content))
		merged = mergePolicies(merged, policy)
		if isCustomBundle {
			customBundleCount++
		}
	}
	if skippedCount > 0 {
		slog.Warn("policy fragments skipped due to errors",
			"skipped", skippedCount,
			"loaded", len(keys)-skippedCount,
		)
	}
	if merged == nil {
		return nil, "", customBundleCount, nil
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	return merged, "cfg:" + hash, customBundleCount, nil
}

func extractPolicyFragment(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case map[string]any:
		if !bundleEnabled(v) {
			return "", false
		}
		if raw, ok := v["content"].(string); ok {
			return raw, true
		}
		if raw, ok := v["policy"].(string); ok {
			return raw, true
		}
		if raw, ok := v["data"].(string); ok {
			return raw, true
		}
	}
	return "", false
}

func bundleEnabled(bundle map[string]any) bool {
	if bundle == nil {
		return true
	}
	raw, ok := bundle["enabled"]
	if !ok {
		return true
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return parseBool(v)
	default:
		return parseBool(fmt.Sprint(v))
	}
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func combineSnapshots(base, extra string) string {
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return base + "|" + extra
}

func mergePolicies(base, extra *config.SafetyPolicy) *config.SafetyPolicy {
	if base == nil {
		return clonePolicy(extra)
	}
	if extra == nil {
		return clonePolicy(base)
	}
	out := clonePolicy(base)
	if out.Version == "" {
		out.Version = extra.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = extra.DefaultTenant
	}
	// Merge input rules with duplicate detection (last-seen wins)
	seenInput := make(map[string]int, len(out.Rules))
	for i, r := range out.Rules {
		if r.ID != "" {
			seenInput[r.ID] = i
		}
	}
	for _, r := range extra.Rules {
		if r.ID != "" {
			if idx, dup := seenInput[r.ID]; dup {
				slog.Warn("duplicate policy rule ID in merge — replacing with latest",
					"rule_id", r.ID, "decision", r.Decision)
				out.Rules[idx] = r
				continue
			}
			seenInput[r.ID] = len(out.Rules)
		}
		out.Rules = append(out.Rules, r)
	}

	// Merge output rules with duplicate detection
	seenOutput := make(map[string]int, len(out.OutputRules))
	for i, r := range out.OutputRules {
		if r.ID != "" {
			seenOutput[r.ID] = i
		}
	}
	for _, r := range extra.OutputRules {
		if r.ID != "" {
			if idx, dup := seenOutput[r.ID]; dup {
				slog.Warn("duplicate output policy rule ID in merge — replacing with latest",
					"rule_id", r.ID)
				out.OutputRules[idx] = r
				continue
			}
			seenOutput[r.ID] = len(out.OutputRules)
		}
		out.OutputRules = append(out.OutputRules, r)
	}
	// Merge input rules with duplicate detection
	seenInputRules := make(map[string]int, len(out.InputRules))
	for i, r := range out.InputRules {
		if r.ID != "" {
			seenInputRules[r.ID] = i
		}
	}
	for _, r := range extra.InputRules {
		if r.ID != "" {
			if idx, dup := seenInputRules[r.ID]; dup {
				slog.Warn("duplicate input policy rule ID in merge — replacing with latest",
					"rule_id", r.ID)
				out.InputRules[idx] = r
				continue
			}
			seenInputRules[r.ID] = len(out.InputRules)
		}
		out.InputRules = append(out.InputRules, r)
	}
	out.Tenants = mergeTenantPolicies(out.Tenants, extra.Tenants)
	return out
}

func clonePolicy(policy *config.SafetyPolicy) *config.SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &config.SafetyPolicy{
		Version:         policy.Version,
		DefaultTenant:   policy.DefaultTenant,
		DefaultDecision: policy.DefaultDecision,
		InputPolicy:     policy.InputPolicy,
		OutputPolicy:    policy.OutputPolicy,
		Rules:           append([]config.PolicyRule{}, policy.Rules...),
		OutputRules:     append([]config.OutputPolicyRule{}, policy.OutputRules...),
		InputRules:      append([]config.InputPolicyRule{}, policy.InputRules...),
		Tenants:         map[string]config.TenantPolicy{},
	}
	if policy.Tenants != nil {
		for k, v := range policy.Tenants {
			out.Tenants[k] = cloneTenantPolicy(v)
		}
	}
	return out
}

func mergeTenantPolicies(base map[string]config.TenantPolicy, extra map[string]config.TenantPolicy) map[string]config.TenantPolicy {
	out := map[string]config.TenantPolicy{}
	for k, v := range base {
		out[k] = cloneTenantPolicy(v)
	}
	for tenant, add := range extra {
		current, ok := out[tenant]
		if !ok {
			out[tenant] = cloneTenantPolicy(add)
			continue
		}
		merged := current
		merged.AllowTopics = append(merged.AllowTopics, add.AllowTopics...)
		merged.DenyTopics = append(merged.DenyTopics, add.DenyTopics...)
		merged.AllowedRepoHosts = append(merged.AllowedRepoHosts, add.AllowedRepoHosts...)
		merged.DeniedRepoHosts = append(merged.DeniedRepoHosts, add.DeniedRepoHosts...)
		if add.MaxConcurrent > 0 && (merged.MaxConcurrent == 0 || add.MaxConcurrent < merged.MaxConcurrent) {
			merged.MaxConcurrent = add.MaxConcurrent
		}
		merged.MCP = mergeMCPPolicy(merged.MCP, add.MCP)
		out[tenant] = merged
	}
	return out
}

func cloneTenantPolicy(policy config.TenantPolicy) config.TenantPolicy {
	return config.TenantPolicy{
		AllowTopics:      append([]string{}, policy.AllowTopics...),
		DenyTopics:       append([]string{}, policy.DenyTopics...),
		AllowedRepoHosts: append([]string{}, policy.AllowedRepoHosts...),
		DeniedRepoHosts:  append([]string{}, policy.DeniedRepoHosts...),
		MaxConcurrent:    policy.MaxConcurrent,
		MCP:              policy.MCP,
	}
}

func mergeMCPPolicy(base, extra config.MCPPolicy) config.MCPPolicy {
	return config.MCPPolicy{
		AllowServers:   append(base.AllowServers, extra.AllowServers...),
		DenyServers:    append(base.DenyServers, extra.DenyServers...),
		AllowTools:     append(base.AllowTools, extra.AllowTools...),
		DenyTools:      append(base.DenyTools, extra.DenyTools...),
		AllowResources: append(base.AllowResources, extra.AllowResources...),
		DenyResources:  append(base.DenyResources, extra.DenyResources...),
		AllowActions:   append(base.AllowActions, extra.AllowActions...),
		DenyActions:    append(base.DenyActions, extra.DenyActions...),
	}
}

func policySourceFromEnv(path string) string {
	if raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_URL")); raw != "" {
		return raw
	}
	return strings.TrimSpace(path)
}

func loadPolicyBundle(source string) (*config.SafetyPolicy, string, error) {
	if source == "" {
		return nil, "", nil
	}
	data, err := readPolicySource(source)
	if err != nil {
		return nil, "", err
	}
	if err := verifyPolicySignature(data, source); err != nil {
		return nil, "", err
	}
	policy, err := config.ParseSafetyPolicy(data)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	snapshot := hash
	if policy != nil && policy.Version != "" {
		snapshot = policy.Version + ":" + hash
	}
	return policy, snapshot, nil
}

func readPolicySource(source string) ([]byte, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return fetchPolicyURL(source)
	}
	return readPolicyFile(source)
}

func fetchPolicyURL(raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid policy url: %w", err)
	}
	// Enforce HTTPS in production to prevent MITM injection of malicious policies.
	if env.IsProduction() && parsed.Scheme == "http" {
		return nil, fmt.Errorf("HTTPS required for policy URL in production (got http://%s)", parsed.Host)
	}
	if err := validatePolicyURL(parsed); err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = policyDialContext

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("policy fetch redirect limit exceeded")
			}
			return validatePolicyURL(req.URL)
		},
	}
	resp, err := client.Get(parsed.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("policy fetch status %d", resp.StatusCode)
	}
	limit := policyMaxBytes()
	if resp.ContentLength > 0 && resp.ContentLength > limit {
		return nil, fmt.Errorf("policy exceeds max size of %d bytes", limit)
	}
	return readPolicyBody(resp.Body, limit)
}

func readPolicyFile(source string) ([]byte, error) {
	limit := policyMaxBytes()
	// #nosec G304 -- policy path is configured by the operator.
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err == nil && info.Size() > limit {
		return nil, fmt.Errorf("policy exceeds max size of %d bytes", limit)
	}
	return readPolicyBody(file, limit)
}

func policyMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv(envPolicyMaxBytes))
	if raw != "" {
		if val, err := strconv.ParseInt(raw, 10, 64); err == nil && val > 0 {
			return val
		}
	}
	return defaultPolicyMaxBytes
}

func readPolicyBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid policy max size")
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("policy exceeds max size of %d bytes", limit)
	}
	return data, nil
}

func validatePolicyURL(u *url.URL) error {
	if u == nil {
		return errors.New("policy url is nil")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return errors.New("policy url missing host")
	}
	allowlist := policyURLAllowlist()
	if len(allowlist) > 0 && !hostAllowed(host, allowlist) {
		return fmt.Errorf("policy url host not allowed: %s", host)
	}
	if !env.Bool("SAFETY_POLICY_URL_ALLOW_PRIVATE") {
		if err := ensurePublicHost(host); err != nil {
			return err
		}
	}
	return nil
}

func policyURLAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_URL_ALLOWLIST"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if val := strings.ToLower(strings.TrimSpace(part)); val != "" {
			out = append(out, val)
		}
	}
	return out
}

func hostAllowed(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.TrimPrefix(entry, ".")
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func ensurePublicHost(host string) error {
	if host == "" {
		return errors.New("policy url missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("policy url host not allowed: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return fmt.Errorf("policy url host not allowed: %s", host)
		}
		return nil
	}
	ips, err := policyLookupIP(host)
	if err != nil {
		return fmt.Errorf("policy url resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("policy url resolve failed: %s", host)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("policy url host not allowed: %s", host)
		}
	}
	return nil
}

func policyDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("policy url missing host")
	}
	if allowlist := policyURLAllowlist(); len(allowlist) > 0 && !hostAllowed(host, allowlist) {
		return nil, fmt.Errorf("policy url host not allowed: %s", host)
	}
	ips, err := policyLookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("policy url resolve failed: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("policy url resolve failed: %s", host)
	}
	if !env.Bool("SAFETY_POLICY_URL_ALLOW_PRIVATE") {
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return nil, fmt.Errorf("policy url host not allowed: %s", host)
			}
		}
	}
	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("policy url resolve failed: %s", host)
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if !ip.IsGlobalUnicast() {
		return true
	}
	return false
}

func verifyPolicySignature(data []byte, source string) error {
	pubRaw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_PUBLIC_KEY"))
	requireSignature := env.IsProduction() || env.Bool("SAFETY_POLICY_SIGNATURE_REQUIRED")
	if pubRaw == "" {
		if requireSignature {
			return errors.New("policy signature required but SAFETY_POLICY_PUBLIC_KEY not configured")
		}
		return nil
	}
	pubKey, err := decodeKey(pubRaw)
	if err != nil {
		return fmt.Errorf("invalid SAFETY_POLICY_PUBLIC_KEY: %w", err)
	}
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid SAFETY_POLICY_PUBLIC_KEY length: got %d want %d", len(pubKey), ed25519.PublicKeySize)
	}
	sig, err := readSignature(source)
	if err != nil {
		return err
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("invalid policy signature length: got %d want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), data, sig) {
		return errors.New("policy signature verification failed")
	}
	return nil
}

func readSignature(source string) ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE")); raw != "" {
		return decodeKey(raw)
	}
	if path := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE_PATH")); path != "" {
		return os.ReadFile(path) // #nosec -- signature path is configured by the operator.
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return nil, errors.New("policy signature required but no signature provided")
	}
	sigPath := source + ".sig"
	if _, err := os.Stat(sigPath); err == nil { // #nosec -- signature path is derived from the policy source.
		return os.ReadFile(sigPath) // #nosec -- signature path is derived from the policy source.
	}
	return nil, errors.New("policy signature required but not found")
}

func decodeKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, errors.New("empty key")
	}
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return data, nil
	}
	if data, err := hex.DecodeString(raw); err == nil {
		return data, nil
	}
	return nil, errors.New("invalid key encoding")
}
