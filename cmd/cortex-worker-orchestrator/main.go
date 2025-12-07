package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/yaront1111/cortex-os/core/internal/scheduler"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	orchestratorWorkerID     = "worker-orchestrator-1"
	orchestratorQueueGroup   = "workers-orchestrator"
	orchestratorJobSubject   = "job.workflow.demo"
	orchestratorHeartbeatSub = "sys.heartbeat.workflow"
	childJobSubject          = "job.code.llm"

	defaultChildTimeout = 60 * time.Second
	childPollInterval   = 300 * time.Millisecond
	maxChildRetries     = 1
	childRetryBackoff   = 800 * time.Millisecond
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

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for job store: %v", err)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	if err := natsBus.Subscribe(orchestratorJobSubject, orchestratorQueueGroup, handleOrchestratorJob(natsBus, memStore, jobStore)); err != nil {
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

type parentContext struct {
	FilePath    string `json:"file_path"`
	CodeSnippet string `json:"code_snippet"`
	Instruction string `json:"instruction"`
}

type codePatch struct {
	FilePath string `json:"file_path"`
	Patch    string `json:"patch"`
}

func handleOrchestratorJob(b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&orchestratorActiveJobs, 1)
		defer atomic.AddInt32(&orchestratorActiveJobs, -1)

		ctx := context.Background()
		var parentCtx parentContext
		if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
			if data, err := store.GetContext(ctx, key); err == nil {
				if err := json.Unmarshal(data, &parentCtx); err != nil {
					log.Printf("[WORKER orchestrator] decode parent context job_id=%s: %v", req.JobId, err)
				}
			} else {
				log.Printf("[WORKER orchestrator] read parent context job_id=%s: %v", req.JobId, err)
			}
		} else {
			log.Printf("[WORKER orchestrator] invalid context_ptr for job_id=%s: %v", req.JobId, err)
		}

		start := time.Now()
		workflowID := pickWorkflowID(req)

		// Child 1: code patch
		patchPtr, err := runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, 0, childJobSubject, parentCtx, nil)
		if err != nil {
			failParent(ctx, b, jobStore, req, packet.TraceId, err)
			return
		}
		patch, err := readPatch(ctx, store, patchPtr)
		if err != nil {
			failParent(ctx, b, jobStore, req, packet.TraceId, err)
			return
		}

		// Child 2: explanation
		explainPtr, err := runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, 1, "job.chat.simple", parentCtx, patch)
		if err != nil {
			failParent(ctx, b, jobStore, req, packet.TraceId, err)
			return
		}
		explanation, err := readExplanation(ctx, store, explainPtr)
		if err != nil {
			failParent(ctx, b, jobStore, req, packet.TraceId, err)
			return
		}

		final := map[string]any{
			"file_path":   patch.FilePath,
			"patch":       patch.Patch,
			"explanation": explanation,
			"workflow_id": workflowID,
		}
		finalBytes, _ := json.Marshal(final)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, finalBytes); err != nil {
			log.Printf("[WORKER orchestrator] store parent result job_id=%s: %v", req.JobId, err)
		}
		resultPtr := memory.PointerForKey(resKey)
		_ = jobStore.SetResultPtr(ctx, req.JobId, resultPtr)
		_ = jobStore.SetState(ctx, req.JobId, scheduler.JobStateCompleted)

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
		} else {
			log.Printf("[WORKER orchestrator] completed parent job_id=%s workflow_id=%s", req.JobId, workflowID)
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

func submitChildJob(ctx context.Context, b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore, traceID, childID string, parentReq *pb.JobRequest, workflowID string, step int32, topic string, parentCtx parentContext, patch *codePatch) error {
	childCtx := map[string]any{
		"file_path":    parentCtx.FilePath,
		"code_snippet": parentCtx.CodeSnippet,
		"instruction":  parentCtx.Instruction,
		"patch":        patch,
		"step_index":   step,
		"workflow_id":  workflowID,
	}
	childCtxBytes, _ := json.Marshal(childCtx)
	childCtxKey := memory.MakeContextKey(childID)
	if err := store.PutContext(ctx, childCtxKey, childCtxBytes); err != nil {
		return err
	}
	childPtr := memory.PointerForKey(childCtxKey)

	childReq := &pb.JobRequest{
		JobId:       childID,
		Topic:       topic,
		Priority:    parentReq.Priority,
		ContextPtr:  childPtr,
		AdapterId:   parentReq.AdapterId,
		EnvVars:     parentReq.EnvVars,
		ParentJobId: parentReq.JobId,
		WorkflowId:  workflowID,
		StepIndex:   step,
	}

	childPacket := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        orchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: childReq,
		},
	}

	log.Printf("[WORKER orchestrator] dispatch child job_id=%s topic=%s parent=%s step=%d", childID, topic, parentReq.JobId, step)
	if err := b.Publish("sys.job.submit", childPacket); err != nil {
		return err
	}
	_ = jobStore.SetState(ctx, childID, scheduler.JobStatePending)
	return nil
}

func waitForChild(ctx context.Context, jobStore scheduler.JobStore, childID string, timeout time.Duration, traceID string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := jobStore.GetState(ctx, childID)
		if err == nil {
			if state == scheduler.JobStateCompleted {
				return nil
			}
			if state == scheduler.JobStateFailed || state == scheduler.JobStateDenied {
				return logErrorf("child job failed job_id=%s state=%s trace_id=%s", childID, state, traceID)
			}
		}
		time.Sleep(childPollInterval)
	}
	return logErrorf("child job timeout job_id=%s trace_id=%s", childID, traceID)
}

func readPatch(ctx context.Context, store memory.Store, ptr string) (*codePatch, error) {
	if ptr == "" {
		return nil, logErrorf("missing patch result_ptr")
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil, err
	}
	var out codePatch
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func readExplanation(ctx context.Context, store memory.Store, ptr string) (string, error) {
	if ptr == "" {
		return "", logErrorf("missing explanation result_ptr")
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return "", err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return "", err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if expl, ok := out["response"].(string); ok {
		return expl, nil
	}
	return string(data), nil
}

func failParent(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, req *pb.JobRequest, traceID string, failure error) {
	log.Printf("[WORKER orchestrator] failing parent job_id=%s error=%v", req.JobId, failure)
	res := &pb.JobResult{
		JobId:     req.JobId,
		Status:    pb.JobStatus_JOB_STATUS_FAILED,
		ResultPtr: "",
		WorkerId:  orchestratorWorkerID,
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        orchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: res,
		},
	}
	_ = jobStore.SetState(ctx, req.JobId, scheduler.JobStateFailed)
	_ = b.Publish("sys.job.result", packet)
}

func logErrorf(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	log.Println(err)
	return err
}

func runChild(ctx context.Context, b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore, traceID string, parentReq *pb.JobRequest, workflowID string, step int32, topic string, parentCtx parentContext, patch *codePatch) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= maxChildRetries; attempt++ {
		childID := uuid.NewString()
		if err := submitChildJob(ctx, b, store, jobStore, traceID, childID, parentReq, workflowID, step, topic, parentCtx, patch); err != nil {
			lastErr = err
		} else if err := waitForChild(ctx, jobStore, childID, defaultChildTimeout, traceID); err != nil {
			lastErr = err
			_ = jobStore.SetState(ctx, childID, scheduler.JobStateFailed)
		} else {
			ptr, _ := jobStore.GetResultPtr(ctx, childID)
			return ptr, nil
		}
		if attempt < maxChildRetries {
			log.Printf("[WORKER orchestrator] retrying child topic=%s step=%d attempt=%d error=%v", topic, step, attempt+1, lastErr)
			time.Sleep(childRetryBackoff)
		}
	}
	return "", lastErr
}
