package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
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

// ---------------------------------------------------------------------------
// Worker pool definitions — matches pools.yaml
// ---------------------------------------------------------------------------

type workerDef struct {
	ID       string
	Pool     string
	Topics   []string
	Capacity int
}

var bankWorkers = []workerDef{
	// Transaction processing
	{ID: "bank-validator-1", Pool: "bank-validators", Topics: []string{"job.bank-validators.process"}, Capacity: 8},
	{ID: "bank-validator-2", Pool: "bank-validators", Topics: []string{"job.bank-validators.process"}, Capacity: 8},

	// Fraud detection
	{ID: "fraud-detector-1", Pool: "fraud-detection", Topics: []string{"job.fraud-detection.process"}, Capacity: 4},
	{ID: "fraud-detector-2", Pool: "fraud-detection", Topics: []string{"job.fraud-detection.process"}, Capacity: 4},

	// Bank executors
	{ID: "bank-executor-1", Pool: "bank-executors", Topics: []string{"job.bank-executors.process"}, Capacity: 8},

	// Compliance / KYC
	{ID: "compliance-agent-1", Pool: "compliance-agents", Topics: []string{"job.compliance-agents.process"}, Capacity: 4},
	{ID: "compliance-agent-2", Pool: "compliance-agents", Topics: []string{"job.compliance-agents.process"}, Capacity: 4},

	// Credit agents
	{ID: "credit-agent-1", Pool: "credit-agents", Topics: []string{"job.credit-agents.process"}, Capacity: 4},

	// Risk agents
	{ID: "risk-agent-1", Pool: "risk-agents", Topics: []string{"job.risk-agents.process"}, Capacity: 4},

	// Loan agents
	{ID: "loan-agent-1", Pool: "loan-agents", Topics: []string{"job.loan-agents.process"}, Capacity: 4},

	// Valuation agents
	{ID: "valuation-agent-1", Pool: "valuation-agents", Topics: []string{"job.valuation-agents.process"}, Capacity: 2},

	// Underwriting agents
	{ID: "underwriter-1", Pool: "underwriting-agents", Topics: []string{"job.underwriting-agents.process"}, Capacity: 2},

	// Notification service
	{ID: "notifier-1", Pool: "notification-service", Topics: []string{"job.notification-service.process"}, Capacity: 16},

	// Legacy demo topics
	{ID: "demo-mock-bank-worker", Pool: "demo-mock-bank", Topics: []string{
		"job.demo-mock-bank.transfer.auto",
		"job.demo-mock-bank.transfer.review",
		"job.demo-mock-bank.transfer.blocked",
	}, Capacity: 4},
}

// ---------------------------------------------------------------------------
// Payload types
// ---------------------------------------------------------------------------

type bankPayload struct {
	Amount      any    `json:"amount"`
	Currency    string `json:"currency"`
	Customer    string `json:"customer"`
	Reason      string `json:"reason"`
	Note        string `json:"note"`
	RequestedBy string `json:"requested_by"`
	Message     string `json:"message"`
	Prompt      string `json:"prompt"`
}

type bankResult struct {
	JobID       string  `json:"job_id"`
	WorkerID    string  `json:"worker_id"`
	Pool        string  `json:"pool"`
	Amount      float64 `json:"amount,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Customer    string  `json:"customer,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Note        string  `json:"note,omitempty"`
	Topic       string  `json:"topic"`
	Status      string  `json:"status"`
	ProcessedAt string  `json:"processed_at"`
	ReferenceID string  `json:"reference_id"`
	Message     string  `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	natsURL := envOr("NATS_URL", defaultNatsURL)
	redisURL := envOr("REDIS_URL", defaultRedisURL)

	log.Println("[mock-bank] connecting to NATS...")
	nc, err := nats.Connect(natsURL, nats.Name("mock-bank-fleet"), nats.Timeout(5*time.Second))
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer func() { _ = nc.Drain() }()

	// Start all workers
	log.Printf("[mock-bank] starting %d workers across %d pools...", len(bankWorkers), countPools())

	for _, w := range bankWorkers {
		worker := w

		agent := &runtime.Agent{
			NATSURL:  natsURL,
			RedisURL: redisURL,
			SenderID: worker.ID,
		}

		handler := makeHandler(worker.ID, worker.Pool)

		for _, topic := range worker.Topics {
			runtime.Register(agent, topic, handler)
		}
		// Register direct subject
		runtime.Register(agent, "worker."+worker.ID+".jobs", handler)

		if err := agent.Start(); err != nil {
			log.Printf("[mock-bank] warning: agent %s start failed: %v", worker.ID, err)
			continue
		}

		// Heartbeat goroutine
		go func() {
			heartbeatFn := func() ([]byte, error) {
				active := rand.Intn(max(worker.Capacity/4, 1))
				return runtime.HeartbeatPayload(worker.ID, worker.Pool, active, worker.Capacity, rand.Float32()*0.3)
			}
			if payload, err := heartbeatFn(); err == nil {
				_ = runtime.EmitHeartbeat(nc, payload)
			}
			runtime.HeartbeatLoop(ctx, nc, heartbeatFn)
		}()

		log.Printf("[mock-bank]   started %-25s pool=%-22s topics=%v cap=%d",
			worker.ID, worker.Pool, worker.Topics, worker.Capacity)
	}

	log.Println("")
	log.Println("=== Mock Bank Fleet Ready ===")
	log.Printf("Workers: %d", len(bankWorkers))
	log.Printf("Pools:   %d", countPools())
	log.Println("Press Ctrl+C to stop...")

	<-ctx.Done()
	log.Println("[mock-bank] shutting down...")
}

// ---------------------------------------------------------------------------
// Handler factory — simulates bank operations
// ---------------------------------------------------------------------------

func makeHandler(workerID, pool string) func(runtime.Context, bankPayload) (bankResult, error) {
	return func(ctx runtime.Context, payload bankPayload) (bankResult, error) {
		jobID := ctx.Job.GetJobId()
		topic := ctx.Job.GetTopic()

		log.Printf("[%s] processing job=%s topic=%s", workerID, jobID, topic)

		// Simulate processing time (200ms - 2s)
		time.Sleep(time.Duration(200+rand.Intn(1800)) * time.Millisecond)

		amount := parseAmount(payload.Amount)
		message := payload.Message
		if message == "" {
			message = payload.Prompt
		}
		if message == "" {
			message = fmt.Sprintf("Processed by %s", pool)
		}

		result := bankResult{
			JobID:       jobID,
			WorkerID:    workerID,
			Pool:        pool,
			Amount:      amount,
			Currency:    orDefault(payload.Currency, "USD"),
			Customer:    orDefault(payload.Customer, "Unknown"),
			Reason:      payload.Reason,
			Note:        payload.Note,
			Topic:       topic,
			Status:      "completed",
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
			ReferenceID: fmt.Sprintf("%s-%s", pool, jobID[:8]),
			Message:     message,
		}

		log.Printf("[%s] completed job=%s ref=%s", workerID, jobID, result.ReferenceID)
		return result, nil
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func countPools() int {
	pools := make(map[string]bool)
	for _, w := range bankWorkers {
		pools[w.Pool] = true
	}
	return len(pools)
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

func orDefault(val, def string) string {
	if strings.TrimSpace(val) == "" {
		return def
	}
	return val
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
