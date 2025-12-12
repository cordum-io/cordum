package chatadvanced

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yaront1111/cortex-os/core/agent"
	ctxengine "github.com/yaront1111/cortex-os/core/context/engine"
	"github.com/yaront1111/cortex-os/core/infra/bus"
	"github.com/yaront1111/cortex-os/core/infra/config"
	"github.com/yaront1111/cortex-os/core/infra/memory"
	pb "github.com/yaront1111/cortex-os/core/protocol/pb/v1"
	"github.com/yaront1111/cortex-os/packages/providers/ollama"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	advancedWorkerID     = "worker-chat-advanced-1"
	advancedQueueGroup   = "workers-chat-advanced"
	advancedJobSubject   = "job.chat.advanced"
	advancedHeartbeatSub = "sys.heartbeat"
)

var advActiveJobs int32

// Run starts the advanced chat worker.
func Run() {
	log.Println("cortex worker chat-advanced starting...")

	cfg := config.Load()

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	defer memStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	ctxClient, closeCtxClient, err := ctxengine.NewClient(context.Background(), cfg.ContextEngineAddr)
	if err != nil {
		log.Fatalf("failed to connect to context engine: %v", err)
	}
	defer closeCtxClient()

	provider := ollama.NewFromEnv()

	if err := natsBus.Subscribe(advancedJobSubject, advancedQueueGroup, handleAdvancedChatJobWithContext(natsBus, memStore, provider, ctxClient)); err != nil {
		log.Fatalf("failed to subscribe to advanced chat jobs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendAdvancedHeartbeats(ctx, natsBus)
	}()

	log.Println("worker chat-advanced running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("worker chat-advanced shutting down")
	cancel()
	wg.Wait()
}

func handleAdvancedChatJob(b *bus.NatsBus, store memory.Store, provider agent.ModelProvider) func(*pb.BusPacket) {
	return handleAdvancedChatJobWithContext(b, store, provider, nil)
}

func handleAdvancedChatJobWithContext(b *bus.NatsBus, store memory.Store, provider agent.ModelProvider, ctxClient pb.ContextEngineClient) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		ctx := context.Background()
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&advActiveJobs, 1)
		defer atomic.AddInt32(&advActiveJobs, -1)

		payloadBytes, _ := loadPayload(ctx, store, req)
		prompt := extractPrompt(payloadBytes)

		var memoryID string
		var mode pb.ContextMode
		// Attempt to enrich prompt via Context Engine if available.
		if ctxClient != nil {
			memoryID = getEnv(req, "memory_id")
			if memoryID == "" {
				memoryID = "session:" + req.GetJobId()
			}
			mode = parseContextMode(req, pb.ContextMode_CONTEXT_MODE_CHAT)
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

		responseText, err := provider.Generate(ctx, prompt)
		if err != nil {
			log.Printf("[WORKER chat-adv] ollama call failed, using fallback: %v", err)
			responseText = "[fallback] " + prompt
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
				log.Printf("[WORKER chat-adv] failed to store result: %v", err)
			}
		}

		result := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
			ResultPtr:   resultPtr,
			WorkerId:    advancedWorkerID,
			ExecutionMs: 0,
		}

		resp := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        advancedWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload: &pb.BusPacket_JobResult{
				JobResult: result,
			},
		}

		if err := b.Publish("sys.job.result", resp); err != nil {
			log.Printf("[WORKER chat-adv] failed to publish result for job_id=%s: %v", req.JobId, err)
		} else {
			log.Printf("[WORKER chat-adv] completed job_id=%s", req.JobId)
		}
	}
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

func sendAdvancedHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:        advancedWorkerID,
				Region:          "local",
				Type:            "cpu",
				CpuLoad:         10,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&advActiveJobs),
				Capabilities:    []string{"chat-advanced"},
				Pool:            "chat-advanced",
				MaxParallelJobs: 2,
			}
			packet := &pb.BusPacket{
				TraceId:         "hb-" + advancedWorkerID,
				SenderId:        advancedWorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}
			if err := b.Publish(advancedHeartbeatSub, packet); err != nil {
				log.Printf("[WORKER chat-adv] failed to publish heartbeat: %v", err)
			}
		}
	}
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
