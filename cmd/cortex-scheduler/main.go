package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	infraMetrics "github.com/yaront1111/cortex-os/core/internal/infrastructure/metrics"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
)

func main() {
	log.Println("cortex scheduler starting...")

	cfg := config.Load()

	timeoutsCfg, err := config.LoadTimeouts(cfg.TimeoutConfigPath)
	if err != nil {
		log.Printf("using default timeout config (could not load %s): %v", cfg.TimeoutConfigPath, err)
	}
	if timeoutsCfg == nil {
		timeoutsCfg, _ = config.LoadTimeouts("")
	}

	metrics := infraMetrics.NewProm("cortex_scheduler")
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

	topicToPool, err := config.LoadPoolTopics(cfg.PoolConfigPath)
	if err != nil {
		log.Fatalf("failed to load pool config (%s): %v", cfg.PoolConfigPath, err)
	}
	log.Printf("loaded %d topic mappings from %s", len(topicToPool), cfg.PoolConfigPath)

	engine := scheduler.NewEngine(
		natsBus,
		safetyClient,
		scheduler.NewMemoryRegistry(),
		scheduler.NewLeastLoadedStrategy(topicToPool),
		jobStore,
		metrics,
	)

	if err := engine.Start(); err != nil {
		log.Fatalf("failed to start scheduler engine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recCfg := timeoutsCfg.Reconciler
	dispatchTimeout := time.Duration(recCfg.DispatchTimeoutSeconds) * time.Second
	if dispatchTimeout == 0 {
		dispatchTimeout = 2 * time.Minute
	}
	runningTimeout := time.Duration(recCfg.RunningTimeoutSeconds) * time.Second
	if runningTimeout == 0 {
		runningTimeout = 5 * time.Minute
	}
	scanInterval := time.Duration(recCfg.ScanIntervalSeconds) * time.Second
	if scanInterval == 0 {
		scanInterval = 30 * time.Second
	}

	reconciler := scheduler.NewReconciler(jobStore, dispatchTimeout, runningTimeout, scanInterval)
	go reconciler.Start(ctx)

	log.Println("scheduler running. waiting for signals...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("scheduler shutting down")
	cancel()
}
