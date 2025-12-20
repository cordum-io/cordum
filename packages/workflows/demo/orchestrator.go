package demo

import (
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

	"github.com/google/uuid"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	infraMetrics "github.com/yaront1111/coretex-os/core/infra/metrics"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	orchestratorWorkerID     = "worker-orchestrator-1"
	orchestratorQueueGroup   = "workers-orchestrator"
	orchestratorJobSubject   = "job.workflow.demo"
	orchestratorHeartbeatSub = "sys.heartbeat"
	childJobSubject          = "job.code.llm"

	defaultChildTimeout = 3 * time.Minute
	childPollInterval   = 300 * time.Millisecond
	maxChildRetries     = 1
	childRetryBackoff   = 800 * time.Millisecond
	workflowName        = "code_review_demo"
)

var orchestratorActiveJobs int32
var cancelMu sync.Mutex
var activeCancels = map[string]context.CancelFunc{}

// Run starts the generic demo orchestrator worker.
func Run() {
	log.Println("coretex worker orchestrator starting...")

	cfg := config.Load()
	timeoutCfg, _ := config.LoadTimeouts(cfg.TimeoutConfigPath)
	workflowCfg := timeoutCfg.Workflows["code_review_demo"]
	childTimeout := time.Duration(workflowCfg.ChildTimeoutSeconds) * time.Second
	if childTimeout == 0 {
		childTimeout = defaultChildTimeout
	}
	totalTimeout := time.Duration(workflowCfg.TotalTimeoutSeconds) * time.Second
	if totalTimeout == 0 {
		totalTimeout = 10 * time.Minute
	}
	retries := workflowCfg.MaxRetries
	if retries <= 0 {
		retries = maxChildRetries
	}

	metrics := infraMetrics.NewWorkflowProm("coretex_orchestrator")
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", infraMetrics.Handler())
		srv := &http.Server{
			Addr:         ":9091",
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		log.Println("orchestrator metrics on :9091/metrics")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

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

	if err := natsBus.Subscribe("sys.job.cancel", "", handleCancelPacket()); err != nil {
		log.Fatalf("failed to subscribe to job cancel: %v", err)
	}

	if err := natsBus.Subscribe(orchestratorJobSubject, orchestratorQueueGroup, handleOrchestratorJob(natsBus, memStore, jobStore, childTimeout, totalTimeout, retries, metrics, cfg)); err != nil {
		log.Fatalf("failed to subscribe to orchestrator jobs: %v", err)
	}
	if direct := bus.DirectSubject(orchestratorWorkerID); direct != "" {
		if err := natsBus.Subscribe(direct, "", handleOrchestratorJob(natsBus, memStore, jobStore, childTimeout, totalTimeout, retries, metrics, cfg)); err != nil {
			log.Fatalf("failed to subscribe to direct orchestrator jobs: %v", err)
		}
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

func handleCancelPacket() func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil || req.GetJobId() == "" {
			return
		}
		cancelMu.Lock()
		cancel := activeCancels[req.GetJobId()]
		cancelMu.Unlock()
		if cancel != nil {
			log.Printf("[WORKER orchestrator] cancelling job_id=%s reason=%s", req.GetJobId(), req.GetEnv()["cancel_reason"])
			cancel()
		}
	}
}

func registerCancel(jobID string, cancel context.CancelFunc) {
	if jobID == "" || cancel == nil {
		return
	}
	cancelMu.Lock()
	activeCancels[jobID] = cancel
	cancelMu.Unlock()
}

func clearCancel(jobID string) {
	if jobID == "" {
		return
	}
	cancelMu.Lock()
	delete(activeCancels, jobID)
	cancelMu.Unlock()
}

type parentContext struct {
	FilePath    string `json:"file_path"`
	CodeSnippet string `json:"code_snippet"`
	Instruction string `json:"instruction"`
}

type structuredPatch struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type codePatch struct {
	FilePath     string          `json:"file_path"`
	OriginalCode string          `json:"original_code"`
	Instruction  string          `json:"instruction"`
	Patch        structuredPatch `json:"patch"`
}

type planStep struct {
	ID        string   `json:"id"`
	Topic     string   `json:"topic"`
	AdapterID string   `json:"adapter_id"`
	DependsOn []string `json:"depends_on"`
}

func readPlan(ctx context.Context, store memory.Store, ptr string) ([]planStep, error) {
	if ptr == "" {
		return nil, fmt.Errorf("missing plan result_ptr")
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil, err
	}
	var out struct {
		Steps []planStep `json:"steps"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Steps, nil
}

func validatePlan(steps []planStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("empty plan")
	}
	allowed := map[string]bool{
		childJobSubject:   true,
		"job.chat.simple": true,
	}
	for _, s := range steps {
		if !allowed[s.Topic] {
			return fmt.Errorf("topic %s not allowed", s.Topic)
		}
	}
	return nil
}

func handleOrchestratorJob(b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore, childTimeout, totalTimeout time.Duration, retries int, metrics infraMetrics.WorkflowMetrics, cfg *config.Config) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&orchestratorActiveJobs, 1)
		defer atomic.AddInt32(&orchestratorActiveJobs, -1)

		ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
		registerCancel(req.JobId, cancel)
		defer cancel()
		defer clearCancel(req.JobId)
		start := time.Now()
		workflowName := "code_review_demo"
		if metrics != nil {
			metrics.IncWorkflowStarted(workflowName)
		}
		status := "failed"
		defer func() {
			if metrics != nil {
				metrics.IncWorkflowCompleted(workflowName, status)
				metrics.ObserveWorkflowDuration(workflowName, time.Since(start).Seconds())
			}
		}()
		var parentCtx parentContext
		if key, err := memory.KeyFromPointer(req.ContextPtr); err == nil {
			data, err := store.GetContext(ctx, key)
			if err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("read parent context: %w", err))
				return
			}
			if err := json.Unmarshal(data, &parentCtx); err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("decode parent context: %w", err))
				return
			}
		} else {
			failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("invalid context_ptr: %w", err))
			return
		}

		workflowID := pickWorkflowID(req)

		patchPtr := ""
		explainPtr := ""

		if cfg.UsePlanner {
			planPtr, err := runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, -1, cfg.PlannerTopic, parentCtx, nil, childTimeout, retries)
			if err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
				return
			}
			steps, err := readPlan(ctx, store, planPtr)
			if err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("invalid plan: %w", err))
				return
			}
			if err := validatePlan(steps); err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("plan validation failed: %w", err))
				return
			}
			for idx, step := range steps {
				switch step.Topic {
				case childJobSubject:
					ptr, err := runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, int32(idx), childJobSubject, parentCtx, nil, childTimeout, retries)
					if err != nil {
						failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
						return
					}
					patchPtr = ptr
				case "job.chat.simple":
					if patchPtr == "" {
						failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("plan missing patch before explanation"))
						return
					}
					patch, err := readPatch(ctx, store, patchPtr)
					if err != nil {
						failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
						return
					}
					ptr, err := runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, int32(idx), "job.chat.simple", parentCtx, patch, childTimeout, retries)
					if err != nil {
						failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
						return
					}
					explainPtr = ptr
				default:
					failParent(ctx, store, b, jobStore, req, packet.TraceId, fmt.Errorf("unsupported plan topic %s", step.Topic))
					return
				}
			}
		} else {
			// Default 2-step plan
			var err error
			patchPtr, err = runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, 0, childJobSubject, parentCtx, nil, childTimeout, retries)
			if err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
				return
			}
			explainPtr, err = runChild(ctx, b, store, jobStore, packet.TraceId, req, workflowID, 1, "job.chat.simple", parentCtx, nil, childTimeout, retries)
			if err != nil {
				failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
				return
			}
		}

		patch, err := readPatch(ctx, store, patchPtr)
		if err != nil {
			failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
			return
		}

		explanation, err := readExplanation(ctx, store, explainPtr)
		if err != nil {
			failParent(ctx, store, b, jobStore, req, packet.TraceId, err)
			return
		}

		final := map[string]any{
			"file_path":     patch.FilePath,
			"original_code": patch.OriginalCode,
			"instruction":   patch.Instruction,
			"patch":         patch.Patch,
			"explanation":   explanation,
			"workflow_id":   workflowID,
		}
		finalBytes, _ := json.Marshal(final)
		resKey := memory.MakeResultKey(req.JobId)
		if err := store.PutResult(ctx, resKey, finalBytes); err != nil {
			log.Printf("[WORKER orchestrator] store parent result job_id=%s: %v", req.JobId, err)
		}
		resultPtr := memory.PointerForKey(resKey)
		_ = jobStore.SetResultPtr(ctx, req.JobId, resultPtr)
		_ = jobStore.SetState(ctx, req.JobId, scheduler.JobStateSucceeded)

		res := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
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
			status = "completed"
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
				Pool:            "workflow-demo",
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
	prompt := parentCtx.Instruction
	if patch != nil {
		prompt = fmt.Sprintf("Explain this patch for %s.\nOriginal instruction: %s\nPatch (type=%s):\n%s",
			parentCtx.FilePath, parentCtx.Instruction, patch.Patch.Type, patch.Patch.Content)
	}

	childCtx := map[string]any{
		"file_path":    parentCtx.FilePath,
		"code_snippet": parentCtx.CodeSnippet,
		"instruction":  parentCtx.Instruction,
		"patch":        patch,
		"step_index":   step,
		"workflow_id":  workflowID,
	}
	if prompt != "" {
		childCtx["prompt"] = prompt
	}
	childCtxBytes, _ := json.Marshal(childCtx)
	childCtxKey := memory.MakeContextKey(childID)
	if err := store.PutContext(ctx, childCtxKey, childCtxBytes); err != nil {
		return err
	}
	childPtr := memory.PointerForKey(childCtxKey)

	childEnv := map[string]string{}
	for k, v := range parentReq.GetEnv() {
		childEnv[k] = v
	}
	childReq := &pb.JobRequest{
		JobId:       childID,
		Topic:       topic,
		Priority:    parentReq.Priority,
		ContextPtr:  childPtr,
		AdapterId:   parentReq.AdapterId,
		Env:         childEnv,
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
		select {
		case <-ctx.Done():
			return logErrorf("child job cancelled job_id=%s trace_id=%s: %w", childID, traceID, ctx.Err())
		default:
		}
		state, err := jobStore.GetState(ctx, childID)
		if err == nil {
			if state == scheduler.JobStateSucceeded {
				return nil
			}
			if state == scheduler.JobStateCancelled {
				return logErrorf("child job cancelled job_id=%s trace_id=%s: %w", childID, traceID, context.Canceled)
			}
			if state == scheduler.JobStateTimeout {
				return logErrorf("child job timeout job_id=%s trace_id=%s: %w", childID, traceID, context.DeadlineExceeded)
			}
			if state == scheduler.JobStateFailed || state == scheduler.JobStateDenied {
				return logErrorf("child job failed job_id=%s state=%s trace_id=%s", childID, state, traceID)
			}
		}
		time.Sleep(childPollInterval)
	}
	return logErrorf("child job timeout job_id=%s trace_id=%s: %w", childID, traceID, context.DeadlineExceeded)
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

func failParent(ctx context.Context, store memory.Store, b *bus.NatsBus, jobStore scheduler.JobStore, req *pb.JobRequest, traceID string, failure error) {
	log.Printf("[WORKER orchestrator] failing parent job_id=%s error=%v", req.JobId, failure)
	errPayload := map[string]any{
		"error":    failure.Error(),
		"job_id":   req.JobId,
		"trace_id": traceID,
		"time":     time.Now().UTC().Format(time.RFC3339),
	}
	errBytes, _ := json.Marshal(errPayload)
	resKey := memory.MakeResultKey(req.JobId)
	resultPtr := ""
	if err := store.PutResult(ctx, resKey, errBytes); err == nil {
		resultPtr = memory.PointerForKey(resKey)
		_ = jobStore.SetResultPtr(ctx, req.JobId, resultPtr)
	} else {
		log.Printf("[WORKER orchestrator] failed to store error payload for job_id=%s: %v", req.JobId, err)
	}

	status := pb.JobStatus_JOB_STATUS_FAILED
	state := scheduler.JobStateFailed
	switch {
	case errors.Is(failure, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		status = pb.JobStatus_JOB_STATUS_CANCELLED
		state = scheduler.JobStateCancelled
	case errors.Is(failure, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = pb.JobStatus_JOB_STATUS_TIMEOUT
		state = scheduler.JobStateTimeout
	}

	res := &pb.JobResult{
		JobId:        req.JobId,
		Status:       status,
		ResultPtr:    resultPtr,
		WorkerId:     orchestratorWorkerID,
		ErrorMessage: failure.Error(),
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
	_ = jobStore.SetState(ctx, req.JobId, state)
	_ = b.Publish("sys.job.result", packet)
}

func logErrorf(format string, args ...interface{}) error {
	err := fmt.Errorf(format, args...)
	log.Println(err)
	return err
}

func runChild(ctx context.Context, b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore, traceID string, parentReq *pb.JobRequest, workflowID string, step int32, topic string, parentCtx parentContext, patch *codePatch, timeout time.Duration, retries int) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		childID := uuid.NewString()
		if err := submitChildJob(ctx, b, store, jobStore, traceID, childID, parentReq, workflowID, step, topic, parentCtx, patch); err != nil {
			lastErr = err
		} else if err := waitForChild(ctx, jobStore, childID, timeout, traceID); err != nil {
			lastErr = err
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				publishCancel(b, childID, err.Error())
			}
			if ctx.Err() != nil {
				return "", err
			}
		} else {
			ptr, _ := jobStore.GetResultPtr(ctx, childID)
			return ptr, nil
		}
		if attempt < retries {
			log.Printf("[WORKER orchestrator] retrying child topic=%s step=%d attempt=%d error=%v", topic, step, attempt+1, lastErr)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(childRetryBackoff):
			}
		}
	}
	return "", lastErr
}

func publishCancel(b *bus.NatsBus, jobID, reason string) {
	if b == nil || jobID == "" {
		return
	}
	cancelReq := &pb.JobRequest{
		JobId: jobID,
		Topic: "sys.job.cancel",
		Env: map[string]string{
			"cancel_reason": reason,
		},
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        orchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: cancelReq},
	}
	_ = b.Publish("sys.job.cancel", packet)
}
