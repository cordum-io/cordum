package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultNATSURL        = "nats://127.0.0.1:4222"
	defaultConnectTimeout = 5 * time.Second
	defaultMaxParallel    = int32(1)
)

// Config controls worker behavior for legacy runtimes.
type Config struct {
	Pool            string
	Subjects        []string
	Queue           string
	NatsURL         string
	MaxParallelJobs int32
	Capabilities    []string
	Labels          map[string]string
	Type            string
	WorkerID        string
	HeartbeatEvery  time.Duration
	PublicKeys      map[string]*ecdsa.PublicKey
	PrivateKey      *ecdsa.PrivateKey
	// NATSTLSConfig is an optional TLS configuration for the NATS connection.
	// When nil, NewWorker attempts to build one from NATS_TLS_* env vars.
	NATSTLSConfig *tls.Config
	// Logger overrides the default stdout logger. When nil, a standard log.Logger is used.
	Logger *log.Logger
	// Metrics receives job lifecycle events for observability (Prometheus, OTel, etc.).
	// When nil, no metrics callbacks are fired.
	Metrics capsdk.MetricsHook
}

// Worker subscribes to subjects and publishes job results.
type Worker struct {
	cfg      Config
	conn     *nats.Conn
	subjects []string
	queue    string
	workerID string
	pool     string

	sem    chan struct{}
	active int32

	subs []*nats.Subscription

	cancelMu sync.Mutex
	cancel   context.CancelFunc
	logger   *log.Logger
	metrics  capsdk.MetricsHook

	consecutiveHBFailures int32 // tracks consecutive heartbeat publish failures
}

// NewWorker builds a worker with a NATS connection.
func NewWorker(cfg Config) (*Worker, error) {
	subjects := trimSubjects(cfg.Subjects)
	if len(subjects) == 0 {
		if strings.TrimSpace(cfg.Type) == "" {
			return nil, errors.New("subjects required")
		}
		subjects = []string{fmt.Sprintf("job.%s.*", strings.TrimSpace(cfg.Type))}
	}

	workerID := resolveWorkerID(cfg.WorkerID, cfg.Type)
	pool := strings.TrimSpace(cfg.Pool)
	if pool == "" {
		pool = strings.TrimSpace(cfg.Type)
	}

	natsURL := strings.TrimSpace(cfg.NatsURL)
	if natsURL == "" {
		natsURL = strings.TrimSpace(os.Getenv("NATS_URL"))
	}
	if natsURL == "" {
		natsURL = defaultNATSURL
	}

	connectTimeout := defaultConnectTimeout
	natsOpts := []nats.Option{nats.Name(workerID), nats.Timeout(connectTimeout)}

	tlsCfg := cfg.NATSTLSConfig
	if tlsCfg == nil {
		var tlsErr error
		tlsCfg, tlsErr = NATSTLSConfigFromEnv()
		if tlsErr != nil {
			return nil, fmt.Errorf("nats tls config: %w", tlsErr)
		}
	}
	if tlsCfg != nil {
		natsOpts = append(natsOpts, nats.Secure(tlsCfg))
	}

	conn, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		return nil, err
	}

	maxParallel := cfg.MaxParallelJobs
	if maxParallel <= 0 {
		maxParallel = defaultMaxParallel
	}

	lgr := cfg.Logger
	if lgr == nil {
		lgr = log.New(os.Stdout, "cordum-runtime ", log.LstdFlags)
	}

	w := &Worker{
		cfg:      cfg,
		conn:     conn,
		subjects: subjects,
		queue:    strings.TrimSpace(cfg.Queue),
		workerID: workerID,
		pool:     pool,
		logger:   lgr,
		metrics:  cfg.Metrics,
	}
	if maxParallel > 0 {
		w.sem = make(chan struct{}, maxParallel)
	}
	// keep the resolved max parallel for heartbeat publishing
	w.cfg.MaxParallelJobs = maxParallel

	return w, nil
}

// Run subscribes to configured subjects and processes jobs until ctx is canceled.
func (w *Worker) Run(ctx context.Context, handler func(context.Context, *agentv1.JobRequest) (*agentv1.JobResult, error)) error {
	if handler == nil {
		return errors.New("handler required")
	}
	if w.conn == nil {
		return errors.New("nats connection unavailable")
	}

	subjects := w.subjectsWithDirect()
	for _, subject := range subjects {
		queue := w.queue
		if queue == "" {
			queue = subject
		}
		sub, err := w.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
			w.dispatch(ctx, msg, handler)
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", subject, err)
		}
		w.subsAppend(sub)
	}

	w.publishHandshake()
	w.startHeartbeat(ctx)

	<-ctx.Done()
	return ctx.Err()
}

// Close drains the NATS connection.
func (w *Worker) Close() error {
	w.cancelMu.Lock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.cancelMu.Unlock()

	if w.conn != nil {
		return w.conn.Drain()
	}
	return nil
}

func (w *Worker) dispatch(ctx context.Context, msg *nats.Msg, handler func(context.Context, *agentv1.JobRequest) (*agentv1.JobResult, error)) {
	if ctx.Err() != nil {
		return
	}
	if w.sem != nil {
		w.sem <- struct{}{}
		atomic.AddInt32(&w.active, 1)
	}

	go func() {
		defer func() {
			if w.sem != nil {
				<-w.sem
				atomic.AddInt32(&w.active, -1)
			}
		}()

		var packet agentv1.BusPacket
		if err := proto.Unmarshal(msg.Data, &packet); err != nil {
			w.logger.Printf("worker: decode packet failed: %v", err)
			return
		}
		if w.cfg.PublicKeys != nil {
			pub, ok := w.cfg.PublicKeys[packet.GetSenderId()]
			if !ok {
				w.logger.Printf("worker: no public key for sender %s", packet.GetSenderId())
				return
			}
			if len(packet.GetSignature()) == 0 {
				w.logger.Printf("worker: missing signature for sender %s", packet.GetSenderId())
				return
			}
			if err := capsdk.VerifyPacketSignature(&packet, pub); err != nil {
				w.logger.Printf("worker: invalid signature from sender %s: %v", packet.GetSenderId(), err)
				return
			}
		}

		req := packet.GetJobRequest()
		if req == nil || req.GetJobId() == "" {
			return
		}
		if w.metrics != nil {
			w.metrics.OnJobReceived(req.GetJobId(), req.GetTopic())
		}

		start := time.Now()
		panicRecovered := false
		res, err := func() (result *agentv1.JobResult, err error) {
			defer func() {
				if rec := recover(); rec != nil {
					panicRecovered = true
					w.logger.Printf("worker: handler panic: %v", rec)
					w.logger.Printf("worker: handler panic stack: %s", debug.Stack())
					err = fmt.Errorf("handler panic: %v", rec)
				}
			}()
			return handler(ctx, req)
		}()
		execMs := time.Since(start).Milliseconds()

		if res == nil {
			res = &agentv1.JobResult{
				JobId:  req.GetJobId(),
				Status: agentv1.JobStatus_JOB_STATUS_FAILED,
			}
			if !panicRecovered && err == nil {
				res.ErrorMessage = "handler returned nil"
			}
		}
		if err != nil {
			if res.Status == agentv1.JobStatus_JOB_STATUS_UNSPECIFIED {
				res.Status = agentv1.JobStatus_JOB_STATUS_FAILED
			}
			if panicRecovered || strings.TrimSpace(res.ErrorMessage) == "" {
				res.ErrorMessage = err.Error()
			}
		}
		if res.JobId == "" {
			res.JobId = req.GetJobId()
		}
		if res.WorkerId == "" {
			res.WorkerId = w.workerID
		}
		if res.ExecutionMs == 0 {
			res.ExecutionMs = execMs
		}
		if w.metrics != nil {
			status := res.Status.String()
			if res.Status == agentv1.JobStatus_JOB_STATUS_FAILED {
				w.metrics.OnJobFailed(req.GetJobId(), res.ErrorMessage)
			} else {
				w.metrics.OnJobCompleted(req.GetJobId(), execMs, status)
			}
		}

		out := &agentv1.BusPacket{
			TraceId:         packet.GetTraceId(),
			SenderId:        w.workerID,
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			CreatedAt:       timestamppb.Now(),
			Payload: &agentv1.BusPacket_JobResult{
				JobResult: res,
			},
		}
		if w.cfg.PrivateKey != nil {
			if err := capsdk.SignPacket(out, w.cfg.PrivateKey); err != nil {
				w.logger.Printf("worker: sign result failed: %v", err)
				return
			}
		}
		data, mErr := capsdk.MarshalDeterministic(out)
		if mErr != nil {
			w.logger.Printf("worker: marshal result failed: %v", mErr)
			return
		}
		if err := w.conn.Publish(capsdk.SubjectResult, data); err != nil {
			w.logger.Printf("worker: publish result failed: %v", err)
		}
	}()
}

// buildHeartbeatPacket builds the heartbeat BusPacket for the current worker state. TraceId is set
// to the workerID correlation id (== SenderId) so the packet passes the bus required-field contract
// (CAP/core capsdk.ValidateBusPacket reject an empty trace_id, which would drop every sys.heartbeat
// at the scheduler/gateway consumer). It is set in the struct literal so it is present inside the
// signed bytes when capsdk.SignPacket runs.
func (w *Worker) buildHeartbeatPacket(active int32) *agentv1.BusPacket {
	return &agentv1.BusPacket{
		TraceId:         w.workerID,
		SenderId:        w.workerID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &agentv1.BusPacket_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				WorkerId:        w.workerID,
				Pool:            w.pool,
				Type:            w.cfg.Type,
				ActiveJobs:      active,
				MaxParallelJobs: w.cfg.MaxParallelJobs,
				Capabilities:    w.cfg.Capabilities,
				Labels:          w.cfg.Labels,
			},
		},
	}
}

func (w *Worker) startHeartbeat(ctx context.Context) {
	interval := w.cfg.HeartbeatEvery
	if interval <= 0 {
		interval = capsdk.DefaultHeartbeatInterval
	}
	hbCtx, cancel := context.WithCancel(ctx)
	w.cancelMu.Lock()
	w.cancel = cancel
	w.cancelMu.Unlock()

	payloadFn := func() ([]byte, error) {
		packet := w.buildHeartbeatPacket(atomic.LoadInt32(&w.active))
		if w.cfg.PrivateKey != nil {
			if err := capsdk.SignPacket(packet, w.cfg.PrivateKey); err != nil {
				return nil, err
			}
		}
		return capsdk.MarshalDeterministic(packet)
	}

	// publishHeartbeat sends a heartbeat and handles errors with retry logic.
	publishHeartbeat := func() {
		payload, err := payloadFn()
		if err != nil {
			w.logger.Printf("worker: heartbeat payload build failed: %v", err)
			return
		}
		if err := w.conn.Publish(capsdk.SubjectHeartbeat, payload); err != nil {
			failures := atomic.AddInt32(&w.consecutiveHBFailures, 1)
			w.logger.Printf("worker: heartbeat publish failed (consecutive=%d): %v", failures, err)

			// Retry once after 500ms
			time.Sleep(500 * time.Millisecond)
			payload2, err2 := payloadFn()
			if err2 == nil {
				if err3 := w.conn.Publish(capsdk.SubjectHeartbeat, payload2); err3 == nil {
					atomic.StoreInt32(&w.consecutiveHBFailures, 0)
					return
				}
				failures = atomic.AddInt32(&w.consecutiveHBFailures, 1)
			}

			if failures >= 3 {
				w.logger.Printf("worker: ERROR heartbeat publish failing consistently — worker may appear dead to scheduler (consecutive=%d)", failures)
			}
			return
		}
		atomic.StoreInt32(&w.consecutiveHBFailures, 0)
		if w.metrics != nil {
			w.metrics.OnHeartbeatSent(w.workerID)
		}
	}

	publishHeartbeat()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				publishHeartbeat()
			}
		}
	}()
}

func (w *Worker) publishHandshake() {
	packet := w.buildHandshakePacket()
	if w.cfg.PrivateKey != nil {
		if err := capsdk.SignPacket(packet, w.cfg.PrivateKey); err != nil {
			w.logger.Printf("worker: sign handshake failed: %v", err)
			return
		}
	}
	data, err := capsdk.MarshalDeterministic(packet)
	if err != nil {
		w.logger.Printf("worker: marshal handshake failed: %v", err)
		return
	}
	if err := w.conn.Publish(capsdk.SubjectHandshake, data); err != nil {
		w.logger.Printf("worker: publish handshake failed: %v", err)
	}
}

// buildHandshakePacket builds the worker handshake BusPacket. TraceId == workerID for the same bus
// required-field reason as buildHeartbeatPacket, set in the literal so it is inside any signed bytes.
func (w *Worker) buildHandshakePacket() *agentv1.BusPacket {
	caps := make(map[string]bool, len(w.cfg.Capabilities))
	for _, c := range w.cfg.Capabilities {
		caps[c] = true
	}
	return &agentv1.BusPacket{
		TraceId:         w.workerID,
		SenderId:        w.workerID,
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &agentv1.BusPacket_Handshake{
			Handshake: &agentv1.Handshake{
				ComponentId:       w.workerID,
				Role:              agentv1.ComponentRole_COMPONENT_ROLE_WORKER,
				SupportedVersions: []int32{capsdk.DefaultProtocolVersion},
				Capabilities:      caps,
			},
		},
	}
}

func (w *Worker) subjectsWithDirect() []string {
	subjects := append([]string{}, w.subjects...)
	direct := DirectSubject(w.workerID)
	if direct == "" {
		return subjects
	}
	for _, subject := range subjects {
		if subject == direct {
			return subjects
		}
	}
	return append(subjects, direct)
}

func (w *Worker) subsAppend(sub *nats.Subscription) {
	if sub == nil {
		return
	}
	w.subs = append(w.subs, sub)
}

func trimSubjects(subjects []string) []string {
	if len(subjects) == 0 {
		return nil
	}
	out := make([]string, 0, len(subjects))
	seen := map[string]struct{}{}
	for _, subject := range subjects {
		if s := strings.TrimSpace(subject); s != "" {
			if _, ok := seen[s]; ok {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func resolveWorkerID(explicit, workerType string) string {
	workerID := strings.TrimSpace(explicit)
	if workerID == "" {
		workerID = strings.TrimSpace(os.Getenv("WORKER_ID"))
	}
	if workerID != "" {
		return workerID
	}

	workerType = strings.TrimSpace(workerType)
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		if workerType != "" {
			return workerType
		}
		return "cordum-worker"
	}
	if workerType == "" {
		return host
	}
	return workerType + "-" + host
}
