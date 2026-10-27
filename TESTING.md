# Testing Guide

Comprehensive testing strategies for Cordum components.

The Edge P0 security closure checklist used for final acceptance lives in
the internal Edge P0 threat model (Cordum engineering).

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

Integration-tagged tests (`//go:build integration`) exercise several components
together in-process. They need no Docker and no external services:

```bash
make test-integration   # go test -tags=integration ./...
```

They use the same in-process Redis, NATS, and HTTP doubles as the unit tests
(see [Test Utilities](#test-utilities)). The full stack (Compose with TLS NATS
and Redis, the platform smoke test, and the production gates) runs in the
nightly workflow, `.github/workflows/integration-nightly.yml`, and locally
with `./tools/scripts/quickstart.sh` and `./tools/scripts/production_gate.sh`.

### Edge backend integration tests

These are package-level P0 Edge regressions. They do **not** require Docker,
real Redis, a real Safety Kernel binary, Claude Code, or external network:
Gateway tests use `httptest` plus stubbed Safety Kernel clients, and store tests
use `miniredis`.

Run the standard Edge backend gates from the repo root:

```bash
# Core Edge models, Redis store, approval store, export, agentd, and hook clients.
go test -count=1 ./core/edge/...

# Gateway Edge routes/evaluate/events/approvals/export/stream regressions.
go test -count=1 ./core/controlplane/gateway -run 'Test.*Edge'

# Targeted repeat suite for auth/tenant/limits/redaction/outages/stream/approvals/export.
go test -count=3 ./core/controlplane/gateway -run 'Test.*Edge.*(Auth|Tenant|Limit|Redact|Unavailable|Stream|Approval|Export)'
```

Expected local duration on the Windows/MSYS dev box is roughly 15-30 seconds
for the first two commands and 10-20 seconds for the repeat suite. `go test
-race` is not available when CGO is disabled on this platform; use `-count=3`
as the flake-detection pass.

For full-core local sweeps on Windows/MSYS shared runners, default Go package
parallelism can exhaust localhost ports when many packages start miniredis or
httptest listeners at once. If `go test ./core/... -count=1` fails only with
Windows `connectex` / miniredis / httptest port-exhaustion errors, preserve the
failure excerpt and use the serialized full-core gate instead:

```bash
go test -p 1 ./core/... -count=1
```

Focused package tests should still run normally without `-p 1`.

Only tests that genuinely need a running stack should use integration tags:

```bash
CORDUM_INTEGRATION=1 go test -tags=integration -count=1 ./core/...
```

When `CORDUM_INTEGRATION` is unset, integration-tagged tests or scripts must
skip cleanly with a documented reason. When it is `1`, missing prerequisites
(`docker`, live Gateway/Safety Kernel, real Redis/NATS, etc.) are a hard fail.
For script-level Edge coverage against a live stack, see
[docs/LOCAL_E2E.md#edge-fake-hook-e2e](docs/LOCAL_E2E.md#edge-fake-hook-e2e).

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

For repeatable performance sweeps, targeted before/after comparisons, and
pprof artifact cleanup, use [BENCHMARKS.md](BENCHMARKS.md#9-benchmark-reproducibility)
as the canonical workflow.

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

Tests run real protocol implementations in-process instead of shared
containers, so `go test ./...` needs no services:

- **Redis**: [miniredis](https://github.com/alicebob/miniredis).
  `miniredis.RunT(t)` starts a server that stops when the test ends; connect a
  client to `srv.Addr()`.
- **NATS**: an embedded `nats-server` (`github.com/nats-io/nats-server/v2/server`)
  on a random port, with JetStream stored in `t.TempDir()` when the test needs it.
- **HTTP**: `net/http/httptest`. Use `httptest.NewRecorder()` to call handlers
  directly, and `httptest.NewServer` or `httptest.NewTLSServer` for clients.

The embedded NATS pattern, from `core/infra/bus/nats_test.go`:

```go
func startTestNATSServer(t *testing.T, enableJS bool) *natsserver.Server {
    t.Helper()
    opts := &natsserver.Options{Port: -1, NoLog: true, NoSigs: true}
    if enableJS {
        opts.JetStream = true
        opts.StoreDir = t.TempDir()
    }
    ns, err := natsserver.NewServer(opts)
    if err != nil {
        t.Fatalf("start test nats: %v", err)
    }
    go ns.Start()
    if !ns.ReadyForConnections(5 * time.Second) {
        t.Fatal("nats server not ready")
    }
    t.Cleanup(ns.Shutdown)
    return ns
}
```

Connect with `nats.Connect(ns.ClientURL())`.

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

`.github/workflows/ci.yml` runs on pushes to `main` and `develop` and on pull
requests into them:

- `test`: `go test -v -race -coverprofile=coverage.out -covermode=atomic ./...`,
  then the benchmarks.
- `integration-tests`: `go test -v -tags=integration -timeout 10m ./...`, with
  no service containers.

`.github/workflows/integration-nightly.yml` generates TLS certificates, starts
the Compose stack, and runs the platform smoke test, the integration-tagged
tests, and the production gates against it.

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
