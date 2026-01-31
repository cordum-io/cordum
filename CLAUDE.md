# Cordum - Claude CLI Configuration

## Project Overview

Cordum is an **AI Agent Governance Platform** - a control plane for autonomous AI agent orchestration with built-in safety, observability, and policy enforcement.

**Core Value Proposition:** "Policy-before-dispatch" - every agent action passes through a Safety Kernel before execution, providing deterministic safety guarantees rather than relying on probabilistic LLM behavior.

## Architecture

```
Client → API Gateway → Scheduler → Safety Kernel → NATS → Worker Pools
                          ↓              ↓
                    [Redis State]   [Policy Engine]
```

### Core Services (3 binaries)
- `cordum-api` - HTTP/WebSocket + gRPC gateway (port 8080, metrics 9092)
- `cordum-scheduler` - Job routing, safety checks, state machine (metrics 9090)
- `cordum-context` - Optional context/memory service (metrics 9093)

### Key Components
| Component | Location | Purpose |
|-----------|----------|---------|
| Safety Kernel | `core/safety/` | Policy enforcement engine |
| Workflow Engine | `core/workflow/` | DAG orchestration, retries, fan-out |
| Scheduler | `core/scheduler/` | Job routing, state management |
| Protocol | `core/protocol/` | CAP v2 types and API protos |
| SDK | `sdk/` | Go SDK + worker runtime |
| Dashboard | `dashboard/` | React UI |

## Technology Stack

| Technology | Version | Purpose |
|------------|---------|---------|
| Go | 1.24+ | Core services |
| NATS JetStream | Latest | Message bus (at-least-once delivery) |
| Redis | 7+ | State store, payload pointers |
| Protocol Buffers | v3 | CAP wire format |
| React | 18+ | Dashboard UI |
| TypeScript | 5+ | Dashboard |
| Docker Compose | v2 | Local development |

## Directory Structure

```
cordum/
├── cmd/                      # Service entrypoints
│   ├── cordum-api/           # API gateway main.go
│   ├── cordum-scheduler/     # Scheduler main.go
│   └── cordum-context/       # Context service main.go
├── core/                     # Shared libraries
│   ├── safety/               # Safety Kernel
│   │   ├── kernel.go         # Core policy engine
│   │   ├── policy.go         # Policy parsing/loading
│   │   ├── evaluate.go       # Policy evaluation
│   │   └── decision.go       # Decision types (allow/deny/approve/throttle)
│   ├── workflow/             # Workflow engine
│   │   ├── engine.go         # DAG execution
│   │   ├── run.go            # Workflow runs
│   │   ├── step.go           # Step types
│   │   └── state.go          # Redis state management
│   ├── scheduler/            # Job scheduler
│   │   ├── scheduler.go      # Core scheduler
│   │   ├── router.go         # Pool-based routing
│   │   ├── reconciler.go     # Timeout/retry handling
│   │   └── state.go          # Job state machine
│   ├── protocol/             # Protocol definitions
│   │   ├── pb/v1/            # Generated Go types
│   │   └── proto/v1/         # Proto source files
│   ├── bus/                  # NATS abstraction
│   ├── store/                # Redis abstraction
│   └── config/               # Configuration loading
├── dashboard/                # React frontend
│   ├── src/
│   │   ├── components/       # UI components
│   │   ├── pages/            # Route pages
│   │   ├── hooks/            # React hooks
│   │   └── api/              # API client
│   └── package.json
├── sdk/                      # Public SDK
│   ├── runtime/              # Worker runtime
│   ├── client/               # Gateway client
│   └── gen/go/cordum/v1/     # Generated SDK types
├── config/                   # Default configurations
├── deploy/k8s/               # Kubernetes manifests
├── docs/                     # Documentation
└── tools/scripts/            # Operational scripts
```

## Coding Standards

### Go Code Style

```go
// Package comments required
// Package safety implements the Safety Kernel for policy enforcement.
package safety

// Use descriptive error wrapping
if err != nil {
    return fmt.Errorf("evaluate policy %s: %w", policy.ID, err)
}

// Context propagation required for all operations
func (k *Kernel) Evaluate(ctx context.Context, req *EvaluateRequest) (*Decision, error)

// Interface-first design for testability
type PolicyStore interface {
    Get(ctx context.Context, id string) (*Policy, error)
    List(ctx context.Context) ([]*Policy, error)
}

// Use structured logging
slog.Info("job dispatched",
    "job_id", job.ID,
    "pool", pool.Name,
    "worker", worker.ID,
)
```

### Error Handling Patterns

```go
// Domain errors in core packages
var (
    ErrPolicyNotFound    = errors.New("policy not found")
    ErrJobAlreadyExists  = errors.New("job already exists")
    ErrApprovalRequired  = errors.New("approval required")
)

// Wrap with context
return fmt.Errorf("scheduler.dispatch: %w", err)
```

### Testing Patterns

```go
// Table-driven tests preferred
func TestKernel_Evaluate(t *testing.T) {
    tests := []struct {
        name     string
        policy   *Policy
        request  *EvaluateRequest
        want     DecisionType
        wantErr  bool
    }{
        {
            name:    "allow read-only",
            policy:  readOnlyPolicy,
            request: readRequest,
            want:    DecisionAllow,
        },
        // ...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}

// Use testify for assertions
assert.Equal(t, expected, actual)
require.NoError(t, err)
```

### Protocol Buffer Conventions

```protobuf
// All messages in core/protocol/proto/v1/
syntax = "proto3";
package cordum.v1;

// Use clear field names
message JobRequest {
  string job_id = 1;
  string context_ptr = 2;      // Pointer to Redis/S3, not payload
  repeated string capabilities = 3;
  repeated string risk_tags = 4;
  map<string, string> metadata = 5;
}
```

## Key Patterns

### 1. Safety Kernel Integration

Every job MUST pass through the Safety Kernel:

```go
// In scheduler
decision, err := s.safetyKernel.Evaluate(ctx, &safety.EvaluateRequest{
    JobID:        job.ID,
    Capabilities: job.Capabilities,
    RiskTags:     job.RiskTags,
    Metadata:     job.Metadata,
})

switch decision.Type {
case safety.DecisionAllow:
    return s.dispatch(ctx, job)
case safety.DecisionDeny:
    return s.reject(ctx, job, decision.Reason)
case safety.DecisionRequireApproval:
    return s.queueForApproval(ctx, job, decision.Reason)
case safety.DecisionThrottle:
    return s.throttle(ctx, job, decision.Duration)
}
```

### 2. Pointer Architecture

Never put large payloads on the bus:

```go
// Store payload, get pointer
contextPtr, err := s.store.SetContext(ctx, job.ID, payload)

// Send only pointer on bus
req := &pb.JobRequest{
    JobId:      job.ID,
    ContextPtr: contextPtr,  // "ctx:job:abc123" -> Redis key
}
```

### 3. State Machine

Job states are explicit and transition-controlled:

```go
type JobStatus int

const (
    JobStatusPending JobStatus = iota
    JobStatusDispatched
    JobStatusRunning
    JobStatusSucceeded
    JobStatusFailed
    JobStatusCancelled
)

// Valid transitions
var validTransitions = map[JobStatus][]JobStatus{
    JobStatusPending:    {JobStatusDispatched, JobStatusCancelled},
    JobStatusDispatched: {JobStatusRunning, JobStatusFailed},
    JobStatusRunning:    {JobStatusSucceeded, JobStatusFailed},
}
```

### 4. Workflow DAG Execution

```go
// Workflow with fan-out
workflow := &Workflow{
    ID: "process-data",
    Steps: []Step{
        {ID: "fetch", Type: StepTypeJob, JobType: "fetch.data"},
        {ID: "process", Type: StepTypeFanOut, 
         FanOut: &FanOutConfig{
             Source: "fetch.items",
             JobType: "process.item",
         }},
        {ID: "aggregate", Type: StepTypeJob, JobType: "aggregate.results",
         DependsOn: []string{"process"}},
    },
}
```

## Common Tasks

### Adding a New API Endpoint

1. Define proto in `core/protocol/proto/v1/api.proto`
2. Run `make proto` to regenerate
3. Implement handler in `cmd/cordum-api/handlers/`
4. Add route in `cmd/cordum-api/routes.go`
5. Add tests in `cmd/cordum-api/handlers/*_test.go`

### Adding a New Policy Rule Type

1. Define rule struct in `core/safety/rules.go`
2. Add matcher in `core/safety/matcher.go`
3. Update policy parser in `core/safety/policy.go`
4. Add tests in `core/safety/*_test.go`
5. Document in `docs/policies.md`

### Creating a New Worker Pack

1. Create pack directory in external repo
2. Implement CAP worker interface
3. Define capabilities and risk tags
4. Add pack manifest (`pack.yaml`)
5. Test with `cordumctl pack install <pack>`

## Build & Test Commands

```bash
# Build all services
make build

# Build specific service
make build SERVICE=cordum-scheduler

# Run all tests
GOCACHE=$(pwd)/.cache/go-build go test ./...

# Run integration tests (requires Docker)
make test-integration

# Regenerate protos
make proto

# Local development
docker compose up -d

# Smoke tests
make smoke
./tools/scripts/platform_smoke.sh
./tools/scripts/cordumctl_smoke.sh

# Build Docker image
make docker SERVICE=cordum-scheduler
```

## Environment Variables

```bash
# Core
CORDUM_API_KEY=your-api-key
CORDUM_LOG_LEVEL=info|debug|warn|error

# NATS
NATS_URL=nats://localhost:4222

# Redis
REDIS_URL=redis://localhost:6379

# API Gateway
API_PORT=8080
API_METRICS_PORT=9092

# Scheduler
SCHEDULER_METRICS_PORT=9090
SCHEDULER_RECONCILE_INTERVAL=30s

# Safety Kernel
SAFETY_POLICY_PATH=/etc/cordum/policies/
SAFETY_DEFAULT_DECISION=deny
```

## Performance Targets

| Metric | Target |
|--------|--------|
| Policy evaluation | < 5ms p99 |
| Job dispatch | < 10ms p99 |
| Event throughput | 10k+/sec/node |
| API response | < 50ms p99 |

## Debugging

### Common Issues

1. **Job stuck in PENDING**
   - Check Safety Kernel logs for policy evaluation
   - Verify worker pool has capacity (heartbeats)
   - Check NATS connectivity

2. **Policy not matching**
   - Use `cordumctl policy simulate` to test
   - Check policy file syntax
   - Verify risk_tags/capabilities in job request

3. **Worker not receiving jobs**
   - Verify NATS subscription on correct subject
   - Check queue group membership
   - Verify capabilities match job requirements

### Useful Commands

```bash
# Check Redis state
docker compose exec redis redis-cli KEYS "job:*"

# Watch NATS subjects
nats sub "job.>" --server=nats://localhost:4222

# View scheduler metrics
curl http://localhost:9090/metrics

# Flush all state (dev only!)
docker compose exec redis redis-cli FLUSHALL
```

## Documentation References

- `docs/README.md` - Documentation index
- `docs/system_overview.md` - Architecture deep dive
- `docs/CORE.MD` - Technical reference
- `docs/AGENT_PROTOCOL.md` - CAP protocol spec
- `docs/pack.md` - Pack development guide
- `docs/LOCAL_E2E.md` - Local testing walkthrough

## Important Constraints

1. **Never bypass Safety Kernel** - All jobs must be evaluated
2. **Pointer architecture** - No large payloads on NATS
3. **Idempotent operations** - Jobs may be retried
4. **Context propagation** - Always pass context.Context
5. **Structured logging** - Use slog with key-value pairs
6. **CAP v2 compliance** - Wire format must match spec
