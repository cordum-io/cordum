package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/yaront1111/cortex-os/core/infra/bus"
	"github.com/yaront1111/cortex-os/core/infra/config"
	"github.com/yaront1111/cortex-os/core/infra/memory"
	pb "github.com/yaront1111/cortex-os/core/protocol/pb/v1"
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
		"file_path":    "foo/bar.go",
		"code_snippet": "func Foo() { /* stub */ }",
		"instruction":  "improve performance and add logging",
		"created_at":   time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(payload)
	if err := memStore.PutContext(context.Background(), ctxKey, data); err != nil {
		log.Fatalf("failed to store context: %v", err)
	}

	req := &pb.JobRequest{
		JobId:      jobID,
		Topic:      "job.workflow.demo",
		Priority:   pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
		ContextPtr: ctxPtr,
		AdapterId:  "workflow-demo",
		WorkflowId: jobID,
		StepIndex:  0,
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "workflow-job-sender",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: req,
		},
	}

	if err := natsBus.Publish("sys.job.submit", packet); err != nil {
		log.Fatalf("failed to publish workflow job: %v", err)
	}
	log.Printf("sent workflow job job_id=%s trace_id=%s context_ptr=%s", jobID, traceID, ctxPtr)
}
