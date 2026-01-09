package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/memory"
	infraMetrics "github.com/cordum/cordum/core/infra/metrics"
	agentregistry "github.com/cordum/cordum/core/infra/registry"
)

func main() {
	log.Println("cordum scheduler starting...")
	buildinfo.Log("cordum-scheduler")

	cfg := config.Load()

	timeoutsCfg, err := config.LoadTimeouts(cfg.TimeoutConfigPath)
	if err != nil {
		log.Printf("using default timeout config (could not load %s): %v", cfg.TimeoutConfigPath, err)
	}
	if timeoutsCfg == nil {
		timeoutsCfg, _ = config.LoadTimeouts("")
	}

	metrics := infraMetrics.NewProm("cordum_scheduler")
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{
			Addr:         ":9090",
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		log.Println("scheduler metrics on :9090/metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for job store: %v", err)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	safetyClient, err := scheduler.NewSafetyClient(cfg.SafetyKernelAddr)
	if err != nil {
		log.Fatalf("failed to connect to safety kernel: %v", err)
	}
	defer safetyClient.Close()

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
	).WithConfig(configSvc)

	if err := engine.Start(); err != nil {
		log.Fatalf("failed to start scheduler engine: %v", err)
	}

	snapshotStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Printf("worker snapshot disabled: failed to connect to Redis: %v", err)
	} else {
		defer snapshotStore.Close()
		snapshotInterval := 5 * time.Second
		if raw := os.Getenv("WORKER_SNAPSHOT_INTERVAL"); raw != "" {
			if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
				snapshotInterval = parsed
			} else {
				log.Printf("invalid WORKER_SNAPSHOT_INTERVAL=%q, using default %s", raw, snapshotInterval)
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
	engine.Stop()
	cancel()
}
