package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"math/big"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
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
		Pool:         "demo-mock-bank",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     8,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"transfer", "wire", "compliance", "aml-check"},
		Labels:       map[string]string{"name": "Transfer Agent 01", "env": "production", "tier": "critical"},
	},
	{
		ID:           "megacorp-transfer-agent-02",
		Pool:         "demo-mock-bank",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     8,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"transfer", "wire", "compliance", "aml-check"},
		Labels:       map[string]string{"name": "Transfer Agent 02", "env": "production", "tier": "critical"},
	},
	{
		ID:           "megacorp-compliance-scanner",
		Pool:         "demo-mock-bank",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     4,
		Region:       "us-west-2",
		Type:         "cpu",
		Capabilities: []string{"compliance", "aml-check", "sanctions-screening", "audit"},
		Labels:       map[string]string{"name": "Compliance Scanner", "env": "production", "tier": "standard"},
	},
	{
		ID:           "megacorp-audit-recorder",
		Pool:         "demo-mock-bank",
		Topics:       []string{"job.demo-mock-bank.transfer"},
		Capacity:     2,
		Region:       "eu-west-1",
		Type:         "cpu",
		Capabilities: []string{"audit", "reporting", "regulatory"},
		Labels:       map[string]string{"name": "Audit Recorder", "env": "production", "tier": "standard"},
	},
	// bank-validators pool — used by production gate tests (gates 5, 6, 7, 8, 11, 12, etc.)
	{
		ID:           "megacorp-validator-01",
		Pool:         "bank-validators",
		Topics:       []string{"job.bank-validators.process"},
		Capacity:     8,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"validate", "compliance", "aml-check"},
		Labels:       map[string]string{"name": "Validator 01", "env": "production", "tier": "critical"},
	},
	{
		ID:           "megacorp-validator-02",
		Pool:         "bank-validators",
		Topics:       []string{"job.bank-validators.process"},
		Capacity:     4,
		Region:       "us-west-2",
		Type:         "cpu",
		Capabilities: []string{"validate", "compliance"},
		Labels:       map[string]string{"name": "Validator 02", "env": "production", "tier": "standard"},
	},
	// bank-executors pool — used by production gate 16 (degradation)
	{
		ID:           "megacorp-executor-01",
		Pool:         "bank-executors",
		Topics:       []string{"job.bank-executors.process"},
		Capacity:     4,
		Region:       "us-east-1",
		Type:         "cpu",
		Capabilities: []string{"execute", "wire", "transfer"},
		Labels:       map[string]string{"name": "Executor 01", "env": "production", "tier": "critical"},
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
	// Structured JSON logs to stderr so operators can grep per-job state
	// (see demo/mock-bank/README.md "Troubleshooting a stalled run").
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

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

		// Per-workerDef in-flight counter. The heartbeat callback reads
		// it on each tick so /api/v1/workers reflects actual load, not
		// a simulated number. Counter state is lost if the process is
		// killed mid-job; the next heartbeat after restart reads zero,
		// which is the correct snapshot.
		var counter atomic.Int32
		handler := newCountingHandler(&counter, makeHandler(worker.ID, worker.Pool))

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
				active := counter.Load()
				cpuLoad := 5.0 + randFloat32()*35.0  // 5–40%
				memLoad := 20.0 + randFloat32()*40.0 // 20–60%
				return buildHeartbeat(worker, active, float32(cpuLoad), float32(memLoad))
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

// newCountingHandler wraps a bank handler with an in-flight counter and
// a panic recovery. counter.Add(1) on entry, defer Add(-1) runs whether
// the handler returns success, error, or panics. The heartbeat callback
// reads counter.Load() to publish a real active_jobs value.
func newCountingHandler(
	counter *atomic.Int32,
	inner func(runtime.Context, bankPayload) (bankResult, error),
) func(runtime.Context, bankPayload) (bankResult, error) {
	return func(ctx runtime.Context, payload bankPayload) (res bankResult, err error) {
		counter.Add(1)
		defer counter.Add(-1)
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("mock-bank handler panic: %v", r)
			}
		}()
		return inner(ctx, payload)
	}
}

// makeHandler returns the production bank handler: structured per-job
// logs, a randomized 200–2000 ms work simulation, and the verdict bucket
// derived from amount (matches the policy fragment in pack/overlays/).
func makeHandler(workerID, pool string) func(runtime.Context, bankPayload) (bankResult, error) {
	return makeHandlerWithLogger(workerID, pool, slog.Default(), randomSleep)
}

// makeHandlerWithLogger is the test seam for makeHandler — the logger
// and sleep function can be swapped for deterministic assertions.
func makeHandlerWithLogger(
	workerID, pool string,
	logger *slog.Logger,
	sleepFn func(context.Context),
) func(runtime.Context, bankPayload) (bankResult, error) {
	return func(ctx runtime.Context, payload bankPayload) (bankResult, error) {
		start := time.Now()
		jobID := ctx.Job.GetJobId()
		topic := ctx.Job.GetTopic()
		amount := parseAmount(payload.Amount)
		verdict, rule := classifyAmount(amount)

		logger.Info("mock-bank job_received",
			"job_id", jobID,
			"worker_id", workerID,
			"pool", pool,
			"topic", topic,
			"amount", amount,
		)
		logger.Info("mock-bank decision_made",
			"job_id", jobID,
			"worker_id", workerID,
			"verdict", verdict,
			"rule", rule,
		)

		sleepFn(context.Background())

		message := payload.Message
		if message == "" {
			message = payload.Prompt
		}
		if message == "" {
			message = fmt.Sprintf("Processed by %s", pool)
		}

		ref := pool
		if n := len(jobID); n > 0 {
			short := jobID
			if n >= 8 {
				short = jobID[:8]
			}
			ref = fmt.Sprintf("%s-%s", pool, short)
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
			ReferenceID: ref,
			Message:     message,
		}

		logger.Info("mock-bank job_completed",
			"job_id", jobID,
			"worker_id", workerID,
			"verdict", verdict,
			"duration_ms", time.Since(start).Milliseconds(),
			"reference_id", ref,
		)
		return result, nil
	}
}

// classifyAmount reports the verdict bucket a transfer falls into. The
// thresholds mirror demo/mock-bank/pack/overlays/policy.fragment.yaml
// so the logged verdict/rule matches the safety kernel decision a
// reviewer sees in the UI. If the policy fragment changes, update both.
func classifyAmount(amount float64) (verdict, rule string) {
	switch {
	case amount < 100:
		return "allow", "bank-transfer-allow"
	case amount < 300:
		return "require_approval", "bank-transfer-review"
	default:
		return "deny", "bank-transfer-blocked"
	}
}

// randomSleep simulates 200–2000 ms of bank work so the demo visibly
// occupies a worker slot. Replaced in tests by a no-op sleep.
func randomSleep(_ context.Context) {
	time.Sleep(time.Duration(200+randInt(1800)) * time.Millisecond)
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
	if token := os.Getenv("NATS_TOKEN"); token != "" {
		opts = append(opts, nats.Token(token))
		log.Println("[mock-bank] NATS token auth configured")
	}
	return nats.Connect(natsURL, opts...)
}
