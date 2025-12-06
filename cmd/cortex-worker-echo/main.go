package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	workerID         = "worker-echo-1"
	heartbeatSubject = "sys.heartbeat.echo"
	jobSubject       = "job.echo"
	queueGroup       = "workers-echo"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	log.Println("cortex worker echo starting...")

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

	if err := natsBus.Subscribe(jobSubject, queueGroup, handleJob(natsBus, memStore)); err != nil {
		log.Fatalf("failed to subscribe to jobs: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendHeartbeats(ctx, natsBus)
	}()

	log.Println("worker echo running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("worker echo shutting down")
	cancel()
	wg.Wait()
}

func handleJob(b *bus.NatsBus, store memory.Store) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		ctx := context.Background()
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		var ctxPayload []byte
		if key, err := memory.KeyFromPointer(req.ContextPtr); err != nil {
			log.Printf("[WORKER echo] invalid context pointer for job_id=%s: %v", req.JobId, err)
		} else {
			ctxPayload, err = store.GetContext(ctx, key)
			if err != nil {
				log.Printf("[WORKER echo] failed to read context for job_id=%s: %v", req.JobId, err)
			}
		}

		log.Printf("[WORKER echo] received job_id=%s topic=%s context_ptr=%s context_payload=%s", req.JobId, req.Topic, req.ContextPtr, string(ctxPayload))

		start := time.Now()
		time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)

		resultKey := memory.MakeResultKey(req.JobId)
		resultPtr := memory.PointerForKey(resultKey)
		resultBody := map[string]any{
			"job_id":           req.JobId,
			"received_ctx":     json.RawMessage(ctxPayload),
			"processed_by":     workerID,
			"completed_at_utc": time.Now().UTC().Format(time.RFC3339),
		}
		resultBytes, err := json.Marshal(resultBody)
		if err != nil {
			log.Printf("[WORKER echo] failed to marshal result for job_id=%s: %v", req.JobId, err)
		} else if err := store.PutResult(ctx, resultKey, resultBytes); err != nil {
			log.Printf("[WORKER echo] failed to store result for job_id=%s: %v", req.JobId, err)
		}

		result := &pb.JobResult{
			JobId:       req.JobId,
			Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
			ResultPtr:   resultPtr,
			WorkerId:    workerID,
			ExecutionMs: time.Since(start).Milliseconds(),
		}

		response := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        workerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: packet.ProtocolVersion,
			Payload: &pb.BusPacket_JobResult{
				JobResult: result,
			},
		}

		if err := b.Publish("sys.job.result", response); err != nil {
			log.Printf("[WORKER echo] failed to publish result for job_id=%s: %v", req.JobId, err)
		} else {
			log.Printf("[WORKER echo] completed job_id=%s duration_ms=%d", req.JobId, result.ExecutionMs)
		}
	}
}

func sendHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:       workerID,
				Region:         "local",
				Type:           "cpu",
				CpuLoad:        float32(rand.Intn(50)),
				GpuUtilization: 0,
				ActiveJobs:     0,
				Capabilities:   []string{"echo"},
			}

			packet := &pb.BusPacket{
				TraceId:         "hb-" + workerID,
				SenderId:        workerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}

			if err := b.Publish(heartbeatSubject, packet); err != nil {
				log.Printf("[WORKER echo] failed to publish heartbeat: %v", err)
			} else {
				log.Printf("[WORKER echo] heartbeat sent worker_id=%s", workerID)
			}
		}
	}
}
