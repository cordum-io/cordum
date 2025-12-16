package chatadvanced

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/yaront1111/coretex-os/core/agent"
	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	ctxengine "github.com/yaront1111/coretex-os/core/context/engine"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"github.com/yaront1111/coretex-os/packages/providers/ollama"
)

const (
	advancedWorkerID   = "worker-chat-advanced-1"
	advancedQueueGroup = "workers-chat-advanced"
	advancedJobSubject = "job.chat.advanced"
)

// Run starts the advanced chat worker.
func Run() {
	log.Println("coretex worker chat-advanced starting...")

	cfg := config.Load()
	provider := ollama.NewFromEnv()

	ctxClient, closeCtxClient, err := ctxengine.NewClient(context.Background(), cfg.ContextEngineAddr)
	if err != nil {
		log.Fatalf("failed to connect to context engine: %v", err)
	}
	defer closeCtxClient()

	wConfig := worker.Config{
		WorkerID:        advancedWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      advancedQueueGroup,
		JobSubject:      advancedJobSubject,
		DirectSubject:   bus.DirectSubject(advancedWorkerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"chat-advanced"},
		Pool:            "chat-advanced",
		MaxParallelJobs: 2,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(func(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
		return handleChatAdvanced(ctx, req, store, provider, ctxClient)
	}); err != nil {
		log.Fatalf("worker chat-advanced failed: %v", err)
	}
}

func handleChatAdvanced(ctx context.Context, req *pb.JobRequest, store memory.Store, provider agent.ModelProvider, ctxClient pb.ContextEngineClient) (*pb.JobResult, error) {
	payloadBytes, _ := loadPayload(ctx, store, req)
	prompt := extractPrompt(payloadBytes)

	memoryID := getEnv(req, "memory_id")
	if memoryID == "" {
		memoryID = "session:" + req.GetJobId()
	}
	mode := parseContextMode(req, pb.ContextMode_CONTEXT_MODE_CHAT)

	if ctxClient != nil {
		win, err := ctxClient.BuildWindow(ctx, &pb.BuildWindowRequest{
			MemoryId:        memoryID,
			Mode:            mode,
			Model:           "chat",
			LogicalPayload:  payloadBytes,
			MaxInputTokens:  parseIntEnv(req, "max_input_tokens", 8000),
			MaxOutputTokens: parseIntEnv(req, "max_output_tokens", 1024),
		})
		if err == nil && len(win.GetMessages()) > 0 {
			prompt = flattenMessages(win.GetMessages())
		}
	}

	start := time.Now()
	responseText, err := provider.Generate(ctx, prompt)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return storeAdvancedResult(ctx, req, store, prompt, "", err)
	}

	if ctxClient != nil && memoryID != "" {
		_, _ = ctxClient.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
			MemoryId:       memoryID,
			LogicalPayload: payloadBytes,
			ModelResponse:  []byte(responseText),
			Mode:           mode,
		})
	}

	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)
	resultBody := map[string]any{
		"job_id":       req.JobId,
		"prompt":       prompt,
		"response":     responseText,
		"processed_by": advancedWorkerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"model":        "ollama",
	}
	if resultBytes, err := json.Marshal(resultBody); err == nil {
		if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
			log.Printf("[WORKER chat-advanced] failed to store result: %v", err)
		}
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   resultPtr,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}

func storeAdvancedResult(ctx context.Context, req *pb.JobRequest, store memory.Store, prompt, response string, err error) (*pb.JobResult, error) {
	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)

	resultBody := map[string]any{
		"job_id":       req.JobId,
		"prompt":       prompt,
		"response":     response,
		"processed_by": advancedWorkerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"model":        "ollama",
	}
	if err != nil {
		resultBody["error"] = map[string]any{"message": err.Error()}
	}
	if resultBytes, mErr := json.Marshal(resultBody); mErr == nil {
		if putErr := store.PutResult(ctx, resultKey, resultBytes); putErr != nil {
			log.Printf("[WORKER chat-advanced] failed to store result: %v", putErr)
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

func loadPayload(ctx context.Context, store memory.Store, req *pb.JobRequest) ([]byte, error) {
	if req == nil {
		return nil, nil
	}
	if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
		return store.GetContext(ctx, key)
	}
	return nil, nil
}

func extractPrompt(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err == nil {
		if p, ok := payload["prompt"].(string); ok {
			return p
		}
		if p, ok := payload["message"].(string); ok {
			return p
		}
	}
	return string(data)
}

func flattenMessages(msgs []*pb.ModelMessage) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, strings.TrimSpace(m.GetRole()+": "+m.GetContent()))
	}
	return strings.Join(parts, "\n")
}

func getEnv(req *pb.JobRequest, key string) string {
	if req == nil {
		return ""
	}
	if v, ok := req.GetEnv()[key]; ok {
		return v
	}
	return ""
}

func parseContextMode(req *pb.JobRequest, fallback pb.ContextMode) pb.ContextMode {
	mode := strings.ToLower(getEnv(req, "context_mode"))
	switch mode {
	case "chat":
		return pb.ContextMode_CONTEXT_MODE_CHAT
	case "rag":
		return pb.ContextMode_CONTEXT_MODE_RAG
	case "raw":
		return pb.ContextMode_CONTEXT_MODE_RAW
	default:
		return fallback
	}
}

func parseIntEnv(req *pb.JobRequest, key string, fallback int32) int32 {
	val := getEnv(req, key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return int32(n)
}

