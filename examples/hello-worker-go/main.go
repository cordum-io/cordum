package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
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

type echoPayload struct {
	Message string `json:"message"`
	Author  string `json:"author,omitempty"`
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

	worker, err := runtime.NewWorker(runtime.Config{
		Pool:            "hello-pack",
		Subjects:        []string{"job.hello-pack.echo"},
		NatsURL:         envOr("NATS_URL", defaultNatsURL),
		MaxParallelJobs: 4,
		Capabilities:    []string{"hello-pack.echo"},
	})
	if err != nil {
		log.Fatalf("worker init: %v", err)
	}
	defer func() {
		_ = worker.Close()
	}()

	log.Printf("hello worker ready (pool=%s)", "hello-pack")

	handler := func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
		payload := echoPayload{Message: "hello from worker"}
		if redisClient != nil && req.GetContextPtr() != "" {
			ctxData, err := fetchContext(ctx, redisClient, req.GetContextPtr())
			if err != nil {
				log.Printf("context fetch failed: %v", err)
			} else {
				if msg, ok := ctxData["message"].(string); ok && msg != "" {
					payload.Message = msg
				}
				if author, ok := ctxData["author"].(string); ok && author != "" {
					payload.Author = author
				}
			}
		}

		resultPtr := ""
		if redisClient != nil {
			ptr, err := storeResult(ctx, redisClient, req.GetJobId(), payload)
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

func storeResult(ctx context.Context, client *redis.Client, jobID string, payload echoPayload) (string, error) {
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
