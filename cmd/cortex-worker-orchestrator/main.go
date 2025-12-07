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

	"github.com/google/uuid"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	orchestratorWorkerID     = "worker-orchestrator-1"
	orchestratorQueueGroup   = "workers-orchestrator"
	orchestratorJobSubject   = "job.workflow.demo"
	orchestratorHeartbeatSub = "sys.heartbeat.workflow"
	childJobSubject          = "job.echo"
)

var orchestratorActiveJobs int32

func main() {
	log.Println("cortex worker orchestrator starting...")

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

	if err := natsBus.Subscribe(orchestratorJobSubject, orchestratorQueueGroup, handleOrchestratorJob(natsBus, memStore)); err != nil {
		log.Fatalf("failed to subscribe to orchestrator jobs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendOrchestratorHeartbeats(ctx, natsBus)
	}()

	log.Println("worker orchestrator running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("worker orchestrator shutting down")
	cancel()
	wg.Wait()
}

func handleOrchestratorJob(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&orchestratorActiveJobs, 1)
		defer atomic.AddInt32(&orchestratorActiveJobs, -1)

		ctx := context.Background()
		var parentCtx map[string]any
		if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
			if data, err := store.GetContext(ctx, key); err == nil {
				if err := json.Unmarshal(data, &parentCtx); err != nil {
					log.Printf("[WORKER orchestrator] failed to decode parent context for job_id=%s: %v", req.JobId, err)
				}
			}
		}

		start := time.Now()

		childJobID := uuid.NewString()
		childCtx := map[string]any{
			"parent_job_id":     req.JobId,
			"workflow_id":       pickWorkflowID(req),
			"step_index":        1,
			"parent_context":    parentCtx,
			"received_at_utc":   time.Now().UTC().Format(time.RFC3339),
			"requested_topic":   req.Topic,
			"requested_trace":   packet.TraceId,
			"requested_adapter": req.AdapterId,
		}
		childCtxBytes, _ := json.Marshal(childCtx)
		childCtxKey := memory.MakeContextKey(childJobID)
		if err := store.PutContext(ctx, childCtxKey, childCtxBytes); err != nil {
			log.Printf("[WORKER orchestrator] failed to store child context job_id=%s: %v", childJobID, err)
		}
		childPtr := memory.PointerForKey(childCtxKey)

		childReq := &pb.JobRequest{
			JobId:       childJobID,
			Topic:       childJobSubject,
			Priority:    req.Priority,
			ContextPtr:  childPtr,
			AdapterId:   req.AdapterId,
			ParentJobId: req.JobId,
			WorkflowId:  pickWorkflowID(req),
			StepIndex:   1,
			EnvVars:     req.EnvVars,
		}

		childPacket := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        orchestratorWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: childReq,
			},
		}

		if err := b.Publish("sys.job.submit", childPacket); err != nil {
			log.Printf("[WORKER orchestrator] failed to publish child job job_id=%s: %v", childJobID, err)
		}

		resultPayload := map[string]any{
			"parent_job_id": req.JobId,
			"child_job_id":  childJobID,
			"child_topic":   childJobSubject,
			"workflow_id":   pickWorkflowID(req),
			"status":        "child_dispatched",
			"dispatched_at": time.Now().UTC().Format(time.RFC3339),
		}
		resultBytes, _ := json.Marshal(resultPayload)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, resultBytes); err != nil {
			log.Printf("[WORKER orchestrator] failed to store parent result job_id=%s: %v", req.JobId, err)
		}
		resultPtr := memory.PointerForKey(resKey)

		res := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
			ResultPtr:   resultPtr,
			WorkerId:    orchestratorWorkerID,
			ExecutionMs: time.Since(start).Milliseconds(),
		}

		response := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        orchestratorWorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: 1,
			Payload: &pb.BusPacket_JobResult{
				JobResult: res,
			},
		}

		if err := b.Publish("sys.job.result", response); err != nil {
			log.Printf("[WORKER orchestrator] failed to publish parent result job_id=%s: %v", req.JobId, err)
		}
	}
}

func sendOrchestratorHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:        orchestratorWorkerID,
				Region:          "local",
				Type:            "cpu",
				CpuLoad:         10,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&orchestratorActiveJobs),
				Capabilities:    []string{"workflow"},
				Pool:            "workflow",
				MaxParallelJobs: 1,
			}

			packet := &pb.BusPacket{
				TraceId:         "hb-" + orchestratorWorkerID,
				SenderId:        orchestratorWorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}

			if err := b.Publish(orchestratorHeartbeatSub, packet); err != nil {
				log.Printf("[WORKER orchestrator] failed to publish heartbeat: %v", err)
			}
		}
	}
}

func pickWorkflowID(req *pb.JobRequest) string {
	if req.GetWorkflowId() != "" {
		return req.GetWorkflowId()
	}
	if req.GetParentJobId() != "" {
		return req.GetParentJobId()
	}
	return req.GetJobId()
}
