package main

import (
	"context"
	"log"
	"log/slog"
	"net/url"
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
	redisURL := envOr("REDIS_URL", defaultRedisURL)

	slog.Info("hello-worker starting",
		"nats_scheme", parseScheme(natsURL),
		"redis_scheme", parseScheme(redisURL),
	)

	store, err := runtime.NewRedisBlobStoreWithPing(redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	natsOpts, err := natsConnectOptions(workerID)
	if err != nil {
		_ = store.Close()
		log.Fatalf("nats tls config: %v", err)
	}
	nc, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		_ = store.Close()
		log.Fatalf("nats connect: %v", err)
	}

	agent := &runtime.Agent{
		NATSURL:  natsURL,
		RedisURL: redisURL,
		NATS:     nc,
		Store:    store,
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
	// The scheduler dispatches via either the topic subject above OR this
	// per-worker direct subject (worker.<id>.jobs) depending on whether it
	// targeted this specific worker. Hello-worker has ONE handler type, so
	// binding the same `handler` to both subjects works.
	//
	// ⚠️ Do NOT copy this line verbatim into a multi-topic worker — a typed
	// handler on the direct subject will silently mis-route every job whose
	// topic differs from its own (the same `handler` runs regardless of
	// JobRequest.Topic). For workers that register more than one topic, use
	// a topic-aware dispatcher on the direct subject — see
	// examples/multi-topic-worker-go/ for the canonical pattern.
	runtime.Register(agent, runtime.DirectSubject(workerID), handler)

	if err := agent.Start(); err != nil {
		_ = agent.Close()
		log.Fatalf("runtime start: %v", err)
	}
	defer func() {
		if err := agent.Close(); err != nil {
			log.Printf("runtime close: %v", err)
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

func parseScheme(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return "unknown"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "unknown"
	}
	return strings.ToLower(parsed.Scheme)
}

func natsConnectOptions(workerID string) ([]nats.Option, error) {
	opts := []nats.Option{
		nats.Name(workerID),
		nats.Timeout(5 * time.Second),
	}
	tlsCfg, err := runtime.NATSTLSConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
	}
	return opts, nil
}
