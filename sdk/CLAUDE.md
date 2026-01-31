# SDK - Claude CLI Configuration

The Cordum SDK enables building CAP-compliant workers in Go.

## Module Structure

```
sdk/
├── runtime/              # Worker runtime
│   ├── worker.go         # Worker lifecycle
│   ├── handler.go        # Job handler interface
│   ├── heartbeat.go      # Heartbeat management
│   └── config.go         # Worker configuration
├── client/               # Gateway client
│   ├── client.go         # HTTP/gRPC client
│   ├── jobs.go           # Job API
│   ├── workflows.go      # Workflow API
│   └── auth.go           # Authentication
├── gen/go/cordum/v1/     # Generated protobuf types
│   ├── bus.pb.go
│   ├── api.pb.go
│   └── types.pb.go
└── examples/             # Example workers
    ├── echo/
    ├── slack/
    └── github/
```

## Worker Development

### Minimal Worker Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/cordum/cordum/sdk/runtime"
)

func main() {
    // Create worker
    worker := runtime.NewWorker(runtime.Config{
        Pool:         "my-workers",
        Capabilities: []string{"echo", "greet"},
        NatsURL:      "nats://localhost:4222",
        RedisURL:     "redis://localhost:6379",
    })
    
    // Register handlers
    worker.Handle("echo", handleEcho)
    worker.Handle("greet", handleGreet)
    
    // Run (blocks until shutdown)
    if err := worker.Run(context.Background()); err != nil {
        log.Fatal(err)
    }
}

func handleEcho(ctx runtime.JobContext) error {
    // Get input from context pointer
    input, err := ctx.GetInput()
    if err != nil {
        return fmt.Errorf("get input: %w", err)
    }
    
    // Return same data as output
    return ctx.Succeed(input)
}

func handleGreet(ctx runtime.JobContext) error {
    var req struct {
        Name string `json:"name"`
    }
    
    if err := ctx.DecodeInput(&req); err != nil {
        return ctx.Fail("invalid input: " + err.Error())
    }
    
    result := map[string]string{
        "message": fmt.Sprintf("Hello, %s!", req.Name),
    }
    
    return ctx.Succeed(result)
}
```

### Job Context API

```go
type JobContext interface {
    // Context returns the underlying context.Context
    Context() context.Context
    
    // Job returns the job request
    Job() *Job
    
    // Input handling
    GetInput() ([]byte, error)
    DecodeInput(v interface{}) error
    
    // Result handling  
    Succeed(result interface{}) error
    Fail(reason string) error
    
    // Progress updates
    UpdateProgress(percent int, message string) error
    
    // Logging
    Log() *slog.Logger
    
    // Metadata
    GetMetadata(key string) (string, bool)
}
```

### Worker Lifecycle

```
┌─────────────┐
│   Start     │
└──────┬──────┘
       ▼
┌─────────────┐     ┌─────────────┐
│  Register   │────▶│  Heartbeat  │◀──┐
│  Handlers   │     │   Loop      │   │
└──────┬──────┘     └─────────────┘   │
       ▼                              │
┌─────────────┐                       │
│  Subscribe  │                       │
│  to Jobs    │                       │
└──────┬──────┘                       │
       ▼                              │
┌─────────────┐                       │
│  Process    │───────────────────────┘
│  Jobs       │
└──────┬──────┘
       ▼
┌─────────────┐
│  Shutdown   │
└─────────────┘
```

### Heartbeat Configuration

```go
worker := runtime.NewWorker(runtime.Config{
    // ...
    HeartbeatInterval: 10 * time.Second,
    HeartbeatTimeout:  30 * time.Second,
    MaxConcurrency:    10,  // Max parallel jobs
})
```

Heartbeat message:
```json
{
  "worker_id": "worker-abc123",
  "pool": "my-workers",
  "capabilities": ["echo", "greet"],
  "current_load": 3,
  "max_capacity": 10,
  "timestamp": "2024-01-15T10:30:00Z"
}
```

## Gateway Client

### Creating a Client

```go
client, err := client.New(client.Config{
    BaseURL: "http://localhost:8080",
    APIKey:  os.Getenv("CORDUM_API_KEY"),
})
```

### Submitting Jobs

```go
// Submit a job
job, err := client.Jobs.Submit(ctx, &client.SubmitJobRequest{
    Type:         "greet",
    Input:        map[string]string{"name": "World"},
    Capabilities: []string{"greet"},
    RiskTags:     []string{"read"},
    Metadata: map[string]string{
        "source": "my-app",
    },
})

// Wait for result
result, err := client.Jobs.Wait(ctx, job.ID, 30*time.Second)
```

### Managing Workflows

```go
// Create workflow
workflow, err := client.Workflows.Create(ctx, &client.CreateWorkflowRequest{
    Name: "data-pipeline",
    Steps: []client.Step{
        {ID: "fetch", Type: "job", JobType: "fetch.data"},
        {ID: "process", Type: "job", JobType: "process.data", DependsOn: []string{"fetch"}},
    },
})

// Start a run
run, err := client.Workflows.StartRun(ctx, workflow.ID, map[string]interface{}{
    "source": "s3://bucket/path",
})

// Get run status
status, err := client.Workflows.GetRun(ctx, run.ID)
```

### Approvals

```go
// List pending approvals
approvals, err := client.Approvals.List(ctx, &client.ListApprovalsRequest{
    Status: "pending",
})

// Approve a job
err = client.Approvals.Approve(ctx, jobID, &client.ApprovalRequest{
    Comment: "Approved by automation",
})

// Reject a job
err = client.Approvals.Reject(ctx, jobID, &client.ApprovalRequest{
    Comment: "Rejected: missing required metadata",
})
```

## Testing Workers

### Unit Testing Handlers

```go
func TestHandleGreet(t *testing.T) {
    // Create mock context
    ctx := runtime.NewMockJobContext(t, runtime.MockJobContextConfig{
        Input: []byte(`{"name": "Test"}`),
    })
    
    // Call handler
    err := handleGreet(ctx)
    require.NoError(t, err)
    
    // Verify result
    assert.True(t, ctx.Succeeded())
    
    var result map[string]string
    ctx.DecodeResult(&result)
    assert.Equal(t, "Hello, Test!", result["message"])
}
```

### Integration Testing

```go
func TestWorkerIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }
    
    // Start test infrastructure
    infra := testutil.StartInfra(t)
    defer infra.Stop()
    
    // Create and start worker
    worker := runtime.NewWorker(runtime.Config{
        Pool:    "test-pool",
        NatsURL: infra.NatsURL,
        RedisURL: infra.RedisURL,
    })
    worker.Handle("echo", handleEcho)
    
    go worker.Run(context.Background())
    defer worker.Shutdown()
    
    // Submit job via client
    client := client.New(client.Config{
        BaseURL: infra.APIURL,
        APIKey:  "test-key",
    })
    
    job, err := client.Jobs.Submit(ctx, &client.SubmitJobRequest{
        Type:  "echo",
        Input: map[string]string{"foo": "bar"},
    })
    require.NoError(t, err)
    
    // Wait and verify
    result, err := client.Jobs.Wait(ctx, job.ID, 10*time.Second)
    require.NoError(t, err)
    assert.Equal(t, "succeeded", result.Status)
}
```

## MCP Integration

Workers can use MCP to call external tools:

```go
import "github.com/mark3labs/mcp-go/client"

func handleWithMCP(ctx runtime.JobContext) error {
    // Create MCP client
    mcpClient, err := client.NewStdioMCPClient(
        "npx", []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"},
    )
    if err != nil {
        return fmt.Errorf("create mcp client: %w", err)
    }
    defer mcpClient.Close()
    
    // Initialize
    if err := mcpClient.Initialize(ctx.Context()); err != nil {
        return fmt.Errorf("mcp initialize: %w", err)
    }
    
    // Call tool
    result, err := mcpClient.CallTool(ctx.Context(), "read_file", map[string]interface{}{
        "path": "/data/input.txt",
    })
    if err != nil {
        return ctx.Fail("mcp call failed: " + err.Error())
    }
    
    return ctx.Succeed(result)
}
```

## Error Handling

```go
// Retriable errors (will be retried per policy)
func handleJob(ctx runtime.JobContext) error {
    if err := doSomething(); err != nil {
        // Return error - job will be retried
        return fmt.Errorf("temporary failure: %w", err)
    }
    return ctx.Succeed(nil)
}

// Permanent failures (no retry)
func handleJob(ctx runtime.JobContext) error {
    if !isValid(input) {
        // Use Fail() - job moves to failed state immediately
        return ctx.Fail("invalid input: missing required field")
    }
    return ctx.Succeed(result)
}
```

## Best Practices

1. **Idempotency** - Handlers may be called multiple times
2. **Timeouts** - Respect context cancellation
3. **Logging** - Use ctx.Log() for structured logging
4. **Metrics** - Export Prometheus metrics for observability
5. **Graceful Shutdown** - Handle SIGTERM properly
6. **Error Classification** - Distinguish retriable vs permanent failures
