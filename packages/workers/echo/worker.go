package echo

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"time"

	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	workerID = "worker-echo-1"
)

// Run starts the echo worker.
func Run() {
	rand.Seed(time.Now().UnixNano())
	log.Println("coretex worker echo starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        workerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-echo",
		JobSubject:      "job.echo",
		DirectSubject:   bus.DirectSubject(workerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"echo"},
		Pool:            "echo",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(echoHandler); err != nil {
		log.Fatalf("worker failed: %v", err)
	}
}

func echoHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	// 1. Fetch Context
	var ctxPayload []byte
	if key, err := memory.KeyFromPointer(req.ContextPtr); err != nil {
		log.Printf("[WORKER echo] invalid context pointer for job_id=%s: %v", req.JobId, err)
	} else {
		var err error
		ctxPayload, err = store.GetContext(ctx, key)
		if err != nil {
			log.Printf("[WORKER echo] failed to read context for job_id=%s: %v", req.JobId, err)
		}
	}

	log.Printf("[WORKER echo] received job_id=%s topic=%s", req.JobId, req.Topic)

	// 2. Simulate Work
	start := time.Now()
	time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)

	// 3. Store Result
	resultKey := memory.MakeResultKey(req.JobId)
	resultPtr := memory.PointerForKey(resultKey)
	resultBody := map[string]any{
		"job_id":           req.JobId,
		"received_ctx":     json.RawMessage(ctxPayload),
		"processed_by":     workerID,
		"completed_at_utc": time.Now().UTC().Format(time.RFC3339),
	}

	resultBytes, _ := json.Marshal(resultBody)
	// Best effort store
	if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
		log.Printf("[WORKER echo] failed to store result: %v", err)
	}

	// 4. Return Result
	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   resultPtr,
		ExecutionMs: time.Since(start).Milliseconds(),
	}, nil
}
