package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	plannerWorkerID   = "worker-planner-1"
	plannerQueueGroup = "workers-planner"
	plannerSubject    = "job.workflow.plan"
)

type plan struct {
	Workflow string     `json:"workflow"`
	Steps    []planStep `json:"steps"`
}

type planStep struct {
	ID        string   `json:"id"`
	Topic     string   `json:"topic"`
	AdapterID string   `json:"adapter_id,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// A minimal planner that returns the current hard-coded plan for code review.
func buildDefaultPlan(workflow string) plan {
	return plan{
		Workflow: workflow,
		Steps: []planStep{
			{
				ID:        "patch",
				Topic:     "job.code.llm",
				AdapterID: "refactor",
			},
			{
				ID:        "explain",
				Topic:     "job.chat.simple",
				AdapterID: "explain",
				DependsOn: []string{"patch"},
			},
		},
	}
}

func main() {
	cfg := config.Load()

	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("planner: failed to connect to Redis: %v", err)
	}
	defer memStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("planner: failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	if err := natsBus.Subscribe(plannerSubject, plannerQueueGroup, handlePlan(natsBus, memStore)); err != nil {
		log.Fatalf("planner: failed to subscribe: %v", err)
	}

	log.Println("planner worker running on subject", plannerSubject)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}

func handlePlan(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		ctx := context.Background()
		workflow := req.GetWorkflowId()
		if workflow == "" {
			workflow = "code_review_demo"
		}

		planPayload := buildDefaultPlan(workflow)
		planBytes, _ := json.Marshal(planPayload)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, planBytes); err != nil {
			log.Printf("planner: failed to store plan job_id=%s err=%v", req.JobId, err)
			return
		}
		resultPtr := memory.PointerForKey(resKey)

		res := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
			ResultPtr:   resultPtr,
			WorkerId:    plannerWorkerID,
			ExecutionMs: 0,
		}

		resp := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        plannerWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload:         &pb.BusPacket_JobResult{JobResult: res},
		}

		if err := b.Publish("sys.job.result", resp); err != nil {
			log.Printf("planner: failed to publish result job_id=%s: %v", req.JobId, err)
		}
	}
}
