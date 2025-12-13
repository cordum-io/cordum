package chat

import (
	"context"
	"encoding/json"
	"log"
	"time"

	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler" // New import for EffectiveConfigEnvVar
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	chatWorkerID = "worker-chat-1"
)

// Run starts the chat worker.
func Run() {
	log.Println("coretex worker chat starting...")

	cfg := config.Load()

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

	if err := w.Start(chatHandler); err != nil {
		log.Fatalf("worker chat failed: %v", err)
	}
}

func chatHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	// Extract and unmarshal EffectiveConfig
	var effectiveConfig config.EffectiveConfig
	if env := req.GetEnv(); env != nil {
		if ecJson, ok := env[scheduler.EffectiveConfigEnvVar]; ok && ecJson != "" {
			if err := json.Unmarshal([]byte(ecJson), &effectiveConfig); err != nil {
				log.Printf("[WORKER chat] job_id=%s: failed to unmarshal effective config: %v", req.JobId, err)
				// Proceed with default EffectiveConfig
			} else {
				log.Printf("[WORKER chat] job_id=%s: received effective config for org %s, team %s", req.JobId, effectiveConfig.OrgID, effectiveConfig.TeamID)
				if effectiveConfig.Safety.PIIDetectionEnabled {
					log.Printf("[WORKER chat] PII Detection is ENABLED for this job.")
				}
				if effectiveConfig.Models.DefaultModel != "" {
					log.Printf("[WORKER chat] Using default model: %s", effectiveConfig.Models.DefaultModel)
				}
			}
		} else {
			log.Printf("[WORKER chat] job_id=%s: No effective config found in EnvVars. Using default behavior.", req.JobId)
		}
	} else {
		log.Printf("[WORKER chat] job_id=%s: No effective config found in EnvVars. Using default behavior.", req.JobId)
	}

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
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   resultPtr,
		ExecutionMs: 0, // Instant
	}, nil
}
