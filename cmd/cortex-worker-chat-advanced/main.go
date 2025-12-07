package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	advancedWorkerID     = "worker-chat-advanced-1"
	advancedQueueGroup   = "workers-chat-advanced"
	advancedJobSubject   = "job.chat.advanced"
	advancedHeartbeatSub = "sys.heartbeat.chat-advanced"
)

var (
	advActiveJobs int32
	ollamaURL     = envOrDefault("OLLAMA_URL", "http://ollama:11434")
	ollamaModel   = envOrDefault("OLLAMA_MODEL", "llama3")
	httpClient    = &http.Client{Timeout: 15 * time.Second}
)

func main() {
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

	if err := natsBus.Subscribe(advancedJobSubject, advancedQueueGroup, handleAdvancedChatJob(natsBus, memStore)); err != nil {
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

func handleAdvancedChatJob(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		ctx := context.Background()
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&advActiveJobs, 1)
		defer atomic.AddInt32(&advActiveJobs, -1)

		prompt := extractPrompt(ctx, store, req)
		responseText, err := callOllama(prompt)
		if err != nil {
			log.Printf("[WORKER chat-adv] ollama call failed, using fallback: %v", err)
			responseText = "[fallback] " + prompt
		}

		resultKey := memory.MakeResultKey(req.JobId)
		resultPtr := memory.PointerForKey(resultKey)
		resultBody := map[string]any{
			"job_id":       req.JobId,
			"prompt":       prompt,
			"response":     responseText,
			"processed_by": advancedWorkerID,
			"completed_at": time.Now().UTC().Format(time.RFC3339),
			"model":        ollamaModel,
		}
		if resultBytes, err := json.Marshal(resultBody); err == nil {
			if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
				log.Printf("[WORKER chat-adv] failed to store result: %v", err)
			}
		}

		result := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
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

func extractPrompt(ctx context.Context, store memory.Store, req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
		if data, err := store.GetContext(ctx, key); err == nil {
			var payload map[string]any
			if err := json.Unmarshal(data, &payload); err == nil {
				if p, ok := payload["prompt"].(string); ok {
					return p
				}
			}
		}
	}
	return ""
}

type ollamaRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Stream  bool   `json:"stream"`
	Options any    `json:"options,omitempty"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

func callOllama(prompt string) (string, error) {
	if prompt == "" {
		return "", errors.New("empty prompt")
	}
	reqBody, _ := json.Marshal(&ollamaRequest{
		Model:  ollamaModel,
		Prompt: prompt,
		Stream: false,
	})

	req, err := http.NewRequest(http.MethodPost, ollamaURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var out ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Response, nil
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
