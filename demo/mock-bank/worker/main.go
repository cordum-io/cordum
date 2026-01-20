package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/redis/go-redis/v9"
)

const (
	defaultNatsURL  = "nats://localhost:4222"
	defaultRedisURL = "redis://localhost:6379"
	resultTTL       = 24 * time.Hour
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

	redisClient, err := newRedisClient(envOr("REDIS_URL", defaultRedisURL))
	if err != nil {
		log.Fatalf("redis init: %v", err)
	}
	if redisClient != nil {
		defer func() {
			_ = redisClient.Close()
		}()
	}

	workerID := envOr("WORKER_ID", "demo-mock-bank-worker")
	directSubject := "worker." + workerID + ".jobs"

	worker, err := runtime.NewWorker(runtime.Config{
		WorkerID: workerID,
		Pool:     "demo-mock-bank",
		Subjects: []string{
			directSubject,
			"job.demo-mock-bank.transfer.auto",
			"job.demo-mock-bank.transfer.review",
			"job.demo-mock-bank.transfer.blocked",
		},
		NatsURL:         envOr("NATS_URL", defaultNatsURL),
		MaxParallelJobs: 4,
		Capabilities:    []string{"demo-mock-bank.transfer"},
		Labels:          map[string]string{"demo": "mock-bank"},
	})
	if err != nil {
		log.Fatalf("worker init: %v", err)
	}
	defer func() {
		_ = worker.Close()
	}()

	log.Printf("mock-bank worker ready (pool=%s)", "demo-mock-bank")

	handler := func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
		payload := transferPayload{
			Currency:    "USD",
			Customer:    "Unknown",
			Reason:      "",
			Note:        "",
			RequestedBy: "agent-demo",
		}

		if redisClient != nil && req.GetContextPtr() != "" {
			ctxData, err := fetchContext(ctx, redisClient, req.GetContextPtr())
			if err != nil {
				log.Printf("context fetch failed: %v", err)
			} else {
				if val, ok := ctxData["amount"]; ok {
					payload.Amount = val
				}
				payload.Currency = readString(ctxData, "currency", payload.Currency)
				payload.Customer = readString(ctxData, "customer", payload.Customer)
				payload.Reason = readString(ctxData, "reason", payload.Reason)
				payload.Note = readString(ctxData, "note", payload.Note)
				payload.RequestedBy = readString(ctxData, "requested_by", payload.RequestedBy)
			}
		}

		amount := parseAmount(payload.Amount)
		result := transferResult{
			JobID:       req.GetJobId(),
			Amount:      amount,
			Currency:    payload.Currency,
			Customer:    payload.Customer,
			Reason:      payload.Reason,
			Note:        payload.Note,
			RequestedBy: payload.RequestedBy,
			Topic:       req.GetTopic(),
			Status:      "executed",
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
			ReferenceID: "transfer-" + req.GetJobId(),
		}

		resultPtr := ""
		if redisClient != nil {
			ptr, err := storeResult(ctx, redisClient, req.GetJobId(), result)
			if err != nil {
				log.Printf("result store failed: %v", err)
			} else {
				resultPtr = ptr
			}
		}

		return &agentv1.JobResult{
			JobId:     req.GetJobId(),
			Status:    agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: resultPtr,
		}, nil
	}

	if err := worker.Run(ctx, handler); err != nil {
		log.Fatalf("worker run: %v", err)
	}
}

func readString(payload map[string]any, key, fallback string) string {
	if payload == nil {
		return fallback
	}
	if val, ok := payload[key]; ok {
		switch v := val.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case int64:
			return strconv.FormatInt(v, 10)
		}
	}
	return fallback
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

func fetchContext(ctx context.Context, client *redis.Client, ptr string) (map[string]any, error) {
	key, err := keyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func storeResult(ctx context.Context, client *redis.Client, jobID string, payload any) (string, error) {
	if jobID == "" {
		return "", errors.New("job id required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	key := "res:" + jobID
	if err := client.Set(ctx, key, data, resultTTL).Err(); err != nil {
		return "", err
	}
	return "redis://" + key, nil
}

func keyFromPointer(ptr string) (string, error) {
	ptr = strings.TrimSpace(ptr)
	if ptr == "" {
		return "", errors.New("empty pointer")
	}
	if !strings.HasPrefix(ptr, "redis://") {
		return "", errors.New("unsupported pointer prefix")
	}
	key := strings.TrimPrefix(ptr, "redis://")
	if key == "" {
		return "", errors.New("missing key")
	}
	return key, nil
}

func envOr(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func newRedisClient(url string) (*redis.Client, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, nil
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return client, nil
}
