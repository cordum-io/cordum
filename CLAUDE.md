# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cordum is an AI Agent Governance Platform—a distributed control plane for orchestrating autonomous agents with safety enforcement, observability, and human-in-the-loop approval. Written in Go 1.24 with a React dashboard.

## Code Standards (CRITICAL)

**This is a mission-critical system. All code must be production-grade with zero tolerance for shortcuts.**

- **No happy path coding**: Handle every error case. Assume all external calls will fail. Validate all inputs at boundaries.
- **Defensive programming**: Check preconditions, validate state transitions, fail fast on invariant violations.
- **Timeouts everywhere**: Every network call, Redis operation, NATS publish, and gRPC call must have explicit timeouts.
- **Resource cleanup**: Always use `defer` for cleanup. Handle context cancellation. Prevent goroutine leaks.
- **Error handling**: Return errors with context (`fmt.Errorf("operation failed: %w", err)`). Never swallow errors silently.
- **Concurrency safety**: Protect shared state with mutexes. Use channels correctly. Avoid data races.
- **Graceful degradation**: Services must handle partial failures, upstream timeouts, and resource exhaustion.
- **Idempotency**: Operations that can be retried must be safe to retry. Use locking where needed.
- **Bounds checking**: Validate array indices, slice lengths, map keys. Never trust external data sizes.
- **Logging discipline**: Log errors with sufficient context for debugging. Never log secrets or PII.

## Build and Test Commands

```bash
# Build
make build                    # Build all services (runs proto generation first)
make build SERVICE=cordum-scheduler  # Build single service
make proto                    # Generate protobuf code only

# Test
go test ./...                 # Run unit tests
go test ./core/workflow/...   # Run tests for a specific package
go test -tags=integration ./... # Run integration tests
make coverage                 # Generate coverage report
make coverage-core            # Enforce 80% coverage on core/

# Local Development
make dev-up                   # Start docker-compose (all services)
make dev-down                 # Stop docker-compose
make dev-logs                 # Tail docker-compose logs
make smoke                    # Run platform smoke test

# Docker
make docker SERVICE=cordum-scheduler  # Build Docker image for a service
```

## Architecture

```
Client → API Gateway → Scheduler → Safety Kernel → NATS → Worker Pools
              ↓            ↓            ↓
          [Redis]      [Redis]     [Policies]
```

**Services (cmd/):**
- `cordum-api-gateway`: HTTP/gRPC/WebSocket entry point; auth, tenant isolation, rate limiting
- `cordum-scheduler`: Job routing, safety checks, worker dispatch, state management
- `cordum-safety-kernel`: Policy evaluation (allow/deny/throttle/require_approval)
- `cordum-workflow-engine`: Multi-step DAG orchestration with retries and approvals
- `cordum-context-engine`: Context window and chat/RAG memory service
- `cordumctl`: CLI tool

**Core packages (core/):**
- `controlplane/gateway`: REST routes, WebSocket streaming, auth middleware
- `controlplane/scheduler`: Job state machine, routing, reconcilers
- `controlplane/safetykernel`: Policy evaluation, bundle hot-reload
- `controlplane/workflowengine`: Run execution, step dispatch
- `workflow`: Workflow model and execution engine (core/workflow/engine.go)
- `context/engine`: Context windows, memory persistence
- `infra/bus`: NATS client abstraction
- `infra/memory`: Redis job store, artifact store
- `infra/config`: Typed config loaders with validation

**Data stores (Redis key patterns):**
- `ctx:<job_id>`, `res:<job_id>`: Job context and results
- `job:state:<id>`, `job:meta:<id>`: Job state machine
- `dlq:entry:<id>`: Dead letter queue
- Pointer format: `redis://<key>` for payload references

**Message bus (NATS subjects):**
- `sys.job.submit`, `sys.job.result`, `sys.job.progress`, `sys.job.cancel`
- `sys.heartbeat`, `sys.workflow.event`
- JetStream optional for durable delivery

## Key Design Patterns

1. **Pointer-based payloads**: Jobs carry Redis pointers (`redis://ctx:id`) instead of large payloads—keeps the bus lean
2. **Safety-first**: All jobs evaluated by Safety Kernel before dispatch
3. **Multi-tenant isolation**: `tenant_id` enforced everywhere; cross-tenant access blocked
4. **Deterministic state**: Job state transitions follow strict rules with locking for idempotency
5. **Saga compensation**: Workflow failures trigger rollback compensation stacks

## Security Requirements (NON-NEGOTIABLE)

**Security is not optional. Every change must maintain or strengthen security posture.**

- **TLS required**: All service endpoints (Gateway, Safety Kernel, Context Engine) use TLS in production
- **Auth enforcement**: API keys or JWT required on Gateway; never bypass auth checks
- **Tenant isolation**: `X-Tenant-ID` required; cross-tenant access blocked; validate tenant on every operation
- **Input validation**: Validate all inputs at system boundaries; reject malformed data early
- **Output encoding**: Prevent injection attacks; sanitize data before rendering or storing
- **Rate limiting**: Enforce limits to prevent abuse and resource exhaustion
- **Policy signatures**: Verify policy signatures in production when public key configured
- **Secret handling**: Detect `secret://` references; never log secrets, tokens, or credentials
- **SSRF protection**: Policy URL fetches reject private/loopback hosts unless explicitly allowed
- **Size limits**: Enforce max payload (2 MiB), max artifact (10 MiB); reject oversized requests

## Default Ports

- Gateway gRPC: `:8080`, HTTP: `:8081`, metrics: `:9092`
- Scheduler metrics: `:9090`
- Workflow engine health: `:9093/health`
- Safety Kernel: `:50051`
- Context Engine: `:50070`

## Testing Requirements

- **Tests are mandatory**: All logic changes in control-plane packages require tests
- **Coverage floor**: 80% minimum coverage enforced on `core/` (`make coverage-core`)
- **Test failure modes**: Test error paths, timeouts, invalid inputs, concurrent access
- **Integration tests**: Use `-tags=integration` for tests requiring Redis/NATS
- **Run before commit**: `go test ./path/to/changed/package/...` on every touched package

## Process Requirements

- Create GitHub issues for new features/significant changes and link in PRs
- Update `docs/` and `wiki/` for major changes
- Commit messages must be clear and professional
- Prefer focused `go test` runs on touched packages over repo-wide runs
