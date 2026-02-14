package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
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
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/artifacts"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/logging"
	"github.com/cordum/cordum/core/infra/store"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/schema"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	wf "github.com/cordum/cordum/core/workflow"
)

const (
	defaultGrpcAddr             = ":8080"
	defaultHttpAddr             = ":8081"
	defaultMetricsAddr          = ":9092"
	maxJobPayloadBytes          = 2 << 20  // 2 MiB limit for incoming job payloads
	defaultArtifactMaxBytes     = 10 << 20 // 10 MiB default artifact size limit
	maxPromptChars              = 100000
	defaultRateLimitRPS         = 2000
	defaultRateLimitBurst       = 4000
	defaultPublicRateLimitRPS   = 20
	defaultPublicRateLimitBurst = 40
	defaultMaxHeaderBytes       = 1 << 20
	maxLabelKeyLen              = 256  // Max length for label keys
	maxLabelValueLen            = 4096 // Max length for label values (4KB)
	wsAuthSubprotocol = "cordum-api-key" // #nosec G101 -- subprotocol identifier, not a credential
	shutdownTimeout  = 15 * time.Second
	wsWriteTimeout   = 5 * time.Second
)

// validTopicRegex validates topic names to prevent injection attacks.
// Allows: job.alphanumeric-underscore-dot.name.with.segments
// Blocks: empty segments (job..), special chars, control chars
var validTopicRegex = regexp.MustCompile(`^job\.[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9_.-]*[a-zA-Z0-9])?)*$`)

// #nosec G101 -- environment variable names are identifiers, not credential material.
const (
	envGatewayGrpcAddr      = "GATEWAY_GRPC_ADDR"
	envGatewayHTTPAddr      = "GATEWAY_HTTP_ADDR"
	envGatewayMetricsAddr   = "GATEWAY_METRICS_ADDR"
	envGatewayMetricsPublic = "GATEWAY_METRICS_PUBLIC"
	envGatewayHTTPTLSCert   = "GATEWAY_HTTP_TLS_CERT"
	envGatewayHTTPTLSKey    = "GATEWAY_HTTP_TLS_KEY"
	envArtifactMaxBytes     = "ARTIFACT_MAX_BYTES"
	envHTTPReadTimeout      = "GATEWAY_HTTP_READ_TIMEOUT"
	envHTTPWriteTimeout     = "GATEWAY_HTTP_WRITE_TIMEOUT"
	envHTTPIdleTimeout      = "GATEWAY_HTTP_IDLE_TIMEOUT"
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

	clients   map[*websocket.Conn]*wsClient
	clientsMu sync.RWMutex
	eventsCh  chan wsEvent

	metrics infraMetrics.GatewayMetrics
	tenant  string
	started time.Time
	auth    AuthProvider

	workflowStore  *wf.RedisStore
	workflowEng    *wf.Engine
	configSvc      *configsvc.Service
	dlqStore       *store.DLQStore
	artifactStore  artifacts.Store
	lockStore      locks.Store
	schemaRegistry *schema.Registry
	safetyConn     *grpc.ClientConn
	safetyClient   pb.SafetyKernelClient
	userStore      UserStore
	keyStore       KeyStore

	auditExporter *audit.BufferedExporter

	marketplaceMu    sync.Mutex
	marketplaceCache marketplaceCache
	stopBusTapsOnce  sync.Once
	eventsStopped    atomic.Bool
	shutdownCh       chan struct{}
}

// Close releases resources owned by the server, notably the user store
// connection. It is safe to call with a nil userStore.
func (s *server) Close() {
	s.stopBusTaps()
	if s.auditExporter != nil {
		if err := s.auditExporter.Close(); err != nil {
			logging.Error("api-gateway", "audit exporter close failed", "error", err)
		}
	}
	if s.userStore != nil {
		if err := s.userStore.Close(); err != nil {
			logging.Error("api-gateway", "user store close failed", "error", err)
		}
	}
	if s.keyStore != nil {
		if ks, ok := s.keyStore.(*RedisKeyStore); ok {
			if err := ks.Close(); err != nil {
				logging.Error("api-gateway", "key store close failed", "error", err)
			}
		}
	}
}

func Run(cfg *config.Config) error {
	return RunWithAuth(cfg, nil)
}

// RunWithAuth starts the gateway with a custom auth provider. When nil, a basic
// single-tenant provider is used.
func RunWithAuth(cfg *config.Config, provider AuthProvider) error {
	if cfg == nil {
		cfg = config.Load()
	}
	grpcAddr := addrFromEnv(envGatewayGrpcAddr, defaultGrpcAddr)
	httpAddr := addrFromEnv(envGatewayHTTPAddr, defaultHttpAddr)
	metricsAddr := addrFromEnv(envGatewayMetricsAddr, defaultMetricsAddr)

	tenantID := strings.TrimSpace(os.Getenv("TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}

	gwMetrics := infraMetrics.NewGatewayProm("cordum_api_gateway")
	var userStore UserStore
	var keyStore KeyStore
	if provider == nil {
		basic, err := newBasicAuthProvider(tenantID)
		if err != nil {
			return fmt.Errorf("init auth: %w", err)
		}
		provider = basic

		// Initialize user store if enabled via environment
		if env.Bool("CORDUM_USER_AUTH_ENABLED") {
			us, err := NewRedisUserStore(cfg.RedisURL)
			if err != nil {
				return fmt.Errorf("init user store: %w", err)
			}
			userStore = us
			basic.SetUserStore(us)

			// Initialize managed API key store
			ks, err := NewRedisKeyStore(cfg.RedisURL)
			if err != nil {
				return fmt.Errorf("init key store: %w", err)
			}
			keyStore = ks
			basic.SetKeyStore(ks)

			if strings.TrimSpace(os.Getenv("CORDUM_ADMIN_PASSWORD")) == "" {
				return fmt.Errorf("cordum_user_auth_enabled is set but cordum_admin_password is empty; set cordum_admin_password to configure the admin account")
			}

			// Seed default admin user if configured
			if err := seedDefaultAdminUser(context.Background(), userStore, tenantID); err != nil {
				logging.Error("api-gateway", "seed admin user failed", "error", err)
			}
		}

		// Initialize OIDC provider if enabled — wraps basic + OIDC in composite
		oidcProvider, err := NewOIDCProviderFromEnv()
		if err != nil {
			return fmt.Errorf("init oidc: %w", err)
		}
		if oidcProvider != nil {
			defer oidcProvider.Close()
			oidcAdapter := NewOIDCAuthAdapter(oidcProvider, tenantID)
			composite, err := NewCompositeAuthProvider(basic, oidcAdapter)
			if err != nil {
				return fmt.Errorf("init composite auth: %w", err)
			}
			provider = composite
			oidcCfg := oidcProvider.Config()
			logging.Info("api-gateway", "[OIDC] enabled",
				"issuer", oidcCfg.IssuerURL,
				"audience", oidcCfg.Audience,
			)
		}
	}

	if env.IsProduction() && env.Bool("CORDUM_DASHBOARD_EMBED_API_KEY") {
		logging.Error("api-gateway", "SECURITY WARNING: CORDUM_DASHBOARD_EMBED_API_KEY is enabled in production — API key will be exposed in browser JavaScript")
	}

	memStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer memStore.Close()

	jobStore, err := store.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis job store: %w", err)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer natsBus.Close()

	workflowStore, err := wf.NewRedisWorkflowStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis workflow store: %w", err)
	}
	defer workflowStore.Close()
	workflowEng := wf.NewEngine(workflowStore, natsBus)

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis config service: %w", err)
	}
	defer configSvc.Close()
	if err := seedDefaultPackCatalogs(context.Background(), configSvc); err != nil {
		logging.Error("api-gateway", "seed pack catalogs failed", "error", err)
	}
	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis schema registry: %w", err)
	}
	defer schemaRegistry.Close()
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
	defer dlqStore.Close()

	artifactStore, err := artifacts.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis artifact store: %w", err)
	}
	defer artifactStore.Close()

	lockStore, err := locks.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis lock store: %w", err)
	}
	defer lockStore.Close()

	var safetyConn *grpc.ClientConn
	var safetyClient pb.SafetyKernelClient
	if cfg.SafetyKernelAddr != "" {
		conn, client, err := dialSafetyKernel(cfg.SafetyKernelAddr)
		if err != nil {
			if env.IsProduction() || env.Bool("SAFETY_KERNEL_TLS_REQUIRED") {
				return fmt.Errorf("safety kernel dial failed: %w", err)
			}
			logging.Error("api-gateway", "safety kernel dial failed", "error", err)
		} else {
			safetyConn = conn
			safetyClient = client
			defer safetyConn.Close()
		}
	}

	auditExporter, err := audit.NewExporterFromEnv()
	if err != nil {
		return fmt.Errorf("init audit exporter: %w", err)
	}

	s := &server{
		memStore:       memStore,
		jobStore:       jobStore,
		bus:            natsBus,
		workers:        make(map[string]*pb.Heartbeat),
		workerSeen:     make(map[string]time.Time),
		clients:        make(map[*websocket.Conn]*wsClient),
		eventsCh:       make(chan wsEvent, 512),
		metrics:        gwMetrics,
		tenant:         tenantID,
		auth:           provider,
		started:        time.Now().UTC(),
		workflowStore:  workflowStore,
		workflowEng:    workflowEng,
		configSvc:      configSvc,
		dlqStore:       dlqStore,
		artifactStore:  artifactStore,
		lockStore:      lockStore,
		schemaRegistry: schemaRegistry,
		safetyConn:     safetyConn,
		safetyClient:   safetyClient,
		userStore:      userStore,
		keyStore:       keyStore,
		auditExporter:  auditExporter,
		shutdownCh:     make(chan struct{}),
	}
	defer s.Close()

	if err := s.startBusTaps(); err != nil {
		return fmt.Errorf("start bus taps: %w", err)
	}

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
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("grpc tls keypair: %w", err)
		}
		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
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
		grpc.ChainUnaryInterceptor(
			apiKeyUnaryInterceptor(s.auth),
			rateLimitUnaryInterceptor(s.auth),
		),
	)
	pb.RegisterCordumApiServer(grpcServer, s)
	if env.Bool(env.EnvGRPCReflection) {
		reflection.Register(grpcServer)
	}

	go func() {
		logging.Info("api-gateway", "grpc listening", "addr", grpcAddr)
		if err := grpcServer.Serve(grpcLis); err != nil {
			logging.Error("api-gateway", "grpc server error", "error", err)
		}
	}()

	return startHTTPServer(s, httpAddr, metricsAddr, grpcServer)
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
		logging.Info("api-gateway", "metrics listening", "addr", metricsAddr+"/metrics")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("api-gateway", "metrics server error", "error", err)
		}
	}()

	// 1. Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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

	// 2. Workers (RPC via NATS)
	mux.HandleFunc("GET /api/v1/workers", s.instrumented("/api/v1/workers", s.handleGetWorkers))

	// 2.5 Status snapshot (Redis/NATS/workers/uptime)
	mux.HandleFunc("GET /api/v1/status", s.instrumented("/api/v1/status", s.handleStatus))

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

	// 11. Workflow approvals
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/steps/{step_id}/approve", s.handleApproveStep))
	mux.HandleFunc("POST /api/v1/workflows/{id}/runs/{run_id}/cancel", s.instrumented("/api/v1/workflows/{id}/runs/{run_id}/cancel", s.handleCancelRun))

	// 11.5 Job approvals
	mux.HandleFunc("GET /api/v1/approvals", s.instrumented("/api/v1/approvals", s.handleListApprovals))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/approve", s.instrumented("/api/v1/approvals/{job_id}/approve", s.handleApproveJob))
	mux.HandleFunc("POST /api/v1/approvals/{job_id}/reject", s.instrumented("/api/v1/approvals/{job_id}/reject", s.handleRejectJob))

	// 12. Policy endpoints
	mux.HandleFunc("POST /api/v1/policy/evaluate", s.instrumented("/api/v1/policy/evaluate", s.handlePolicyEvaluate))
	mux.HandleFunc("POST /api/v1/policy/simulate", s.instrumented("/api/v1/policy/simulate", s.handlePolicySimulate))
	mux.HandleFunc("POST /api/v1/policy/explain", s.instrumented("/api/v1/policy/explain", s.handlePolicyExplain))
	mux.HandleFunc("GET /api/v1/policy/snapshots", s.instrumented("/api/v1/policy/snapshots", s.handlePolicySnapshots))
	mux.HandleFunc("GET /api/v1/policy/rules", s.instrumented("/api/v1/policy/rules", s.handlePolicyRules))
	mux.HandleFunc("GET /api/v1/policy/output/rules", s.instrumented("/api/v1/policy/output/rules", s.handlePolicyOutputRules))
	mux.HandleFunc("GET /api/v1/policy/output/stats", s.instrumented("/api/v1/policy/output/stats", s.handlePolicyOutputStats))
	mux.HandleFunc("PUT /api/v1/policy/output/rules/{id}", s.instrumented("/api/v1/policy/output/rules/{id}", s.handlePutPolicyOutputRule))
	mux.HandleFunc("GET /api/v1/policy/bundles", s.instrumented("/api/v1/policy/bundles", s.handlePolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handleGetPolicyBundle))
	mux.HandleFunc("PUT /api/v1/policy/bundles/{id}", s.instrumented("/api/v1/policy/bundles/{id}", s.handlePutPolicyBundle))
	mux.HandleFunc("POST /api/v1/policy/bundles/{id}/simulate", s.instrumented("/api/v1/policy/bundles/{id}/simulate", s.handleSimulatePolicyBundle))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleListPolicyBundleSnapshots))
	mux.HandleFunc("POST /api/v1/policy/bundles/snapshots", s.instrumented("/api/v1/policy/bundles/snapshots", s.handleCapturePolicyBundleSnapshot))
	mux.HandleFunc("GET /api/v1/policy/bundles/snapshots/{id}", s.instrumented("/api/v1/policy/bundles/snapshots/{id}", s.handleGetPolicyBundleSnapshot))
	mux.HandleFunc("POST /api/v1/policy/publish", s.instrumented("/api/v1/policy/publish", s.handlePublishPolicyBundles))
	mux.HandleFunc("POST /api/v1/policy/rollback", s.instrumented("/api/v1/policy/rollback", s.handleRollbackPolicyBundles))
	mux.HandleFunc("GET /api/v1/policy/audit", s.instrumented("/api/v1/policy/audit", s.handleListPolicyAudit))

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

	// Middleware chain: CORS → auth → rate limit → tenant → body limit → mux
	handler := corsMiddleware(apiKeyMiddleware(s.auth, rateLimitMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux)))))

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

	logging.Info("api-gateway", "http listening", "addr", httpAddr)
	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           handler,
		ReadTimeout:       durationFromEnv(envHTTPReadTimeout, 15*time.Second),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      durationFromEnv(envHTTPWriteTimeout, 30*time.Second),
		IdleTimeout:       durationFromEnv(envHTTPIdleTimeout, 60*time.Second),
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}
	if httpTLSCert != "" {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
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

	go func() {
		<-sigCtx.Done()
		logging.Info("api-gateway", "shutting down gracefully", "timeout", shutdownTimeout.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Stop the event broadcast goroutine.
		if s.shutdownCh != nil {
			s.stopBusTaps()
			close(s.shutdownCh)
		}

		// Drain HTTP server (stops accepting, waits for in-flight requests).
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logging.Error("api-gateway", "http shutdown error", "error", err)
		}

		// Drain gRPC server (waits for in-flight RPCs to finish).
		if grpcServer != nil {
			grpcServer.GracefulStop()
		}

		if basic := basicAuthProvider(s.auth); basic != nil {
			basic.DrainUsage()
		}

		// Shut down metrics server.
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logging.Error("api-gateway", "metrics shutdown error", "error", err)
		}
	}()

	if err := func() error {
		if httpTLSCert != "" {
			// #nosec G304 -- TLS cert path is configured by the operator.
			return srv.ListenAndServeTLS(httpTLSCert, httpTLSKey)
		}
		return srv.ListenAndServe()
	}(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			logging.Info("api-gateway", "http server closed")
			return nil
		}
		logging.Error("api-gateway", "http server error", "error", err)
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// instrumented wraps handlers to record metrics.
func (s *server) instrumented(route string, fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fn(rec, r)
		duration := time.Since(start)
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
				logging.Error("api-gateway", "audit export failed", "error", err)
			}
		}
	}
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

// LicenseInfo describes license metadata for the status endpoint.
type LicenseInfo struct {
	Mode           string           `json:"mode,omitempty"`
	Status         string           `json:"status,omitempty"`
	Plan           string           `json:"plan,omitempty"`
	OrgID          string           `json:"org_id,omitempty"`
	LicenseID      string           `json:"license_id,omitempty"`
	DeploymentType string           `json:"deployment_type,omitempty"`
	IssuedAt       string           `json:"issued_at,omitempty"`
	NotBefore      string           `json:"not_before,omitempty"`
	ExpiresAt      string           `json:"expires_at,omitempty"`
	Features       []string         `json:"features,omitempty"`
	Limits         map[string]int64 `json:"limits,omitempty"`
}

// LicenseInfoProvider optionally supplies license metadata for status responses.
type LicenseInfoProvider interface {
	LicenseInfo() *LicenseInfo
}
