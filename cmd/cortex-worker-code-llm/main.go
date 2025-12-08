package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	codeLLMWorkerID     = "worker-code-llm-1"
	codeLLMQueueGroup   = "workers-code-llm"
	codeLLMJobSubject   = "job.code.llm"
	codeLLMHeartbeatSub = "sys.heartbeat.code-llm"
)

var codeActiveJobs int32

var (
	ollamaURL   = envOrDefault("OLLAMA_URL", "http://ollama:11434")
	ollamaModel = envOrDefault("OLLAMA_MODEL", "llama3")
	httpClient  = &http.Client{Timeout: 150 * time.Second}
)

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

func main() {
	log.Println("cortex worker code-llm starting...")

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

	if err := checkOllamaHealth(context.Background()); err != nil {
		log.Fatalf("ollama health check failed: %v", err)
	}

	if err := natsBus.Subscribe(codeLLMJobSubject, codeLLMQueueGroup, handleCodeJob(natsBus, memStore)); err != nil {
		log.Fatalf("failed to subscribe to code llm jobs: %v", err)
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

func handleCodeJob(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&codeActiveJobs, 1)
		defer atomic.AddInt32(&codeActiveJobs, -1)

		ctx := context.Background()
		var ctxPayload codeContext
		if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
			if data, err := store.GetContext(ctx, key); err == nil {
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

		result, err := callOllamaWithRetry(ctxPayload)
		status := pb.JobStatus_JOB_STATUS_COMPLETED
		if err != nil {
			log.Printf("[WORKER code-llm] ollama call failed job_id=%s: %v", req.JobId, err)
			status = pb.JobStatus_JOB_STATUS_FAILED
			result = codeResult{
				FilePath:     ctxPayload.FilePath,
				OriginalCode: ctxPayload.CodeSnippet,
				Instruction:  ctxPayload.Instruction,
				Patch: structuredPatch{
					Type:    "error",
					Content: err.Error(),
				},
			}
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
			WorkerId:    codeLLMWorkerID,
			ExecutionMs: time.Since(start).Milliseconds(),
		}

		response := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        codeLLMWorkerID,
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
				WorkerId:        codeLLMWorkerID,
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
				TraceId:         "hb-" + codeLLMWorkerID,
				SenderId:        codeLLMWorkerID,
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

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

func callOllama(ctxPayload codeContext) (codeResult, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	prompt := buildPrompt(ctxPayload)
	reqBody, _ := json.Marshal(&ollamaRequest{
		Model:  ollamaModel,
		Prompt: prompt,
		Stream: false,
	})

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ollamaURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return codeResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return codeResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return codeResult{}, fmt.Errorf("ollama status %d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return codeResult{}, err
	}
	if out.Error != "" {
		return codeResult{}, fmt.Errorf("ollama error: %s", out.Error)
	}
	if out.Response == "" {
		return codeResult{}, fmt.Errorf("ollama empty response")
	}

	return codeResult{
		FilePath:     ctxPayload.FilePath,
		OriginalCode: ctxPayload.CodeSnippet,
		Instruction:  ctxPayload.Instruction,
		Patch: structuredPatch{
			Type:    "unified_diff",
			Content: out.Response,
		},
	}, nil
}

func buildPrompt(ctxPayload codeContext) string {
	return fmt.Sprintf("You are a code assistant. Given file %s and instruction: %s\nCode:\n%s\nGenerate a concise patch (diff or replacement) to satisfy the instruction.",
		ctxPayload.FilePath, ctxPayload.Instruction, ctxPayload.CodeSnippet)
}

func checkOllamaHealth(ctx context.Context) error {
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(healthCtx, http.MethodGet, ollamaURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ollama health status %d", resp.StatusCode)
	}
	return nil
}

func callOllamaWithRetry(ctxPayload codeContext) (codeResult, error) {
	const maxAttempts = 3

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		res, err := callOllama(ctxPayload)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts {
			break
		}
		backoff := time.Duration(attempt*2) * time.Second
		log.Printf("[WORKER code-llm] retrying ollama attempt=%d after error: %v", attempt, err)
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
