# Core Libraries - Claude CLI Configuration

This directory contains the shared libraries that power Cordum's control plane.

## Module Structure

```
core/
├── safety/      # Safety Kernel - CRITICAL COMPONENT
├── workflow/    # Workflow Engine - DAG orchestration
├── scheduler/   # Job Scheduler - routing and state
├── protocol/    # CAP protocol types
├── bus/         # NATS abstraction layer
├── store/       # Redis abstraction layer
└── config/      # Configuration loading
```

## Safety Kernel (`core/safety/`)

### Purpose
The Safety Kernel is the **most critical component** of Cordum. It enforces policy-before-dispatch, ensuring no agent action executes without explicit policy approval.

### Key Files
- `kernel.go` - Main Safety Kernel struct and lifecycle
- `policy.go` - Policy loading, parsing, validation
- `evaluate.go` - Policy evaluation engine
- `matcher.go` - Rule matching logic
- `decision.go` - Decision types and reasons

### Decision Types
```go
const (
    DecisionAllow          DecisionType = "allow"
    DecisionDeny           DecisionType = "deny"
    DecisionRequireApproval DecisionType = "require_approval"
    DecisionThrottle       DecisionType = "throttle"
)
```

### Policy Structure
```yaml
version: "1"
rules:
  - id: unique-rule-id
    match:
      capabilities: [list, of, caps]
      risk_tags: [list, of, tags]
      metadata:
        key: value
    decision: allow|deny|require_approval|throttle
    reason: "Human-readable reason"
    throttle_duration: 5m  # if decision is throttle
```

### Key Interfaces
```go
type Kernel interface {
    Evaluate(ctx context.Context, req *EvaluateRequest) (*Decision, error)
    Explain(ctx context.Context, req *EvaluateRequest) (*Explanation, error)
    Simulate(ctx context.Context, policy *Policy, req *EvaluateRequest) (*Decision, error)
    Reload(ctx context.Context) error
}

type PolicyStore interface {
    Get(ctx context.Context, id string) (*Policy, error)
    List(ctx context.Context) ([]*Policy, error)
    Watch(ctx context.Context) (<-chan PolicyEvent, error)
}
```

### Performance Requirements
- Evaluation MUST complete in < 5ms p99
- No external I/O during hot path evaluation
- Policies cached in memory, reloaded on change

### Testing Requirements
- 100% coverage on matcher logic
- Fuzz testing on policy parsing
- Benchmark tests for evaluation latency

---

## Workflow Engine (`core/workflow/`)

### Purpose
Orchestrates multi-step DAGs with fan-out, retries, approvals, and conditions.

### Key Files
- `engine.go` - DAG execution engine
- `workflow.go` - Workflow definition types
- `run.go` - Workflow run management
- `step.go` - Step types and execution
- `state.go` - Redis-backed state persistence
- `fanout.go` - Fan-out/fan-in logic
- `retry.go` - Retry with backoff

### Step Types
```go
const (
    StepTypeJob       StepType = "job"       // Single job execution
    StepTypeFanOut    StepType = "fan_out"   // Parallel expansion
    StepTypeCondition StepType = "condition" // Conditional branching
    StepTypeDelay     StepType = "delay"     // Time delay
    StepTypeApproval  StepType = "approval"  // Human gate
    StepTypeNotify    StepType = "notify"    // Notification
)
```

### Workflow Definition
```go
type Workflow struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Steps       []Step            `json:"steps"`
    Timeout     time.Duration     `json:"timeout"`
    RetryPolicy *RetryPolicy      `json:"retry_policy"`
    Metadata    map[string]string `json:"metadata"`
}

type Step struct {
    ID        string   `json:"id"`
    Type      StepType `json:"type"`
    DependsOn []string `json:"depends_on"`
    // Type-specific config
    Job       *JobConfig       `json:"job,omitempty"`
    FanOut    *FanOutConfig    `json:"fan_out,omitempty"`
    Condition *ConditionConfig `json:"condition,omitempty"`
}
```

### State Machine
```
RUN_PENDING → RUN_RUNNING → RUN_SUCCEEDED
                    ↓
              RUN_FAILED
                    ↓
              (retry logic)
```

### Key Patterns
1. **Idempotent execution** - Steps may run multiple times
2. **Crash recovery** - All state in Redis, resumable
3. **Fan-out aggregation** - Collect results from parallel jobs
4. **Timeline tracking** - Full audit of step transitions

---

## Scheduler (`core/scheduler/`)

### Purpose
Routes jobs to worker pools, manages job state, handles reconciliation.

### Key Files
- `scheduler.go` - Core scheduler logic
- `router.go` - Capability-based routing
- `state.go` - Job state machine
- `reconciler.go` - Timeout and retry handling
- `pool.go` - Worker pool management
- `heartbeat.go` - Worker liveness tracking

### Job State Machine
```go
const (
    JobPending    JobStatus = "pending"
    JobDispatched JobStatus = "dispatched"
    JobRunning    JobStatus = "running"
    JobSucceeded  JobStatus = "succeeded"
    JobFailed     JobStatus = "failed"
    JobCancelled  JobStatus = "cancelled"
)
```

### Routing Logic
```go
// Route by capabilities and tags
func (r *Router) Route(job *Job) (*Pool, error) {
    for _, pool := range r.pools {
        if pool.HasCapabilities(job.RequiredCapabilities) &&
           pool.MatchesTags(job.RoutingTags) &&
           pool.HasCapacity() {
            return pool, nil
        }
    }
    return nil, ErrNoMatchingPool
}
```

### Reconciliation
- Runs every 30s (configurable)
- Detects timed-out jobs
- Triggers retries per retry policy
- Moves dead jobs to DLQ

---

## Protocol (`core/protocol/`)

### Purpose
CAP v2 protocol types and API definitions.

### Structure
```
protocol/
├── pb/v1/           # Generated Go types
│   ├── bus.pb.go    # BusPacket, JobRequest, JobResult
│   ├── api.pb.go    # API service definitions
│   └── types.pb.go  # Common types
└── proto/v1/        # Proto source files
    ├── bus.proto    # Wire protocol
    ├── api.proto    # HTTP/gRPC API
    └── types.proto  # Shared types
```

### Key Messages
```protobuf
message BusPacket {
  string trace_id = 1;
  int64 timestamp = 2;
  oneof payload {
    JobRequest job_request = 10;
    JobResult job_result = 11;
    Heartbeat heartbeat = 12;
  }
}

message JobRequest {
  string job_id = 1;
  string job_type = 2;
  string context_ptr = 3;     // Pointer, not payload!
  repeated string capabilities = 4;
  repeated string risk_tags = 5;
  string workflow_id = 6;
  string parent_job_id = 7;
  int32 step_index = 8;
}
```

### Regenerating Protos
```bash
make proto
# Runs: protoc --go_out=. --go-grpc_out=. proto/v1/*.proto
```

---

## Bus (`core/bus/`)

### Purpose
NATS JetStream abstraction for pub/sub and queues.

### Key Interfaces
```go
type Bus interface {
    Publish(ctx context.Context, subject string, msg *BusPacket) error
    Subscribe(ctx context.Context, subject string, handler Handler) (Subscription, error)
    QueueSubscribe(ctx context.Context, subject, queue string, handler Handler) (Subscription, error)
}

type Handler func(ctx context.Context, msg *BusPacket) error
```

### Subject Naming
```
sys.job.submit         # Job submission
sys.job.result         # Job results
sys.heartbeat          # Worker heartbeats
job.<type>.<subtype>   # Job dispatch by type
pool.<name>.jobs       # Pool-specific dispatch
```

---

## Store (`core/store/`)

### Purpose
Redis abstraction for state and payload management.

### Key Interfaces
```go
type Store interface {
    // Job state
    GetJob(ctx context.Context, id string) (*Job, error)
    SetJob(ctx context.Context, job *Job) error
    
    // Payload pointers
    SetContext(ctx context.Context, jobID string, payload []byte) (string, error)
    GetContext(ctx context.Context, ptr string) ([]byte, error)
    SetResult(ctx context.Context, jobID string, payload []byte) (string, error)
    GetResult(ctx context.Context, ptr string) ([]byte, error)
}
```

### Key Patterns
```go
// Pointer format: "ctx:job:<job_id>" or "res:job:<job_id>"
func (s *RedisStore) SetContext(ctx context.Context, jobID string, payload []byte) (string, error) {
    key := fmt.Sprintf("ctx:job:%s", jobID)
    err := s.client.Set(ctx, key, payload, s.ttl).Err()
    return key, err
}
```

---

## Common Patterns Across Core

### Context Propagation
```go
// Always accept and propagate context
func DoSomething(ctx context.Context, ...) error {
    // Check for cancellation
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
    }
    // Continue...
}
```

### Error Wrapping
```go
// Wrap errors with context
if err != nil {
    return fmt.Errorf("safety.evaluate job %s: %w", jobID, err)
}
```

### Logging
```go
// Structured logging with slog
slog.InfoContext(ctx, "job dispatched",
    "job_id", job.ID,
    "pool", pool.Name,
    "capabilities", job.Capabilities,
)
```

### Metrics
```go
// Prometheus metrics
var (
    jobsDispatched = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "cordum_jobs_dispatched_total",
            Help: "Total jobs dispatched",
        },
        []string{"pool", "job_type"},
    )
)
```
