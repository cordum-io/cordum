package main

import (
	"context"
	"encoding/json"
	"log"
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
	chatWorkerID     = "worker-chat-1"
	chatQueueGroup   = "workers-chat"
	chatJobSubject   = "job.chat.simple"
	chatHeartbeatSub = "sys.heartbeat.chat"
)

var chatActiveJobs int32

func main() {
	log.Println("cortex worker chat starting...")

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

	if err := natsBus.Subscribe(chatJobSubject, chatQueueGroup, handleChatJob(natsBus, memStore)); err != nil {
		log.Fatalf("failed to subscribe to chat jobs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendChatHeartbeats(ctx, natsBus)
	}()

	log.Println("worker chat running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("worker chat shutting down")
	cancel()
	wg.Wait()
}

func handleChatJob(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		ctx := context.Background()
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&chatActiveJobs, 1)
		defer atomic.AddInt32(&chatActiveJobs, -1)

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

		responseText := "Echo: " + prompt
		resultKey := memory.MakeResultKey(req.JobId)
		resultPtr := memory.PointerForKey(resultKey)
		resultBody := map[string]any{
			"job_id":       req.JobId,
			"prompt":       prompt,
			"response":     responseText,
			"processed_by": chatWorkerID,
			"completed_at": time.Now().UTC().Format(time.RFC3339),
		}
		if resultBytes, err := json.Marshal(resultBody); err == nil {
			if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
				log.Printf("[WORKER chat] failed to store result: %v", err)
			}
		}

		result := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
			ResultPtr:   resultPtr,
			WorkerId:    chatWorkerID,
			ExecutionMs: 0,
		}

		resp := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        chatWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload: &pb.BusPacket_JobResult{
				JobResult: result,
			},
		}

		if err := b.Publish("sys.job.result", resp); err != nil {
			log.Printf("[WORKER chat] failed to publish result for job_id=%s: %v", req.JobId, err)
		} else {
			log.Printf("[WORKER chat] completed job_id=%s", req.JobId)
		}
	}
}

func sendChatHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:        chatWorkerID,
				Region:          "local",
				Type:            "cpu",
				CpuLoad:         10,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&chatActiveJobs),
				Capabilities:    []string{"chat"},
				Pool:            "chat-simple",
				MaxParallelJobs: 2,
			}
			packet := &pb.BusPacket{
				TraceId:         "hb-" + chatWorkerID,
				SenderId:        chatWorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}
			if err := b.Publish(chatHeartbeatSub, packet); err != nil {
				log.Printf("[WORKER chat] failed to publish heartbeat: %v", err)
			}
		}
	}
}
