package chat

import (
	"context"
	"errors"
	"encoding/json"
	"log"
	"time"

	"github.com/yaront1111/coretex-os/core/agent"
	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"github.com/yaront1111/coretex-os/packages/providers/ollama"
)

const (
	chatWorkerID = "worker-chat-1"
)

// Run starts the chat worker.
func Run() {
	log.Println("coretex worker chat starting...")

	cfg := config.Load()
	provider := ollama.NewFromEnv()

	wConfig := worker.Config{
		WorkerID:        chatWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-chat",
		JobSubject:      "job.chat.simple",
		DirectSubject:   bus.DirectSubject(chatWorkerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"chat"},
		Pool:            "chat-simple",
		MaxParallelJobs: 2,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(func(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
		return chatHandlerWithProvider(ctx, req, store, provider)
	}); err != nil {
		log.Fatalf("worker chat failed: %v", err)
	}
}

func chatHandlerWithProvider(ctx context.Context, req *pb.JobRequest, store memory.Store, provider agent.ModelProvider) (*pb.JobResult, error) {
	// 1. Fetch & parse context
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

	start := time.Now()

	// 2. Model call
	responseText, err := provider.Generate(ctx, prompt)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return storeChatResult(ctx, req, store, prompt, "", err)
	}

	// 3. Store result
	resultBody := map[string]any{
		"job_id":       req.JobId,
		"prompt":       prompt,
		"response":     responseText,
		"processed_by": chatWorkerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"model":        "ollama",
	}

	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)
	if resultBytes, err := json.Marshal(resultBody); err == nil {
		if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
			log.Printf("[WORKER chat] failed to store result: %v", err)
		}
	}
	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   resultPtr,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}

func storeChatResult(ctx context.Context, req *pb.JobRequest, store memory.Store, prompt, response string, err error) (*pb.JobResult, error) {
	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)

	resultBody := map[string]any{
		"job_id":       req.JobId,
		"prompt":       prompt,
		"response":     response,
		"processed_by": chatWorkerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"model":        "ollama",
	}
	if err != nil {
		resultBody["error"] = map[string]any{"message": err.Error()}
	}
	if resultBytes, mErr := json.Marshal(resultBody); mErr == nil {
		if putErr := store.PutResult(ctx, resultKey, resultBytes); putErr != nil {
			log.Printf("[WORKER chat] failed to store result: %v", putErr)
		}
	}

	if err != nil {
		return &pb.JobResult{
			JobId:        req.JobId,
			Status:       pb.JobStatus_JOB_STATUS_FAILED,
			ResultPtr:    resultPtr,
			ErrorMessage: err.Error(),
		}, nil
	}
	return &pb.JobResult{
		JobId:     req.JobId,
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: resultPtr,
	}, nil
}
