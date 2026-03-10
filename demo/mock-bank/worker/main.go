package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	defaultNatsURL = "nats://127.0.0.1:4222"
	// Default includes auth for password-protected Redis (docker-compose default).
	// Override REDIS_URL env to connect to a different instance.
	defaultRedisURL = "redis://:cordum-dev@127.0.0.1:6379/0"
)

// ---------------------------------------------------------------------------
// Worker pool definitions — matches pools.yaml
// ---------------------------------------------------------------------------

type workerDef struct {
	ID           string
	Pool         string
	Topics       []string
	Capacity     int
	Region       string
	Type         string
	Capabilities []string
	Labels       map[string]string
}

var bankWorkers = []workerDef{
	{
		ID:           "megacorp-transfer-agent-01",
		Pool:         "megacorp-banking",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     8,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"transfer", "wire", "compliance", "aml-check"},
		Labels:       map[string]string{"name": "Transfer Agent 01", "env": "production", "tier": "critical"},
	},
	{
		ID:           "megacorp-transfer-agent-02",
		Pool:         "megacorp-banking",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     8,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"transfer", "wire", "compliance", "aml-check"},
		Labels:       map[string]string{"name": "Transfer Agent 02", "env": "production", "tier": "critical"},
	},
	{
		ID:           "megacorp-compliance-scanner",
		Pool:         "megacorp-compliance",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     4,
		Region:       "us-west-2",
		Type:         "cpu",
		Capabilities: []string{"compliance", "aml-check", "sanctions-screening", "audit"},
		Labels:       map[string]string{"name": "Compliance Scanner", "env": "production", "tier": "standard"},
	},
	{
		ID:           "megacorp-audit-recorder",
		Pool:         "megacorp-audit",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     2,
		Region:       "eu-west-1",
		Type:         "cpu",
		Capabilities: []string{"audit", "reporting", "regulatory"},
		Labels:       map[string]string{"name": "Audit Recorder", "env": "production", "tier": "standard"},
	},
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

	if !runtime.ValidateRedisURL(redisURL) {
		log.Println("[mock-bank] WARNING: REDIS_URL has no '@' — may be missing auth credentials")
	}
	// Create a TLS-aware blob store that applies REDIS_TLS_* env vars.
	blobStore, err := runtime.NewRedisBlobStoreWithPing(redisURL)
	if err != nil {
		log.Fatalf("[mock-bank] Redis connection failed (check REDIS_URL, password, and TLS certs): %v", err)
	}
	log.Println("[mock-bank] Redis connection verified (TLS-aware)")

	log.Println("[mock-bank] connecting to NATS...")
	nc, err := connectNATSWithTLS(natsURL)
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer func() { _ = nc.Drain() }()

	// Start all workers
	log.Printf("[mock-bank] starting %d workers across %d pools...", len(bankWorkers), countPools())

	for _, w := range bankWorkers {
		worker := w

		agent := &runtime.Agent{
			NATS:     nc,
			NATSURL:  natsURL,
			RedisURL: redisURL,
			Store:    blobStore,
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

		// Heartbeat goroutine — builds full proto with capabilities, labels, region
		go func() {
			heartbeatFn := func() ([]byte, error) {
				active := randInt(max(worker.Capacity/4, 1))
				cpuLoad := 5.0 + randFloat32()*35.0  // 5–40%
				memLoad := 20.0 + randFloat32()*40.0 // 20–60%
				return buildHeartbeat(worker, safeInt32(active), float32(cpuLoad), float32(memLoad))
			}
			if payload, err := heartbeatFn(); err == nil {
				_ = runtime.EmitHeartbeat(nc, payload)
			}
			runtime.HeartbeatLoop(ctx, nc, heartbeatFn)
		}()

		log.Printf("[mock-bank]   started %-35s pool=%-22s region=%-12s topics=%v cap=%d",
			worker.ID, worker.Pool, worker.Region, worker.Topics, worker.Capacity)
	}

	log.Println("")
	log.Println("=== MegaCorp Agent Fleet Ready ===")
	log.Printf("Workers: %d", len(bankWorkers))
	log.Printf("Pools:   %d", countPools())
	log.Println("Press Ctrl+C to stop...")

	<-ctx.Done()
	log.Println("[mock-bank] shutting down...")
}

// ---------------------------------------------------------------------------
// Custom heartbeat builder — populates all proto fields
// ---------------------------------------------------------------------------

func buildHeartbeat(w workerDef, activeJobs int32, cpuLoad, memoryLoad float32) ([]byte, error) {
	hb := &agentv1.BusPacket{
		SenderId:        w.ID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &agentv1.BusPacket_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				WorkerId:        w.ID,
				Pool:            w.Pool,
				Region:          w.Region,
				Type:            w.Type,
				CpuLoad:         cpuLoad,
				MemoryLoad:      memoryLoad,
				ActiveJobs:      activeJobs,
				MaxParallelJobs: safeInt32(w.Capacity),
				Capabilities:    w.Capabilities,
				Labels:          w.Labels,
			},
		},
	}
	return proto.Marshal(hb)
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
		time.Sleep(time.Duration(200+randInt(1800)) * time.Millisecond)

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

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

func safeInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

func randFloat32() float32 {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return 0
	}
	return float32(n.Int64()) / 1_000_000
}

// connectNATSWithTLS creates a NATS connection, adding TLS if NATS_TLS_* env
// vars are set (via sdk/runtime.NATSTLSConfigFromEnv).
func connectNATSWithTLS(natsURL string) (*nats.Conn, error) {
	opts := []nats.Option{nats.Name("megacorp-agent-fleet"), nats.Timeout(5 * time.Second)}
	tlsCfg, err := runtime.NATSTLSConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("nats tls config: %w", err)
	}
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
		log.Println("[mock-bank] NATS TLS enabled")
	}
	return nats.Connect(natsURL, opts...)
}
