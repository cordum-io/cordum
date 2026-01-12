package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	"github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/redis/go-redis/v9"
)

const pointerPrefix = "redis://"

func main() {
	natsURL := getenv("NATS_URL", "nats://localhost:4222")
	redisURL := getenv("REDIS_URL", "redis://localhost:6379/0")
	pool := getenv("CORDUM_POOL", "hello-pack")
	subject := getenv("CORDUM_SUBJECT", "job.hello-pack.echo")
	workerID := getenv("WORKER_ID", fmt.Sprintf("hello-pack-worker-%d", time.Now().UTC().UnixNano()))

	rdb, err := newRedis(redisURL)
	if err != nil {
		log.Printf("redis disabled: %v", err)
	}
	if rdb != nil {
		defer rdb.Close()
	}

	worker, err := runtime.NewWorker(runtime.Config{
		WorkerID: workerID,
		Pool:     pool,
		Subjects: []string{subject, directSubject(workerID)},
		NatsURL:  natsURL,
	})
	if err != nil {
		log.Fatalf("worker init: %v", err)
	}
	defer worker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("hello worker ready subject=%s pool=%s worker_id=%s", subject, pool, workerID)
	if err := worker.Run(ctx, func(jobCtx context.Context, req *v1.JobRequest) (*v1.JobResult, error) {
		input := readContext(jobCtx, rdb, req.GetContextPtr())
		message := extractMessage(input)
		if message == "" {
			message = "hello from cordum"
		}

		output := map[string]any{
			"echo":        message,
			"job_id":      req.GetJobId(),
			"received_at": time.Now().UTC().Format(time.RFC3339),
			"input":       input,
		}

		resultPtr := ""
		if rdb != nil {
			if ptr, err := writeResult(jobCtx, rdb, req.GetJobId(), output); err == nil {
				resultPtr = ptr
			} else {
				log.Printf("result write failed job=%s err=%v", req.GetJobId(), err)
			}
		}

		return &v1.JobResult{
			JobId:     req.GetJobId(),
			Status:    v1.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr: resultPtr,
		}, nil
	}); err != nil && ctx.Err() == nil {
		log.Printf("worker stopped: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func newRedis(url string) (*redis.Client, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opt)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func readContext(ctx context.Context, rdb *redis.Client, ptr string) map[string]any {
	if rdb == nil || strings.TrimSpace(ptr) == "" {
		return map[string]any{}
	}
	key := strings.TrimPrefix(ptr, pointerPrefix)
	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil || len(data) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"raw": string(data)}
	}
	return out
}

func writeResult(ctx context.Context, rdb *redis.Client, jobID string, payload map[string]any) (string, error) {
	if rdb == nil || jobID == "" {
		return "", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	key := "res:" + jobID
	if err := rdb.Set(ctx, key, data, 0).Err(); err != nil {
		return "", err
	}
	return pointerPrefix + key, nil
}

func extractMessage(input map[string]any) string {
	if input == nil {
		return ""
	}
	if raw, ok := input["message"]; ok {
		if msg, ok := raw.(string); ok {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

func directSubject(workerID string) string {
	if strings.TrimSpace(workerID) == "" {
		return ""
	}
	return "worker." + workerID + ".jobs"
}
