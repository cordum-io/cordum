// Package main is the demo-quickstart greeter worker.
//
// It subscribes to a single topic (job.demo-quickstart.greet) and replies with
// "hello, <name>!". The DENY and REQUIRE_APPROVAL paths never reach a
// worker — the kernel blocks or escalates them before dispatch — so this
// binary only needs to service the ALLOW rule.
//
// Design notes:
//   - Structured errors: handler never panics on bad input. Nil/empty name
//     falls back to "world"; unexpected payload types return a named error
//     so the run's result surfaces a useful message.
//   - SIGTERM graceful shutdown bounded at 5 s. The NATS Drain() call
//     flushes in-flight jobs back to the server so nothing is lost.
//   - Metrics on :9091/metrics (standard Prometheus text format, empty body
//     when no jobs have been served — Prometheus tolerates this).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	workerID       = "demo-quickstart-greeter"
	workerPool     = "demo-quickstart"
	topicGreet     = "job.demo-quickstart.greet"
	metricsAddr    = ":9091"
	shutdownBudget = 5 * time.Second
)

// greetPayload is the typed input for job.demo-quickstart.greet. All fields are
// optional — the handler copes with every field being empty.
type greetPayload struct {
	Name string `json:"name"`
}

// greetResult is what the worker writes back to the blob store. The
// summary transform step in the workflow pulls `greeting` and `topic`
// directly.
type greetResult struct {
	Greeting string `json:"greeting"`
	Topic    string `json:"topic"`
	JobID    string `json:"job_id"`
	WorkerID string `json:"worker_id"`
	Time     string `json:"time"`
}

// metrics is the tiny counter surface the /metrics endpoint exposes.
// Using atomic integers keeps the handler lock-free.
type metrics struct {
	jobsTotal    atomic.Int64
	jobsFailed   atomic.Int64
	lastDuration atomic.Int64 // microseconds
}

var m metrics

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	natsURL := envOr("NATS_URL", "nats://127.0.0.1:4222")
	redisURL := envOr("REDIS_URL", "redis://:cordum-dev@127.0.0.1:6379/0")

	if !runtime.ValidateRedisURL(redisURL) {
		log.Println("[demo-quickstart] WARNING: REDIS_URL missing '@' — may lack auth credentials")
	}
	blobStore, err := runtime.NewRedisBlobStoreWithPing(redisURL)
	if err != nil {
		log.Fatalf("[demo-quickstart] redis connect: %v", err)
	}
	log.Println("[demo-quickstart] redis ok")

	nc, err := connectNATS(natsURL)
	if err != nil {
		log.Fatalf("[demo-quickstart] nats connect: %v", err)
	}
	// NATS drain happens explicitly after <-ctx.Done() so it runs before
	// srv.Shutdown (see bottom of main) — a deferred drain would fire
	// AFTER the metrics server stopped, delaying graceful exit.
	log.Printf("[demo-quickstart] nats connected (url=%s)", natsURL)

	agent := &runtime.Agent{
		NATS:     nc,
		NATSURL:  natsURL,
		RedisURL: redisURL,
		Store:    blobStore,
		SenderID: workerID,
	}
	runtime.Register(agent, topicGreet, greetHandler)
	runtime.Register(agent, "worker."+workerID+".jobs", greetHandler)

	if err := agent.Start(); err != nil {
		log.Fatalf("[demo-quickstart] agent start: %v", err)
	}

	// Heartbeat so the dashboard and scheduler see the worker as alive.
	go heartbeatLoop(ctx, nc)

	// Metrics HTTP server. Shut down cleanly with the main context.
	srv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[demo-quickstart] metrics: %v", err)
		}
	}()

	log.Printf("[demo-quickstart] greeter ready (topic=%s, metrics=%s)", topicGreet, metricsAddr)

	<-ctx.Done()
	log.Println("[demo-quickstart] shutdown requested")

	// Drain NATS before stopping the metrics server so in-flight jobs
	// get dispatched cleanly — nc.Drain blocks until every subscription
	// has finished processing its backlog. Metrics /healthz probes tolerate
	// the short window where NATS is draining but HTTP is still serving.
	if err := nc.Drain(); err != nil {
		log.Printf("[demo-quickstart] nats drain: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	log.Println("[demo-quickstart] bye")
}

// greetHandler is the typed handler the runtime invokes per job.
//
// Runtime typed-handler generics pre-decode payload, so a broken JSON
// payload fails at the SDK layer (and the runtime publishes a failed
// JobResult with our job id). Here we only have to do our own
// validation — an empty / too-long name is business-rule invalid.
// m.jobsFailed increments on those paths so the /metrics endpoint has a
// meaningful failure counter rather than a permanently-zero dead gauge.
const maxGreetNameLen = 256

func greetHandler(ctx runtime.Context, payload greetPayload) (greetResult, error) {
	start := time.Now()
	defer func() {
		m.jobsTotal.Add(1)
		m.lastDuration.Store(time.Since(start).Microseconds())
	}()

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "world"
	}
	if len(name) > maxGreetNameLen {
		m.jobsFailed.Add(1)
		return greetResult{}, fmt.Errorf("greet: name too long (%d > %d)", len(name), maxGreetNameLen)
	}

	return greetResult{
		Greeting: fmt.Sprintf("hello, %s!", name),
		Topic:    ctx.Job.GetTopic(),
		JobID:    ctx.Job.GetJobId(),
		WorkerID: workerID,
		Time:     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func connectNATS(natsURL string) (*nats.Conn, error) {
	opts := []nats.Option{nats.Name(workerID), nats.Timeout(5 * time.Second)}
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
		hb := &agentv1.BusPacket{
			SenderId:        workerID,
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			Payload: &agentv1.BusPacket_Heartbeat{
				Heartbeat: &agentv1.Heartbeat{
					WorkerId:        workerID,
					Pool:            workerPool,
					Type:            "cpu",
					MaxParallelJobs: 4,
					Capabilities:    []string{"demo-quickstart.greet"},
					Labels: map[string]string{
						"name": "Quickstart Greeter",
						"env":  "demo",
					},
				},
			},
		}
		return proto.Marshal(hb)
	}
	if payload, err := build(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	runtime.HeartbeatLoop(ctx, nc, build)
}

// metricsHandler returns the /metrics endpoint in Prometheus text format.
// No external dependency — a hand-rolled emission keeps the worker lean.
func metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		total := m.jobsTotal.Load()
		failed := m.jobsFailed.Load()
		lastMicros := m.lastDuration.Load()
		_, _ = fmt.Fprintf(w, "# HELP demo_quickstart_jobs_total Total jobs processed.\n")
		_, _ = fmt.Fprintf(w, "# TYPE demo_quickstart_jobs_total counter\n")
		_, _ = fmt.Fprintf(w, "demo_quickstart_jobs_total %d\n", total)
		_, _ = fmt.Fprintf(w, "# HELP demo_quickstart_jobs_failed_total Jobs that returned an error.\n")
		_, _ = fmt.Fprintf(w, "# TYPE demo_quickstart_jobs_failed_total counter\n")
		_, _ = fmt.Fprintf(w, "demo_quickstart_jobs_failed_total %d\n", failed)
		_, _ = fmt.Fprintf(w, "# HELP demo_quickstart_last_duration_microseconds Duration of the most recent handler invocation.\n")
		_, _ = fmt.Fprintf(w, "# TYPE demo_quickstart_last_duration_microseconds gauge\n")
		_, _ = fmt.Fprintf(w, "demo_quickstart_last_duration_microseconds %d\n", lastMicros)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
