package codellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
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
	defaultWorkerID = "worker-code-llm-1"
	queueGroup      = "workers-code-llm"
	jobSubject      = "job.code.llm"
)

var workerID = resolveWorkerID(defaultWorkerID)

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
	Type    string `json:"type"`
	Content string `json:"content"`
}

// Run starts the code-llm worker.
func Run() {
	log.Println("coretex worker code-llm starting...")

	cfg := config.Load()
	provider := ollama.NewFromEnv()

	ctxClient, closeCtxClient, err := ctxengine.NewClient(context.Background(), cfg.ContextEngineAddr)
	if err != nil {
		log.Fatalf("failed to connect to context engine: %v", err)
	}
	defer closeCtxClient()

	wConfig := worker.Config{
		WorkerID:        workerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      queueGroup,
		JobSubject:      jobSubject,
		DirectSubject:   bus.DirectSubject(workerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"code-llm"},
		Pool:            "code-llm",
		MaxParallelJobs: 2,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(func(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
		return handleCodeLLM(ctx, req, store, provider, ctxClient)
	}); err != nil {
		log.Fatalf("worker code-llm failed: %v", err)
	}
}

func handleCodeLLM(ctx context.Context, req *pb.JobRequest, store memory.Store, provider agent.ModelProvider, ctxClient pb.ContextEngineClient) (*pb.JobResult, error) {
	start := time.Now()

	payloadBytes, ctxPayload, err := loadCodeContext(ctx, req, store)
	if err != nil {
		return storeCodeResult(ctx, req, store, codeResult{}, err, 0)
	}

	prompt := buildPrompt(ctxPayload)

	memoryID := getEnv(req, "memory_id")
	if memoryID == "" {
		memoryID = "session:" + req.GetJobId()
	}
	mode := parseContextMode(req, pb.ContextMode_CONTEXT_MODE_RAG)
	maxIn := parseIntEnv(req, "max_input_tokens", 12000)
	maxOut := parseIntEnv(req, "max_output_tokens", 2048)

	if ctxClient != nil && len(payloadBytes) > 0 {
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
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return storeCodeResult(ctx, req, store, codeResult{
			FilePath:     ctxPayload.FilePath,
			OriginalCode: ctxPayload.CodeSnippet,
			Instruction:  ctxPayload.Instruction,
			Patch: structuredPatch{
				Type:    "unified_diff",
				Content: err.Error(),
			},
		}, err, time.Since(start).Milliseconds())
	}

	respText = strings.TrimSpace(respText)
	result := codeResult{
		FilePath:     ctxPayload.FilePath,
		OriginalCode: ctxPayload.CodeSnippet,
		Instruction:  ctxPayload.Instruction,
		Patch: structuredPatch{
			Type:    "unified_diff",
			Content: respText,
		},
	}

	if ctxClient != nil {
		_, _ = ctxClient.UpdateMemory(ctx, &pb.UpdateMemoryRequest{
			MemoryId:       memoryID,
			LogicalPayload: payloadBytes,
			ModelResponse:  []byte(respText),
			Mode:           mode,
		})
	}

	return storeCodeResult(ctx, req, store, result, nil, time.Since(start).Milliseconds())
}

func loadCodeContext(ctx context.Context, req *pb.JobRequest, store memory.Store) ([]byte, codeContext, error) {
	if req == nil {
		return nil, codeContext{}, fmt.Errorf("nil request")
	}
	if req.ContextPtr == "" {
		return nil, codeContext{}, fmt.Errorf("missing context_ptr")
	}
	key, err := memory.KeyFromPointer(req.ContextPtr)
	if err != nil {
		return nil, codeContext{}, fmt.Errorf("invalid context_ptr: %w", err)
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, codeContext{}, fmt.Errorf("read context: %w", err)
	}
	var payload codeContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return data, codeContext{}, fmt.Errorf("decode context: %w", err)
	}
	return data, payload, nil
}

func storeCodeResult(ctx context.Context, req *pb.JobRequest, store memory.Store, result codeResult, runErr error, execMs int64) (*pb.JobResult, error) {
	resKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resKey)

	payload := map[string]any{
		"file_path":     result.FilePath,
		"original_code": result.OriginalCode,
		"instruction":   result.Instruction,
		"patch": map[string]any{
			"type":    result.Patch.Type,
			"content": result.Patch.Content,
		},
		"processed_by": workerID,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"model":        "ollama",
	}
	if runErr != nil {
		payload["error"] = map[string]any{"message": runErr.Error()}
	}
	if resultBytes, err := json.Marshal(payload); err == nil {
		if err := store.PutResult(ctx, resKey, resultBytes); err != nil {
			log.Printf("[WORKER code-llm] failed to store result for job_id=%s: %v", req.JobId, err)
		}
	}

	status := pb.JobStatus_JOB_STATUS_SUCCEEDED
	errMsg := ""
	if runErr != nil {
		status = pb.JobStatus_JOB_STATUS_FAILED
		errMsg = runErr.Error()
	}

	return &pb.JobResult{
		JobId:        req.JobId,
		Status:       status,
		ResultPtr:    resultPtr,
		ExecutionMs:  execMs,
		ErrorMessage: errMsg,
	}, nil
}

func buildPrompt(ctxPayload codeContext) string {
	return fmt.Sprintf(
		"You are a code assistant. Given file %s and instruction: %s\nCode:\n%s\nGenerate a concise patch (diff or replacement) to satisfy the instruction.",
		ctxPayload.FilePath,
		ctxPayload.Instruction,
		ctxPayload.CodeSnippet,
	)
}

func resolveWorkerID(fallback string) string {
	if v := os.Getenv("WORKER_ID"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		if len(h) > 8 {
			h = h[len(h)-8:]
		}
		return fmt.Sprintf("%s-%s", fallback, h)
	}
	return fallback
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

func flattenMessages(msgs []*pb.ModelMessage) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, strings.TrimSpace(m.GetRole()+": "+m.GetContent()))
	}
	return strings.Join(parts, "\n")
}

