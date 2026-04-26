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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/governance"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/health"
	"github.com/cordum/cordum/core/infra/locks"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	cordumotel "github.com/cordum/cordum/core/infra/otel"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/infra/tlsreload"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policyshadow"
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
	maxPromptChars              = 50000
	defaultRateLimitRPS         = 30
	defaultRateLimitBurst       = 50
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
	memStore              store.Store
	jobStore              *store.RedisJobStore // Typed for ListRecentJobs
	decisionLogStore      model.DecisionLogStore
	copilotStore          copilot.Store
	governanceHealthCache *governance.Cache
	routeTable            []routeInfo
	// approvalAnalyticsCache memoises approval-analytics responses
	// per (tenant, window, group_by, limit) for
	// approvalAnalyticsCacheTTL (30s) to smooth dashboard polling.
	approvalAnalyticsCache *approvalAnalyticsMemCache
	bus                    model.Bus
	workers                map[string]*pb.Heartbeat
	workerSeen             map[string]time.Time
	workerMu               sync.RWMutex

	clients             map[*websocket.Conn]*wsClient
	clientsMu           sync.RWMutex
	eventsCh            chan wsEvent
	wsClientBufSz       int
	recentWSDisconnects sync.Map
	wsSummaryOnce       sync.Once

	metrics         infraMetrics.GatewayMetrics
	otelMetrics     *cordumotel.GatewayMetricsBridge
	approvalMetrics infraMetrics.ApprovalMetrics
	tenant          string
	started         time.Time
	auth            auth.AuthProvider
	entitlements    *licensing.EntitlementResolver
	telemetry       *telemetry.Collector
	telemetryState  *telemetry.Store

	workflowStore         *wf.RedisStore
	workflowEng           *wf.Engine
	configSvc             *configsvc.Service
	topicRegistry         *topicregistry.Service
	workerCredentialStore *workercredentials.Service
	agentIdentityStore    *store.AgentIdentityStore
	// evalDatasetStore holds curated, immutable policy-regression test
	// fixtures that the sibling eval-runner task (epic-e1c4321a) will
	// replay through the policy engine. Only the CRUD surface lives in
	// this field — the coupling to replay is intentionally deferred so
	// this task stays scope-clean.
	evalDatasetStore  model.EvalDatasetStore
	evalRunStore      *store.EvalRunStore
	dlqStore          *store.DLQStore
	artifactStore     artifacts.Store
	lockStore         locks.Store
	schemaRegistry    *schema.Registry
	schemaEnforcement schema.EnforcementMode
	safetyConn        *grpc.ClientConn
	safetyClient      pb.SafetyKernelClient
	userStore         auth.UserStore
	keyStore          auth.KeyStore
	rbacStore         *auth.RBACStore
	permChecker       *auth.PermissionChecker

	auditExporter     audit.AuditSender
	auditChainer      *audit.Chainer
	legalHoldStore    *audit.LegalHoldStore
	statusCacheObj    *statusCache
	policyShadowStore *policyshadow.Store
	mcpDenyRing       *denyEventRing
	sessionIssuer     *scheduler.SessionTokenIssuer
	trustResolver     *scheduler.TrustResolver
	heartbeatMode     scheduler.HeartbeatMode

	apiRL    rateLimiter
	publicRL rateLimiter

	instanceRegistry *registry.InstanceRegistry
	instanceID       string

	marketplaceMu    sync.Mutex
	marketplaceCache packs.MarketplaceCache
	stopBusTapsOnce  sync.Once
	eventsStopped    atomic.Bool
	shutdownCh       chan struct{}

	workerExpireStop chan struct{}
	workerExpireOnce sync.Once
	probes           *health.ProbeServer
}

type routeInfo struct {
	Method string
	Path   string
	Auth   string
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
		if ks, ok := s.keyStore.(*auth.RedisKeyStore); ok {
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
func RunWithAuth(cfg *config.Config, provider auth.AuthProvider, entitlementResolvers ...*licensing.EntitlementResolver) error {
	if cfg == nil {
		cfg = config.Load()
	}
	entitlementResolver := resolveEntitlementResolver(entitlementResolvers...)

	if _, err := cordumotel.InitTracer("cordum-api-gateway"); err != nil {
		slog.Error("otel tracer init failed", "error", err)
	}
	if err := cordumotel.InitMetrics("cordum-api-gateway"); err != nil {
		slog.Error("otel metrics init failed", "error", err)
	}
	defer func() {
		if err := cordumotel.Shutdown(context.Background()); err != nil {
			slog.Error("otel tracer shutdown failed", "error", err)
		}
		if err := cordumotel.ShutdownMetrics(); err != nil {
			slog.Error("otel metrics shutdown failed", "error", err)
		}
	}()

	grpcAddr := addrFromEnv(envGatewayGrpcAddr, defaultGrpcAddr)
	httpAddr := addrFromEnv(envGatewayHTTPAddr, defaultHttpAddr)
	metricsAddr := addrFromEnv(envGatewayMetricsAddr, defaultMetricsAddr)

	tenantID := strings.TrimSpace(os.Getenv("TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}

	gwMetrics := infraMetrics.NewGatewayProm("cordum_api_gateway")
	otelGwMetrics := cordumotel.NewGatewayMetricsBridge()
	approvalMetrics := infraMetrics.NewApprovalProm("cordum")
	var userStore auth.UserStore
	var keyStore auth.KeyStore
	var err error
	userAuthRequested := env.Bool("CORDUM_USER_AUTH_ENABLED")
	samlRequested := samlConfiguredFromEnv()
	oidcFlowRequested := oidcFlowConfiguredFromEnv()
	scimRequested := scimConfiguredFromEnv() || entitlementResolver.Entitlements().SCIM
	var basic *auth.BasicAuthProvider
	if provider == nil {
		basic, err = auth.NewBasicAuthProvider(tenantID)
		if err != nil {
			return fmt.Errorf("init auth: %w", err)
		}
		provider = basic
	} else {
		basic = auth.ExtractBasicAuth(provider)
		if usp, ok := provider.(auth.UserStoreProvider); ok {
			userStore = usp.UserStore()
		}
	}

	if userStore == nil && (userAuthRequested || samlRequested || oidcFlowRequested || scimRequested) {
		us, err := auth.NewRedisUserStore(cfg.RedisURL)
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
		ks, err := auth.NewRedisKeyStore(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("init key store: %w", err)
		}
		keyStore = ks
		basic.SetKeyStore(ks)

		if strings.TrimSpace(os.Getenv("CORDUM_ADMIN_PASSWORD")) == "" {
			return fmt.Errorf("cordum_user_auth_enabled is set but cordum_admin_password is empty; set cordum_admin_password to configure the admin account")
		}

		if err := auth.SeedDefaultAdminUser(context.Background(), basic.UserStore(), tenantID); err != nil {
			slog.Error("seed admin user failed", "error", err)
		}
	}

	// Initialize RBAC store
	var rbacStore *auth.RBACStore
	var permChecker *auth.PermissionChecker
	rbacStore, err = auth.NewRBACStore(cfg.RedisURL)
	if err != nil {
		slog.Warn("rbac store init failed, advanced RBAC unavailable", "error", err)
	} else {
		if err := rbacStore.BootstrapDefaultRoles(context.Background()); err != nil {
			slog.Warn("rbac bootstrap default roles failed", "error", err)
		}
		permChecker = auth.NewPermissionChecker(rbacStore, func() licensing.Entitlements {
			return entitlementResolver.Entitlements()
		})
	}

	authProviders := []auth.AuthProvider{provider}

	oidcProvider, err := auth.NewOIDCProviderFromEnv()
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
		authProviders = append(authProviders, auth.NewOIDCAuthAdapter(oidcProvider, tenantID))
		if oidcFlowRequested {
			oidcFlow, err := auth.NewOIDCFlowAdapter(oidcProvider, userStore, tenantID, entitlementResolver)
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
		samlService, err := auth.NewSAMLService(userStore, tenantID, entitlementResolver)
		if err != nil {
			return fmt.Errorf("init saml: %w", err)
		}
		if samlService != nil && samlService.Enabled() {
			authProviders = append(authProviders, samlService)
		}
	}

	if scimRequested {
		scimService, err := auth.NewSCIMService(userStore, tenantID, entitlementResolver)
		if err != nil {
			return fmt.Errorf("init scim: %w", err)
		}
		if scimService != nil && scimService.Enabled() {
			authProviders = append(authProviders, scimService)
		}
	}

	if len(authProviders) > 1 {
		composite, err := auth.NewCompositeAuthProvider(authProviders...)
		if err != nil {
			return fmt.Errorf("init composite auth: %w", err)
		}
		provider = composite
	}

	if env.Bool("CORDUM_DASHBOARD_EMBED_API_KEY") {
		// Refuse to start in production. The flag bakes the system API
		// key into /config.json, served unauthenticated by nginx — anyone
		// who can reach the dashboard URL gets the key. Allowed in dev
		// for local kiosk-style smoke tests; prohibited in prod no
		// matter what the operator put in their .env.
		if env.IsProduction() {
			return fmt.Errorf("refusing to start: CORDUM_DASHBOARD_EMBED_API_KEY=true is forbidden in production — the API key would be served unauthenticated at /config.json (set CORDUM_DASHBOARD_EMBED_API_KEY=false or unset CORDUM_PRODUCTION/CORDUM_ENV to override)")
		}
		// Always-on warning in non-prod so dev operators don't run this
		// silently. Emits both as a structured slog.Error and a coloured
		// stderr banner so it can't get lost in startup log volume.
		slog.Error("SECURITY WARNING: CORDUM_DASHBOARD_EMBED_API_KEY is enabled — API key will be exposed in browser JavaScript at /config.json (use only in trusted dev environments)")
		fmt.Fprintln(os.Stderr, "\x1b[1;31m"+strings.Repeat("=", 78)+"\x1b[0m")
		fmt.Fprintln(os.Stderr, "\x1b[1;31m  SECURITY WARNING  CORDUM_DASHBOARD_EMBED_API_KEY=true\x1b[0m")
		fmt.Fprintln(os.Stderr, "\x1b[1;31m  /config.json now contains the API key in plaintext.\x1b[0m")
		fmt.Fprintln(os.Stderr, "\x1b[1;31m  Use only in trusted dev environments. NEVER in production.\x1b[0m")
		fmt.Fprintln(os.Stderr, "\x1b[1;31m"+strings.Repeat("=", 78)+"\x1b[0m")
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

	decisionLogStore, err := store.NewRedisDecisionLogStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis decision log store: %w", err)
	}
	defer func() { _ = decisionLogStore.Close() }()

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
	if err := packs.SeedDefaultPackCatalogs(context.Background(), configSvc); err != nil {
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

	auditSender, auditChainer, err := initAuditPipeline(jobStore.Client(), natsBus, entitlementResolver)
	if err != nil {
		return fmt.Errorf("init audit exporter: %w", err)
	}

	s := &server{
		memStore:               memStore,
		jobStore:               jobStore,
		decisionLogStore:       decisionLogStore,
		copilotStore:           copilot.NotImplementedStore{},
		governanceHealthCache:  governance.NewCache(60 * time.Second),
		approvalAnalyticsCache: newApprovalAnalyticsCache(),
		bus:                    natsBus,
		workers:                make(map[string]*pb.Heartbeat),
		workerSeen:             make(map[string]time.Time),
		clients:                make(map[*websocket.Conn]*wsClient),
		eventsCh:               make(chan wsEvent, 512),
		wsClientBufSz:          wsClientBufferSize(),
		metrics:                gwMetrics,
		otelMetrics:            otelGwMetrics,
		approvalMetrics:        approvalMetrics,
		tenant:                 tenantID,
		auth:                   provider,
		entitlements:           entitlementResolver,
		started:                time.Now().UTC(),
		workflowStore:          workflowStore,
		workflowEng:            workflowEng,
		configSvc:              configSvc,
		topicRegistry:          topicregistry.NewService(configSvc),
		workerCredentialStore:  workercredentials.NewService(configSvc),
		agentIdentityStore:     store.NewAgentIdentityStoreFromClient(jobStore.Client()),
		evalDatasetStore:       store.NewEvalDatasetStoreFromClient(jobStore.Client()),
		evalRunStore:           store.NewEvalRunStoreFromClient(jobStore.Client()),
		dlqStore:               dlqStore,
		artifactStore:          artifactStore,
		lockStore:              lockStore,
		schemaRegistry:         schemaRegistry,
		schemaEnforcement:      schema.ParseEnforcementMode(os.Getenv("SCHEMA_ENFORCEMENT")),
		safetyConn:             safetyConn,
		safetyClient:           safetyClient,
		userStore:              userStore,
		keyStore:               keyStore,
		rbacStore:              rbacStore,
		permChecker:            permChecker,
		auditExporter:          auditSender,
		auditChainer:           auditChainer,
		legalHoldStore:         initLegalHoldStore(cfg.RedisURL),
		statusCacheObj:         newStatusCache(2 * time.Second),
		policyShadowStore:      policyshadow.NewStore(configSvc),
		mcpDenyRing:            newDenyEventRing(500),
		trustResolver:          scheduler.NewTrustResolver(jobStore.Client()),
		heartbeatMode:          scheduler.ParseHeartbeatMode(os.Getenv(scheduler.EnvHeartbeatMode)),
		shutdownCh:             make(chan struct{}),
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
		s.publishConfigChanged(string(configsvc.ScopeSystem), packs.PolicyConfigID)
		slog.Info("gateway startup migrated legacy policy bundles",
			"from_scope", "system/default",
			"to_scope", "system/"+packs.PolicyConfigID,
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
	if !s.requireLicensePermission(w, r, licensing.BreakGlassPermissionSystemHealth) {
		return
	}

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

func initAuditPipeline(client redis.UniversalClient, natsBus audit.AuditBus, entitlementResolver *licensing.EntitlementResolver) (audit.AuditSender, *audit.Chainer, error) {
	// The Redis-backed audit chain is the tamper-evident primary record. It
	// runs unconditionally at boot so a blocked SIEM entitlement or an
	// operator who explicitly opts out of external SIEM export cannot
	// silently disable the chain — exporters are consumers of the chain, not
	// a prerequisite for it.
	auditChainer := audit.NewChainer(client, "")
	chainFailMode := audit.ParseChainFailMode(os.Getenv(audit.EnvChainFailMode))
	slog.Info("audit chain enabled",
		"stream_prefix", audit.ChainKeyPrefix,
		"fail_mode", chainFailMode,
		"tenant_isolation", "per-tenant stream audit:chain:<tenant>",
	)

	bufExporter, err := audit.NewExporterFromEnvWithEntitlements(entitlementResolver)
	if err != nil {
		return nil, nil, err
	}

	if bufExporter == nil {
		slog.Info("audit chain active, no external SIEM export",
			"reason", "export type empty/none or SIEM entitlement blocked for plan",
		)
		return newAuditChainSender(auditChainer, nil), auditChainer, nil
	}

	transport := strings.ToLower(strings.TrimSpace(os.Getenv("AUDIT_TRANSPORT")))
	if transport == "nats" && natsBus != nil {
		// The NATS consumer (queue-grouped across replicas) performs the
		// chain append via audit.WithChainer. Do NOT wrap the publisher
		// with newAuditChainSender here — that would double-append events
		// to the chain at publish time and again at consume time.
		auditSender := audit.NewNATSAuditPublisher(natsBus, bufExporter)
		if _, err := audit.NewNATSAuditConsumer(
			natsBus,
			bufExporter.Backend(),
			audit.WithChainer(auditChainer),
			audit.WithChainFailMode(chainFailMode),
		); err != nil {
			slog.Warn("audit NATS consumer failed to start; events will publish to NATS but local chain verification may lag", "error", err)
		}
		return auditSender, auditChainer, nil
	}

	// Direct transport: chain the event synchronously at the gateway, then
	// forward to the buffered exporter. This fixes a latent bug where
	// direct-mode gateways only saw chain appends when a NATS consumer
	// happened to be reading the stream.
	return newAuditChainSender(auditChainer, bufExporter), auditChainer, nil
}

func newHTTPHandler(s *server) (http.Handler, error) {
	mux := http.NewServeMux()
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
	if err := s.registerRoutes(mux); err != nil {
		return nil, err
	}

	// Middleware chain: logging → CORS → rate limit → auth → read audit → tenant → body limit → mux
	// SECURITY: Rate limiter MUST run before auth so that invalid API key
	// brute-force attempts are rate-limited by IP. When auth context is
	// absent, rateLimitKey falls back to IP-based keying automatically.
	readAuditRate := parseFloatEnv("CORDUM_AUDIT_READ_SAMPLE_RATE", 0.0)
	inner := auditReadMiddleware(s.auditExporter, readAuditRate, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
	return requestLoggingMiddleware(tracingMiddleware(corsMiddleware(rateLimitMiddleware(s.auth, s.apiRL, s.publicRL, apiKeyMiddleware(s.auth, inner, s.auditExporter))))), nil
}

func startHTTPServer(s *server, httpAddr, metricsAddr string, grpcServer *grpc.Server) error {
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

	handler, err := newHTTPHandler(s)
	if err != nil {
		return err
	}

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
	if basic := auth.ExtractBasicAuth(s.auth); basic != nil {
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

		if basic := auth.ExtractBasicAuth(s.auth); basic != nil {
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

func (s *server) registerRoutes(mux *http.ServeMux) error {
	s.routeTable = s.routeTable[:0]
	s.probes.Register(mux)
	s.registerRoute(mux, "GET /health", s.handleHealth)
	s.registerRoute(mux, "GET /api/v1/health", s.instrumented("/api/v1/health", s.handleHealth))

	// 1.5 Auth config (public)
	s.registerRoute(mux, "GET /api/v1/auth/config", s.instrumented("/api/v1/auth/config", s.handleAuthConfig))
	s.registerRoute(mux, "PUT /api/v1/auth/oidc/group-role-mapping", s.instrumented("/api/v1/auth/oidc/group-role-mapping", s.handleUpdateOIDCGroupRoleMapping))

	// 1.6 Auth endpoints
	s.registerRoute(mux, "POST /api/v1/auth/login", s.instrumented("/api/v1/auth/login", s.handleLogin))
	s.registerRoute(mux, "GET /api/v1/auth/session", s.instrumented("/api/v1/auth/session", s.handleSession))
	s.registerRoute(mux, "POST /api/v1/auth/logout", s.instrumented("/api/v1/auth/logout", s.handleLogout))
	s.registerRoute(mux, "POST /api/v1/auth/password", s.instrumented("/api/v1/auth/password", s.handleChangePassword))

	// 1.7 User management (admin only)
	s.registerRoute(mux, "POST /api/v1/users", s.instrumented("/api/v1/users", s.handleCreateUser))
	s.registerRoute(mux, "GET /api/v1/users", s.instrumented("/api/v1/users", s.handleListUsers))
	s.registerRoute(mux, "PUT /api/v1/users/{id}", s.instrumented("/api/v1/users/{id}", s.handleUpdateUser))
	s.registerRoute(mux, "DELETE /api/v1/users/{id}", s.instrumented("/api/v1/users/{id}", s.handleDeleteUser))
	s.registerRoute(mux, "POST /api/v1/users/{id}/password", s.instrumented("/api/v1/users/{id}/password", s.handleChangeUserPassword))

	// 1.8 API Key management (admin only)
	s.registerRoute(mux, "GET /api/v1/auth/keys", s.instrumented("/api/v1/auth/keys", s.handleListKeys))
	s.registerRoute(mux, "POST /api/v1/auth/keys", s.instrumented("/api/v1/auth/keys", s.handleCreateKey))
	s.registerRoute(mux, "DELETE /api/v1/auth/keys/{id}", s.instrumented("/api/v1/auth/keys/{id}", s.handleRevokeKey))

	// 1.9 RBAC role management (admin only, entitlement-gated)
	s.registerRoute(mux, "GET /api/v1/auth/roles", s.instrumented("/api/v1/auth/roles", s.handleListRoles))
	s.registerRoute(mux, "GET /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handleGetRole))
	s.registerRoute(mux, "PUT /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handlePutRole))
	s.registerRoute(mux, "DELETE /api/v1/auth/roles/{name}", s.instrumented("/api/v1/auth/roles/{name}", s.handleDeleteRole))

	// 2. Workers (RPC via NATS)
	s.registerRoute(mux, "GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))
	s.registerRoute(mux, "GET /api/v1/workers/{id}", s.instrumented("/api/v1/workers/{id}", s.handleGetWorker))
	s.registerRoute(mux, "GET /api/v1/workers/{id}/jobs", s.instrumented("/api/v1/workers/{id}/jobs", s.handleGetWorkerJobs))
	s.registerRoute(mux, "GET /api/v1/workers/credentials", s.instrumented("/api/v1/workers/credentials", s.handleListWorkerCredentials))
	s.registerRoute(mux, "POST /api/v1/workers/credentials", s.instrumented("/api/v1/workers/credentials", s.handleCreateWorkerCredential))
	s.registerRoute(mux, "DELETE /api/v1/workers/credentials/{worker_id}", s.instrumented("/api/v1/workers/credentials/{worker_id}", s.handleDeleteWorkerCredential))

	// 2.1 Agent Identities (admin only)
	s.registerRoute(mux, "GET /api/v1/agents", s.instrumented("/api/v1/agents", s.handleListAgents))
	s.registerRoute(mux, "POST /api/v1/agents", s.instrumented("/api/v1/agents", s.handleCreateAgent))
	s.registerRoute(mux, "GET /api/v1/agents/{id}", s.instrumented("/api/v1/agents/{id}", s.handleGetAgent))
	s.registerRoute(mux, "PUT /api/v1/agents/{id}", s.instrumented("/api/v1/agents/{id}", s.handleUpdateAgent))
	s.registerRoute(mux, "DELETE /api/v1/agents/{id}", s.instrumented("/api/v1/agents/{id}", s.handleDeleteAgent))
	s.registerRoute(mux, "GET /api/v1/agents/{id}/stats", s.instrumented("/api/v1/agents/{id}/stats", s.handleAgentStats))
	s.registerRoute(mux, "GET /api/v1/agents/{id}/delegations", s.instrumented("/api/v1/agents/{id}/delegations", s.handleListAgentDelegations))
	s.registerRoute(mux, "POST /api/v1/agents/{id}/delegate", s.instrumented("/api/v1/agents/{id}/delegate", s.handleDelegateAgent))
	s.registerRoute(mux, "GET /api/v1/delegations", s.instrumented("/api/v1/delegations", s.handleListDelegations))
	s.registerRoute(mux, "POST /api/v1/agents/verify-delegation", s.instrumented("/api/v1/agents/verify-delegation", s.handleVerifyDelegation))
	s.registerRoute(mux, "POST /api/v1/agents/revoke-delegation", s.instrumented("/api/v1/agents/revoke-delegation", s.handleRevokeDelegation))

	s.registerRoute(mux, "GET /api/v1/pools", s.instrumented("/api/v1/pools", s.handleListPools))
	s.registerRoute(mux, "GET /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleGetPool))
	s.registerRoute(mux, "GET /api/v1/topics", s.instrumented("/api/v1/topics", s.handleListTopics))
	s.registerRoute(mux, "POST /api/v1/topics", s.instrumented("/api/v1/topics", s.handleCreateTopic))
	s.registerRoute(mux, "DELETE /api/v1/topics/{name}", s.instrumented("/api/v1/topics/{name}", s.handleDeleteTopic))
	s.registerRoute(mux, "PUT /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleCreatePool))
	s.registerRoute(mux, "PATCH /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleUpdatePool))
	s.registerRoute(mux, "DELETE /api/v1/pools/{name}", s.instrumented("/api/v1/pools/{name}", s.handleDeletePool))
	s.registerRoute(mux, "POST /api/v1/pools/{name}/drain", s.instrumented("/api/v1/pools/{name}/drain", s.handleDrainPool))
	s.registerRoute(mux, "PUT /api/v1/pools/{name}/topics/{topic}", s.instrumented("/api/v1/pools/{name}/topics/{topic}", s.handleAddTopicToPool))
	s.registerRoute(mux, "DELETE /api/v1/pools/{name}/topics/{topic}", s.instrumented("/api/v1/pools/{name}/topics/{topic}", s.handleRemoveTopicFromPool))

	// 2.5 Status snapshot (Redis/NATS/workers/uptime)
	s.registerRoute(mux, "GET /api/v1/status", s.instrumented("/api/v1/status", s.handleStatus))
	s.registerRoute(mux, "GET /api/v1/license", s.instrumented("/api/v1/license", s.handleGetLicense))
	s.registerRoute(mux, "GET /api/v1/license/usage", s.instrumented("/api/v1/license/usage", s.handleGetLicenseUsage))
	s.registerRoute(mux, "POST /api/v1/license/reload", s.instrumented("/api/v1/license/reload", s.handleReloadLicense))
	s.registerRoute(mux, "GET /api/v1/telemetry/status", s.instrumented("/api/v1/telemetry/status", s.handleGetTelemetryStatus))
	s.registerRoute(mux, "GET /api/v1/telemetry/inspect", s.instrumented("/api/v1/telemetry/inspect", s.handleGetTelemetryInspect))
	s.registerRoute(mux, "GET /api/v1/telemetry/export", s.instrumented("/api/v1/telemetry/export", s.handleGetTelemetryExport))
	s.registerRoute(mux, "GET /api/v1/telemetry/usage", s.instrumented("/api/v1/telemetry/usage", s.handleGetTelemetryUsage))
	s.registerRoute(mux, "POST /api/v1/telemetry/consent", s.instrumented("/api/v1/telemetry/consent", s.handleSetTelemetryConsent))

	// 2.6 Admin endpoints (read-only, admin auth required)
	s.registerRoute(mux, "GET /api/v1/admin/locks", s.instrumented("/api/v1/admin/locks", s.handleAdminLocks))

	// 2.7 Audit export management (admin only, entitlement-gated)
	// 2.7 Audit export — main endpoint plus operational sub-routes.
	// The top-level GET /api/v1/audit/export was missing despite the
	// handler being fully implemented in handlers_audit_compliance.go:61
	// (same wire-up gap class as /api/v1/audit/verify below).
	s.registerRoute(mux, "GET /api/v1/audit/export", s.instrumented("/api/v1/audit/export", s.handleAuditExport))
	s.registerRoute(mux, "GET /api/v1/audit/export/health", s.instrumented("/api/v1/audit/export/health", s.handleAuditExportHealth))
	s.registerRoute(mux, "GET /api/v1/audit/export/config", s.instrumented("/api/v1/audit/export/config", s.handleAuditExportConfig))
	s.registerRoute(mux, "POST /api/v1/audit/export/test", s.instrumented("/api/v1/audit/export/test", s.handleAuditExportTest))

	// 2.7.1 Audit chain verify (admin only) — handler lives in
	// handlers_audit_verify.go; missing this line was a wire-up regression
	// that had /api/v1/audit/verify 404ing on fresh deploys despite the
	// handler being fully implemented and unit-tested.
	s.registerRoute(mux, "GET /api/v1/audit/verify", s.instrumented("/api/v1/audit/verify", s.handleAuditVerify))
	s.registerRoute(mux, "GET /api/v1/governance/health", s.instrumented("/api/v1/governance/health", s.handleGovernanceHealth))

	// 2.8 Legal hold management (admin only, entitlement-gated)
	s.registerRoute(mux, "POST /api/v1/audit/legal-hold", s.instrumented("/api/v1/audit/legal-hold", s.handleCreateLegalHold))
	s.registerRoute(mux, "GET /api/v1/audit/legal-holds", s.instrumented("/api/v1/audit/legal-holds", s.handleListLegalHolds))
	s.registerRoute(mux, "DELETE /api/v1/audit/legal-hold/{id}", s.instrumented("/api/v1/audit/legal-hold/{id}", s.handleReleaseLegalHold))

	// 3. Jobs (Redis ZSet)
	s.registerRoute(mux, "GET /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleListJobs))
	s.registerRoute(mux, "GET /api/v1/copilot/sessions/{sessionId}", s.instrumented("/api/v1/copilot/sessions/:sessionId", s.handleGetCopilotSession))

	// 4. Job Details
	s.registerRoute(mux, "GET /api/v1/jobs/{id}", s.instrumented("/api/v1/jobs/{id}", s.handleGetJob))
	s.registerRoute(mux, "GET /api/v1/jobs/{id}/stream", s.instrumented("/api/v1/jobs/{id}/stream", s.handleJobStream))
	s.registerRoute(mux, "GET /api/v1/jobs/{id}/decisions", s.instrumented("/api/v1/jobs/{id}/decisions", s.handleListJobDecisions))
	s.registerRoute(mux, "POST /api/v1/jobs/{id}/cancel", s.instrumented("/api/v1/jobs/{id}/cancel", s.handleCancelJob))
	s.registerRoute(mux, "POST /api/v1/jobs/{id}/remediate", s.instrumented("/api/v1/jobs/{id}/remediate", s.handleRemediateJob))

	// 4.5 Memory pointers (debug)
	s.registerRoute(mux, "GET /api/v1/memory", s.instrumented("/api/v1/memory", s.handleGetMemory))
	// 4.6 Artifact store
	s.registerRoute(mux, "POST /api/v1/artifacts", s.instrumented("/api/v1/artifacts", s.handlePutArtifact))
	s.registerRoute(mux, "GET /api/v1/artifacts/{ptr}", s.instrumented("/api/v1/artifacts/{ptr}", s.handleGetArtifact))

	// 5. Submit Job (REST)
	s.registerRoute(mux, "POST /api/v1/jobs", s.instrumented("/api/v1/jobs", s.handleSubmitJobHTTP))

	// 6. Trace Details
	s.registerRoute(mux, "GET /api/v1/traces/{id}", s.instrumented("/api/v1/traces/{id}", s.handleGetTrace))

	// 8. Workflows
	s.registerRoute(mux, "GET /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleListWorkflows))
	s.registerRoute(mux, "POST /api/v1/workflows", s.instrumented("/api/v1/workflows", s.handleCreateWorkflow))
	s.registerRoute(mux, "GET /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleGetWorkflow))
	s.registerRoute(mux, "DELETE /api/v1/workflows/{id}", s.instrumented("/api/v1/workflows/{id}", s.handleDeleteWorkflow))
	s.registerRoute(mux, "POST /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleStartRun))
	s.registerRoute(mux, "GET /api/v1/workflows/{id}/runs", s.instrumented("/api/v1/workflows/{id}/runs", s.handleListRuns))
	s.registerRoute(mux, "GET /api/v1/workflow-runs", s.instrumented("/api/v1/workflow-runs", s.handleListAllRuns))
	s.registerRoute(mux, "GET /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleGetRun))
	s.registerRoute(mux, "GET /api/v1/workflow-runs/{id}/timeline", s.instrumented("/api/v1/workflow-runs/{id}/timeline", s.handleGetRunTimeline))
	s.registerRoute(mux, "GET /api/v1/workflow-runs/{id}/chat", s.instrumented("/api/v1/workflow-runs/{id}/chat", s.handleGetRunChat))
	s.registerRoute(mux, "POST /api/v1/workflow-runs/{id}/chat", s.instrumented("/api/v1/workflow-runs/{id}/chat", s.handlePostRunChat))
	s.registerRoute(mux, "DELETE /api/v1/workflow-runs/{id}", s.instrumented("/api/v1/workflow-runs/{id}", s.handleDeleteRun))
	s.registerRoute(mux, "POST /api/v1/workflow-runs/{id}/rerun", s.instrumented("/api/v1/workflow-runs/{id}/rerun", s.handleRerunRun))
	s.registerRoute(mux, "POST /api/v1/workflows/{id}/dry-run", s.instrumented("/api/v1/workflows/{id}/dry-run", s.handleWorkflowDryRun))

	// 9. Config
	s.registerRoute(mux, "GET /api/v1/config", s.instrumented("/api/v1/config", s.handleGetConfig))
	s.registerRoute(mux, "GET /api/v1/config/effective", s.instrumented("/api/v1/config/effective", s.handleGetEffectiveConfig))
	s.registerRoute(mux, "PUT /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))
	s.registerRoute(mux, "POST /api/v1/config", s.instrumented("/api/v1/config", s.handleSetConfig))

	// 9.25 Packs
	s.registerRoute(mux, "GET /api/v1/packs", s.instrumented("/api/v1/packs", s.handleListPacks))
	s.registerRoute(mux, "GET /api/v1/packs/{id}", s.instrumented("/api/v1/packs/{id}", s.handleGetPack))
	s.registerRoute(mux, "POST /api/v1/packs/install", s.instrumented("/api/v1/packs/install", s.handleInstallPack))
	s.registerRoute(mux, "POST /api/v1/packs/{id}/uninstall", s.instrumented("/api/v1/packs/{id}/uninstall", s.handleUninstallPack))
	s.registerRoute(mux, "POST /api/v1/packs/{id}/verify", s.instrumented("/api/v1/packs/{id}/verify", s.handleVerifyPack))
	s.registerRoute(mux, "GET /api/v1/marketplace/packs", s.instrumented("/api/v1/marketplace/packs", s.handleMarketplacePacks))
	s.registerRoute(mux, "POST /api/v1/marketplace/install", s.instrumented("/api/v1/marketplace/install", s.handleMarketplaceInstall))

	// 9.5 Schemas
	s.registerRoute(mux, "POST /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleRegisterSchema))
	s.registerRoute(mux, "GET /api/v1/schemas", s.instrumented("/api/v1/schemas", s.handleListSchemas))
	s.registerRoute(mux, "GET /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleGetSchema))
	s.registerRoute(mux, "DELETE /api/v1/schemas/{id}", s.instrumented("/api/v1/schemas/{id}", s.handleDeleteSchema))

	// 9.6 Resource locks
	s.registerRoute(mux, "GET /api/v1/locks", s.instrumented("/api/v1/locks", s.handleGetLock))
	s.registerRoute(mux, "POST /api/v1/locks/acquire", s.instrumented("/api/v1/locks/acquire", s.handleAcquireLock))
	s.registerRoute(mux, "POST /api/v1/locks/release", s.instrumented("/api/v1/locks/release", s.handleReleaseLock))
	s.registerRoute(mux, "POST /api/v1/locks/renew", s.instrumented("/api/v1/locks/renew", s.handleRenewLock))

	// 10. DLQ
	s.registerRoute(mux, "GET /api/v1/dlq", s.instrumented("/api/v1/dlq", s.handleListDLQ))
	s.registerRoute(mux, "GET /api/v1/dlq/page", s.instrumented("/api/v1/dlq/page", s.handleListDLQPage))
	s.registerRoute(mux, "DELETE /api/v1/dlq/{job_id}", s.instrumented("/api/v1/dlq/{job_id}", s.handleDeleteDLQ))
	s.registerRoute(mux, "POST /api/v1/dlq/{job_id}/retry", s.instrumented("/api/v1/dlq/{job_id}/retry", s.handleRetryDLQ))

	// 11. Workflow run operations
	s.registerRoute(mux, "POST /api/v1/workflows/{id}/runs/{run_id}/cancel", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/cancel", s.handleCancelRun))

	// 11.5 Job approvals
	s.registerRoute(mux, "GET /api/v1/approvals", s.instrumented("/api/v1/approvals", s.handleListApprovals))
	s.registerRoute(mux, "POST /api/v1/approvals/{job_id}/approve", s.instrumented("/api/v1/approvals/{job_id}/approve", s.handleApproveJob))
	s.registerRoute(mux, "POST /api/v1/approvals/{job_id}/reject", s.instrumented("/api/v1/approvals/{job_id}/reject", s.handleRejectJob))
	s.registerRoute(mux, "POST /api/v1/approvals/{job_id}/repair", s.instrumented("/api/v1/approvals/{job_id}/repair", s.handleRepairApproval))
	s.registerRoute(mux, "GET /api/v1/approvals/{job_id}/context", s.instrumented("/api/v1/approvals/{job_id}/context", s.handleApprovalContext))
	s.registerRoute(mux, "GET /api/v1/governance/decisions", s.instrumented("/api/v1/governance/decisions", s.handleListGovernanceDecisions))
	s.registerRoute(mux, "GET /api/v1/governance/approvals/analytics", s.instrumented("/api/v1/governance/approvals/analytics", s.handleApprovalAnalytics))
	s.registerRoute(mux, "GET /api/v1/mcp/approvals", s.instrumented("/api/v1/mcp/approvals", s.handleMCPApprovalList))
	s.registerRoute(mux, "GET /api/v1/mcp/approvals/{id}", s.instrumented("/api/v1/mcp/approvals/{id}", s.handleMCPApprovalGet))
	s.registerRoute(mux, "POST /api/v1/mcp/approvals/{id}/approve", s.instrumented("/api/v1/mcp/approvals/{id}/approve", s.handleMCPApprovalApprove))
	s.registerRoute(mux, "POST /api/v1/mcp/approvals/{id}/reject", s.instrumented("/api/v1/mcp/approvals/{id}/reject", s.handleMCPApprovalReject))
	s.registerRoute(mux, "POST /api/v1/mcp/verify-signature", s.instrumented("/api/v1/mcp/verify-signature", s.handleMCPVerifySignature))
	s.registerRoute(mux, "GET /api/v1/mcp/outbound", s.instrumented("/api/v1/mcp/outbound", s.handleMCPOutbound))
	s.registerRoute(mux, "GET /api/v1/mcp/usage", s.instrumented("/api/v1/mcp/usage", s.handleMCPUsage))
	// MCP tool visibility (dashboard consumes these via src/hooks/useAgentTools.ts):
	s.registerRoute(mux, "GET /api/v1/mcp/tools", s.instrumented("/api/v1/mcp/tools", s.handleListMCPTools))
	s.registerRoute(mux, "GET /api/v1/agents/{id}/tools", s.instrumented("/api/v1/agents/{id}/tools", s.handleAgentToolVisibility))
	s.registerRoute(mux, "GET /api/v1/agents/{id}/denied-events", s.instrumented("/api/v1/agents/{id}/denied-events", s.handleAgentDeniedEvents))

	// 12. Policy endpoints
	s.registerRoute(mux, "POST /api/v1/policy/evaluate", s.instrumented("/api/v1/policy/evaluate", s.handlePolicyEvaluate))
	s.registerRoute(mux, "POST /api/v1/policy/simulate", s.instrumented("/api/v1/policy/simulate", s.handlePolicySimulate))
	s.registerRoute(mux, "POST /api/v1/policy/explain", s.instrumented("/api/v1/policy/explain", s.handlePolicyExplain))
	s.registerRoute(mux, "GET /api/v1/policy/snapshots", s.instrumented("/api/v1/policy/snapshots", s.handlePolicySnapshots))
	s.registerRoute(mux, "GET /api/v1/policy/rules", s.instrumented("/api/v1/policy/rules", s.handlePolicyRules))
	s.registerRoute(mux, "GET /api/v1/policy/output/rules", s.instrumented("/api/v1/policy/output/rules", s.handlePolicyOutputRules))
	s.registerRoute(mux, "GET /api/v1/policy/output/stats", s.instrumented("/api/v1/policy/output/stats", s.handlePolicyOutputStats))
	s.registerRoute(mux, "PUT /api/v1/policy/output/rules/{id}", s.instrumented("/api/v1/policy/output/rules/{id}", s.handlePutPolicyOutputRule))
	s.registerRoute(mux, "GET /api/v1/policy/velocity-rules", s.instrumented("/api/v1/policy/velocity-rules", s.handleVelocityRules))
	s.registerRoute(mux, "GET /api/v1/policy/velocity-rules/stats", s.instrumented("/api/v1/policy/velocity-rules/stats", s.handleVelocityRuleStats))
	s.registerRoute(mux, "POST /api/v1/policy/velocity-rules", s.instrumented("/api/v1/policy/velocity-rules", s.handleCreateVelocityRule))
	s.registerRoute(mux, "PUT /api/v1/policy/velocity-rules/{id}", s.instrumented("/api/v1/policy/velocity-rules/{id}", s.handlePutVelocityRule))
	s.registerRoute(mux, "DELETE /api/v1/policy/velocity-rules/{id}", s.instrumented("/api/v1/policy/velocity-rules/{id}", s.handleDeleteVelocityRule))
	s.registerRoute(mux, "GET /api/v1/policy/bundles", s.instrumented("/api/v1/policy/bundles", s.handlePolicyBundles))
	s.registerRoute(mux, "GET /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleGetPolicyBundle))
	s.registerRoute(mux, "PUT /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handlePutPolicyBundle))
	s.registerRoute(mux, "DELETE /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleDeletePolicyBundle))
	s.registerRoute(mux, "POST /api/v1/policy/bundles/{id}/simulate", s.instrumented("/api/v1/policy/bundles/{id}/simulate", s.handleSimulatePolicyBundle))
	// Policy shadow (task-44807b2c) — shadow-evaluation surface for a bundle.
	// PUT upserts the shadow policy, GET fetches current status, DELETE removes it.
	// The three results endpoints expose dashboard analytics (summary counters,
	// paginated comparisons, and a bucketed timeseries) over the same window.
	//
	// URL layout: `/api/v1/policy/shadows/{id}` rather than nesting under
	// `/bundles/{id}/shadow`. Nesting would overlap with the existing
	// `/api/v1/policy/bundles/snapshots/{id}` at path
	// `/api/v1/policy/bundles/snapshots/shadow` — Go 1.22+ ServeMux treats
	// both patterns as equally specific and panics at registration. Go does
	// not honor third-pattern disambiguators for this case (verified on go
	// 1.25.9). The top-level `/shadows/{id}` collection also mirrors the
	// sibling `/api/v1/policy/snapshots/{id}` pattern so operator muscle
	// memory carries across the two features.
	s.registerRoute(mux, "PUT /api/v1/policy/shadows/{id}", s.instrumented("/api/v1/policy/shadows/{id}", s.handlePutPolicyShadow))
	s.registerRoute(mux, "GET /api/v1/policy/shadows/{id}", s.instrumented("/api/v1/policy/shadows/{id}", s.handleGetPolicyShadow))
	s.registerRoute(mux, "DELETE /api/v1/policy/shadows/{id}", s.instrumented("/api/v1/policy/shadows/{id}", s.handleDeletePolicyShadow))
	s.registerRoute(mux, "GET /api/v1/policy/shadows/{id}/results/summary", s.instrumented("/api/v1/policy/shadows/{id}/results/summary", s.handleShadowResultsSummary))
	s.registerRoute(mux, "GET /api/v1/policy/shadows/{id}/results/comparisons", s.instrumented("/api/v1/policy/shadows/{id}/results/comparisons", s.handleShadowResultsComparisons))
	s.registerRoute(mux, "GET /api/v1/policy/shadows/{id}/results/timeseries", s.instrumented("/api/v1/policy/shadows/{id}/results/timeseries", s.handleShadowResultsTimeseries))
	s.registerRoute(mux, "GET /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleListPolicyBundleSnapshots))
	s.registerRoute(mux, "POST /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleCapturePolicyBundleSnapshot))
	s.registerRoute(mux, "GET /api/v1/policy/bundles/snapshots/{id}", s.instrumented("/api/v1/policy/bundles/snapshots/{id}", s.handleGetPolicyBundleSnapshot))
	s.registerRoute(mux, "POST /api/v1/policy/publish", s.instrumented("/api/v1/policy/publish", s.handlePublishPolicyBundles))
	s.registerRoute(mux, "POST /api/v1/policy/rollback", s.instrumented("/api/v1/policy/rollback", s.handleRollbackPolicyBundles))
	s.registerRoute(mux, "GET /api/v1/policy/audit", s.instrumented("/api/v1/policy/audit", s.handleListPolicyAudit))
	s.registerRoute(mux, "POST /api/v1/policy/replay", s.instrumented("/api/v1/policy/replay", s.handlePolicyReplay))
	s.registerRoute(mux, "POST /api/v1/policy/analytics", s.instrumented("/api/v1/policy/analytics", s.handlePolicyAnalytics))

	// 12.6 Eval datasets — curated, immutable policy-regression fixtures.
	// The sibling eval-runner task (epic-e1c4321a) will replay these
	// through the policy engine. PUT creates a successor version; it does
	// not mutate an existing dataset in place.
	s.registerRoute(mux, "POST /api/v1/evals/datasets/from-incidents", s.instrumented("/api/v1/evals/datasets/from-incidents", s.handleCreateDatasetFromIncidents))
	s.registerRoute(mux, "POST /api/v1/evals/datasets", s.instrumented("/api/v1/evals/datasets", s.handleCreateEvalDataset))
	s.registerRoute(mux, "GET /api/v1/evals/datasets", s.instrumented("/api/v1/evals/datasets", s.handleListEvalDatasets))
	s.registerRoute(mux, "/api/v1/evals/datasets/", s.instrumented("/api/v1/evals/datasets/*", s.handleEvalDatasetSubroutes))
	s.registerRoute(mux, "GET /api/v1/evals/runs/{run_id}", s.instrumented("/api/v1/evals/runs/{run_id}", s.handleGetEvalRun))
	s.registerRoute(mux, "DELETE /api/v1/evals/runs/{run_id}", s.instrumented("/api/v1/evals/runs/{run_id}", s.handleDeleteEvalRun))

	// 12.5 MCP (HTTP/SSE) routes
	if err := s.registerMCPRoutes(mux); err != nil {
		return fmt.Errorf("register mcp routes: %w", err)
	}

	// 7. Stream (WebSocket)
	s.registerRoute(mux, "/api/v1/stream", s.instrumented("/api/v1/stream", s.handleStream))

	// Extension routes (enterprise auth, SSO, etc.)
	if registrar, ok := s.auth.(auth.RouteRegistrar); ok {
		registrar.RegisterRoutes(mux, s.instrumented)
	}

	for _, route := range s.adminRoutes() {
		slog.Info("route.registered",
			"method", route.Method,
			"path", route.Path,
			"auth", route.Auth,
		)
	}

	return nil
}

func (s *server) registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if mux == nil {
		return
	}
	method, path := parseRoutePattern(pattern)
	s.routeTable = append(s.routeTable, routeInfo{
		Method: method,
		Path:   path,
		Auth:   inferRouteAuth(path),
	})
	mux.HandleFunc(pattern, handler)
}

func (s *server) Routes() []routeInfo {
	if len(s.routeTable) == 0 {
		return nil
	}
	routes := append([]routeInfo(nil), s.routeTable...)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
	return routes
}

func (s *server) adminRoutes() []routeInfo {
	routes := s.Routes()
	if len(routes) == 0 {
		return nil
	}
	admin := make([]routeInfo, 0, len(routes))
	for _, route := range routes {
		if route.Auth == "admin" {
			admin = append(admin, route)
		}
	}
	return admin
}

func parseRoutePattern(pattern string) (method, path string) {
	pattern = strings.TrimSpace(pattern)
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[1], "/") {
		return parts[0], parts[1]
	}
	return "ANY", pattern
}

func inferRouteAuth(path string) string {
	switch {
	case publicRoutePaths[path]:
		return "public"
	case adminRoutePaths[path]:
		return "admin"
	case hasRoutePrefix(path, adminRoutePrefixes):
		return "admin"
	case hasRoutePrefix(path, tenantRoutePrefixes):
		return "tenant"
	default:
		return "tenant"
	}
}

func hasRoutePrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

var publicRoutePaths = map[string]bool{
	"/health":              true,
	"/api/v1/health":       true,
	"/api/v1/auth/config":  true,
	"/api/v1/auth/login":   true,
	"/api/v1/auth/session": true,
}

var adminRoutePaths = map[string]bool{
	"/api/v1/governance/health":            true,
	"/api/v1/auth/oidc/group-role-mapping": true,
}

var adminRoutePrefixes = []string{
	"/mcp/",
	"/api/v1/admin",
	"/api/v1/agents",
	"/api/v1/audit",
	"/api/v1/auth/keys",
	"/api/v1/auth/roles",
	"/api/v1/config",
	"/api/v1/delegations",
	"/api/v1/dlq",
	"/api/v1/license",
	"/api/v1/locks",
	"/api/v1/marketplace",
	"/api/v1/mcp",
	"/api/v1/packs",
	"/api/v1/pools",
	"/api/v1/schemas",
	"/api/v1/status",
	"/api/v1/telemetry",
	"/api/v1/topics",
	"/api/v1/users",
	"/api/v1/workers",
}

var tenantRoutePrefixes = []string{
	"/api/v1/approvals",
	"/api/v1/artifacts",
	"/api/v1/auth/logout",
	"/api/v1/auth/password",
	"/api/v1/evals",
	"/api/v1/governance/approvals/analytics",
	"/api/v1/governance/decisions",
	"/api/v1/jobs",
	"/api/v1/memory",
	"/api/v1/policy",
	"/api/v1/stream",
	"/api/v1/traces",
	"/api/v1/workflow-runs",
	"/api/v1/workflows",
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
		statusStr := fmt.Sprintf("%d", rec.status)
		if s.metrics != nil {
			s.metrics.ObserveRequest(r.Method, route, statusStr, duration.Seconds())
		}
		if s.otelMetrics != nil {
			s.otelMetrics.RecordRequest(r.Context(), r.Method, route, statusStr, duration.Seconds())
		}
		if exporter, ok := s.auth.(AuditExporter); ok {
			authCtx := auth.FromRequest(r)
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
	rawLegacyBundles := packs.NormalizeJSON(defaultDoc.Data[packs.PolicyConfigKey])
	legacyBundles, ok := rawLegacyBundles.(map[string]any)
	if !ok || len(legacyBundles) == 0 {
		return false, 0, nil
	}
	if err := svc.SetWithRetry(ctx, configsvc.ScopeSystem, packs.PolicyConfigID, 3, func(doc *configsvc.Document) error {
		if doc.Data == nil {
			doc.Data = map[string]any{}
		}
		rawPolicyBundles := packs.NormalizeJSON(doc.Data[packs.PolicyConfigKey])
		policyBundles, _ := rawPolicyBundles.(map[string]any)
		if policyBundles == nil {
			policyBundles = map[string]any{}
		}
		for fragmentID, bundle := range legacyBundles {
			if _, exists := policyBundles[fragmentID]; exists {
				continue
			}
			policyBundles[fragmentID] = packs.DeepCopy(bundle)
		}
		doc.Data[packs.PolicyConfigKey] = policyBundles
		return nil
	}); err != nil {
		return false, 0, fmt.Errorf("merge legacy bundles into system/%s: %w", packs.PolicyConfigID, err)
	}
	if err := deleteSystemDefaultKeyWithRetry(ctx, svc, packs.PolicyConfigKey, 3); err != nil {
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
	Time       time.Time       `json:"time"`
	Method     string          `json:"method"`
	Route      string          `json:"route"`
	Path       string          `json:"path"`
	Status     int             `json:"status"`
	DurationMs int64           `json:"duration_ms"`
	RemoteAddr string          `json:"remote_addr"`
	UserAgent  string          `json:"user_agent"`
	Tenant     string          `json:"tenant"`
	Principal  string          `json:"principal"`
	Role       string          `json:"role"`
	AuthSource auth.AuthSource `json:"auth_source,omitempty"`
	RequestID  string          `json:"request_id"`
}

// AuditExporter allows auth providers to emit audit events.
type AuditExporter interface {
	ExportAudit(ctx context.Context, event AuditEvent) error
}

type LicenseInfo = licensing.LicenseInfo

type LicenseInfoProvider = licensing.LicenseInfoProvider
