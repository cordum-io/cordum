package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	cfg := config.Load()

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}
	defer memStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	jobID := uuid.NewString()
	traceID := uuid.NewString()
	ctxKey := memory.MakeContextKey(jobID)
	ctxPtr := memory.PointerForKey(ctxKey)

	payload := map[string]any{
		"prompt":     "hello from chat job",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)
	if err := memStore.PutContext(context.Background(), ctxKey, data); err != nil {
		log.Fatalf("failed to store context: %v", err)
	}

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.chat.simple",
		Priority:   pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
		ContextPtr: ctxPtr,
		AdapterId:  "chat-adapter",
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "chat-sender",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}

	if err := natsBus.Publish("sys.job.submit", packet); err != nil {
		log.Fatalf("failed to publish chat job: %v", err)
	}
	log.Printf("sent chat job job_id=%s trace_id=%s context_ptr=%s", jobID, traceID, ctxPtr)
}
