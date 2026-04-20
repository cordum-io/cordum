# Testing Guide

Comprehensive testing strategies for Cordum components.

## Testing Philosophy

1. **Safety is paramount** - Safety Kernel requires 100% test coverage
2. **Table-driven tests** - Preferred for Go code
3. **Integration tests** - Verify component interactions
4. **Contract tests** - Ensure CAP protocol compliance
5. **Benchmarks** - Performance regressions caught early

## Deterministic Redeploy Workflow

A reproducible process for deploying the full Cordum stack and validating production readiness.

### Prerequisites

- Docker with Compose v2 plugin
- `curl`, Go toolchain, `openssl`
- `jq` (or `tools/scripts/jq.exe` on Windows/MSYS)
- `CORDUM_API_KEY` environment variable set (generate with `openssl rand -hex 32`)

### Quick Deploy

```bash
# Fresh clean deploy with artifact capture
export CORDUM_API_KEY=$(openssl rand -hex 32)
./tools/scripts/quickstart.sh --clean --artifacts-dir ./deploy-artifacts
```

### Quickstart Options

| Flag | Description |
|------|-------------|
| `--clean` | Tear down existing stack (`docker compose down -v`) before starting |
| `--artifacts-dir DIR` | Capture deploy logs, status, and image versions to DIR |
| `--skip-build` | Reuse existing images (skip rebuild) |
| `--skip-smoke` | Skip the post-deploy smoke test |
| `--health-timeout N` | Seconds to wait for health readiness (default: 120) |

### What the Deploy Does

1. **Teardown** (with `--clean`): `docker compose down -v --remove-orphans`
2. **Build & start**: `docker compose up -d --build` (7 services + dashboard)
3. **Health readiness**: Polls `GET /api/v1/status` until `nats.connected=true` and `redis.ok=true` (bounded timeout)
4. **Artifact capture** (with `--artifacts-dir`): Saves container status, per-service logs (last 200 lines), image versions, and git log
5. **Smoke test**: Creates a workflow, runs it, approves, verifies completion, cleans up

### Artifacts Captured

When `--artifacts-dir` is specified, the following files are written:

| File | Contents |
|------|----------|
| `compose-status-{timestamp}.txt` | Container names, status, ports |
| `{service}-{timestamp}.log` | Last 200 log lines per service |
| `compose-images-{timestamp}.txt` | Image names and versions |
| `git-log-{timestamp}.txt` | Last 5 git commits |

### Full Production Gate

After the deploy, run the 17-gate production readiness suite:

```bash
./tools/scripts/production_gate.sh                    # All 17 gates
./tools/scripts/production_gate.sh --gate 3           # Single gate
./tools/scripts/production_gate.sh --skip-rebuild     # Skip gate 1 teardown
```

Gates 1-17 cover: deploy, auth/tenant, workflows, policy, reliability, performance, security, MCP/output-policy, identity, data lifecycle, streaming, advanced workflows, config hierarchy, policy lifecycle, pack management, degradation, and dashboard.

Results are written to `production_gate_results.json`.

### Port Mapping

| Port | Protocol | Service |
|------|----------|---------|
| 8080 | gRPC | API Gateway gRPC endpoint |
| 8081 | HTTP | API Gateway REST API |
| 9092 | HTTP | API Gateway metrics |
| 4222 | TCP | NATS |
| 6379 | TCP | Redis |
| 50051 | gRPC | Safety Kernel |
| 50070 | gRPC | Context Engine |
| 9093 | gRPC | Workflow Engine |
| 8082 | HTTP | Dashboard (nginx proxy) |

### Production Gate Matrix

| Gate | Name | Coverage |
|------|------|----------|
| 1 | Deploy | Docker Compose build + health readiness |
| 2 | Auth/Tenant | API key auth, tenant isolation, CORS |
| 3 | Workflow Matrix | Multi-step workflow with policy evaluation |
| 4 | Policy | Safety rules, deny/allow/approval decisions |
| 5 | Reliability | Retry, timeout, circuit-breaker, reconciler |
| 6 | Performance | Latency p99 < 5ms, throughput benchmarks |
| 7 | Security | SSRF, injection, header hardening |
| 8 | Extensions | MCP server, output policy, context engine |
| 9 | Identity/Access | RBAC enforcement, key lifecycle, user CRUD |
| 10 | Data Lifecycle | DLQ, artifact storage, job cleanup |
| 11 | Streaming | WebSocket events, presence, reconnection |
| 12 | Adv Workflows | Multi-step, branching, failure handling |
| 13 | Config Hierarchy | System/tenant/team config cascade |
| 14 | Policy Lifecycle | Bundle CRUD, rule versioning, snapshots |
| 15 | Pack Management | Install, activate, overlay, uninstall |
| 16 | Degradation | Pool failover, orphan detection, recovery |
| 17 | Dashboard | SPA serving, CSP headers, proxy routing |

**CI runs**: Gates 2-4, 8-17 on PRs; all 17 nightly.

### Validation Evidence Checklist

After a production gate run, verify:

- [ ] `go build ./...` — clean (0 errors)
- [ ] `go vet ./...` — clean (0 warnings)
- [ ] `go test ./... -count=1` — all packages pass
- [ ] Docker images rebuilt from current source
- [ ] All 8 services healthy (`docker compose ps`)
- [ ] Gates 2-4, 8-17 pass (gate 3 requires policy config)
- [ ] Auth: login returns masked token, session returns user context
- [ ] Tenant isolation: cross-tenant requests get 403
- [ ] RBAC: viewer blocked from job submit (HTTP 403, gRPC PermissionDenied)
- [ ] Dashboard: serves SPA, has CSP + X-Frame-Options + X-Content-Type-Options

## Test Categories

### Unit Tests

Fast, isolated tests for individual functions.

```go
// core/safety/matcher_test.go
func TestMatcher_MatchCapabilities(t *testing.T) {
    tests := []struct {
        name         string
        ruleCapabs   []string
        jobCapabs    []string
        wantMatch    bool
    }{
        {
            name:       "exact match",
            ruleCapabs: []string{"read", "write"},
            jobCapabs:  []string{"read", "write"},
            wantMatch:  true,
        },
        {
            name:       "subset matches",
            ruleCapabs: []string{"read"},
            jobCapabs:  []string{"read", "write"},
            wantMatch:  true,
        },
        {
            name:       "no match",
            ruleCapabs: []string{"admin"},
            jobCapabs:  []string{"read"},
            wantMatch:  false,
        },
        {
            name:       "wildcard matches all",
            ruleCapabs: []string{"*"},
            jobCapabs:  []string{"anything"},
            wantMatch:  true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            m := NewMatcher()
            got := m.MatchCapabilities(tt.ruleCapabs, tt.jobCapabs)
            assert.Equal(t, tt.wantMatch, got)
        })
    }
}
```

### Integration Tests

Test component interactions with real dependencies.

```go
// core/scheduler/scheduler_integration_test.go
//go:build integration

func TestScheduler_FullFlow(t *testing.T) {
    // Setup infrastructure
    ctx := context.Background()
    infra := testutil.NewInfra(t)
    defer infra.Cleanup()
    
    // Create scheduler with real dependencies
    sched := NewScheduler(Config{
        Bus:          infra.NatsBus,
        Store:        infra.RedisStore,
        SafetyKernel: safety.New(testPolicy),
    })
    
    go sched.Run(ctx)
    defer sched.Shutdown()
    
    // Submit job
    job := &Job{
        ID:           "test-job-1",
        Type:         "echo",
        Capabilities: []string{"read"},
        RiskTags:     []string{"low"},
    }
    
    err := sched.Submit(ctx, job)
    require.NoError(t, err)
    
    // Wait for dispatch
    time.Sleep(100 * time.Millisecond)
    
    // Verify state
    stored, err := infra.RedisStore.GetJob(ctx, job.ID)
    require.NoError(t, err)
    assert.Equal(t, JobStatusDispatched, stored.Status)
}
```

### Contract Tests

Verify CAP protocol compliance.

```go
// core/protocol/contract_test.go
func TestBusPacket_CAP_Compliance(t *testing.T) {
    tests := []struct {
        name    string
        packet  *BusPacket
        wantErr bool
    }{
        {
            name: "valid job request",
            packet: &BusPacket{
                TraceId:   "trace-123",
                Timestamp: time.Now().UnixMilli(),
                Payload: &BusPacket_JobRequest{
                    JobRequest: &JobRequest{
                        JobId:      "job-123",
                        JobType:    "echo",
                        ContextPtr: "ctx:job:job-123",
                    },
                },
            },
            wantErr: false,
        },
        {
            name: "missing job_id is invalid",
            packet: &BusPacket{
                Payload: &BusPacket_JobRequest{
                    JobRequest: &JobRequest{
                        JobType: "echo",
                    },
                },
            },
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateCAP(tt.packet)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

### Benchmark Tests

Ensure performance requirements are met.

```go
// core/safety/kernel_bench_test.go
func BenchmarkKernel_Evaluate(b *testing.B) {
    kernel := NewKernel(loadBenchmarkPolicy())
    req := &EvaluateRequest{
        JobID:        "bench-job",
        Capabilities: []string{"read", "write"},
        RiskTags:     []string{"prod"},
        Metadata:     map[string]string{"source": "benchmark"},
    }
    ctx := context.Background()
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := kernel.Evaluate(ctx, req)
        if err != nil {
            b.Fatal(err)
        }
    }
}

// BenchmarkKernel_Evaluate-8   500000   2847 ns/op   512 B/op   8 allocs/op
// Target: < 5ms p99
```

### Fuzz Tests

Find edge cases in parsing logic.

```go
// core/safety/policy_fuzz_test.go
func FuzzPolicyParse(f *testing.F) {
    // Seed corpus
    f.Add([]byte(`{"version":"1","rules":[]}`))
    f.Add([]byte(`version: "1"
rules:
  - id: test
    match:
      capabilities: [read]
    decision: allow`))
    
    f.Fuzz(func(t *testing.T, data []byte) {
        // Should not panic
        _, _ = ParsePolicy(data)
    })
}
```

## Test Infrastructure

### Test Utilities

```go
// testutil/infra.go
type Infra struct {
    NatsBus    bus.Bus
    RedisStore store.Store
    NatsURL    string
    RedisURL   string
    t          *testing.T
}

func NewInfra(t *testing.T) *Infra {
    t.Helper()
    
    // Start containers if not running
    natsURL := getEnvOrDefault("TEST_NATS_URL", "nats://localhost:4222")
    redisURL := getEnvOrDefault("TEST_REDIS_URL", "redis://localhost:6379")
    
    // Create connections
    natsBus, err := bus.NewNats(natsURL)
    require.NoError(t, err)
    
    redisStore, err := store.NewRedis(redisURL)
    require.NoError(t, err)
    
    // Clean state
    redisStore.FlushAll(context.Background())
    
    return &Infra{
        NatsBus:    natsBus,
        RedisStore: redisStore,
        NatsURL:    natsURL,
        RedisURL:   redisURL,
        t:          t,
    }
}

func (i *Infra) Cleanup() {
    i.NatsBus.Close()
    i.RedisStore.Close()
}
```

### Mock Generation

```go
// Using mockgen
//go:generate mockgen -source=kernel.go -destination=mocks/kernel_mock.go -package=mocks

// Usage in tests
func TestScheduler_WithMockedKernel(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()
    
    mockKernel := mocks.NewMockKernel(ctrl)
    mockKernel.EXPECT().
        Evaluate(gomock.Any(), gomock.Any()).
        Return(&Decision{Type: DecisionAllow}, nil)
    
    sched := NewScheduler(Config{
        SafetyKernel: mockKernel,
    })
    
    // Test with mock...
}
```

### Test Fixtures

```go
// testutil/fixtures.go
func LoadTestPolicy(t *testing.T, name string) *Policy {
    t.Helper()
    
    path := filepath.Join("testdata", "policies", name+".yaml")
    data, err := os.ReadFile(path)
    require.NoError(t, err)
    
    policy, err := ParsePolicy(data)
    require.NoError(t, err)
    
    return policy
}

func LoadTestJob(t *testing.T, name string) *Job {
    t.Helper()
    
    path := filepath.Join("testdata", "jobs", name+".json")
    data, err := os.ReadFile(path)
    require.NoError(t, err)
    
    var job Job
    require.NoError(t, json.Unmarshal(data, &job))
    
    return &job
}
```

## Test Data

### Policy Test Data

```yaml
# testdata/policies/strict.yaml
version: "1"
rules:
  - id: deny-all-prod
    match:
      risk_tags: [prod]
    decision: deny
    reason: "Production access denied in test"
    
  - id: allow-read
    match:
      capabilities: [read]
    decision: allow
```

### Job Test Data

```json
// testdata/jobs/read_job.json
{
  "id": "test-read-job",
  "type": "data.read",
  "capabilities": ["read"],
  "risk_tags": ["low"],
  "metadata": {
    "source": "test"
  }
}
```

## Running Tests

### Quick Commands

```bash
# All unit tests
go test ./...

# Specific package
go test ./core/safety/...

# With coverage
go test -cover ./...

# Verbose
go test -v ./...

# Run specific test
go test -v -run TestKernel_Evaluate ./core/safety/...

# Integration tests
go test -tags=integration ./...

# Benchmarks
go test -bench=. ./core/safety/...

# Fuzz tests
go test -fuzz=FuzzPolicyParse ./core/safety/...
```

### Production Gate

Run the full release gate suite (deploy, auth/tenant, workflow matrix, policy, reliability, performance, security):

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh
```

Run a single gate for debugging:

```bash
CORDUM_API_KEY=${CORDUM_API_KEY:?set CORDUM_API_KEY} \
./tools/scripts/production_gate.sh --gate 3
```

The script writes `production_gate_results.json`, which can be consumed by CI artifacts and future dashboard System Health visualizations.

### CI Pipeline Tests

```yaml
# .github/workflows/test.yml
name: Tests
on: [push, pull_request]

jobs:
  unit:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go test -race -coverprofile=coverage.out ./...
      - uses: codecov/codecov-action@v3
        with:
          files: coverage.out

  integration:
    runs-on: ubuntu-latest
    services:
      nats:
        image: nats:latest
        ports:
          - 4222:4222
      redis:
        image: redis:7
        ports:
          - 6379:6379
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go test -tags=integration ./...
        env:
          TEST_NATS_URL: nats://localhost:4222
          TEST_REDIS_URL: redis://localhost:6379

  benchmark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - run: go test -bench=. -benchmem ./... | tee bench.txt
      - uses: benchmark-action/github-action-benchmark@v1
        with:
          tool: 'go'
          output-file-path: bench.txt
          fail-on-alert: true
          alert-threshold: '150%'
```

## Testing Best Practices

### Safety Kernel Tests

```go
// Every policy decision path MUST be tested
func TestKernel_AllDecisionPaths(t *testing.T) {
    tests := []struct {
        name     string
        policy   *Policy
        request  *EvaluateRequest
        wantType DecisionType
    }{
        // ALLOW path
        {"allow read-only", readPolicy, readRequest, DecisionAllow},
        
        // DENY path
        {"deny destructive", denyPolicy, deleteRequest, DecisionDeny},
        
        // REQUIRE_APPROVAL path
        {"approval for prod", approvalPolicy, prodRequest, DecisionRequireApproval},
        
        // THROTTLE path
        {"throttle high-volume", throttlePolicy, bulkRequest, DecisionThrottle},
        
        // Default deny when no match
        {"default deny", emptyPolicy, anyRequest, DecisionDeny},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            kernel := NewKernel(WithPolicy(tt.policy))
            decision, err := kernel.Evaluate(context.Background(), tt.request)
            require.NoError(t, err)
            assert.Equal(t, tt.wantType, decision.Type)
        })
    }
}
```

### Error Path Tests

```go
// Always test error conditions
func TestKernel_Evaluate_Errors(t *testing.T) {
    tests := []struct {
        name    string
        setup   func(*Kernel)
        request *EvaluateRequest
        wantErr error
    }{
        {
            name:    "nil request",
            request: nil,
            wantErr: ErrNilRequest,
        },
        {
            name: "missing job_id",
            request: &EvaluateRequest{
                Capabilities: []string{"read"},
            },
            wantErr: ErrMissingJobID,
        },
        {
            name: "policy store error",
            setup: func(k *Kernel) {
                k.policyStore = &failingStore{err: errors.New("db error")}
            },
            request: validRequest,
            wantErr: ErrPolicyStoreFailure,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            kernel := NewKernel()
            if tt.setup != nil {
                tt.setup(kernel)
            }
            
            _, err := kernel.Evaluate(context.Background(), tt.request)
            assert.ErrorIs(t, err, tt.wantErr)
        })
    }
}
```

### Race Condition Tests

```go
// Test concurrent access
func TestKernel_ConcurrentEvaluate(t *testing.T) {
    kernel := NewKernel(WithPolicy(testPolicy))
    ctx := context.Background()
    
    var wg sync.WaitGroup
    errCh := make(chan error, 100)
    
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            
            req := &EvaluateRequest{
                JobID:        fmt.Sprintf("job-%d", i),
                Capabilities: []string{"read"},
            }
            
            _, err := kernel.Evaluate(ctx, req)
            if err != nil {
                errCh <- err
            }
        }(i)
    }
    
    wg.Wait()
    close(errCh)
    
    for err := range errCh {
        t.Errorf("concurrent evaluation error: %v", err)
    }
}

// Run with race detector:
// go test -race -run TestKernel_ConcurrentEvaluate
```
