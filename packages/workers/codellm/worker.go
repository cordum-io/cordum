package codellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yaront1111/coretex-os/core/agent"
	ctxengine "github.com/yaront1111/coretex-os/core/context/engine"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"github.com/yaront1111/coretex-os/packages/providers/ollama"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	codeLLMWorkerID     = "worker-code-llm-1"
	codeLLMQueueGroup   = "workers-code-llm"
	codeLLMJobSubject   = "job.code.llm"
	codeLLMHeartbeatSub = "sys.heartbeat"
)

var codeWorkerID = resolveWorkerID(codeLLMWorkerID)

var codeActiveJobs int32

type codeContext struct {
	FilePath    string `json:"file_path"`
	CodeSnippet string `json:"code_snippet"`
	Instruction string `json:"instruction"`
}

type codeResult struct {
	FilePath     string          `json:"file_path"`
	OriginalCode string          `json:"original_code"`
	Instruction  string          `json:"instruction"`
	Patch        structuredPatch `json:"patch"`
}

type structuredPatch struct {
	Type    string `json:"type"`    // e.g., unified_diff
	Content string `json:"content"` // diff or patch text
}

// Run starts the code-llm worker.
func Run() {
	log.Println("coretex worker code-llm starting...")

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

	if err := natsBus.Subscribe(codeLLMJobSubject, codeLLMQueueGroup, handleCodeJob(natsBus, memStore, provider, ctxClient)); err != nil {
		log.Fatalf("failed to subscribe to code llm jobs: %v", err)
	}
	if direct := bus.DirectSubject(codeWorkerID); direct != "" {
		if err := natsBus.Subscribe(direct, "", handleCodeJob(natsBus, memStore, provider, ctxClient)); err != nil {
			log.Fatalf("failed to subscribe to direct code llm jobs: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendCodeHeartbeats(ctx, natsBus)
	}()

	log.Println("worker code-llm running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("worker code-llm shutting down")
	cancel()
	wg.Wait()
}

func handleCodeJob(b *bus.NatsBus, store memory.Store, provider agent.ModelProvider, ctxClient pb.ContextEngineClient) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&codeActiveJobs, 1)
		defer atomic.AddInt32(&codeActiveJobs, -1)

		ctx := context.Background()
		var ctxPayload codeContext
		var payloadBytes []byte
		if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
			if data, err := store.GetContext(ctx, key); err == nil {
				payloadBytes = data
				if err := json.Unmarshal(data, &ctxPayload); err != nil {
					log.Printf("[WORKER code-llm] failed to decode context for job_id=%s: %v", req.JobId, err)
				}
			} else {
				log.Printf("[WORKER code-llm] failed to read context for job_id=%s: %v", req.JobId, err)
			}
		} else {
			log.Printf("[WORKER code-llm] invalid context_ptr for job_id=%s: %v", req.JobId, err)
		}

		log.Printf("[WORKER code-llm] received job_id=%s topic=%s file=%s", req.JobId, req.Topic, ctxPayload.FilePath)

		start := time.Now()

		prompt := buildPrompt(ctxPayload)
		memoryID := getEnv(req, "memory_id")
		if memoryID == "" {
			memoryID = "session:" + req.GetJobId()
		}
		mode := parseContextMode(req, pb.ContextMode_CONTEXT_MODE_RAG)
		maxIn := parseIntEnv(req, "max_input_tokens", 12000)
		maxOut := parseIntEnv(req, "max_output_tokens", 2048)
		if ctxClient != nil {
			win, err := ctxClient.BuildWindow(ctx, &pb.BuildWindowRequest{
				MemoryId:        memoryID,
				Mode:            mode,
				Model:           "code",
				LogicalPayload:  payloadBytes,
				MaxInputTokens:  maxIn,
				MaxOutputTokens: maxOut,
			})
			if err == nil && len(win.GetMessages()) > 0 {
				prompt = flattenMessages(win.GetMessages())
			}
		}

		respText, err := provider.Generate(ctx, prompt)
		status := pb.JobStatus_JOB_STATUS_SUCCEEDED
		if err != nil {
			log.Printf("[WORKER code-llm] ollama call failed job_id=%s: %v", req.JobId, err)
			status = pb.JobStatus_JOB_STATUS_FAILED
			respText = err.Error()
		}

		if ctxClient != nil {
			_, _ = ctxClient.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
				MemoryId:       memoryID,
				LogicalPayload: payloadBytes,
				ModelResponse:  []byte(respText),
				Mode:           mode,
			})
		}

		result := codeResult{
			FilePath:     ctxPayload.FilePath,
			OriginalCode: ctxPayload.CodeSnippet,
			Instruction:  ctxPayload.Instruction,
			Patch: structuredPatch{
				Type:    "unified_diff",
				Content: respText,
			},
		}

		resultBytes, _ := json.Marshal(result)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, resultBytes); err != nil {
			log.Printf("[WORKER code-llm] failed to store result for job_id=%s: %v", req.JobId, err)
		}
		resultPtr := memory.PointerForKey(resKey)

		jobRes := &pb.JobResult{
			JobId:       req.JobId,
			Status:      status,
			ResultPtr:   resultPtr,
			WorkerId:    codeWorkerID,
			ExecutionMs: time.Since(start).Milliseconds(),
		}

		response := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        codeWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload: &pb.BusPacket_JobResult{
				JobResult: jobRes,
			},
		}

		if err := b.Publish("sys.job.result", response); err != nil {
			log.Printf("[WORKER code-llm] failed to publish result for job_id=%s: %v", req.JobId, err)
		} else {
			log.Printf("[WORKER code-llm] completed job_id=%s duration_ms=%d", req.JobId, jobRes.ExecutionMs)
		}
	}
}

func sendCodeHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:        codeWorkerID,
				Region:          "local",
				Type:            "cpu",
				CpuLoad:         5,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&codeActiveJobs),
				Capabilities:    []string{"code-llm"},
				Pool:            "code-llm",
				MaxParallelJobs: 2,
			}

			packet := &pb.BusPacket{
				TraceId:         "hb-" + codeWorkerID,
				SenderId:        codeWorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}

			if err := b.Publish(codeLLMHeartbeatSub, packet); err != nil {
				log.Printf("[WORKER code-llm] failed to publish heartbeat: %v", err)
			}
		}
	}
}

func resolveWorkerID(defaultID string) string {
	if v := os.Getenv("WORKER_ID"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		if len(h) > 8 {
			h = h[len(h)-8:]
		}
		return fmt.Sprintf("%s-%s", defaultID, h)
	}
	return defaultID
}

func callModel(ctxPayload codeContext, provider agent.ModelProvider) (codeResult, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	prompt := buildPrompt(ctxPayload)
	resp, err := provider.Generate(reqCtx, prompt)
	if err != nil {
		return codeResult{}, err
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return codeResult{}, fmt.Errorf("model empty response")
	}

	return codeResult{
		FilePath:     ctxPayload.FilePath,
		OriginalCode: ctxPayload.CodeSnippet,
		Instruction:  ctxPayload.Instruction,
		Patch: structuredPatch{
			Type:    "unified_diff",
			Content: resp,
		},
	}, nil
}

func buildPrompt(ctxPayload codeContext) string {
	return fmt.Sprintf("You are a code assistant. Given file %s and instruction: %s\nCode:\n%s\nGenerate a concise patch (diff or replacement) to satisfy the instruction.",
		ctxPayload.FilePath, ctxPayload.Instruction, ctxPayload.CodeSnippet)
}

func callModelWithRetry(ctxPayload codeContext, provider agent.ModelProvider) (codeResult, error) {
	const maxAttempts = 3

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := callModel(ctxPayload, provider)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts {
			break
		}
		backoff := time.Duration(attempt*2) * time.Second
		log.Printf("[WORKER code-llm] retrying model attempt=%d after error: %v", attempt, err)
		time.Sleep(backoff)
	}

	return codeResult{}, lastErr
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	retryHints := []string{
		"timeout",
		"deadline exceeded",
		"connection refused",
		"connection reset",
		"temporarily unavailable",
		"503",
	}
	for _, hint := range retryHints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
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

func flattenMessages(msgs []*pb.ModelMessage) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, strings.TrimSpace(m.GetRole()+": "+m.GetContent()))
	}
	return strings.Join(parts, "\n")
}
