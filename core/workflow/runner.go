package workflow

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/health"
	cordumotel "github.com/cordum/cordum/core/infra/otel"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"log/slog"

	"github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	defaultHTTPAddr        = ":9093"
	defaultReadTimeout     = 5 * time.Second
	defaultWriteTimeout    = 5 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 15 * time.Second
	workflowEngineQueue    = "cordum-workflow-engine"
)

// Run starts the workflow engine control-plane component.
func Run(cfg *config.Config) error {
	return RunWithEntitlements(cfg, nil)
}

// RunWithEntitlements starts the workflow engine with an optional shared
// entitlement resolver. Nil falls back to community defaults.
func RunWithEntitlements(cfg *config.Config, resolver *licensing.EntitlementResolver) error {
	if cfg == nil {
		cfg = config.Load()
	}

	if _, err := cordumotel.InitTracer("cordum-workflow-engine"); err != nil {
		slog.Error("otel tracer init failed", "error", err)
	}
	defer func() {
		if err := cordumotel.Shutdown(context.Background()); err != nil {
			slog.Error("otel tracer shutdown failed", "error", err)
		}
	}()

	httpAddr := os.Getenv("WORKFLOW_ENGINE_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}
	scanInterval := 5 * time.Second
	if v := os.Getenv("WORKFLOW_ENGINE_SCAN_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			scanInterval = d
		}
	}
	runScanLimit := int64(200)
	if v := os.Getenv("WORKFLOW_ENGINE_RUN_SCAN_LIMIT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			runScanLimit = n
		}
	}
	maxForEachItems := 0
	if v := strings.TrimSpace(os.Getenv("WORKFLOW_FOREACH_MAX_ITEMS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxForEachItems = n
		}
	}

	memStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis memory store: %w", err)
	}
	defer func() { _ = memStore.Close() }()

	jobStore, err := store.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis job store: %w", err)
	}
	defer func() { _ = jobStore.Close() }()

	workflowStore, err := NewRedisWorkflowStore(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis workflow store: %w", err)
	}
	defer func() { _ = workflowStore.Close() }()

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis config service: %w", err)
	}
	defer func() { _ = configSvc.Close() }()

	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("connect redis schema registry: %w", err)
	}
	defer func() { _ = schemaRegistry.Close() }()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer natsBus.Close()

	if err := bus.PublishHandshake(natsBus, "workflow-engine", pb.ComponentRole_COMPONENT_ROLE_ORCHESTRATOR, map[string]bool{
		"workflows": true, "approvals": true,
	}); err != nil {
		slog.Warn("handshake publish failed", "error", err)
	}

	engine := NewEngine(workflowStore, natsBus).
		WithMemory(memStore).
		WithConfig(configSvc).
		WithSchemaRegistry(schemaRegistry).
		WithEntitlements(resolver).
		WithRunLocker(jobStore)
	if maxForEachItems > 0 {
		engine = engine.WithMaxForEachItems(maxForEachItems)
	}

	// Wire control-plane service-token minting for internal cancel broadcasts.
	// The workflow-engine binary loads NO signing key by default; minting is
	// enabled only when one is configured. CORDUM_SDK_HANDSHAKE=enforce REQUIRES
	// the key be deployed here (see deploy config); absent a key, cancels
	// publish token-less, which a peer scheduler admits under off/warn.
	if priv, kid, kerr := policysign.LoadPrivateKeyFromEnv(); kerr == nil {
		engine = engine.WithServiceTokenMinter(func() (string, error) {
			return servicetoken.MintService(priv, kid, servicetoken.IdentityWorkflow, time.Now())
		})
		slog.Info("workflow-engine service-token minting enabled", "kid", kid)
	} else {
		// Enforce mode with no signing key is a startup misconfiguration:
		// every internal cancel would publish token-less and be rejected by
		// enforce-mode schedulers, leaving cancelled/timed-out runs with
		// still-running jobs. Fail fast instead of degrading silently.
		// NOTE: the env value is compared locally instead of reusing
		// scheduler.ParseHandshakeModeStrict — importing the scheduler
		// package here creates a test-only import cycle (core/audit tests
		// import core/workflow; scheduler imports core/audit). The scheduler
		// remains the authority for strict mode validation at its own boot.
		if mode := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_SDK_HANDSHAKE"))); mode == "enforce" {
			return fmt.Errorf("workflow-engine requires a control-plane service-token signing key when CORDUM_SDK_HANDSHAKE=enforce (internal cancel broadcasts would be rejected by enforce-mode schedulers): %w", kerr)
		}
		slog.Info("workflow-engine service-token minting disabled (no signing key configured); internal cancels are unauthenticated and dropped by peer schedulers only under CORDUM_SDK_HANDSHAKE=enforce", "reason", kerr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Instance registry: self-register this workflow-engine replica in Redis.
	instanceID := registry.ResolveInstanceID()
	instReg := registry.NewInstanceRegistry(jobStore.Client(), "workflow-engine", instanceID, buildinfo.Version, buildinfo.Commit)
	instReg.Start(ctx)
	defer instReg.Stop()

	// Recover durable delay timers from Redis (fast — runs before accepting work).
	recoverCtx, recoverCancel := context.WithTimeout(ctx, 5*time.Second)
	engine.recoverDelayTimers(recoverCtx)
	recoverCancel()

	// Start background delay timer poller (catches timers lost by crashed replicas).
	go engine.startDelayPoller(ctx)

	rec := newReconciler(workflowStore, engine, jobStore, scanInterval, runScanLimit)
	go rec.Start(ctx)

	// Use context-aware subscription for trace propagation from NATS headers.
	if err := natsBus.SubscribeWithContext(capsdk.SubjectResult, workflowEngineQueue, func(traceCtx context.Context, p *pb.BusPacket) error {
		if jr := p.GetJobResult(); jr != nil {
			handlerCtx, handlerCancel := context.WithTimeout(traceCtx, 30*time.Second)
			defer handlerCancel()
			return rec.HandleJobResult(handlerCtx, jr)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("subscribe %s: %w", capsdk.SubjectResult, err)
	}

	wfProbes := health.New()
	wfProbes.RegisterReadiness("redis", func(ctx context.Context) error {
		return jobStore.Ping(ctx)
	})
	wfProbes.RegisterReadiness("nats", func(ctx context.Context) error {
		if !natsBus.IsConnected() {
			return fmt.Errorf("disconnected")
		}
		return nil
	})
	wfProbes.SetStartupComplete()

	srv := startHealthServer(httpAddr, wfProbes)
	slog.Info("started", "http", httpAddr, "scan_interval", scanInterval.String(), "run_scan_limit", runScanLimit)

	<-ctx.Done()

	slog.Info("shutting down gracefully", "timeout", defaultShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http server shutdown failed", "error", err)
	}

	slog.Info("stopped")
	return nil
}

func startHealthServer(addr string, probes *health.ProbeServer) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if probes != nil {
		probes.Register(mux)
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
		IdleTimeout:  defaultIdleTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
		}
	}()
	return srv
}
