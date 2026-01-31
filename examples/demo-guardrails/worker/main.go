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
	defaultNatsURL  = "nats://127.0.0.1:4222"
	defaultRedisURL = "redis://127.0.0.1:6379/0"
)

type demoInput struct {
	Message string `json:"message"`
	Actor   string `json:"actor,omitempty"`
	Note    string `json:"note,omitempty"`
}

type demoOutput struct {
	Message string `json:"message"`
	Actor   string `json:"actor,omitempty"`
	Note    string `json:"note,omitempty"`
	Topic   string `json:"topic,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	workerID := envOr("WORKER_ID", "demo-guardrails-worker")
	natsURL := envOr("NATS_URL", defaultNatsURL)

	agent := &runtime.Agent{
		NATSURL:  natsURL,
		RedisURL: envOr("REDIS_URL", defaultRedisURL),
		SenderID: workerID,
	}

	handler := func(ctx runtime.Context, input demoInput) (demoOutput, error) {
		message := strings.TrimSpace(input.Message)
		if message == "" {
			message = "demo result"
		}
		return demoOutput{
			Message: message,
			Actor:   input.Actor,
			Note:    input.Note,
			Topic:   ctx.Job.GetTopic(),
		}, nil
	}

	runtime.Register(agent, "job.demo-guardrails.write", handler)
	runtime.Register(agent, "job.demo-guardrails.safe", handler)
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
		return runtime.HeartbeatPayload(workerID, "demo-guardrails", 0, 2, 0)
	}
	if payload, err := heartbeatFn(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	go runtime.HeartbeatLoop(ctx, nc, heartbeatFn)

	log.Printf("guardrails worker ready (topics=%s, worker_id=%s)", "job.demo-guardrails.write, job.demo-guardrails.safe", workerID)

	<-ctx.Done()
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
