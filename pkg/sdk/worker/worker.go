package worker

import (
	"context"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/bus"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config holds configuration for a Worker.
type Config struct {
	WorkerID       string
	NatsURL        string
	RedisURL       string
	QueueGroup     string
	JobSubject     string
	HeartbeatSub   string
	Capabilities   []string
	Pool           string
	MaxParallelJobs int32
}

// HandlerFunc is the signature for the worker's business logic.
// It receives the job request and the memory store.
// It should return the result payload (as a byte slice or struct that can be marshaled),
// and any error. The wrapper handles sending the result or error back.
// Or we can keep it simple: just pass the request and let the user return the JobResult object.
// Let's stick closer to the existing pattern: pass the request, get a JobResult back.
type HandlerFunc func(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error)

// Worker represents a CortexOS worker.
type Worker struct {
	Config     Config
	Bus        *bus.NatsBus
	Store      memory.Store
	ActiveJobs int32
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// New creates a new Worker instance.
func New(cfg Config) (*Worker, error) {
	// Defaults
	if cfg.HeartbeatSub == "" {
		cfg.HeartbeatSub = "sys.heartbeat." + cfg.Pool
	}

	store, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		return nil, err
	}

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		store.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Worker{
		Config: cfg,
		Bus:    natsBus,
		Store:  store,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start begins listening for jobs and sending heartbeats.
// It blocks until a signal is received.
func (w *Worker) Start(handler HandlerFunc) error {
	// Subscribe to jobs
	if err := w.Bus.Subscribe(w.Config.JobSubject, w.Config.QueueGroup, w.wrapHandler(handler)); err != nil {
		return err
	}

	// Start Heartbeat loop
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.heartbeatLoop()
	}()

	log.Printf("✅ Worker %s running. Waiting for jobs...", w.Config.WorkerID)

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down worker...")
	w.Stop()
	return nil
}

// Stop initiates a graceful shutdown.
func (w *Worker) Stop() {
	w.cancel()
	w.wg.Wait()
	w.Bus.Close()
	w.Store.Close()
}

func (w *Worker) wrapHandler(handler HandlerFunc) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}

		atomic.AddInt32(&w.ActiveJobs, 1)
		defer atomic.AddInt32(&w.ActiveJobs, -1)

		ctx := context.Background() // Could use a timeout context here

		// Execute business logic
		result, err := handler(ctx, req, w.Store)
		
		// Prepare response packet
		respPacket := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        w.Config.WorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: packet.ProtocolVersion,
		}

		if err != nil {
			log.Printf("[WORKER %s] Handler error job_id=%s: %v", w.Config.WorkerID, req.JobId, err)
			// Ensure we send a failed result if one wasn't returned
			if result == nil {
				result = &pb.JobResult{
					JobId:    req.JobId,
					Status:   pb.JobStatus_JOB_STATUS_FAILED,
					WorkerId: w.Config.WorkerID,
				}
			}
		}

		if result != nil {
			// Ensure essential fields are set if missed by handler
			if result.WorkerId == "" {
				result.WorkerId = w.Config.WorkerID
			}
			respPacket.Payload = &pb.BusPacket_JobResult{JobResult: result}

			if err := w.Bus.Publish("sys.job.result", respPacket); err != nil {
				log.Printf("[WORKER %s] failed to publish result: %v", w.Config.WorkerID, err)
			} else {
				log.Printf("[WORKER %s] completed job_id=%s", w.Config.WorkerID, req.JobId)
			}
		}
	}
}

func (w *Worker) heartbeatLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			// Stub CPU load for now
			cpuLoad := float32(rand.Intn(50)) 

			hb := &pb.Heartbeat{
				WorkerId:        w.Config.WorkerID,
				Region:          "local",
				Type:            "cpu", // Default, could be configurable
				CpuLoad:         cpuLoad,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&w.ActiveJobs),
				Capabilities:    w.Config.Capabilities,
				Pool:            w.Config.Pool,
				MaxParallelJobs: w.Config.MaxParallelJobs,
			}

			packet := &pb.BusPacket{
				TraceId:         "hb-" + w.Config.WorkerID,
				SenderId:        w.Config.WorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}

			if err := w.Bus.Publish(w.Config.HeartbeatSub, packet); err != nil {
				log.Printf("[WORKER %s] failed to publish heartbeat: %v", w.Config.WorkerID, err)
			}
		}
	}
}
