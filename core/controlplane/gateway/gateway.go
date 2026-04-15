package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/health"
	"github.com/cordum/cordum/core/infra/locks"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tlsreload"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/telemetry"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	wf "github.com/cordum/cordum/core/workflow"
)

const (
	defaultGrpcAddr             = ":8080"
	defaultHttpAddr             = ":8081"
	defaultMetricsAddr          = ":9092"
	defaultArtifactMaxBytes     = 10 << 20 // 10 MiB default artifact size limit
	maxPromptChars              = 100000
	defaultRateLimitRPS         = 2000
	defaultRateLimitBurst       = 4000
	defaultPublicRateLimitRPS   = 20
	defaultPublicRateLimitBurst = 40
	defaultMaxHeaderBytes       = 1 << 20
	maxLabelKeyLen              = 256              // Max length for label keys
	maxLabelValueLen            = 4096             // Max length for label values (4KB)
	wsAuthSubprotocol           = "cordum-api-key" // #nosec G101 -- subprotocol identifier, not a credential
	shutdownTimeout             = 15 * time.Second
	wsWriteTimeout              = 5 * time.Second
)

// validTopicRegex validates topic names to prevent injection attacks.
// Allows: job.alphanumeric-underscore-dot.name.with.segments
// Blocks: empty segments (job..), special chars, control chars
var validTopicRegex = regexp.MustCompile(`^job\.[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?)*$`)

// #nosec G101 -- environment variable names are identifiers, not credential material.
const (
	envGatewayGrpcAddr              = "GATEWAY_GRPC_ADDR"
	envGatewayHTTPAddr              = "GATEWAY_HTTP_ADDR"
	envGatewayMetricsAddr           = "GATEWAY_METRICS_ADDR"
	envGatewayMetricsPublic         = "GATEWAY_METRICS_PUBLIC"
	envGatewayHTTPTLSCert           = "GATEWAY_HTTP_TLS_CERT"
	envGatewayHTTPTLSKey            = "GATEWAY_HTTP_TLS_KEY"
	envArtifactMaxBytes             = "ARTIFACT_MAX_BYTES"
	envHTTPReadTimeout              = "GATEWAY_HTTP_READ_TIMEOUT"
	envHTTPWriteTimeout             = "GATEWAY_HTTP_WRITE_TIMEOUT"
	envHTTPIdleTimeout              = "GATEWAY_HTTP_IDLE_TIMEOUT"
	envGatewayWSPingInterval        = "GATEWAY_WS_PING_INTERVAL"
	envGatewayWSPongTimeout         = "GATEWAY_WS_PONG_TIMEOUT"
	envGRPCServerKeepaliveTime      = "CORDUM_GRPC_SERVER_KEEPALIVE_TIME"
	envGRPCServerKeepaliveTimeout   = "CORDUM_GRPC_SERVER_KEEPALIVE_TIMEOUT"
	envGRPCServerMaxConnectionAge   = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE"
	envGRPCServerMaxConnectionGrace = "CORDUM_GRPC_SERVER_MAX_CONNECTION_AGE_GRACE"
	envGRPCServerEnforcementMinTime = "CORDUM_GRPC_SERVER_ENFORCEMENT_MIN_TIME"
)

var (
	defaultMaxJobPayloadBytes = int64(env.IntOr("GATEWAY_MAX_JOB_PAYLOAD_BYTES", 2<<20))
	wsPingInterval            = durationFromEnv(envGatewayWSPingInterval, 30*time.Second)
	wsPongTimeout             = durationFromEnv(envGatewayWSPongTimeout, 10*time.Second)
)

const (
	microsPerSecond      = int64(1_000_000)
	microsPerMillisecond = int64(1_000)
	secondsThreshold     = int64(1_000_000_000_000)
	millisThreshold      = int64(1_000_000_000_000_000)
	microsThreshold      = int64(1_000_000_000_000_000_000)
)

type server struct {
	pb.UnimplementedCordumApiServer
	memStore   store.Store
	jobStore   *store.RedisJobStore // Typed for ListRecentJobs
	bus        model.Bus
	workers    map[string]*pb.Heartbeat
	workerSeen map[string]time.Time
	workerMu   sync.RWMutex

	clients             map[*websocket.Conn]*wsClient
	clientsMu           sync.RWMutex
	eventsCh            chan wsEvent
	wsClientBufSz       int
	recentWSDisconnects sync.Map
	wsSummaryOnce       sync.Once

	metrics         infraMetrics.GatewayMetrics
	approvalMetrics infraMetrics.ApprovalMetrics
	tenant          string
	started         time.Time
	auth            AuthProvider
	entitlements    *licensing.EntitlementResolver
	telemetry       *telemetry.Collector
	telemetryState  *telemetry.Store

	workflowStore         *wf.RedisStore
	workflowEng           *wf.Engine
	configSvc             *configsvc.Service
	topicRegistry         *topicregistry.Service
	workerCredentialStore *workercredentials.Service
	dlqStore              *store.DLQStore
	artifactStore         artifacts.Store
	lockStore             locks.Store
	schemaRegistry        *schema.Registry
	schemaEnforcement     schema.EnforcementMode
	safetyConn            *grpc.ClientConn
	safetyClient          pb.SafetyKernelClient
	userStore             UserStore
	keyStore              KeyStore
	rbacStore             *RBACStore
	permChecker           *PermissionChecker

	auditExporter  audit.AuditSender
	legalHoldStore *audit.LegalHoldStore
	statusCacheObj *statusCache

	apiRL    rateLimiter
	publicRL rateLimiter

	instanceRegistry *registry.InstanceRegistry
	instanceID       string

	marketplaceMu    sync.Mutex
	marketplaceCache marketplaceCache
	stopBusTapsOnce  sync.Once
	eventsStopped    atomic.Bool
	shutdownCh       chan struct{}

	workerExpireStop chan struct{}
	workerExpireOnce sync.Once
	probes           *health.ProbeServer
}

// snapshotFromRedis reads the full scheduler worker snapshot from Redis.
// Returns nil, nil if the snapshot key is missing (cold Redis).
func (s *server) snapshotFromRedis() (*registry.Snapshot, error) {
	if s.memStore == nil {
		return nil, fmt.Errorf("mem store unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := s.memStore.GetResult(ctx, registry.SnapshotKey)
	if err != nil {
		return nil, fmt.Errorf("read worker snapshot: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var snap registry.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal worker snapshot: %w", err)
	}
	return &snap, nil
}

// workersFromRedisSnapshot reads the scheduler's worker snapshot from Redis.
// Returns nil, nil if the snapshot key is missing (cold Redis).
func (s *server) workersFromRedisSnapshot() ([]registry.WorkerSummary, error) {
	snap, err := s.snapshotFromRedis()
	if err != nil || snap == nil {
		return nil, err
	}
	return snap.Workers, nil
}

// workerSummariesToHeartbeats converts snapshot summaries to the Heartbeat
// protobuf format used by the workers API, preserving the API contract.
func workerSummariesToHeartbeats(workers []registry.WorkerSummary) []*pb.Heartbeat {
	out := make([]*pb.Heartbeat, len(workers))
	for i, w := range workers {
		out[i] = &pb.Heartbeat{
			WorkerId:        w.WorkerID,
			Pool:            w.Pool,
			ActiveJobs:      w.ActiveJobs,
			MaxParallelJobs: w.MaxParallelJobs,
			Capabilities:    w.Capabilities,
			CpuLoad:         w.CpuLoad,
			GpuUtilization:  w.GpuUtilization,
			MemoryLoad:      w.MemoryLoad,
			Region:          w.Region,
			Type:            w.Type,
			Labels:          w.Labels,
		}
	}
	return out
}

// Close releases resources owned by the server, notably the user store
// connection. It is safe to call with a nil userStore.
func (s *server) Close() {
	if s.instanceRegistry != nil {
		s.instanceRegistry.Stop()
	}
	s.stopBusTaps()
	s.stopWorkerExpiry()
	// Close safety kernel gRPC connection AFTER HTTP shutdown completes so
	// in-flight handlers can finish their safety RPCs during the drain window.
	if s.safetyConn != nil {
		if err := s.safetyConn.Close(); err != nil {
			slog.Error("safety conn close failed", "error", err)
		}
	}
	if nb, ok := s.bus.(*bus.NatsBus); ok {
		nb.Drain()
	}
	if s.auditExporter != nil {
		if err := s.auditExporter.Close(); err != nil {
			slog.Error("audit exporter close failed", "error", err)
		}
	}
	if s.telemetry != nil {
		if err := s.telemetry.Close(); err != nil {
			slog.Error("telemetry collector close failed", "error", err)
		}
	}
	if s.userStore != nil {
		if err := s.userStore.Close(); err != nil {
			slog.Error("user store close failed", "error", err)
		}
	}
	if s.keyStore != nil {
		if ks, ok := s.keyStore.(*RedisKeyStore); ok {
			if err := ks.Close(); err != nil {
				slog.Error("key store close failed", "error", err)
			}
		}
	}
}

func Run(cfg *config.Config) error {
	return RunWithAuth(cfg, nil)
}

func samlConfiguredFromEnv() bool {
	return env.Bool("CORDUM_SAML_ENABLED") ||
		strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA_URL")) != "" ||
		strings.TrimSpace(os.Getenv("CORDUM_SAML_IDP_METADATA")) != ""
}

func oidcFlowConfiguredFromEnv() bool {
	return strings.TrimSpace(os.Getenv("CORDUM_OIDC_CLIENT_ID")) != ""
}

func scimConfiguredFromEnv() bool {
	return strings.TrimSpace(os.Getenv("CORDUM_SCIM_BEARER_TOKEN")) != ""
}

// RunWithAuth starts the gateway with a custom auth provider. When nil, a basic
// single-tenant provider is used.
func RunWithAuth(cfg *config.Config, provider AuthProvider, entitlementResolvers ...*licensing.EntitlementResolver) error {
	if cfg == nil {
		cfg = config.Load()
	}
	entitlementResolver := resolveEntitlementResolver(entitlementResolvers...)
	grpcAddr := addrFromEnv(envGatewayGrpcAddr, defaultGrpcAddr)
	httpAddr := addrFromEnv(envGatewayHTTPAddr, defaultHttpAddr)
	metricsAddr := addrFromEnv(envGatewayMetricsAddr, defaultMetricsAddr)

	tenantID := strings.TrimSpace(os.Getenv("TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}

	gwMetrics := infraMetrics.NewGatewayProm("cordum_api_gateway")
	approvalMetrics := infraMetrics.NewApprovalProm("cordum")
	var userStore UserStore
	var keyStore KeyStore
	var err error
	userAuthRequested := env.Bool("CORDUM_USER_AUTH_ENABLED")
	samlRequested := samlConfiguredFromEnv()
	oidcFlowRequested := oidcFlowConfiguredFromEnv()
	scimRequested := scimConfiguredFromEnv() || entitlementResolver.Entitlements().SCIM
	var basic *BasicAuthProvider
	if provider == nil {
		basic, err = newBasicAuthProvider(tenantID)
		if err != nil {
			return fmt.Errorf("init auth: %w", err)
		}
		provider = basic
	} else {
		basic = basicAuthProvider(provider)
		if usp, ok := provider.(UserStoreProvider); ok {
			userStore = usp.UserStore()
		}
	}

	if userStore == nil && (userAuthRequested || samlRequested || oidcFlowRequested || scimRequested) {
		us, err := NewRedisUserStore(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("init user store: %w", err)
		}
		userStore = us
		if basic != nil {
			basic.SetUserStore(us)
		}
	} else if basic != nil && basic.UserStore() != nil {
		userStore = basic.UserStore()
	}

	if basic != nil && userAuthRequested {
		ks, err := NewRedisKeyStore(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("init key store: %w", err)
		}
		keyStore = ks
		basic.SetKeyStore(ks)

		if strings.TrimSpace(os.Getenv("CORDUM_ADMIN_PASSWORD")) == "" {
			return fmt.Errorf("cordum_user_auth_enabled is set but cordum_admin_password is empty; set cordum_admin_password to configure the admin account")
		}

		if err := seedDefaultAdminUser(context.Background(), basic.UserStore(), tenantID); err != nil {
			slog.Error("seed admin user failed", "error", err)
		}
	}

	// Initialize RBAC store
	var rbacStore *RBACStore
	var permChecker *PermissionChecker
	rbacStore, err = NewRBACStore(cfg.RedisURL)
	if err != nil {
		slog.Warn("rbac store init failed, advanced RBAC unavailable", "error", err)
	} else {
		if err := rbacStore.BootstrapDefaultRoles(context.Background()); err != nil {
			slog.Warn("rbac bootstrap default roles failed", "error", err)
		}
		permChecker = NewPermissionChecker(rbacStore, func() licensing.Entitlements {
			return entitlementResolver.Entitlements()
		})
	}

	authProviders := []AuthProvider{provider}

	oidcProvider, err := NewOIDCProviderFromEnv()
	if err != nil {
		return fmt.Errorf("init oidc: %w", err)
	}
	if oidcProvider != nil {
		defer oidcProvider.Close()
		if oidcRedis, rErr := redisutil.NewClient(cfg.RedisURL); rErr == nil {
			oidcProvider.WithRedis(oidcRedis)
			defer func() { _ = oidcRedis.Close() }()
		} else {
			slog.Error("oidc redis cache unavailable, continuing without", "error", rErr)
		}
		authProviders = append(authProviders, NewOIDCAuthAdapter(oidcProvider, tenantID))
		if oidcFlowRequested {
			oidcFlow, err := NewOIDCFlowAdapter(oidcProvider, userStore, tenantID, entitlementResolver)
			if err != nil {
				return fmt.Errorf("init oidc sso: %w", err)
			}
			if oidcFlow != nil && oidcFlow.Enabled() {
				authProviders = append(authProviders, oidcFlow)
			}
		}
		oidcCfg := oidcProvider.Config()
		slog.Info("[OIDC] enabled",
			"issuer", oidcCfg.IssuerURL,
			"audience", oidcCfg.Audience,
			"browser_sso", oidcFlowRequested,
		)
	}

	if samlRequested {
		samlService, err := NewSAMLService(userStore, tenantID, entitlementResolver)
		if err != nil {
			return fmt.Errorf("init saml: %w", err)
		}
		if samlService != nil && samlService.Enabled() {
			authProviders = append(authProviders, samlService)
		}
	}

	if scimRequested {
		scimService, err := NewSCIMService(userStore, tenantID, entitlementResolver)
		if err != nil {
			return fmt.Errorf("init scim: %w", err)
		}
		if scimService != nil && scimService.Enabled() {
			authProviders = append(authProviders, scimService)
		}
	}

	if len(authProviders) > 1 {
		composite, err := NewCompositeAuthProvider(authProviders...)
		if err != nil {
			return fmt.Errorf("init composite auth: %w", err)
		}
		provider = composite
	}

	if env.IsProduction() && env.Bool("CORDUM_DASHBOARD_EMBED_API_KEY") {
		slog.Error("SECURITY WARNING: CORDUM_DASHBOARD_EMBED_API_KEY is enabled in production — API key will be exposed in browser JavaScript")
	}

	memStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer func() { _ = memStore.Close() }()

	jobStore, err := store.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis job store: %w", err)
	}
	defer func() { _ = jobStore.Close() }()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer natsBus.Close()

	if err := bus.PublishHandshake(natsBus, "api-gateway", pb.ComponentRole_COMPONENT_ROLE_GATEWAY, map[string]bool{
		"http": true, "grpc": true, "websocket": true, "mcp": true,
	}); err != nil {
		slog.Warn("handshake publish failed", "error", err)
	}

	workflowStore, err := wf.NewRedisWorkflowStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis workflow store: %w", err)
	}
	defer func() { _ = workflowStore.Close() }()
	wfCtx, wfCancel := context.WithCancel(context.Background())
	defer wfCancel()
	workflowEng := wf.NewEngine(workflowStore, natsBus).WithContext(wfCtx)

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis config service: %w", err)
	}
	defer func() { _ = configSvc.Close() }()
	if err := seedDefaultPackCatalogs(context.Background(), configSvc); err != nil {
		slog.Error("seed pack catalogs failed", "error", err)
	}
	if err := configSvc.EnsureDefault(context.Background()); err != nil {
		slog.Warn("auto-bootstrap default config failed", "error", err)
	}
	legacyPolicyBundlesMigrated, legacyPolicyBundleCount, err := migrateLegacyPolicyBundles(context.Background(), configSvc)
	if err != nil {
		return fmt.Errorf("migrate legacy policy bundles: %w", err)
	}
	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis schema registry: %w", err)
	}
	defer func() { _ = schemaRegistry.Close() }()
	workflowEng = workflowEng.WithMemory(memStore).WithConfig(configSvc).WithSchemaRegistry(schemaRegistry)
	if raw := strings.TrimSpace(os.Getenv("WORKFLOW_FOREACH_MAX_ITEMS")); raw != "" {
		if limit, err := strconv.Atoi(raw); err == nil && limit > 0 {
			workflowEng = workflowEng.WithMaxForEachItems(limit)
		}
	}

	dlqStore, err := store.NewDLQStore(cfg.RedisURL, 0)
	if err != nil {
		return fmt.Errorf("connect redis dlq store: %w", err)
	}
	defer func() { _ = dlqStore.Close() }()
	// Periodic cleanup of stale DLQ index entries whose data keys have expired.
	dlqCleanupCtx, dlqCleanupCancel := context.WithCancel(context.Background())
	defer dlqCleanupCancel()
	dlqStore.StartCleanupLoop(dlqCleanupCtx, time.Hour)

	artifactStore, err := artifacts.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis artifact store: %w", err)
	}
	defer func() { _ = artifactStore.Close() }()

	lockStore, err := locks.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis lock store: %w", err)
	}
	defer func() { _ = lockStore.Close() }()

	var safetyConn *grpc.ClientConn
	var safetyClient pb.SafetyKernelClient
	if cfg.SafetyKernelAddr != "" {
		conn, client, err := dialSafetyKernel(cfg.SafetyKernelAddr)
		if err != nil {
			if env.IsProduction() || env.Bool("SAFETY_KERNEL_TLS_REQUIRED") {
				return fmt.Errorf("safety kernel dial failed: %w", err)
			}
			slog.Error("safety kernel dial failed", "error", err)
		} else {
			safetyConn = conn
			safetyClient = client
			// safetyConn is closed in s.Close(), NOT here — handlers may still
			// use safetyClient during the graceful shutdown window.
		}
	}

	var auditSender audit.AuditSender
	bufExporter, err := audit.NewExporterFromEnvWithEntitlements(entitlementResolver)
	if err != nil {
		return fmt.Errorf("init audit exporter: %w", err)
	}
	if bufExporter != nil {
		transport := strings.ToLower(strings.TrimSpace(os.Getenv("AUDIT_TRANSPORT")))
		if transport == "nats" && natsBus != nil {
			auditSender = audit.NewNATSAuditPublisher(natsBus, bufExporter)
			// Start consumer in the same process — queue group ensures only
			// one replica across the cluster handles each event.
			if _, err := audit.NewNATSAuditConsumer(natsBus, bufExporter.Backend()); err != nil {
				slog.Warn("audit NATS consumer failed to start, falling back to local buffer", "error", err)
			}
		} else {
			auditSender = bufExporter
		}
	}

	s := &server{
		memStore:              memStore,
		jobStore:              jobStore,
		bus:                   natsBus,
		workers:               make(map[string]*pb.Heartbeat),
		workerSeen:            make(map[string]time.Time),
		clients:               make(map[*websocket.Conn]*wsClient),
		eventsCh:              make(chan wsEvent, 512),
		wsClientBufSz:         wsClientBufferSize(),
		metrics:               gwMetrics,
		approvalMetrics:       approvalMetrics,
		tenant:                tenantID,
		auth:                  provider,
		entitlements:          entitlementResolver,
		started:               time.Now().UTC(),
		workflowStore:         workflowStore,
		workflowEng:           workflowEng,
		configSvc:             configSvc,
		topicRegistry:         topicregistry.NewService(configSvc),
		workerCredentialStore: workercredentials.NewService(configSvc),
		dlqStore:              dlqStore,
		artifactStore:         artifactStore,
		lockStore:             lockStore,
		schemaRegistry:        schemaRegistry,
		schemaEnforcement:     schema.ParseEnforcementMode(os.Getenv("SCHEMA_ENFORCEMENT")),
		safetyConn:            safetyConn,
		safetyClient:          safetyClient,
		userStore:             userStore,
		keyStore:              keyStore,
		rbacStore:             rbacStore,
		permChecker:           permChecker,
		auditExporter:         auditSender,
		legalHoldStore:        initLegalHoldStore(cfg.RedisURL),
		statusCacheObj:        newStatusCache(2 * time.Second),
		shutdownCh:            make(chan struct{}),
	}
	s.syncApprovalQueueDepth(context.Background())
	defer s.Close()
	telemetryStore, err := telemetry.NewStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect telemetry store: %w", err)
	}
	s.telemetryState = telemetryStore

	// If operator previously consented via the dashboard, apply that mode
	if consentMode, cErr := telemetryStore.GetConsentMode(context.Background()); cErr == nil && consentMode != "" {
		cfg.TelemetryMode = consentMode
	}

	s.telemetry = telemetry.NewCollector(telemetry.CollectorOptions{
		Mode:              telemetry.NormalizeMode(cfg.TelemetryMode),
		Store:             telemetryStore,
		Reporter:          telemetry.NewReporter(cfg.TelemetryEndpoint, nil),
		TierProvider:      func() string { return string(s.resolvedPlan()) },
		JobStore:          jobStore,
		WorkflowStore:     workflowStore,
		ConfigSvc:         configSvc,
		SchemaRegistry:    schemaRegistry,
		TopicRegistry:     s.topicRegistry,
		WorkerCredentials: s.workerCredentialStore,
		TenantID:          tenantID,
	})
	s.telemetry.Start(context.Background())
	if legacyPolicyBundlesMigrated {
		s.publishConfigChanged(string(configsvc.ScopeSystem), "default")
		s.publishConfigChanged(string(configsvc.ScopeSystem), policyConfigID)
		slog.Info("gateway startup migrated legacy policy bundles",
			"from_scope", "system/default",
			"to_scope", "system/"+policyConfigID,
			"bundle_count", legacyPolicyBundleCount,
		)
	}

	// Wire distributed rate limiters. Use Redis-backed counters by default;
	// fall back to in-memory when REDIS_RATE_LIMIT=false or Redis unavailable.
	redisRL := strings.ToLower(strings.TrimSpace(os.Getenv("REDIS_RATE_LIMIT")))
	apiRPSDefault, apiBurstDefault := s.tierRateLimitDefaults()
	tierEntitlements := s.currentEntitlements()
	if redisRL != "false" && redisRL != "0" && redisRL != "no" && jobStore != nil {
		redisClient := jobStore.Client()
		apiRPS, apiBurst := rateLimitFromEnv("API_RATE_LIMIT_RPS", "API_RATE_LIMIT_BURST", apiRPSDefault, apiBurstDefault)
		apiRPS, apiBurst = clampRateLimitToEntitlements(apiRPS, apiBurst, tierEntitlements)
		pubRPS, pubBurst := rateLimitFromEnv("API_PUBLIC_RATE_LIMIT_RPS", "API_PUBLIC_RATE_LIMIT_BURST", defaultPublicRateLimitRPS, defaultPublicRateLimitBurst)
		s.apiRL = newRedisRateLimiter(redisClient, apiRPS, apiBurst)
		s.publicRL = newRedisRateLimiter(redisClient, pubRPS, pubBurst)
	} else {
		s.apiRL = newKeyedRateLimiterFromEnvWithDefaults(apiRPSDefault, apiBurstDefault)
		s.publicRL = newPublicRateLimiterFromEnvWithDefaults(defaultPublicRateLimitRPS, defaultPublicRateLimitBurst)
	}

	// Instance registry: self-register this gateway replica in Redis.
	instanceID := registry.ResolveInstanceID()
	s.instanceID = instanceID
	if jobStore != nil {
		s.instanceRegistry = registry.NewInstanceRegistry(
			jobStore.Client(), "api-gateway", instanceID,
			buildinfo.Version, buildinfo.Commit,
		)
		s.instanceRegistry.Start(context.Background())
	}

	if err := s.startBusTaps(); err != nil {
		return fmt.Errorf("start bus taps: %w", err)
	}

	// Start workflow reconciler as a safety net for stuck runs. The reconciler
	// polls every 5 seconds, scanning Running/Pending/Waiting runs for completed
	// jobs that were missed (e.g. due to lock contention during NATS delivery).
	// Uses its own distributed lock so multiple gateway replicas won't conflict.
	reconcilerCtx, reconcilerCancel := context.WithCancel(context.Background())
	defer reconcilerCancel()
	wfReconciler := wf.NewReconciler(workflowStore, workflowEng, jobStore, 5*time.Second, 200)
	go wfReconciler.Start(reconcilerCtx)

	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc (%s): %w", grpcAddr, err)
	}
	grpcCreds := insecure.NewCredentials()
	grpcTLSConfigured := false
	certFile := strings.TrimSpace(os.Getenv("GRPC_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("GRPC_TLS_KEY"))
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return fmt.Errorf("grpc tls requires both GRPC_TLS_CERT and GRPC_TLS_KEY")
		}
		grpcReloader, err := tlsreload.NewCertReloader(certFile, keyFile, "gateway-grpc")
		if err != nil {
			return fmt.Errorf("grpc tls keypair: %w", err)
		}
		go grpcReloader.WatchLoop(context.Background(), 30*time.Second)
		cfg := &tls.Config{
			GetCertificate: grpcReloader.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			cfg.MinVersion = tls.VersionTLS13
		}
		grpcCreds = credentials.NewTLS(cfg)
		grpcTLSConfigured = true
	}
	if env.IsProduction() && !grpcTLSConfigured {
		return fmt.Errorf("grpc tls required in production")
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(grpcCreds),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge:      durationFromEnv(envGRPCServerMaxConnectionAge, 2*time.Hour),
			MaxConnectionAgeGrace: durationFromEnv(envGRPCServerMaxConnectionGrace, 30*time.Second),
			Time:                  durationFromEnv(envGRPCServerKeepaliveTime, 30*time.Second),
			Timeout:               durationFromEnv(envGRPCServerKeepaliveTimeout, 10*time.Second),
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             durationFromEnv(envGRPCServerEnforcementMinTime, 15*time.Second),
			PermitWithoutStream: true,
		}),
		grpc.ChainUnaryInterceptor(
			apiKeyUnaryInterceptor(s.auth),
			rateLimitUnaryInterceptor(s.auth, s.apiRL, s.publicRL),
		),
	)
	pb.RegisterCordumApiServer(grpcServer, s)
	if env.Bool(env.EnvGRPCReflection) {
		if env.IsProduction() && !env.Bool("CORDUM_GRPC_REFLECTION_FORCE") {
			slog.Error("gRPC reflection blocked in production mode (exposes service definitions). Set CORDUM_GRPC_REFLECTION_FORCE=1 to override.")
		} else {
			if env.IsProduction() {
				slog.Warn("gRPC reflection enabled in production via force override")
			}
			reflection.Register(grpcServer)
		}
	}

	go func() {
		slog.Info("grpc listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			slog.Error("grpc server error", "error", err)
		}
	}()

	return startHTTPServer(s, httpAddr, metricsAddr, grpcServer)
}

type busStatusReporter interface {
	IsConnected() bool
	Status() string
}

func (s *server) natsHealthStatus() (string, bool) {
	reporter, ok := s.bus.(busStatusReporter)
	if !ok || reporter == nil {
		return "unavailable", false
	}
	connected := reporter.IsConnected()
	status := strings.ToLower(strings.TrimSpace(reporter.Status()))
	if status == "" {
		if connected {
			status = "connected"
		} else {
			status = "disconnected"
		}
	}
	return status, connected
}

func (s *server) redisClient() redis.UniversalClient {
	if s == nil || s.jobStore == nil {
		return nil
	}
	return s.jobStore.Client()
}

func (s *server) redisHealthStatus(ctx context.Context) (string, error) {
	client := s.redisClient()
	if client == nil {
		return "unavailable", fmt.Errorf("redis client unavailable")
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return "error", err
	}
	return "ok", nil
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	natsStatus, natsConnected := s.natsHealthStatus()
	redisStatus, redisErr := s.redisHealthStatus(ctx)
	payload := map[string]any{
		"status": "healthy",
		"nats":   natsStatus,
		"redis":  redisStatus,
	}

	var healthErrors []string
	if !natsConnected {
		payload["status"] = "unhealthy"
		healthErrors = append(healthErrors, fmt.Sprintf("nats status=%s", natsStatus))
	}
	if redisErr != nil {
		payload["status"] = "unhealthy"
		healthErrors = append(healthErrors, fmt.Sprintf("redis ping failed: %v", redisErr))
	}
	if len(healthErrors) > 0 {
		payload["error"] = strings.Join(healthErrors, "; ")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			slog.Error("encode health response failed", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("encode health response failed", "error", err)
	}
}

func startHTTPServer(s *server, httpAddr, metricsAddr string, grpcServer *grpc.Server) error {
	mux := http.NewServeMux()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", infraMetrics.Handler())
	if env.IsProduction() {
		if err := infraMetrics.ValidateBindAddr(metricsAddr, env.Bool(envGatewayMetricsPublic)); err != nil {
			return fmt.Errorf("metrics bind rejected: %w", err)
		}
	}
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	go func() {
		slog.Info("metrics listening", "addr", metricsAddr+"/metrics")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	// 1. Health probes (/healthz, /readyz, /livez) + backward-compatible aliases.
	s.probes = health.New()
	s.probes.RegisterReadiness("nats", func(ctx context.Context) error {
		_, connected := s.natsHealthStatus()
		if !connected {
			return fmt.Errorf("nats disconnected")
		}
		return nil
	})
	s.probes.RegisterReadiness("redis", func(ctx context.Context) error {
		_, err := s.redisHealthStatus(ctx)
		return err
	})
	s.probes.Register(mux)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/health", s.instrumented("/api/v1/health", s.handleHealth))

	// 1.5 Auth config (public)
	mux.HandleFunc("GET /api/v1/auth/config", s.instrumented("/api/v1/auth/config", s.handleAuthConfig))

	// 1.6 Auth endpoints
	mux.HandleFunc("POST /api/v1/auth/login", s.instrumented("/api/v1/auth/login", s.handleLogin))
	mux.HandleFunc("GET /api/v1/auth/session", s.instrumented("/api/v1/auth/session", s.handleSession))
	mux.HandleFunc("POST /api/v1/auth/logout", s.instrumented("/api/v1/auth/logout", s.handleLogout))
	mux.HandleFunc("POST /api/v1/auth/password", s.instrumented("/api/v1/auth/password", s.handleChangePassword))

	// 1.7 User management (admin only)
	mux.HandleFunc("POST /api/v1/users", s.instrumented("/api/v1/users", s.handleCreateUser))
	mux.HandleFunc("GET /api/v1/users", s.instrumented("/api/v1/users", s.handleListUsers))
	mux.HandleFunc("PUT /api/v1/users/{id}", s.instrumented("/api/v1/users/{id}", s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.instrumented("/api/v1/users/{id}", s.handleDeleteUser))
	mux.HandleFunc("POST /api/v1/users/{id}/password", s.instrumented("/api/v1/users/{id}/password", s.handleChangeUserPassword))

	// 1.8 API Key management (admin only)
	mux.HandleFunc("GET /api/v1/auth/keys", s.instrumented("/api/v1/auth/keys", s.handleListKeys))
	mux.HandleFunc("POST /api/v1/auth/keys", s.instrumented("/api/v1/auth/keys", s.handleCreateKey))
	mux.HandleFunc("DELETE /api/v1/auth/keys/{id}", s.instrumented("/api/v1/auth/keys/{id}", s.handleRevokeKey))

	// 1.9 RBAC role management (admin only, entitlement-gated)
	mux.HandleFunc("GET /api/v1/auth/roles", s.instrumented("/api/v1/auth/roles", s.handleListRoles))
	mux.HandleFunc("GET /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handleGetRole))
	mux.HandleFunc("PUT /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handlePutRole))
	mux.HandleFunc("DELETE /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handleDeleteRole))

	// 2. Workers (RPC via NATS)
	mux.HandleFunc("GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))
	mux.HandleFunc("GET /api/v1/workers/{id}", s.instrumented("/api/v1/workers/{id}", s.handleGetWorker))
	mux.HandleFunc("GET /api/v1/workers/{id}/jobs", s.instrumented("/api/v1/workers/{id}/jobs", s.handleGetWorkerJobs))
	mux.HandleFunc("GET /api/v1/workers/credentials", s.instrumented("/api/v1/workers/credentials", s.handleListWorkerCredentials))
	mux.HandleFunc("POST /api/v1/workers/credentials", s.instrumented("/api/v1/workers/credentials", s.handleCreateWorkerCredential))
	mux.HandleFunc("DELETE /api/v1/workers/credentials/{worker_id}", s.instrumented("/api/v1/workers/credentials/{worker_id}", s.handleDeleteWorkerCredential))
	mux.HandleFunc("GET /api/v1/pools", s.instrumented("/api/v1/pools", s.handleListPools))
	mux.HandleFunc("GET /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleGetPool))
	mux.HandleFunc("GET /api/v1/topics", s.instrumented("/api/v1/topics", s.handleListTopics))
	mux.HandleFunc("POST /api/v1/topics", s.instrumented("/api/v1/topics", s.handleCreateTopic))
	mux.HandleFunc("DELETE /api/v1/topics/{name}", s.instrumented("/api/v1/topics/{name}", s.handleDeleteTopic))
	mux.HandleFunc("PUT /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleCreatePool))
	mux.HandleFunc("PATCH /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleUpdatePool))
	mux.HandleFunc("DELETE /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleDeletePool))
	mux.HandleFunc("POST /api/v1/pools/{name}/drain", s.instrumented("/api/v1/pools/{name}/drain", s.handleDrainPool))
	mux.HandleFunc("PUT /api/v1/pools/{name}/topics/{topic}", s.instrumented("/api/v1/pools/{name}/topics/{topic}", s.handleAddTopicToPool))
	mux.HandleFunc("DELETE /api/v1/pools/{name}/topics/{topic}", s.instrumented("/api/v1/pools/{name}/topics/{topic}", s.handleRemoveTopicFromPool))

	// 2.5 Status snapshot (Redis/NATS/workers/uptime)
	mux.HandleFunc("GET /api/v1/status", s.instrumented("/api/v1/status", s.handleStatus))
	mux.HandleFunc("GET /api/v1/license", s.instrumented("/api/v1/license", s.handleGetLicense))
	mux.HandleFunc("GET /api/v1/license/usage", s.instrumented("/api/v1/license/usage", s.handleGetLicenseUsage))
	mux.HandleFunc("POST /api/v1/license/reload", s.instrumented("/api/v1/license/reload", s.handleReloadLicense))
	mux.HandleFunc("GET /api/v1/telemetry/status", s.instrumented("/api/v1/telemetry/status", s.handleGetTelemetryStatus))
	mux.HandleFunc("GET /api/v1/telemetry/inspect", s.instrumented("/api/v1/telemetry/inspect", s.handleGetTelemetryInspect))
	mux.HandleFunc("GET /api/v1/telemetry/export", s.instrumented("/api/v1/telemetry/export", s.handleGetTelemetryExport))
	mux.HandleFunc("GET /api/v1/telemetry/usage", s.instrumented("/api/v1/telemetry/usage", s.handleGetTelemetryUsage))
	mux.HandleFunc("POST /api/v1/telemetry/consent", s.instrumented("/api/v1/telemetry/consent", s.handleSetTelemetryConsent))

	// 2.6 Admin endpoints (read-only, admin auth required)
	mux.HandleFunc("GET /api/v1/admin/locks", s.instrumented("/api/v1/admin/locks", s.handleAdminLocks))

	// 2.7 Audit export management (admin only, entitlement-gated)
	mux.HandleFunc("GET /api/v1/audit/export/health", s.instrumented("/api/v1/audit/export/health", s.handleAuditExportHealth))
	mux.HandleFunc("GET /api/v1/audit/export/config", s.instrumented("/api/v1/audit/export/config", s.handleAuditExportConfig))
	mux.HandleFunc("POST /api/v1/audit/export/test", s.instrumented("/api/v1/audit/export/test", s.handleAuditExportTest))

	// 2.8 Legal hold management (admin only, entitlement-gated)
	mux.HandleFunc("POST /api/v1/audit/legal-hold", s.instrumented("/api/v1/audit/legal-hold", s.handleCreateLegalHold))
	mux.HandleFunc("GET /api/v1/audit/legal-holds", s.instrumented("/api/v1/audit/legal-holds", s.handleListLegalHolds))
	mux.HandleFunc("DELETE /api/v1/audit/legal-hold/{id}", s.instrumented("/api/v1/audit/legal-hold/{id}", s.handleReleaseLegalHold))

	// 3. Jobs (Redis ZSet)
	mux.HandleFunc("GET /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleListJobs))

	// 4. Job Details
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.instrumented("/api/v1/jobs/{id}", s.handleGetJob))
	mux.HandleFunc("GET /api/v1/jobs/{id}/stream", s.instrumented("/api/v1/jobs/{id}/stream", s.handleJobStream))
	mux.HandleFunc("GET /api/v1/jobs/{id}/decisions", s.instrumented("/api/v1/jobs/{id}/decisions", s.handleListJobDecisions))
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.instrumented("/api/v1/jobs/{id}/cancel", s.handleCancelJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/remediate", s.instrumented("/api/v1/jobs/{id}/remediate", s.handleRemediateJob))

	// 4.5 Memory pointers (debug)
	mux.HandleFunc("GET /api/v1/memory", s.instrumented("/api/v1/memory", s.handleGetMemory))
	// 4.6 Artifact store
	mux.HandleFunc("POST /api/v1/artifacts", s.instrumented("/api/v1/artifacts", s.handlePutArtifact))
	mux.HandleFunc("GET /api/v1/artifacts/{ptr}", s.instrumented("/api/v1/artifacts/{ptr}", s.handleGetArtifact))

	// 5. Submit Job (REST)
	mux.HandleFunc("POST /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleSubmitJobHTTP))

	// 6. Trace Details
	mux.HandleFunc("GET /api/v1/traces/{id}", s.instrumented("/api/v1/traces/{id}", s.handleGetTrace))

	// 8. Workflows
	mux.HandleFunc("GET /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleListWorkflows))
	mux.HandleFunc("POST /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleCreateWorkflow))
	mux.HandleFunc("GET /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleGetWorkflow))
	mux.HandleFunc("DELETE /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleDeleteWorkflow))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleStartRun))
	mux.HandleFunc("GET /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleListRuns))
	mux.HandleFunc("GET /api/v1/workflow-runs", s.instrumented("/api/v1/workflow-runs", s.handleListAllRuns))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleGetRun))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}/timeline", s.instrumented("/api/v1/workflow-runs/{id}/timeline", s.handleGetRunTimeline))
	mux.HandleFunc("GET /api/v1/workflow-runs/{id}/chat", s.instrumented("/api/v1/workflow-runs/{id}/chat", s.handleGetRunChat))
	mux.HandleFunc("POST /api/v1/workflow-runs/{id}/chat", s.instrumented("/api/v1/workflow-runs/{id}/chat", s.handlePostRunChat))
	mux.HandleFunc("DELETE /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleDeleteRun))
	mux.HandleFunc("POST /api/v1/workflow-runs/{id}/rerun", s.instrumented("/api/v1/workflow-runs/{id}/rerun", s.handleRerunRun))
	mux.HandleFunc("POST /api/v1/workflows/{id}/dry-run", s.instrumented("/api/v1/workflows/{id}/dry-run", s.handleWorkflowDryRun))

	// 9. Config
	mux.HandleFunc("GET /api/v1/config", s.instrumented("/api/v1/config", s.handleGetConfig))
	mux.HandleFunc("GET /api/v1/config/effective", s.instrumented("/api/v1/config/effective", s.handleGetEffectiveConfig))
	mux.HandleFunc("PUT /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))
	mux.HandleFunc("POST /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))

	// 9.25 Packs
	mux.HandleFunc("GET /api/v1/packs", s.instrumented("/api/v1/packs", s.handleListPacks))
	mux.HandleFunc("GET /api/v1/packs/{id}", s.instrumented("/api/v1/packs/{id}", s.handleGetPack))
	mux.HandleFunc("POST /api/v1/packs/install", s.instrumented("/api/v1/packs/install", s.handleInstallPack))
	mux.HandleFunc("POST /api/v1/packs/{id}/uninstall", s.instrumented("/api/v1/packs/{id}/uninstall", s.handleUninstallPack))
	mux.HandleFunc("POST /api/v1/packs/{id}/verify", s.instrumented("/api/v1/packs/{id}/verify", s.handleVerifyPack))
	mux.HandleFunc("GET /api/v1/marketplace/packs", s.instrumented("/api/v1/marketplace/packs", s.handleMarketplacePacks))
	mux.HandleFunc("POST /api/v1/marketplace/install", s.instrumented("/api/v1/marketplace/install", s.handleMarketplaceInstall))

	// 9.5 Schemas
	mux.HandleFunc("POST /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleRegisterSchema))
	mux.HandleFunc("GET /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleListSchemas))
	mux.HandleFunc("GET /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleGetSchema))
	mux.HandleFunc("DELETE /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleDeleteSchema))

	// 9.6 Resource locks
	mux.HandleFunc("GET /api/v1/locks", s.instrumented("/api/v1/locks", s.handleGetLock))
	mux.HandleFunc("POST /api/v1/locks/acquire", s.instrumented("/api/v1/locks/acquire", s.handleAcquireLock))
	mux.HandleFunc("POST /api/v1/locks/release", s.instrumented("/api/v1/locks/release", s.handleReleaseLock))
	mux.HandleFunc("POST /api/v1/locks/renew", s.instrumented("/api/v1/locks/renew", s.handleRenewLock))

	// 10. DLQ
	mux.HandleFunc("GET /api/v1/dlq", s.instrumented("/api/v1/dlq", s.handleListDLQ))
	mux.HandleFunc("GET /api/v1/dlq/page", s.instrumented("/api/v1/dlq/page", s.handleListDLQPage))
	mux.HandleFunc("DELETE /api/v1/dlq/{job_id}", s.instrumented("/api/v1/dlq/{job_id}", s.handleDeleteDLQ))
	mux.HandleFunc("POST /api/v1/dlq/{job_id}/retry", s.instrumented("/api/v1/dlq/{job_id}/retry", s.handleRetryDLQ))

	// 11. Workflow run operations
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/cancel", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/cancel", s.handleCancelRun))

	// 11.5 Job approvals
	mux.HandleFunc("GET /api/v1/approvals", s.instrumented("/api/v1/approvals", s.handleListApprovals))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/approve", s.instrumented("/api/v1/approvals/{job_id}/approve", s.handleApproveJob))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/reject", s.instrumented("/api/v1/approvals/{job_id}/reject", s.handleRejectJob))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/repair", s.instrumented("/api/v1/approvals/{job_id}/repair", s.handleRepairApproval))
	mux.HandleFunc("GET /api/v1/approvals/{job_id}/context", s.instrumented("/api/v1/approvals/{job_id}/context", s.handleApprovalContext))

	// 12. Policy endpoints
	mux.HandleFunc("POST /api/v1/policy/evaluate", s.instrumented("/api/v1/policy/evaluate", s.handlePolicyEvaluate))
	mux.HandleFunc("POST /api/v1/policy/simulate", s.instrumented("/api/v1/policy/simulate", s.handlePolicySimulate))
	mux.HandleFunc("POST /api/v1/policy/explain", s.instrumented("/api/v1/policy/explain", s.handlePolicyExplain))
	mux.HandleFunc("GET /api/v1/policy/snapshots", s.instrumented("/api/v1/policy/snapshots", s.handlePolicySnapshots))
	mux.HandleFunc("GET /api/v1/policy/rules", s.instrumented("/api/v1/policy/rules", s.handlePolicyRules))
	mux.HandleFunc("GET /api/v1/policy/output/rules", s.instrumented("/api/v1/policy/output/rules", s.handlePolicyOutputRules))
	mux.HandleFunc("GET /api/v1/policy/output/stats", s.instrumented("/api/v1/policy/output/stats", s.handlePolicyOutputStats))
	mux.HandleFunc("PUT /api/v1/policy/output/rules/{id}", s.instrumented("/api/v1/policy/output/rules/{id}", s.handlePutPolicyOutputRule))
	mux.HandleFunc("GET /api/v1/policy/velocity-rules", s.instrumented("/api/v1/policy/velocity-rules", s.handleVelocityRules))
	mux.HandleFunc("GET /api/v1/policy/velocity-rules/stats", s.instrumented("/api/v1/policy/velocity-rules/stats", s.handleVelocityRuleStats))
	mux.HandleFunc("POST /api/v1/policy/velocity-rules", s.instrumented("/api/v1/policy/velocity-rules", s.handleCreateVelocityRule))
	mux.HandleFunc("PUT /api/v1/policy/velocity-rules/{id}", s.instrumented("/api/v1/policy/velocity-rules/{id}", s.handlePutVelocityRule))
	mux.HandleFunc("DELETE /api/v1/policy/velocity-rules/{id}", s.instrumented("/api/v1/policy/velocity-rules/{id}", s.handleDeleteVelocityRule))
	mux.HandleFunc("GET /api/v1/policy/bundles", s.instrumented("/api/v1/policy/bundles", s.handlePolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleGetPolicyBundle))
	mux.HandleFunc("PUT /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handlePutPolicyBundle))
	mux.HandleFunc("DELETE /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleDeletePolicyBundle))
	mux.HandleFunc("POST /api/v1/policy/bundles/{id}/simulate", s.instrumented("/api/v1/policy/bundles/{id}/simulate", s.handleSimulatePolicyBundle))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleListPolicyBundleSnapshots))
	mux.HandleFunc("POST /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleCapturePolicyBundleSnapshot))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots/{id}", s.instrumented("/api/v1/policy/bundles/snapshots/{id}", s.handleGetPolicyBundleSnapshot))
	mux.HandleFunc("POST /api/v1/policy/publish", s.instrumented("/api/v1/policy/publish", s.handlePublishPolicyBundles))
	mux.HandleFunc("POST /api/v1/policy/rollback", s.instrumented("/api/v1/policy/rollback", s.handleRollbackPolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/audit", s.instrumented("/api/v1/policy/audit", s.handleListPolicyAudit))
	mux.HandleFunc("POST /api/v1/policy/replay", s.instrumented("/api/v1/policy/replay", s.handlePolicyReplay))
	mux.HandleFunc("POST /api/v1/policy/analytics", s.instrumented("/api/v1/policy/analytics", s.handlePolicyAnalytics))

	// 12.5 MCP (HTTP/SSE) routes
	if err := s.registerMCPRoutes(mux); err != nil {
		return fmt.Errorf("register mcp routes: %w", err)
	}

	// 7. Stream (WebSocket)
	mux.HandleFunc("/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))

	// Extension routes (enterprise auth, SSO, etc.)
	if registrar, ok := s.auth.(RouteRegistrar); ok {
		registrar.RegisterRoutes(mux, s.instrumented)
	}

	// Middleware chain: logging → CORS → rate limit → auth → read audit → tenant → body limit → mux
	// SECURITY: Rate limiter MUST run before auth so that invalid API key
	// brute-force attempts are rate-limited by IP. When auth context is
	// absent, rateLimitKey falls back to IP-based keying automatically.
	readAuditRate := parseFloatEnv("CORDUM_AUDIT_READ_SAMPLE_RATE", 0.0)
	inner := auditReadMiddleware(s.auditExporter, readAuditRate, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
	handler := requestLoggingMiddleware(corsMiddleware(rateLimitMiddleware(s.auth, s.apiRL, s.publicRL, apiKeyMiddleware(s.auth, inner, s.auditExporter))))

	httpTLSCert := strings.TrimSpace(os.Getenv(envGatewayHTTPTLSCert))
	httpTLSKey := strings.TrimSpace(os.Getenv(envGatewayHTTPTLSKey))
	if httpTLSCert != "" || httpTLSKey != "" {
		if httpTLSCert == "" || httpTLSKey == "" {
			return fmt.Errorf("http tls requires both %s and %s", envGatewayHTTPTLSCert, envGatewayHTTPTLSKey)
		}
	}
	if env.IsProduction() && httpTLSCert == "" {
		return fmt.Errorf("http tls required in production")
	}

	if s.probes != nil {
		s.probes.SetStartupComplete()
	}
	slog.Info("http listening", "addr", httpAddr)
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           handler,
		ReadTimeout:       durationFromEnv(envHTTPReadTimeout, 15*time.Second),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      durationFromEnv(envHTTPWriteTimeout, 30*time.Second),
		IdleTimeout:       durationFromEnv(envHTTPIdleTimeout, 120*time.Second),
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	var httpReloader *tlsreload.CertReloader
	if httpTLSCert != "" {
		var err error
		httpReloader, err = tlsreload.NewCertReloader(httpTLSCert, httpTLSKey, "gateway-http")
		if err != nil {
			return fmt.Errorf("http tls keypair: %w", err)
		}
		go httpReloader.WatchLoop(context.Background(), 30*time.Second)
		srv.TLSConfig = &tls.Config{
			GetCertificate: httpReloader.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			srv.TLSConfig.MinVersion = tls.VersionTLS13
		}
	}

	// Graceful shutdown: on SIGINT/SIGTERM, drain all servers and goroutines.
	sigCtx, sigStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer sigStop()
	if basic := basicAuthProvider(s.auth); basic != nil {
		basic.SetUsageContext(sigCtx)
	}

	// Start pool drain lifecycle checker.
	drainChecker := newPoolDrainChecker(s)
	go drainChecker.Run(sigCtx)

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-sigCtx.Done()
		slog.Info("shutting down gracefully", "timeout", shutdownTimeout.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Stop the event broadcast goroutine.
		if s.shutdownCh != nil {
			s.stopBusTaps()
			close(s.shutdownCh)
		}

		// Drain HTTP server (stops accepting, waits for in-flight requests).
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("http shutdown error", "error", err)
		}

		// Drain gRPC server with timeout — fallback to force Stop if it hangs.
		if grpcServer != nil {
			grpcDone := make(chan struct{})
			go func() {
				grpcServer.GracefulStop()
				close(grpcDone)
			}()
			select {
			case <-grpcDone:
				slog.Info("gRPC server drained")
			case <-shutdownCtx.Done():
				slog.Warn("gRPC graceful stop timed out, forcing")
				grpcServer.Stop()
			}
		}

		if basic := basicAuthProvider(s.auth); basic != nil {
			basic.DrainUsage()
		}

		// Shut down metrics server.
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			slog.Error("metrics shutdown error", "error", err)
		}
	}()

	if err := func() error {
		if httpReloader != nil {
			return srv.ListenAndServeTLS("", "")
		}
		return srv.ListenAndServe()
	}(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			// Wait for the shutdown goroutine to finish draining all servers
			// before returning, so defers (s.Close, store closes) fire AFTER
			// in-flight handlers complete.
			<-shutdownDone
			slog.Info("http server closed")
			return nil
		}
		slog.Error("http server error", "error", err)
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// instrumented wraps handlers to record metrics and debug logging.
func (s *server) instrumented(route string, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := loggerFromContext(r.Context())
		logger.Debug("handler entry", "route", route)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fn(rec, r)
		duration := time.Since(start)
		logger.Debug("handler exit", "route", route, "status", rec.status, "duration", duration.String())
		if s.metrics != nil {
			s.metrics.ObserveRequest(r.Method, route, fmt.Sprintf("%d", rec.status), duration.Seconds())
		}
		if exporter, ok := s.auth.(AuditExporter); ok {
			authCtx := authFromRequest(r)
			event := AuditEvent{
				Time:       start.UTC(),
				Method:     r.Method,
				Route:      route,
				Path:       r.URL.Path,
				Status:     rec.status,
				DurationMs: duration.Milliseconds(),
				RemoteAddr: r.RemoteAddr,
				UserAgent:  r.UserAgent(),
				RequestID:  strings.TrimSpace(r.Header.Get("X-Request-Id")),
			}
			if authCtx != nil {
				event.Tenant = authCtx.Tenant
				event.Principal = authCtx.PrincipalID
				event.Role = authCtx.Role
				event.AuthSource = authCtx.AuthSource
			}
			if err := exporter.ExportAudit(r.Context(), event); err != nil {
				slog.Error("audit export failed", "error", err)
			}
		}
	}
}

func migrateLegacyPolicyBundles(ctx context.Context, svc *configsvc.Service) (bool, int, error) {
	if svc == nil {
		return false, 0, nil
	}
	defaultDoc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("load system/default config: %w", err)
	}
	if defaultDoc.Data == nil {
		return false, 0, nil
	}
	rawLegacyBundles := normalizeJSON(defaultDoc.Data[policyConfigKey])
	legacyBundles, ok := rawLegacyBundles.(map[string]any)
	if !ok || len(legacyBundles) == 0 {
		return false, 0, nil
	}
	if err := svc.SetWithRetry(ctx, configsvc.ScopeSystem, policyConfigID, 3, func(doc *configsvc.Document) error {
		if doc.Data == nil {
			doc.Data = map[string]any{}
		}
		rawPolicyBundles := normalizeJSON(doc.Data[policyConfigKey])
		policyBundles, _ := rawPolicyBundles.(map[string]any)
		if policyBundles == nil {
			policyBundles = map[string]any{}
		}
		for fragmentID, bundle := range legacyBundles {
			if _, exists := policyBundles[fragmentID]; exists {
				continue
			}
			policyBundles[fragmentID] = deepCopy(bundle)
		}
		doc.Data[policyConfigKey] = policyBundles
		return nil
	}); err != nil {
		return false, 0, fmt.Errorf("merge legacy bundles into system/%s: %w", policyConfigID, err)
	}
	if err := deleteSystemDefaultKeyWithRetry(ctx, svc, policyConfigKey, 3); err != nil {
		return false, 0, fmt.Errorf("remove legacy bundles from system/default: %w", err)
	}
	return true, len(legacyBundles), nil
}

func deleteSystemDefaultKeyWithRetry(ctx context.Context, svc *configsvc.Service, key string, maxAttempts int) error {
	for attempt := range maxAttempts {
		doc, err := svc.Get(ctx, configsvc.ScopeSystem, "default")
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil
			}
			return fmt.Errorf("load system/default config: %w", err)
		}
		if doc.Data == nil {
			return nil
		}
		if _, exists := doc.Data[key]; !exists {
			return nil
		}
		delete(doc.Data, key)
		if err := svc.Set(ctx, doc); err != nil {
			if errors.Is(err, configsvc.ErrRevisionConflict) && attempt < maxAttempts-1 {
				continue
			}
			return err
		}
		return nil
	}
	return configsvc.ErrRevisionConflict
}

// AuditEvent captures an HTTP request summary for audit export.
type AuditEvent struct {
	Time       time.Time  `json:"time"`
	Method     string     `json:"method"`
	Route      string     `json:"route"`
	Path       string     `json:"path"`
	Status     int        `json:"status"`
	DurationMs int64      `json:"duration_ms"`
	RemoteAddr string     `json:"remote_addr"`
	UserAgent  string     `json:"user_agent"`
	Tenant     string     `json:"tenant"`
	Principal  string     `json:"principal"`
	Role       string     `json:"role"`
	AuthSource AuthSource `json:"auth_source,omitempty"`
	RequestID  string     `json:"request_id"`
}

// AuditExporter allows auth providers to emit audit events.
type AuditExporter interface {
	ExportAudit(ctx context.Context, event AuditEvent) error
}

type LicenseInfo = licensing.LicenseInfo

type LicenseInfoProvider = licensing.LicenseInfoProvider
