package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/store"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	"github.com/cordum/cordum/core/infra/redisutil"
	agentregistry "github.com/cordum/cordum/core/infra/registry"
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
	log.Println("cordum scheduler starting...")
	buildinfo.Log("cordum-scheduler")

	cfg := config.Load()

	timeoutsCfg, err := config.LoadTimeouts(cfg.TimeoutConfigPath)
	if err != nil {
		explicitPath := os.Getenv("TIMEOUT_CONFIG_PATH")
		if env.IsProduction() && explicitPath != "" {
			log.Fatalf("timeout config load failed (production mode, TIMEOUT_CONFIG_PATH=%s): %v", sanitizeLogValue(explicitPath), sanitizeLogValue(err.Error())) // #nosec G104 G706 -- sanitized values
		}
		log.Printf("using default timeout config (could not load %s): %v", sanitizeLogValue(cfg.TimeoutConfigPath), sanitizeLogValue(err.Error()))
	}
	if timeoutsCfg == nil {
		timeoutsCfg = config.DefaultTimeouts()
	}
	if err == nil && cfg.TimeoutConfigPath != "" {
		log.Printf("timeout config loaded from %s", cfg.TimeoutConfigPath)
	} else if err != nil {
		log.Printf("timeout config: using built-in defaults")
	}

	metrics := infraMetrics.NewProm("cordum_scheduler")
	metricsAddr := strings.TrimSpace(os.Getenv("SCHEDULER_METRICS_ADDR"))
	if metricsAddr == "" {
		metricsAddr = ":9090"
	}
	if env.IsProduction() {
		if err := infraMetrics.ValidateBindAddr(metricsAddr, env.Bool("SCHEDULER_METRICS_PUBLIC")); err != nil {
			log.Fatalf("metrics bind rejected: %v", err)
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
		log.Printf("scheduler metrics on %s/metrics", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	jobStore, err := store.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for job store: %v", err)
	}
	defer jobStore.Close()

	var dlqStore *store.DLQStore
	dlqStore, err = store.NewDLQStore(cfg.RedisURL, 0)
	if err != nil {
		log.Printf("scheduler dlq sink disabled: %v", err)
	} else {
		defer dlqStore.Close()
	}

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	if err := bus.PublishHandshake(natsBus, "scheduler", pb.ComponentRole_COMPONENT_ROLE_SCHEDULER, map[string]bool{
		"safety_check": true, "routing": true, "compensation": true,
	}); err != nil {
		log.Printf("handshake publish failed: %v", err)
	}

	sagaRedis, err := redisutil.NewClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for saga: %v", err)
	}
	{
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := sagaRedis.Ping(pingCtx).Err(); err != nil {
			cancel()
			log.Fatalf("failed to ping Redis for saga: %v", err)
		}
		cancel()
	}
	defer sagaRedis.Close()
	sagaManager := scheduler.NewSagaManager(natsBus, sagaRedis).WithMetrics(metrics)

	safetyClient, err := scheduler.NewSafetyClient(cfg.SafetyKernelAddr)
	if err != nil {
		log.Fatalf("failed to connect to safety kernel: %v", err)
	}
	defer safetyClient.Close()
	sagaManager.WithSafety(safetyClient)

	// Populate health check dependencies now that all critical deps are created.
	health.jobStore = jobStore
	health.bus = natsBus
	health.safetyClient = safetyClient

	var outputSafetyClient *scheduler.OutputSafetyClient
	if cfg.OutputPolicyEnabled {
		outputSafetyClient, err = scheduler.NewOutputSafetyClientWithRedis(cfg.SafetyKernelAddr, cfg.RedisURL)
		if err != nil {
			log.Fatalf("failed to connect output policy client: %v", err)
		}
		defer outputSafetyClient.Close()
	}

	poolCfg, err := config.LoadPoolConfig(cfg.PoolConfigPath)
	if err != nil {
		log.Fatalf("failed to load pool config (%s): %v", cfg.PoolConfigPath, err)
	}

	configSvc, err := configsvc.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for config service: %v", err)
	}
	defer configSvc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := configSvc.EnsureDefault(ctx); err != nil {
		log.Printf("auto-bootstrap default config failed: %v", err)
	}
	if err := bootstrapConfig(ctx, configSvc, poolCfg, timeoutsCfg); err != nil {
		log.Printf("config bootstrap failed: %v", err)
	}

	snapshot, err := loadConfigSnapshot(ctx, configSvc, poolCfg, timeoutsCfg)
	if err != nil {
		log.Printf("config snapshot failed: %v", err)
	}
	if snapshot.Pools == nil {
		snapshot.Pools = poolCfg
	}
	if snapshot.Timeouts == nil {
		snapshot.Timeouts = timeoutsCfg
	}
	log.Printf("loaded %d topic mappings (config + %s)", len(snapshot.Pools.Topics), cfg.PoolConfigPath)

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

	engine := scheduler.NewEngine(
		natsBus,
		safetyClient,
		registry,
		strategy,
		jobStore,
		metrics,
	).WithConfig(configSvc).WithSaga(sagaManager)
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

	if err := engine.Start(); err != nil {
		log.Fatalf("failed to start scheduler engine: %v", err)
	}

	snapshotStore, err := store.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Printf("worker snapshot disabled: failed to connect to Redis: %v", err)
	} else {
		defer snapshotStore.Close()
		snapshotInterval := 5 * time.Second
		if raw := os.Getenv("WORKER_SNAPSHOT_INTERVAL"); raw != "" {
			if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
				snapshotInterval = parsed
			} else {
				log.Printf("invalid WORKER_SNAPSHOT_INTERVAL=%q, using default %s", raw, snapshotInterval) // #nosec -- value is config input for diagnostics.
			}
		}
		go func() {
			ticker := time.NewTicker(snapshotInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					current := strategy.CurrentRouting()
					snap := agentregistry.BuildSnapshot(registry.Snapshot(), current.TopicToPool())
					data, err := json.Marshal(snap)
					if err != nil {
						log.Printf("worker snapshot marshal failed: %v", err)
						continue
					}
					if err := snapshotStore.PutResult(ctx, agentregistry.SnapshotKey, data); err != nil {
						log.Printf("worker snapshot write failed: %v", err)
					}
				}
			}
		}()
	}

	dispatchTimeout, runningTimeout, scanInterval := reconcilerTimeouts(snapshot.Timeouts)
	reconciler := scheduler.NewReconciler(jobStore, dispatchTimeout, runningTimeout, scanInterval)
	go reconciler.Start(ctx)
	pendingReplayer := scheduler.NewPendingReplayer(engine, jobStore, dispatchTimeout, scanInterval)
	go pendingReplayer.Start(ctx)

	go watchConfigChanges(ctx, configSvc, poolCfg, timeoutsCfg, strategy, reconciler)

	log.Println("scheduler running. waiting for signals...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("scheduler shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics server shutdown error: %v", err)
	}
	engine.Stop()
	cancel()
}
