package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Config configures a CAP worker runtime.
type Config struct {
	WorkerID          string
	Pool              string
	Subjects          []string
	Queue             string
	MaxParallelJobs   int32
	Capabilities      []string
	Labels            map[string]string
	Region            string
	Type              string
	NatsURL           string
	NatsOptions       []nats.Option
	HeartbeatInterval time.Duration
	OnCancel          func(jobID, reason string)
}

// JobHandler processes a job request and returns a job result.
type JobHandler func(ctx context.Context, req *v1.JobRequest) (*v1.JobResult, error)

// Worker provides a minimal runtime for CAP workers.
type Worker struct {
	cfg        Config
	nc         *nats.Conn
	sem        chan struct{}
	activeJobs atomic.Int32

	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// NewWorker connects to NATS and prepares a worker runtime.
func NewWorker(cfg Config) (*Worker, error) {
	if cfg.WorkerID == "" {
		cfg.WorkerID = uuid.NewString()
	}
	if cfg.NatsURL == "" {
		cfg.NatsURL = nats.DefaultURL
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.Queue == "" {
		cfg.Queue = cfg.Pool
	}
	if len(cfg.Subjects) == 0 {
		if cfg.Pool == "" {
			return nil, fmt.Errorf("pool or subjects required")
		}
		cfg.Subjects = []string{cfg.Pool}
	}
	nc, err := nats.Connect(cfg.NatsURL, cfg.NatsOptions...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	var sem chan struct{}
	if cfg.MaxParallelJobs > 0 {
		sem = make(chan struct{}, cfg.MaxParallelJobs)
	}

	return &Worker{
		cfg:     cfg,
		nc:      nc,
		sem:     sem,
		cancels: make(map[string]context.CancelFunc),
	}, nil
}

// Close drains and closes the NATS connection.
func (w *Worker) Close() error {
	if w == nil || w.nc == nil {
		return nil
	}
	return w.nc.Drain()
}

// Run subscribes to job subjects and blocks until context cancellation.
func (w *Worker) Run(ctx context.Context, handler JobHandler) error {
	if w == nil || w.nc == nil {
		return fmt.Errorf("worker not initialized")
	}
	if handler == nil {
		return fmt.Errorf("job handler required")
	}

	subscriptions := make([]*nats.Subscription, 0, len(w.cfg.Subjects)+1)
	for _, subject := range w.cfg.Subjects {
		sub, err := w.nc.QueueSubscribe(subject, w.cfg.Queue, func(msg *nats.Msg) {
			w.handleJob(ctx, handler, msg)
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		subscriptions = append(subscriptions, sub)
	}

	cancelSub, err := w.nc.Subscribe(SubjectCancel, func(msg *nats.Msg) {
		w.handleCancel(msg)
	})
	if err != nil {
		return fmt.Errorf("subscribe cancel: %w", err)
	}
	subscriptions = append(subscriptions, cancelSub)

	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	go w.heartbeatLoop(heartbeatCtx)

	<-ctx.Done()
	stopHeartbeat()
	for _, sub := range subscriptions {
		_ = sub.Unsubscribe()
	}
	return w.Close()
}

// Progress emits a job progress packet.
func (w *Worker) Progress(jobID string, percent int32, message string, resultPtr string, artifactPtrs []string) error {
	if w == nil || w.nc == nil || jobID == "" {
		return fmt.Errorf("job id required")
	}
	progress := &v1.JobProgress{
		JobId:        jobID,
		Percent:      percent,
		Message:      message,
		ResultPtr:    resultPtr,
		ArtifactPtrs: artifactPtrs,
	}
	packet := w.packet(jobID, &v1.BusPacket_JobProgress{JobProgress: progress})
	return w.publish(SubjectProgress, packet)
}

func (w *Worker) handleJob(ctx context.Context, handler JobHandler, msg *nats.Msg) {
	if msg == nil || len(msg.Data) == 0 {
		return
	}
	var packet v1.BusPacket
	if err := proto.Unmarshal(msg.Data, &packet); err != nil {
		return
	}
	req := packet.GetJobRequest()
	if req == nil {
		return
	}

	if w.sem != nil {
		w.sem <- struct{}{}
		defer func() { <-w.sem }()
	}

	jobCtx, cancel := context.WithCancel(ctx)
	w.trackCancel(req.JobId, cancel)
	defer w.clearCancel(req.JobId)

	w.activeJobs.Add(1)
	defer w.activeJobs.Add(-1)

	result, err := handler(jobCtx, req)
	if result == nil {
		result = &v1.JobResult{JobId: req.JobId}
	}
	if result.JobId == "" {
		result.JobId = req.JobId
	}
	if result.WorkerId == "" {
		result.WorkerId = w.cfg.WorkerID
	}
	if result.Status == v1.JobStatus_JOB_STATUS_UNSPECIFIED {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				result.Status = v1.JobStatus_JOB_STATUS_CANCELLED
			} else {
				result.Status = v1.JobStatus_JOB_STATUS_FAILED
			}
		} else {
			result.Status = v1.JobStatus_JOB_STATUS_SUCCEEDED
		}
	}
	if err != nil && result.ErrorMessage == "" {
		result.ErrorMessage = err.Error()
	}
	packetOut := w.packet(req.JobId, &v1.BusPacket_JobResult{JobResult: result})
	_ = w.publish(SubjectResult, packetOut)
}

func (w *Worker) handleCancel(msg *nats.Msg) {
	if msg == nil || len(msg.Data) == 0 {
		return
	}
	var packet v1.BusPacket
	if err := proto.Unmarshal(msg.Data, &packet); err != nil {
		return
	}
	cancel := packet.GetJobCancel()
	if cancel == nil || cancel.JobId == "" {
		return
	}
	w.cancelMu.Lock()
	fn := w.cancels[cancel.JobId]
	w.cancelMu.Unlock()
	if fn != nil {
		fn()
	}
	if w.cfg.OnCancel != nil {
		w.cfg.OnCancel(cancel.JobId, cancel.Reason)
	}
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.sendHeartbeat()
		}
	}
}

func (w *Worker) sendHeartbeat() error {
	hb := &v1.Heartbeat{
		WorkerId:        w.cfg.WorkerID,
		Region:          w.cfg.Region,
		Type:            w.cfg.Type,
		ActiveJobs:      w.activeJobs.Load(),
		MaxParallelJobs: w.cfg.MaxParallelJobs,
		Capabilities:    w.cfg.Capabilities,
		Pool:            w.cfg.Pool,
		Labels:          w.cfg.Labels,
	}
	packet := w.packet(w.cfg.WorkerID, &v1.BusPacket_Heartbeat{Heartbeat: hb})
	return w.publish(SubjectHeartbeat, packet)
}

func (w *Worker) packet(traceID string, payload isBusPacketPayload) *v1.BusPacket {
	return &v1.BusPacket{
		TraceId:         traceID,
		SenderId:        w.cfg.WorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: DefaultProtocolVersion,
		Payload:         payload,
	}
}

type isBusPacketPayload interface {
	isBusPacket_Payload()
}

func (w *Worker) publish(subject string, packet *v1.BusPacket) error {
	if packet == nil {
		return fmt.Errorf("packet required")
	}
	data, err := proto.Marshal(packet)
	if err != nil {
		return err
	}
	return w.nc.Publish(subject, data)
}

func (w *Worker) trackCancel(jobID string, cancel context.CancelFunc) {
	if jobID == "" {
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
	delete(w.cancels, jobID)
	w.cancelMu.Unlock()
}
