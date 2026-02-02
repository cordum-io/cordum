package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strconv"
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

type transferPayload struct {
	Amount      any    `json:"amount"`
	Currency    string `json:"currency"`
	Customer    string `json:"customer"`
	Reason      string `json:"reason"`
	Note        string `json:"note"`
	RequestedBy string `json:"requested_by"`
}

type transferResult struct {
	JobID       string  `json:"job_id"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Customer    string  `json:"customer"`
	Reason      string  `json:"reason"`
	Note        string  `json:"note"`
	RequestedBy string  `json:"requested_by"`
	Topic       string  `json:"topic"`
	Status      string  `json:"status"`
	ProcessedAt string  `json:"processed_at"`
	ReferenceID string  `json:"reference_id"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	workerID := envOr("WORKER_ID", "demo-mock-bank-worker")
	directSubject := "worker." + workerID + ".jobs"
	natsURL := envOr("NATS_URL", defaultNatsURL)

	agent := &runtime.Agent{
		NATSURL:  natsURL,
		RedisURL: envOr("REDIS_URL", defaultRedisURL),
		SenderID: workerID,
	}

	handler := func(ctx runtime.Context, payload transferPayload) (transferResult, error) {
		if strings.TrimSpace(payload.Currency) == "" {
			payload.Currency = "USD"
		}
		if strings.TrimSpace(payload.Customer) == "" {
			payload.Customer = "Unknown"
		}
		if strings.TrimSpace(payload.RequestedBy) == "" {
			payload.RequestedBy = "agent-demo"
		}

		amount := parseAmount(payload.Amount)
		jobID := ctx.Job.GetJobId()
		return transferResult{
			JobID:       jobID,
			Amount:      amount,
			Currency:    payload.Currency,
			Customer:    payload.Customer,
			Reason:      payload.Reason,
			Note:        payload.Note,
			RequestedBy: payload.RequestedBy,
			Topic:       ctx.Job.GetTopic(),
			Status:      "executed",
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
			ReferenceID: "transfer-" + jobID,
		}, nil
	}

	for _, topic := range []string{
		directSubject,
		"job.demo-mock-bank.transfer.auto",
		"job.demo-mock-bank.transfer.review",
		"job.demo-mock-bank.transfer.blocked",
	} {
		runtime.Register(agent, topic, handler)
	}

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
		return runtime.HeartbeatPayload(workerID, "demo-mock-bank", 0, 4, 0)
	}
	if payload, err := heartbeatFn(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	go runtime.HeartbeatLoop(ctx, nc, heartbeatFn)

	log.Printf("mock-bank worker ready (topics=%s, worker_id=%s)", "job.demo-mock-bank.transfer.auto, job.demo-mock-bank.transfer.review, job.demo-mock-bank.transfer.blocked", workerID)

	<-ctx.Done()
}

func parseAmount(val any) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 0
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
