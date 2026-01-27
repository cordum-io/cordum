# Architecture
Tags: saga, compensation, durability, scheduler

Cordum is a distributed control plane built around a policy-first execution
model. The system is composed of small Go services backed by Redis and NATS
JetStream.

## Core components

- **API Gateway**: HTTP + gRPC entrypoint, auth, rate limits, tenant isolation.
- **Scheduler**: evaluates jobs against timeouts, retries, and pools.
- **Safety Kernel**: policy evaluation and approval workflow.
- **Workflow Engine**: orchestrates workflow steps and state transitions.
- **Context Engine**: optional context store for AI memory.
- **Redis**: system state, workflow definitions, config, pointers.
- **NATS JetStream**: durable job dispatch and at-least-once delivery.

## Data flow

1. Client submits a workflow/run/job via the Gateway.
2. Scheduler evaluates policy and constraints.
3. Safety Kernel returns allow/deny/approve/remediate decisions.
4. Approved jobs are dispatched on NATS topics.
5. Workers consume jobs, perform actions, and publish results.

## Durable Saga (Compensation)

When a workflow step completes with a defined compensation action, the scheduler records a compensation job on a Redis LIFO stack keyed by `saga:{workflow_id}:stack`. If a job later fails with `FAILED_FATAL`, the scheduler triggers rollback and dispatches the stored compensations in reverse order with critical priority.

See `docs/system_overview.md` for a detailed diagram and data flow.
