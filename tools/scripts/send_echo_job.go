package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
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
		"prompt":     "hello from send_echo_job",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("failed to marshal context payload: %v", err)
	}

	if err := memStore.PutContext(context.Background(), ctxKey, payloadBytes); err != nil {
		log.Fatalf("failed to store context in redis: %v", err)
	}

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.echo",
		Priority:   pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
		ContextPtr: ctxPtr,
		AdapterId:  "echo-adapter",
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "job-sender",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}

	// Subscribe to results to verify completion
	resCh := make(chan *pb.JobResult, 1)
	if err := natsBus.Subscribe("sys.job.result", "", func(p *pb.BusPacket) {
		if res := p.GetJobResult(); res != nil && res.JobId == jobID {
			resCh <- res
		}
	}); err != nil {
		log.Fatalf("failed to subscribe to results: %v", err)
	}

	if err := natsBus.Publish("sys.job.submit", packet); err != nil {
		log.Fatalf("failed to publish job: %v", err)
	}

	log.Printf("sent job job_id=%s trace_id=%s context_ptr=%s", jobID, traceID, ctxPtr)
	log.Println("waiting for result...")

	select {
	case res := <-resCh:
		log.Printf("✅ received result: status=%s worker=%s duration=%dms", res.Status, res.WorkerId, res.ExecutionMs)
		// Retrieve result payload
		if res.ResultPtr != "" {
			resKey, _ := memory.KeyFromPointer(res.ResultPtr)
			if data, err := memStore.GetResult(context.Background(), resKey); err == nil {
				log.Printf("   payload: %s", string(data))
			}
		}
	case <-time.After(5 * time.Second):
		log.Fatalf("❌ timed out waiting for result")
	}
}
