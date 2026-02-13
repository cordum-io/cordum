package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
)

const (
	defaultNatsURL = "nats://127.0.0.1:4222"
	// Default includes auth for password-protected Redis (docker-compose default).
	defaultRedisURL = "redis://:cordum-dev@127.0.0.1:6379/0"
)

type echoInput struct {
	Message string `json:"message"`
	Author  string `json:"author,omitempty"`
}

type echoOutput struct {
	Message string `json:"message"`
	Author  string `json:"author,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	workerID := envOr("WORKER_ID", "hello-worker")
	natsURL := envOr("NATS_URL", defaultNatsURL)

	agent := &runtime.Agent{
		NATSURL:  natsURL,
		RedisURL: envOr("REDIS_URL", defaultRedisURL),
		SenderID: workerID,
	}

	handler := func(ctx runtime.Context, input echoInput) (echoOutput, error) {
		message := strings.TrimSpace(input.Message)
		if message == "" {
			message = "hello from worker"
		}
		return echoOutput{
			Message: message,
			Author:  input.Author,
		}, nil
	}

	runtime.Register(agent, "job.hello-pack.echo", handler)
	runtime.Register(agent, runtime.DirectSubject(workerID), handler)

	if err := agent.Start(); err != nil {
		log.Fatalf("runtime start: %v", err)
	}
	defer func() {
		if err := agent.Close(); err != nil {
			log.Printf("runtime close: %v", err)
		}
	}()

	nc, err := nats.Connect(natsURL, nats.Name(workerID), nats.Timeout(5*time.Second))
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer func() {
		if err := nc.Drain(); err != nil {
			log.Printf("nats drain: %v", err)
		}
	}()

	heartbeatFn := func() ([]byte, error) {
		return runtime.HeartbeatPayload(workerID, "hello-pack", 0, 4, 0)
	}
	if payload, err := heartbeatFn(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	go runtime.HeartbeatLoop(ctx, nc, heartbeatFn)

	log.Printf("hello worker ready (topic=%s, worker_id=%s)", "job.hello-pack.echo", workerID)

	<-ctx.Done()
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
