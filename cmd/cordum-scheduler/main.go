package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/logging"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/redisutil"
	agentregistry "github.com/cordum/cordum/core/infra/registry"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// healthDeps holds references to scheduler dependencies for the /health endpoint.
type healthDeps struct {
	jobStore     *store.RedisJobStore
	bus          *bus.NatsBus
	safetyClient *scheduler.SafetyClient
}

func (h *healthDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	type depStatus struct {
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}
	result := map[string]any{}
	allOK := true

	// Redis
	if h.jobStore != nil {
		if err := h.jobStore.Ping(ctx); err != nil {
			result["redis"] = depStatus{Status: "error", Error: err.Error()}
			allOK = false
		} else {
			result["redis"] = depStatus{Status: "ok"}
		}
	} else {
		result["redis"] = depStatus{Status: "error", Error: "not initialized"}
		allOK = false
	}

	// NATS
	if h.bus != nil && h.bus.IsConnected() {
		result["nats"] = depStatus{Status: "ok"}
	} else {
		result["nats"] = depStatus{Status: "error", Error: "disconnected"}
		allOK = false
	}

	// Safety kernel (optional — degrade gracefully)
	if h.safetyClient != nil {
		result["safety"] = depStatus{Status: "ok"}
	} else {
		result["safety"] = depStatus{Status: "warn", Error: "not configured"}
	}

	if allOK {
		result["status"] = "ok"
	} else {
		result["status"] = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	if allOK {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(result)
}

type redisDLQSink struct {
	store    *store.DLQStore
	jobStore scheduler.JobStore
}

func (s *redisDLQSink) Add(ctx context.Context, entry scheduler.DLQEntry) error {
	if s == nil || s.store == nil || strings.TrimSpace(entry.JobID) == "" {
		return nil
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	if s.jobStore != nil {
		if strings.TrimSpace(entry.Topic) == "" {
			if topic, err := s.jobStore.GetTopic(ctx, entry.JobID); err == nil {
				entry.Topic = topic
			}
		}
		if state, err := s.jobStore.GetState(ctx, entry.JobID); err == nil {
			entry.LastState = string(state)
		}
		if attempts, err := s.jobStore.GetAttempts(ctx, entry.JobID); err == nil {
			entry.Attempts = attempts
		}
	}
	return s.store.Add(ctx, store.DLQEntry{
		JobID:      entry.JobID,
		Topic:      entry.Topic,
		Status:     entry.Status,
		Reason:     entry.Reason,
		ReasonCode: entry.ReasonCode,
		LastState:  entry.LastState,
		Attempts:   entry.Attempts,
		CreatedAt:  entry.CreatedAt,
	})
}

// sanitizeLogValue strips newlines and control characters to prevent log injection.
func sanitizeLogValue(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if r < 0x20 && r != ' ' {
			return -1
		}
		return r
	}, s)
}

func main() {
	logging.Init("scheduler")
	slog.Info("cordum scheduler starting...")
	buildinfo.Log("cordum-scheduler")

	cfg := config.Load()

	timeoutsCfg, err := config.LoadTimeouts(cfg.TimeoutConfigPath)
	if err != nil {
		explicitPath := os.Getenv("TIMEOUT_CONFIG_PATH")
		if env.IsProduction() && explicitPath != "" {
			slog.Error("timeout config load failed", "path", sanitizeLogValue(explicitPath), "error", sanitizeLogValue(err.Error()))
			os.Exit(1)
		}
		slog.Warn("using default timeout config", "path", sanitizeLogValue(cfg.TimeoutConfigPath), "error", sanitizeLogValue(err.Error()))
	}
	if timeoutsCfg == nil {
		timeoutsCfg = config.DefaultTimeouts()
	}
	if err == nil && cfg.TimeoutConfigPath != "" {
		slog.Info("timeout config loaded", "path", cfg.TimeoutConfigPath)
	} else if err != nil {
		slog.Info("timeout config: using built-in defaults")
	}

	metrics := infraMetrics.NewProm("cordum_scheduler")
	metricsAddr := strings.TrimSpace(os.Getenv("SCHEDULER_METRICS_ADDR"))
	if metricsAddr == "" {
		metricsAddr = ":9090"
	}
	if env.IsProduction() {
		if err := infraMetrics.ValidateBindAddr(metricsAddr, env.Bool("SCHEDULER_METRICS_PUBLIC")); err != nil {
			slog.Error("metrics bind rejected", "error", err)
			os.Exit(1)
		}
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	health := &healthDeps{}
	metricsMux.Handle("/health", health)
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		slog.Info("scheduler metrics started", "addr", metricsAddr+"/metrics")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	jobStore, err := store.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis for job store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = jobStore.Close() }()

	var dlqStore *store.DLQStore
	dlqStore, err = store.NewDLQStore(cfg.RedisURL, 0)
	if err != nil {
		slog.Warn("scheduler dlq sink disabled", "error", err)
	} else {
		defer func() { _ = dlqStore.Close() }()
	}

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		slog.Error("failed to connect to NATS", "error", err)
		os.Exit(1)
	}
	defer natsBus.Close()

	if err := bus.PublishHandshake(natsBus, "scheduler", pb.ComponentRole_COMPONENT_ROLE_SCHEDULER, map[string]bool{
		"safety_check": true, "routing": true, "compensation": true,
	}); err != nil {
		slog.Warn("handshake publish failed", "error", err)
	}

	sagaRedis, err := redisutil.NewClient(cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis for saga", "error", err)
		os.Exit(1)
	}
	{
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := sagaRedis.Ping(pingCtx).Err(); err != nil {
			cancel()
			slog.Error("failed to ping Redis for saga", "error", err)
			os.Exit(1)
		}
		cancel()
	}
	defer func() { _ = sagaRedis.Close() }()
	sagaManager := scheduler.NewSagaManager(natsBus, sagaRedis).WithMetrics(metrics)

	safetyClient, err := scheduler.NewSafetyClient(cfg.SafetyKernelAddr)
	if err != nil {
		slog.Error("failed to connect to safety kernel", "error", err)
		os.Exit(1)
	}
	defer func() { _ = safetyClient.Close() }()
	// Enable both the distributed circuit breaker and input context dereferencing
	// so native input-policy rules can inspect workflow step payloads pre-dispatch.
	safetyClient.WithRedis(sagaRedis).WithContextClient(jobStore.Client())
	sagaManager.WithSafety(safetyClient)

	// Populate health check dependencies now that all critical deps are created.
	health.jobStore = jobStore
	health.bus = natsBus
	health.safetyClient = safetyClient

	var outputSafetyClient *scheduler.OutputSafetyClient
	if cfg.OutputPolicyEnabled {
		outputSafetyClient, err = scheduler.NewOutputSafetyClientWithRedis(cfg.SafetyKernelAddr, cfg.RedisURL)
		if err != nil {
			slog.Error("failed to connect output policy client", "error", err)
			os.Exit(1)
		}
		defer func() { _ = outputSafetyClient.Close() }()
	}

	poolCfg, err := config.LoadPoolConfig(cfg.PoolConfigPath)
	if err != nil {
		slog.Error("failed to load pool config", "path", cfg.PoolConfigPath, "error", err)
		os.Exit(1)
	}

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis for config service", "error", err)
		os.Exit(1)
	}
	defer func() { _ = configSvc.Close() }()

	schemaRegistry, err := schema.NewRegistry(cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis for schema registry", "error", err)
		os.Exit(1)
	}
	defer func() { _ = schemaRegistry.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hostname, _ := os.Hostname()
	instanceID := hostname + "-" + uuid.NewString()[:8]
	slog.Info("scheduler instance", "instance_id", instanceID)

	// Instance registry: self-register this scheduler replica in Redis.
	instReg := agentregistry.NewInstanceRegistry(sagaRedis, "scheduler", instanceID, buildinfo.Version, buildinfo.Commit)
	instReg.Start(ctx)
	defer instReg.Stop()

	if err := configSvc.EnsureDefault(ctx); err != nil {
		slog.Warn("auto-bootstrap default config failed", "error", err)
	}
	if err := bootstrapConfig(ctx, configSvc, poolCfg, timeoutsCfg); err != nil {
		slog.Warn("config bootstrap failed", "error", err)
	}

	snapshot, err := loadConfigSnapshot(ctx, configSvc, poolCfg, timeoutsCfg)
	if err != nil {
		slog.Warn("config snapshot failed", "error", err)
	}
	if snapshot.Pools == nil {
		snapshot.Pools = poolCfg
	}
	if snapshot.Timeouts == nil {
		snapshot.Timeouts = timeoutsCfg
	}
	slog.Info("loaded topic mappings", "count", len(snapshot.Pools.Topics), "path", cfg.PoolConfigPath)

	routing := scheduler.PoolRouting{
		Topics: snapshot.Pools.Topics,
		Pools:  make(map[string]scheduler.PoolProfile, len(snapshot.Pools.Pools)),
	}
	for name, pool := range snapshot.Pools.Pools {
		routing.Pools[name] = scheduler.PoolProfile{Requires: append([]string{}, pool.Requires...)}
	}
	strategy := scheduler.NewLeastLoadedStrategy(routing)

	registry := scheduler.NewMemoryRegistry()
	defer registry.Close()

	workerCredentialCache := scheduler.NewWorkerCredentialCache(workercredentials.NewService(configSvc))
	entitlementResolver := licensing.NewEntitlementResolver()
	entitlementResolver.Init()

	engine := scheduler.NewEngine(
		natsBus,
		safetyClient,
		registry,
		strategy,
		jobStore,
		metrics,
	).WithConfig(configSvc).
		WithTopicRegistry(topicregistry.NewService(configSvc)).
		WithWorkerCredentialCache(workerCredentialCache).
		WithSchemaRegistry(schemaRegistry).
		WithEntitlements(entitlementResolver).
		WithContextClient(jobStore.Client()).
		WithSaga(sagaManager)
	if dlqStore != nil {
		engine.WithDLQSink(&redisDLQSink{
			store:    dlqStore,
			jobStore: jobStore,
		})
	}
	if outputSafetyClient != nil {
		engine.WithOutputChecker(outputSafetyClient).WithOutputSafetyEnabled(true)
		if fm := strings.TrimSpace(os.Getenv("OUTPUT_POLICY_FAIL_MODE")); fm != "" {
			engine.WithAsyncFailMode(fm)
		}
	}
	if fm := strings.TrimSpace(os.Getenv("POLICY_CHECK_FAIL_MODE")); fm != "" {
		engine.WithInputFailMode(fm)
	}
	engine.WithCounterClient(jobStore.Client())

	if err := engine.Start(); err != nil {
		slog.Error("failed to start scheduler engine", "error", err)
		os.Exit(1)
	}

	snapshotStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		slog.Warn("worker snapshot disabled: failed to connect to Redis", "error", err)
	} else {
		defer func() { _ = snapshotStore.Close() }()

		// Warm-start: hydrate registry from last-written snapshot to avoid 0–30s cold-start window.
		hydrateCtx, hydrateCancel := context.WithTimeout(ctx, 5*time.Second)
		snapData, snapErr := snapshotStore.GetResult(hydrateCtx, agentregistry.SnapshotKey)
		hydrateCancel()
		if snapErr != nil {
			slog.Warn("registry warm-start: failed to read snapshot", "error", snapErr)
		} else if len(snapData) == 0 {
			slog.Info("registry warm-start: no snapshot found, starting cold")
		} else if hydrateErr := registry.HydrateFromSnapshot(snapData); hydrateErr != nil {
			slog.Warn("registry warm-start: failed to hydrate", "error", hydrateErr)
		}

		snapshotInterval := 5 * time.Second
		if raw := os.Getenv("WORKER_SNAPSHOT_INTERVAL"); raw != "" {
			if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
				snapshotInterval = parsed
			} else {
				slog.Warn("invalid WORKER_SNAPSHOT_INTERVAL, using default", "raw", raw, "default", snapshotInterval)
			}
		}
		const snapshotLockKey = "cordum:scheduler:snapshot:writer"
		const snapshotLockTTL = 30 * time.Second
		go func() {
			ticker := time.NewTicker(snapshotInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
					token, err := jobStore.TryAcquireLock(lockCtx, snapshotLockKey, snapshotLockTTL)
					lockCancel()
					if err != nil {
						slog.Warn("snapshot writer lock acquire failed", "instance_id", instanceID, "error", err)
						continue
					}
					if token == "" {
						slog.Debug("snapshot writer lock held by another replica, skipping", "instance_id", instanceID)
						continue
					}

					current := strategy.CurrentRouting()
					snap := agentregistry.BuildSnapshot(registry.Snapshot(), current.TopicToPool())
					snap.WriterID = instanceID
					data, err := json.Marshal(snap)
					if err != nil {
						slog.Error("worker snapshot marshal failed", "error", err)
						releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
						_ = jobStore.ReleaseLock(releaseCtx, snapshotLockKey, token)
						releaseCancel()
						continue
					}
					writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
					if err := snapshotStore.PutResult(writeCtx, agentregistry.SnapshotKey, data); err != nil {
						slog.Error("worker snapshot write failed", "error", err)
					}
					writeCancel()

					releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
					if err := jobStore.ReleaseLock(releaseCtx, snapshotLockKey, token); err != nil {
						slog.Debug("snapshot writer lock release failed, will expire via TTL", "instance_id", instanceID, "error", err)
					}
					releaseCancel()
				}
			}
		}()
	}

	dispatchTimeout, runningTimeout, scanInterval := reconcilerTimeouts(snapshot.Timeouts)
	reconciler := scheduler.NewReconciler(jobStore, dispatchTimeout, runningTimeout, scanInterval)
	go reconciler.Start(ctx)
	pendingReplayer := scheduler.NewPendingReplayer(engine, jobStore, dispatchTimeout, scanInterval)
	go pendingReplayer.Start(ctx)

	go watchConfigChanges(ctx, configSvc, poolCfg, timeoutsCfg, strategy, reconciler, natsBus, engine)

	slog.Info("scheduler running, waiting for signals...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	const shutdownTimeout = 15 * time.Second
	slog.Info("scheduler shutting down gracefully", "timeout", shutdownTimeout)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("metrics server shutdown error", "error", err)
	}
	engine.Stop()
	cancel()
}
