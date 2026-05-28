// multi-topic-worker-go — canonical pattern for a Cordum worker that handles
// MORE THAN ONE topic from the same process.
//
// Why this example exists
// =======================
// `examples/hello-worker-go/main.go` is intentionally minimal — one handler,
// registered on the topic subject AND the per-worker direct subject. That
// works because there is only one possible handler type to invoke.
//
// The naive copy-paste for a worker with multiple typed handlers looks like:
//
//   runtime.Register(agent, "job.app.a", handlerA)         // Handler[InA, OutA]
//   runtime.Register(agent, "job.app.b", handlerB)         // Handler[InB, OutB]
//   runtime.Register(agent, runtime.DirectSubject(id), handlerA)  // ← bug
//
// The last line silently makes `handlerA` run for EVERY job the scheduler
// dispatches via the per-worker direct subject — regardless of JobRequest.Topic.
// A workflow's `job.app.b` step succeeds with garbage output (handlerA happens
// to JSON-encode SOMETHING), and the next step fails on parse.
//
// The fix: register a single topic-aware *dispatcher* on every subject. The
// dispatcher reads `ctx.Job.Topic` and routes to the right typed implementation
// internally. See `makeDispatcher` below — the same function is registered on
// every topic and on the direct subject, so the scheduler can deliver via
// whichever path it likes and the dispatcher always picks the right handler.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
)

const (
	defaultNatsURL  = "nats://127.0.0.1:4222"
	defaultRedisURL = "redis://:cordum-dev@127.0.0.1:6379/0"
	poolName        = "multi-topic"

	topicUpper = "job.multi-topic.upper"
	topicAdd   = "job.multi-topic.add"
	topicTag   = "job.multi-topic.tag"
)

// ---------------------------------------------------------------------------
// Typed I/O for each topic — kept as separate Go types so the compiler catches
// shape drift. The dispatcher below converts between JSON and these types.
// ---------------------------------------------------------------------------

type upperIn struct {
	Text string `json:"text"`
}
type upperOut struct {
	Text string `json:"text"`
}

type addIn struct {
	A int `json:"a"`
	B int `json:"b"`
}
type addOut struct {
	Sum int `json:"sum"`
}

type tagIn struct {
	Items []string `json:"items"`
	Tag   string   `json:"tag"`
}
type tagOut struct {
	Tagged []string `json:"tagged"`
}

// ---------------------------------------------------------------------------
// Pure typed handlers — exactly what you'd write if you had ONE topic each.
// ---------------------------------------------------------------------------

func handleUpper(_ runtime.Context, in upperIn) (upperOut, error) {
	return upperOut{Text: strings.ToUpper(in.Text)}, nil
}

func handleAdd(_ runtime.Context, in addIn) (addOut, error) {
	return addOut{Sum: in.A + in.B}, nil
}

func handleTag(_ runtime.Context, in tagIn) (tagOut, error) {
	out := tagOut{Tagged: make([]string, 0, len(in.Items))}
	for _, item := range in.Items {
		out.Tagged = append(out.Tagged, in.Tag+":"+item)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The dispatcher — the one piece that makes multi-topic safe.
//
// CAP handlers are typed [TIn, TOut]; this dispatcher uses json.RawMessage on
// both sides so the same Go function value can be registered on every topic
// AND the direct subject. It looks at ctx.Job.Topic, decodes the raw input
// into the correct typed struct, calls the right typed handler, and re-encodes
// the typed output.
//
// Add a new topic in three places: a typed Handler above, a `case` here, and
// the topic constant + Register call in main().
// ---------------------------------------------------------------------------

func makeDispatcher() runtime.Handler[json.RawMessage, json.RawMessage] {
	return func(ctx runtime.Context, raw json.RawMessage) (json.RawMessage, error) {
		topic := ""
		if ctx.Job != nil {
			topic = ctx.Job.Topic
		}
		switch topic {
		case topicUpper:
			return invokeTyped(ctx, raw, handleUpper)
		case topicAdd:
			return invokeTyped(ctx, raw, handleAdd)
		case topicTag:
			return invokeTyped(ctx, raw, handleTag)
		default:
			// Unknown topic on a subject we're subscribed to — return a typed
			// error so the scheduler logs the failure with the topic visible.
			return nil, fmt.Errorf("multi-topic dispatcher: no handler for topic %q", topic)
		}
	}
}

// invokeTyped is the type-erasure shim that lets the dispatcher (which works
// in json.RawMessage) call a typed Handler[TIn, TOut] generically.
func invokeTyped[TIn any, TOut any](
	ctx runtime.Context,
	raw json.RawMessage,
	handler runtime.Handler[TIn, TOut],
) (json.RawMessage, error) {
	var in TIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	out, err := handler(ctx, in)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	workerID := envOr("WORKER_ID", "multi-topic-worker")
	natsURL := envOr("NATS_URL", defaultNatsURL)
	redisURL := envOr("REDIS_URL", defaultRedisURL)

	slog.Info("multi-topic-worker starting",
		"worker_id", workerID,
		"pool", poolName,
		"nats_scheme", parseScheme(natsURL),
		"redis_scheme", parseScheme(redisURL),
	)

	store, err := runtime.NewRedisBlobStoreWithPing(redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer func() { _ = store.Close() }()

	natsOpts, err := natsConnectOptions(workerID)
	if err != nil {
		log.Fatalf("nats tls config: %v", err)
	}
	nc, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer nc.Drain() //nolint:errcheck

	agent := &runtime.Agent{
		NATSURL:  natsURL,
		RedisURL: redisURL,
		NATS:     nc,
		Store:    store,
		SenderID: workerID,
	}

	// ---- the canonical pattern: one dispatcher, registered everywhere ----
	dispatcher := makeDispatcher()
	runtime.Register(agent, topicUpper, dispatcher)
	runtime.Register(agent, topicAdd, dispatcher)
	runtime.Register(agent, topicTag, dispatcher)
	// Direct subject — scheduler routes here when it picks this specific
	// worker. The dispatcher's topic-switch ensures the right handler runs.
	runtime.Register(agent, runtime.DirectSubject(workerID), dispatcher)

	if err := agent.Start(); err != nil {
		_ = agent.Close()
		log.Fatalf("runtime start: %v", err)
	}
	defer func() {
		if err := agent.Close(); err != nil {
			log.Printf("runtime close: %v", err)
		}
	}()

	heartbeatFn := func() ([]byte, error) {
		return runtime.HeartbeatPayload(workerID, poolName, 0, 4, 0)
	}
	if payload, err := heartbeatFn(); err == nil {
		_ = runtime.EmitHeartbeat(nc, payload)
	}
	go runtime.HeartbeatLoop(ctx, nc, heartbeatFn)

	slog.Info("multi-topic-worker ready", "topics", []string{topicUpper, topicAdd, topicTag})
	<-ctx.Done()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseScheme(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return "unknown"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "unknown"
	}
	return strings.ToLower(parsed.Scheme)
}

func natsConnectOptions(workerID string) ([]nats.Option, error) {
	opts := []nats.Option{
		nats.Name(workerID),
		nats.Timeout(5 * time.Second),
	}
	tlsCfg, err := runtime.NATSTLSConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
	}
	return opts, nil
}
