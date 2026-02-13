# SDK Reference

Complete reference for the Cordum Go SDK — worker runtime, gateway client, bus helpers, and testing patterns.

> For the CAP bus protocol and pointer semantics, see [AGENT_PROTOCOL.md](AGENT_PROTOCOL.md).
> For the REST API endpoint reference, see [api-reference.md](api-reference.md).

---

## Installation

```bash
go get github.com/cordum/cordum/sdk@latest
```

The SDK has two main packages:

| Package | Import | Purpose |
|---------|--------|---------|
| `sdk/client` | `github.com/cordum/cordum/sdk/client` | HTTP client for the API gateway |
| `sdk/runtime` | `github.com/cordum/cordum/sdk/runtime` | Worker runtime (NATS subscriptions, heartbeats, blob store) |

Generated protobuf types live in `sdk/gen/go/cordum/v1/`.

---

## 1. Gateway Client

### Creating a Client

```go
import "github.com/cordum/cordum/sdk/client"

c := client.New("http://localhost:8081", os.Getenv("CORDUM_API_KEY"))
```

`New()` reads `CORDUM_TENANT_ID` from the environment (defaults to `"default"`). The client sets `X-API-Key` and `X-Tenant-ID` headers on every request.

You can override the tenant or HTTP client after creation:

```go
c.TenantID = "my-org"
c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
```

### Job Operations

#### Submit a Job

```go
resp, err := c.SubmitJob(ctx, &client.JobSubmitRequest{
    Prompt:     "Summarize this document",
    Topic:      "job.summarize.text",
    Labels:     map[string]string{"source": "api"},
    RiskTags:   []string{"read"},
    Capability: "summarize",
    PackID:     "my-pack",
})
// resp.JobID, resp.TraceID
```

**Required fields:** `Prompt` (the job payload). `Topic` defaults to `job.default` if omitted.

**Optional fields:**

| Field | Type | Description |
|-------|------|-------------|
| `Topic` | string | NATS subject for routing (e.g. `job.hello-pack.echo`) |
| `Context` | any | Additional context attached to the job |
| `OrgID` | string | Organization scope |
| `PrincipalID` | string | Acting principal |
| `IdempotencyKey` | string | Prevents duplicate submission |
| `PackID` | string | Pack that owns this job type |
| `Capability` | string | Required worker capability |
| `RiskTags` | []string | Risk tags for safety evaluation |
| `Requires` | []string | Required capabilities list |
| `Labels` | map[string]string | Arbitrary key-value labels |

#### Get Job Status

```go
job, err := c.GetJob(ctx, "job-abc123")
// job["status"], job["result_ptr"], job["safety_decision"], etc.
```

Returns a `map[string]any` matching the gateway JSON response.

#### Approve or Reject a Job

```go
// Approve
err := c.ApproveJob(ctx, "job-abc123", true)

// Reject
err := c.ApproveJob(ctx, "job-abc123", false)
```

#### Retry from DLQ

```go
err := c.RetryDLQ(ctx, "job-abc123")
```

### Workflow Operations

#### Create a Workflow

```go
wfID, err := c.CreateWorkflow(ctx, &client.CreateWorkflowRequest{
    Name:        "data-pipeline",
    Description: "Fetch, process, and store data",
    TimeoutSec:  3600,
    Steps: map[string]client.Step{
        "fetch": {
            "type":  "job",
            "topic": "job.fetch.data",
        },
        "process": {
            "type":       "job",
            "topic":      "job.process.data",
            "depends_on": []string{"fetch"},
        },
    },
})
```

#### Start a Run

```go
runID, err := c.StartRun(ctx, wfID, map[string]any{
    "source": "s3://bucket/path",
})
```

With options (dry-run, idempotency):

```go
runID, err := c.StartRunWithOptions(ctx, wfID, input, client.RunOptions{
    DryRun:         true,
    IdempotencyKey: "unique-key-123",
})
```

#### Get Run Status

```go
run, err := c.GetRun(ctx, "run-abc123")
// run.Status, run.Steps, run.Output, run.Error
```

#### Get Run Timeline

```go
events, err := c.GetRunTimeline(ctx, "run-abc123")
for _, ev := range events {
    fmt.Printf("%s %s step=%s status=%s\n", ev.Time, ev.Type, ev.StepID, ev.Status)
}
```

#### Approve a Workflow Step

```go
err := c.ApproveStep(ctx, workflowID, runID, stepID, true)  // approve
err := c.ApproveStep(ctx, workflowID, runID, stepID, false) // reject
```

#### Delete Workflow / Run

```go
err := c.DeleteWorkflow(ctx, wfID)
err := c.DeleteRun(ctx, runID)
```

### Artifact Operations

#### Upload an Artifact

```go
ptr, err := c.PutArtifact(ctx, []byte("file contents"), client.ArtifactMetadata{
    ContentType: "text/plain",
    Retention:   "30d",
    Labels:      map[string]string{"job_id": "job-abc123"},
}, 0) // 0 = no size limit
```

#### Download an Artifact

```go
artifact, err := c.GetArtifact(ctx, "art:job:abc123")
fmt.Println(string(artifact.Content))
fmt.Println(artifact.Metadata.ContentType)
```

### System Status

```go
status, err := c.GetStatus(ctx)
fmt.Println(status["version"], status["uptime"])
```

### Error Handling

All client methods return errors for non-2xx responses:

```go
resp, err := c.SubmitJob(ctx, req)
if err != nil {
    // err message includes HTTP status code and response body
    // e.g. "unexpected status 403: invalid API key"
    log.Fatal(err)
}
```

### Client Types Reference

```go
type Client struct {
    BaseURL    string
    APIKey     string
    TenantID   string
    HTTPClient *http.Client
}

type JobSubmitRequest struct {
    Prompt, Topic, OrgID, TenantID, PrincipalID string
    ActorID, ActorType, IdempotencyKey, PackID, Capability string
    Context  any
    RiskTags []string
    Requires []string
    Labels   map[string]string
}

type JobSubmitResponse struct {
    JobID   string
    TraceID string
}

type CreateWorkflowRequest struct {
    ID, OrgID, TeamID, Name, Description, Version, CreatedBy string
    TimeoutSec  int64
    Steps       map[string]Step
    InputSchema map[string]any
    Parameters  []map[string]any
    Config      map[string]any
}

type WorkflowRun struct {
    ID, WorkflowID, Status, UpdatedAt string
    Steps    map[string]StepRun
    Metadata map[string]string
    Labels   map[string]string
    Context  map[string]any
    Output   map[string]any
    Error    map[string]any
}

type RunOptions struct {
    DryRun         bool
    IdempotencyKey string
}

type ArtifactMetadata struct {
    ContentType string
    SizeBytes   int64
    Retention   string
    Labels      map[string]string
}
```

---

## 2. Worker Runtime

The runtime package provides two worker models: the **Agent** model (typed handlers with generics) and the **Worker** model (raw protobuf handler).

### Agent Model (Recommended)

The Agent model uses Go generics for type-safe input/output:

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/cordum/cordum/sdk/runtime"
)

type EchoInput struct {
    Message string `json:"message"`
}

type EchoOutput struct {
    Message string `json:"message"`
}

func main() {
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    agent := &runtime.Agent{
        NATSURL:  os.Getenv("NATS_URL"),
        RedisURL: os.Getenv("REDIS_URL"),
        SenderID: "my-worker",
    }

    runtime.Register(agent, "job.my-pack.echo", func(ctx runtime.Context, in EchoInput) (EchoOutput, error) {
        return EchoOutput{Message: in.Message}, nil
    })

    if err := agent.Start(); err != nil {
        log.Fatal(err)
    }
    defer agent.Close()

    <-ctx.Done()
}
```

Key points:
- `runtime.Register[TIn, TOut]()` deserializes input and serializes output automatically
- The blob store (Redis) handles context pointer dereferencing
- Use `runtime.WithRetries(3)` as an option to override retry count

### Worker Model (Low-Level)

For full control over protobuf messages:

```go
import agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"

worker, err := runtime.NewWorker(runtime.Config{
    Pool:            "my-pool",
    Subjects:        []string{"job.my-pack.*"},
    NatsURL:         os.Getenv("NATS_URL"),
    MaxParallelJobs: 4,
    Capabilities:    []string{"echo", "greet"},
    Labels:          map[string]string{"env": "prod"},
    Type:            "my-worker",
    HeartbeatEvery:  10 * time.Second,
})

err = worker.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
    return &agentv1.JobResult{
        JobId:    req.GetJobId(),
        Status:   agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
        ResultPtr: "result-pointer",
    }, nil
})
```

### Worker Config Reference

```go
type Config struct {
    Pool            string                        // Pool name for heartbeats
    Subjects        []string                      // NATS subjects to subscribe to
    Queue           string                        // Queue group (defaults to subject)
    NatsURL         string                        // NATS connection URL (or NATS_URL env)
    MaxParallelJobs int32                         // Concurrency limit (default 1)
    Capabilities    []string                      // Advertised capabilities
    Labels          map[string]string             // Worker labels
    Type            string                        // Worker type identifier
    WorkerID        string                        // Explicit worker ID (or WORKER_ID env, or auto-generated)
    HeartbeatEvery  time.Duration                 // Heartbeat interval
    PublicKeys      map[string]*ecdsa.PublicKey    // Sender verification keys
    PrivateKey      *ecdsa.PrivateKey             // Signing key for results
}
```

### Worker ID Resolution

Worker ID is resolved in order:
1. `Config.WorkerID` (explicit)
2. `WORKER_ID` environment variable
3. `{Type}-{hostname}` (auto-generated)
4. `hostname` (if no type set)
5. `"cordum-worker"` (fallback)

### Concurrency Control

`MaxParallelJobs` controls how many jobs execute concurrently. The worker uses a semaphore channel:

```go
// Allow up to 8 parallel jobs
worker, _ := runtime.NewWorker(runtime.Config{
    MaxParallelJobs: 8,
    // ...
})
```

When all slots are full, new messages wait until a slot frees up.

### Direct Addressing

Workers automatically subscribe to a direct subject `worker.<workerID>.jobs` in addition to configured subjects. This allows the scheduler to route jobs to a specific worker.

### Graceful Shutdown

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()

// worker.Run blocks until ctx is canceled
err := worker.Run(ctx, handler)

// Close drains the NATS connection
worker.Close()
```

`Close()` calls `conn.Drain()`, which waits for in-flight messages to complete before disconnecting.

### Panic Recovery

The worker runtime recovers from handler panics:
- The panic is logged with a full stack trace
- The job result is set to `JOB_STATUS_FAILED` with the panic message
- The worker continues processing other jobs

---

## 3. Heartbeats

Workers must emit periodic heartbeats so the scheduler knows they are alive.

### Agent Model

The Agent model handles heartbeats automatically via its internal loop.

### Worker Model

The Worker model starts heartbeats automatically in `Run()`. Configure the interval via `Config.HeartbeatEvery`.

### Manual Heartbeats

For custom workers or sidecar processes:

```go
import "github.com/cordum/cordum/sdk/runtime"

nc, _ := nats.Connect("nats://localhost:4222")

// Build heartbeat payload
payload, _ := runtime.HeartbeatPayload(
    "my-worker",  // workerID
    "my-pool",    // pool name
    2,            // active jobs
    8,            // max parallel
    0.45,         // CPU load (0-1)
)

// Emit once
runtime.EmitHeartbeat(nc, payload)

// Or run a loop
go runtime.HeartbeatLoop(ctx, nc, func() ([]byte, error) {
    return runtime.HeartbeatPayload("my-worker", "my-pool", activeJobs, 8, cpuLoad)
})
```

### Heartbeat Variants

| Function | Extra Fields |
|----------|-------------|
| `HeartbeatPayload` | workerID, pool, activeJobs, maxParallel, cpuLoad |
| `HeartbeatPayloadWithMemory` | + memoryLoad |
| `HeartbeatPayloadWithProgress` | + progressPct, lastMemo |

### Heartbeat Subject

Heartbeats are published to `sys.heartbeat`. The scheduler uses them to:
- Track worker liveness (workers not heard from in 3x interval are marked offline)
- Build the worker pool snapshot (active jobs, capacity, capabilities)
- Display worker status in the dashboard

---

## 4. Bus Helpers

The `runtime` package exports bus subject constants and helper functions.

### Subject Constants

```go
const (
    SubjectSubmit        = "sys.job.submit"
    SubjectResult        = "sys.job.result"
    SubjectHeartbeat     = "sys.heartbeat"
    SubjectProgress      = "sys.job.progress"
    SubjectCancel        = "sys.job.cancel"
    SubjectDLQ           = "sys.job.dlq"
    SubjectWorkflowEvent = "sys.workflow.event"
)
```

### Cancel a Job

```go
err := runtime.PublishCancel(nc, &agentv1.JobCancel{
    JobId:  "job-abc123",
    Reason: "user requested cancellation",
}, "trace-id", "my-service", privateKey)
```

### Direct Subject

```go
subject := runtime.DirectSubject("worker-abc")
// "worker.worker-abc.jobs"
```

---

## 5. Blob Store

The runtime provides blob stores for context/result pointer resolution.

### Redis Blob Store (Production)

```go
store, err := runtime.NewRedisBlobStore("redis://:password@localhost:6379/0")
```

With connection verification at startup:

```go
store, err := runtime.NewRedisBlobStoreWithPing("redis://:password@localhost:6379/0")
```

### In-Memory Blob Store (Testing)

```go
store := runtime.NewInMemoryBlobStore()
```

### Redis URL Validation

```go
if !runtime.ValidateRedisURL(redisURL) {
    log.Warn("Redis URL appears to be missing auth credentials")
}
```

### Redis Connectivity Check

```go
if err := runtime.PingRedis(redisURL); err != nil {
    log.Fatalf("Cannot reach Redis: %v", err)
}
```

---

## 6. Packet Signing

Workers can sign outgoing packets and verify incoming ones using ECDSA keys.

### Signing Results

```go
worker, _ := runtime.NewWorker(runtime.Config{
    PrivateKey: myECDSAPrivateKey,
    // ...
})
```

All outgoing `BusPacket` envelopes (results, heartbeats) are signed automatically.

### Verifying Incoming Jobs

```go
worker, _ := runtime.NewWorker(runtime.Config{
    PublicKeys: map[string]*ecdsa.PublicKey{
        "cordum-scheduler": schedulerPublicKey,
    },
    // ...
})
```

Jobs from unknown senders or with invalid signatures are silently dropped.

---

## 7. Testing

### Testing the Gateway Client

Use `roundTripFunc` to mock HTTP responses:

```go
func TestSubmitJob(t *testing.T) {
    c := &client.Client{
        BaseURL:  "http://example.test",
        APIKey:   "test-key",
        TenantID: "test-tenant",
        HTTPClient: &http.Client{
            Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
                // Verify request
                assert.Equal(t, "POST", req.Method)
                assert.Equal(t, "/api/v1/jobs", req.URL.Path)
                assert.Equal(t, "test-key", req.Header.Get("X-API-Key"))

                // Return mock response
                body := `{"job_id":"job-1","trace_id":"tr-1"}`
                return &http.Response{
                    StatusCode: 200,
                    Body:       io.NopCloser(strings.NewReader(body)),
                    Header:     make(http.Header),
                }, nil
            }),
        },
    }

    resp, err := c.SubmitJob(context.Background(), &client.JobSubmitRequest{
        Prompt: "test",
        Topic:  "job.test",
    })
    require.NoError(t, err)
    assert.Equal(t, "job-1", resp.JobID)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
    return f(r)
}
```

### Testing Workers with miniredis

For store-level tests, use [miniredis](https://github.com/alicebob/miniredis):

```go
import "github.com/alicebob/miniredis/v2"

func TestWorkerHandler(t *testing.T) {
    mr := miniredis.RunT(t)

    store, _ := runtime.NewRedisBlobStore("redis://" + mr.Addr())

    // Use store in your test...
}
```

### Integration Test Pattern

```go
func TestFullPipeline(t *testing.T) {
    if testing.Short() {
        t.Skip("integration test")
    }

    // 1. Start NATS (use nats-server -js in a container or test helper)
    // 2. Start Redis (use miniredis or a container)
    // 3. Create worker
    agent := &runtime.Agent{
        NATSURL:  natsURL,
        RedisURL: redisURL,
        SenderID: "test-worker",
    }
    runtime.Register(agent, "job.test.echo", echoHandler)
    agent.Start()
    defer agent.Close()

    // 4. Submit job via client
    c := client.New(gatewayURL, apiKey)
    resp, _ := c.SubmitJob(ctx, &client.JobSubmitRequest{
        Prompt: "hello",
        Topic:  "job.test.echo",
    })

    // 5. Poll for result
    for i := 0; i < 30; i++ {
        job, _ := c.GetJob(ctx, resp.JobID)
        if job["status"] == "succeeded" {
            break
        }
        time.Sleep(500 * time.Millisecond)
    }
}
```

---

## 8. Performance Tuning

### Concurrency

| Setting | Default | Recommendation |
|---------|---------|----------------|
| `MaxParallelJobs` | 1 | Set to CPU count for CPU-bound work; higher for I/O-bound |
| `HeartbeatEvery` | 10s (CAP default) | 5-10s for production; 30s for dev |

### Memory Management

- **Context pointers**: Jobs use pointer semantics (`context_ptr` / `result_ptr`) — payloads are stored in Redis, not sent over NATS. This keeps bus messages small.
- **Large payloads**: Use the artifact API (`PutArtifact` / `GetArtifact`) for files > 1MB rather than inline context.
- **Blob store TTL**: Context and result pointers expire based on `REDIS_DATA_TTL` (default 24h) and `JOB_META_TTL` (default 168h).

### Connection Tuning

```go
// Custom HTTP client with connection pooling
c := client.New(baseURL, apiKey)
c.HTTPClient = &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 20,
        IdleConnTimeout:     90 * time.Second,
    },
}
```

---

## 9. Environment Variables

| Variable | Used By | Description |
|----------|---------|-------------|
| `CORDUM_API_KEY` | Client | API key for gateway authentication |
| `CORDUM_TENANT_ID` | Client | Tenant ID header (default: `"default"`) |
| `NATS_URL` | Worker | NATS connection URL (default: `nats://127.0.0.1:4222`) |
| `REDIS_URL` | Worker | Redis connection URL for blob store |
| `WORKER_ID` | Worker | Explicit worker ID override |

---

## Related Docs

- [AGENT_PROTOCOL.md](AGENT_PROTOCOL.md) — CAP bus protocol and pointer semantics
- [api-reference.md](api-reference.md) — REST endpoint reference
- [configuration.md](configuration.md) — Environment variables and config files
- [SCHEDULER_POOL_SPEC.md](SCHEDULER_POOL_SPEC.md) — Pool routing specification
