// Package main is the perf-harness worker used by the PR2-10 scenarios.
//
// It serves two topics in pool "perf":
//   - job.perf succeeds after --service-time;
//   - job.perf.fail fails every handler attempt (the failing dependency).
//
// --handler-retries sets the CAP runtime's handler retry count. Every handler
// run is counted against the original request id the job carries in the
// payload field perf_request_id, and GET /counts on a loopback-only address
// serves those counts as JSON.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
)

const (
	workerID       = "perfworker"
	workerPool     = "perf"
	topicPerf      = "job.perf"
	topicPerfFail  = "job.perf.fail"
	requestIDField = "perf_request_id"
	maxParallel    = 4
	shutdownBudget = 5 * time.Second
)

var errNotLoopback = errors.New("counts address must be a loopback host")

type config struct {
	serviceTime    time.Duration
	handlerRetries int
	countsAddr     string
}

func parseFlags(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("perfworker", flag.ContinueOnError)
	fs.DurationVar(&cfg.serviceTime, "service-time", 10*time.Millisecond, "time a job.perf handler run takes before it succeeds")
	fs.IntVar(&cfg.handlerRetries, "handler-retries", 0, "CAP runtime handler retries per job")
	fs.StringVar(&cfg.countsAddr, "counts-addr", "127.0.0.1:9092", "loopback address for GET /counts")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.handlerRetries < 0 {
		return config{}, fmt.Errorf("--handler-retries must not be negative, got %d", cfg.handlerRetries)
	}
	return cfg, nil
}

// countsDoc is the JSON body of GET /counts.
type countsDoc struct {
	Counts     map[string]int64 `json:"counts"`
	Unlabelled int64            `json:"unlabelled"`
}

// counter counts handler runs per perf_request_id. The runtime runs one
// handler goroutine per subscription, so runs can overlap.
type counter struct {
	mu   sync.Mutex
	runs countsDoc
}

func newCounter() *counter {
	return &counter{runs: countsDoc{Counts: map[string]int64{}}}
}

func (c *counter) inc(requestID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if requestID == "" {
		c.runs.Unlabelled++
		return
	}
	c.runs.Counts[requestID]++
}

func (c *counter) snapshot() countsDoc {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := countsDoc{Counts: make(map[string]int64, len(c.runs.Counts)), Unlabelled: c.runs.Unlabelled}
	for id, n := range c.runs.Counts {
		out.Counts[id] = n
	}
	return out
}

type perfResult struct {
	RequestID string `json:"perf_request_id"`
	Topic     string `json:"topic"`
}

type perfWorker struct {
	ctx         context.Context // ends a job.perf wait on shutdown
	serviceTime time.Duration
	counts      *counter
}

// requestID reads perf_request_id from the top level (a workflow step's input
// is the whole job input), else from "context" (a gateway submit's context).
func requestID(input map[string]any) string {
	if id, ok := input[requestIDField].(string); ok && id != "" {
		return id
	}
	if nested, ok := input["context"].(map[string]any); ok {
		if id, ok := nested[requestIDField].(string); ok {
			return id
		}
	}
	return ""
}

// handle runs once per handler attempt, so every attempt is counted.
func (w *perfWorker) handle(ctx runtime.Context, input map[string]any) (perfResult, error) {
	id := requestID(input)
	w.counts.inc(id)
	topic := ctx.Job.GetTopic()
	switch topic {
	case topicPerf:
		timer := time.NewTimer(w.serviceTime)
		defer timer.Stop()
		select {
		case <-timer.C:
			return perfResult{RequestID: id, Topic: topic}, nil
		case <-w.ctx.Done():
			return perfResult{}, fmt.Errorf("perf: shutting down before the service time ended: %w", w.ctx.Err())
		}
	case topicPerfFail:
		return perfResult{}, errors.New("perf: job.perf.fail fails every attempt")
	default:
		return perfResult{}, fmt.Errorf("perf: unsupported topic %q", topic)
	}
}

// register binds the handler to both topics and to the worker's direct
// subject; handle branches on the job's topic, not the subject.
func register(agent *runtime.Agent, w *perfWorker, retries int) {
	for _, subject := range []string{topicPerf, topicPerfFail, runtime.DirectSubject(workerID)} {
		runtime.Register(agent, subject, w.handle, runtime.WithRetries(retries))
	}
}

// listenCounts refuses any host that is not loopback before it listens.
func listenCounts(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("counts address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("%w: %q", errNotLoopback, addr)
	}
	return net.Listen("tcp", addr)
}

func countsHandler(c *counter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /counts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(c.snapshot()); err != nil {
			log.Printf("[perfworker] write counts: %v", err)
		}
	})
	return mux
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("[perfworker] %v", err)
	}
}

func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}
	ln, err := listenCounts(cfg.countsAddr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	natsURL := envOr("NATS_URL", "nats://127.0.0.1:4222")
	redisURL := envOr("REDIS_URL", "redis://:cordum-dev@127.0.0.1:6379/0")
	store, err := runtime.NewRedisBlobStoreWithPing(redisURL)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	defer func() { _ = store.Close() }()
	closed := make(chan struct{})
	nc, err := connectNATS(natsURL, closed)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}

	w := &perfWorker{ctx: ctx, serviceTime: cfg.serviceTime, counts: newCounter()}
	agent := runtime.NewLegacyUnsignedAgent(natsURL, redisURL, workerID, nc, store)
	register(agent, w, cfg.handlerRetries)
	if err := agent.Start(); err != nil {
		nc.Close()
		return fmt.Errorf("agent start: %w", err)
	}
	go heartbeatLoop(ctx, nc)

	srv := &http.Server{Handler: countsHandler(w.counts), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[perfworker] counts server: %v", err)
		}
	}()
	log.Printf("[perfworker] ready (pool=%s, service-time=%s, handler-retries=%d, counts=%s)",
		workerPool, cfg.serviceTime, cfg.handlerRetries, ln.Addr())

	<-ctx.Done()
	return shutdown(nc, closed, srv)
}

// shutdown drains NATS, then stops the counts server, inside one budget.
func shutdown(nc *nats.Conn, closed <-chan struct{}, srv *http.Server) error {
	log.Println("[perfworker] shutdown requested")
	budget, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()
	if err := nc.Drain(); err != nil {
		log.Printf("[perfworker] nats drain: %v", err)
	}
	select {
	case <-closed:
	case <-budget.Done():
		log.Println("[perfworker] nats drain did not finish within the budget")
		nc.Close()
	}
	return srv.Shutdown(budget)
}

func connectNATS(natsURL string, closed chan<- struct{}) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name(workerID),
		nats.Timeout(5 * time.Second),
		nats.DrainTimeout(shutdownBudget),
		nats.ClosedHandler(func(*nats.Conn) { close(closed) }),
	}
	tlsCfg, err := runtime.NATSTLSConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("nats tls config: %w", err)
	}
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
	}
	if token := os.Getenv("NATS_TOKEN"); token != "" {
		opts = append(opts, nats.Token(token))
	}
	return nats.Connect(natsURL, opts...)
}

func heartbeatLoop(ctx context.Context, nc *nats.Conn) {
	build := func() ([]byte, error) {
		return runtime.HeartbeatPayload(workerID, workerPool, 0, maxParallel, 0)
	}
	if payload, err := build(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	runtime.HeartbeatLoop(ctx, nc, build)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
