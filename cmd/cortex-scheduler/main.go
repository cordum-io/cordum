package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
)

func main() {
	log.Println("cortex scheduler starting...")

	cfg := config.Load()

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

	engine := scheduler.NewEngine(
		natsBus,
		safetyClient,
		scheduler.NewMemoryRegistry(),
		scheduler.NewNaiveStrategy(),
		jobStore,
	)

	if err := engine.Start(); err != nil {
		log.Fatalf("failed to start scheduler engine: %v", err)
	}

	log.Println("scheduler running. waiting for signals...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("scheduler shutting down")
}
