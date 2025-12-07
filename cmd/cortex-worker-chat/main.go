package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"github.com/yaront1111/cortex-os/core/pkg/sdk/worker"
)

const (
	chatWorkerID = "worker-chat-1"
)

func main() {
	log.Println("cortex worker chat starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        chatWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-chat",
		JobSubject:      "job.chat.simple",
		HeartbeatSub:    "sys.heartbeat.chat",
		Capabilities:    []string{"chat"},
		Pool:            "chat-simple",
		MaxParallelJobs: 2,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(chatHandler); err != nil {
		log.Fatalf("worker chat failed: %v", err)
	}
}

func chatHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	// 1. Fetch & Parse Context
	var prompt string
	if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
		if data, err := store.GetContext(ctx, key); err == nil {
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err == nil {
				if p, ok := payload["prompt"].(string); ok {
					prompt = p
				}
			}
		}
	}

	log.Printf("[WORKER chat] processing job_id=%s", req.JobId)

	// 2. Business Logic
	responseText := "Echo: " + prompt

	// 3. Store Result
	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)
	resultBody := map[string]any{
		"job_id":       req.JobId,
		"prompt":       prompt,
		"response":     responseText,
		"processed_by": chatWorkerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	
	resultBytes, _ := json.Marshal(resultBody)
	if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
		log.Printf("[WORKER chat] failed to store result: %v", err)
	}

	// 4. Return Result
	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
		ResultPtr:   resultPtr,
		ExecutionMs: 0, // Instant
	}, nil
}