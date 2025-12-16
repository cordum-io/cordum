package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config holds configuration for a Worker.
type Config struct {
	WorkerID        string
	NatsURL         string
	RedisURL        string
	QueueGroup      string
	JobSubject      string
	DirectSubject   string
	HeartbeatSub    string
	Capabilities    []string
	Pool            string
	MaxParallelJobs int32
	Labels          map[string]string
}

// HandlerFunc is the signature for the worker's business logic.
// It receives the job request and the memory store.
// It should return the result payload (as a byte slice or struct that can be marshaled),
// and any error. The wrapper handles sending the result or error back.
// Or we can keep it simple: just pass the request and let the user return the JobResult object.
// Let's stick closer to the existing pattern: pass the request, get a JobResult back.
type HandlerFunc func(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error)

// Worker represents a coretexOS worker.
type Worker struct {
	Config     Config
	Bus        *bus.NatsBus
	Store      memory.Store
	ActiveJobs int32
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	cancelMu   sync.Mutex
	cancels    map[string]context.CancelFunc
}

type contextKey string

const (
	contextHintsKey contextKey = "context_hints"
	budgetKey       contextKey = "budget"
)

// New creates a new Worker instance.
func New(cfg Config) (*Worker, error) {
	// Defaults
	if cfg.HeartbeatSub == "" {
		cfg.HeartbeatSub = "sys.heartbeat"
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
		Config:  cfg,
		Bus:     natsBus,
		Store:   store,
		ctx:     ctx,
		cancel:  cancel,
		cancels: make(map[string]context.CancelFunc),
	}, nil
}

// Start begins listening for jobs and sending heartbeats.
// It blocks until a signal is received.
func (w *Worker) Start(handler HandlerFunc) error {
	// Subscribe to jobs
	if err := w.Bus.Subscribe(w.Config.JobSubject, w.Config.QueueGroup, w.wrapHandler(handler)); err != nil {
		return err
	}
	if w.Config.DirectSubject != "" {
		if err := w.Bus.Subscribe(w.Config.DirectSubject, "", w.wrapHandler(handler)); err != nil {
			return err
		}
	}
	// Subscribe to cancel notifications (best-effort)
	_ = w.Bus.Subscribe("sys.job.cancel", "", w.handleCancel)

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

		ctx := w.ctx
		if budget := req.GetBudget(); budget != nil && budget.GetDeadlineMs() > 0 {
			var cancelTimeout context.CancelFunc
			ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(budget.GetDeadlineMs())*time.Millisecond)
			defer cancelTimeout()
		}
		ctx, cancelJob := context.WithCancel(ctx)
		w.registerCancel(req.JobId, cancelJob)
		defer w.clearCancel(req.JobId)

		// Attach hints/budget to context for handlers that want them.
		if hints := req.GetContextHints(); hints != nil {
			ctx = context.WithValue(ctx, contextHintsKey, hints)
		}
		if budget := req.GetBudget(); budget != nil {
			ctx = context.WithValue(ctx, budgetKey, budget)
		}

		// Execute business logic
		result, err := handler(ctx, req, w.Store)
		if err == nil && result == nil {
			err = fmt.Errorf("handler returned nil result")
		}

		// Prepare response packet
		respPacket := &pb.BusPacket{
			TraceId:         packet.TraceId,
			SenderId:        w.Config.WorkerID,
			CreatedAt:       timestamppb.Now(),
			ProtocolVersion: packet.ProtocolVersion,
		}

		if err != nil {
			log.Printf("[WORKER %s] Handler error job_id=%s: %v", w.Config.WorkerID, req.JobId, err)
			status := pb.JobStatus_JOB_STATUS_FAILED
			switch {
			case errors.Is(err, context.Canceled):
				status = pb.JobStatus_JOB_STATUS_CANCELLED
			case errors.Is(err, context.DeadlineExceeded):
				status = pb.JobStatus_JOB_STATUS_TIMEOUT
			}

			// Ensure we send a failed result if one wasn't returned
			if result == nil {
				result = &pb.JobResult{
					JobId:    req.JobId,
					Status:   status,
					WorkerId: w.Config.WorkerID,
					ErrorMessage: err.Error(),
				}
			} else {
				if result.Status == pb.JobStatus_JOB_STATUS_UNSPECIFIED || result.Status == pb.JobStatus_JOB_STATUS_SUCCEEDED {
					result.Status = status
				}
				if result.ErrorMessage == "" {
					result.ErrorMessage = err.Error()
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

func (w *Worker) handleCancel(packet *pb.BusPacket) {
	req := packet.GetJobRequest()
	if req == nil || req.JobId == "" {
		return
	}
	w.cancelMu.Lock()
	cancel := w.cancels[req.JobId]
	w.cancelMu.Unlock()
	if cancel != nil {
		log.Printf("[WORKER %s] cancelling job_id=%s reason=%s", w.Config.WorkerID, req.JobId, req.GetEnv()["cancel_reason"])
		cancel()
	}
}

func (w *Worker) registerCancel(jobID string, cancel context.CancelFunc) {
	if jobID == "" || cancel == nil {
		return
	}
	w.cancelMu.Lock()
	w.cancels[jobID] = cancel
	w.cancelMu.Unlock()
}

func (w *Worker) clearCancel(jobID string) {
	if jobID == "" {
		return
	}
	w.cancelMu.Lock()
	if cancel, ok := w.cancels[jobID]; ok {
		cancel()
		delete(w.cancels, jobID)
	}
	w.cancelMu.Unlock()
}

func (w *Worker) heartbeatLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			cpuLoad := readCPULoad()

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
				Labels:          w.Config.Labels,
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

// readCPULoad derives a rough CPU load percentage from /proc/loadavg to avoid random scheduling signals.
func readCPULoad() float32 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	cores := runtime.NumCPU()
	if cores <= 0 {
		cores = 1
	}
	pct := (load / float64(cores)) * 100
	if pct < 0 {
		pct = 0
	}
	// Cap to avoid runaway values if load spikes; scheduler just needs relative signal.
	if pct > 1000 {
		pct = 1000
	}
	return float32(pct)
}
